// Package consul provides a Consul implementation of the DCS interface.
package consul

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

func init() {
	dcs.Register("consul", New)
}

// Consul implements the DCS interface using HashiCorp Consul.
type Consul struct {
	*dcs.BaseDCS
	client    *api.Client
	kv        *api.KV
	session   *api.Session
	sessionID string
	mu        sync.RWMutex
}

// New creates a new Consul DCS instance.
func New(config *dcs.Config) (dcs.DCS, error) {
	hosts := config.GetHosts()
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1:8500"}
	}

	// Parse the first host
	host := hosts[0]
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}

	consulConfig := api.DefaultConfig()
	consulConfig.Address = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")

	// Configure authentication
	if config.Username != "" {
		consulConfig.HttpAuth = &api.HttpBasicAuth{
			Username: config.Username,
			Password: config.Password,
		}
	}

	// Configure TLS
	if config.CACert != "" || config.Cert != "" {
		consulConfig.TLSConfig = api.TLSConfig{
			CAFile:   config.CACert,
			CertFile: config.Cert,
			KeyFile:  config.Key,
		}
		if strings.HasPrefix(host, "https://") {
			consulConfig.Scheme = "https"
		}
	}

	client, err := api.NewClient(consulConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %w", err)
	}

	c := &Consul{
		BaseDCS: dcs.NewBaseDCS(config),
		client:  client,
		kv:      client.KV(),
		session: client.Session(),
	}

	// Create session
	if err := c.createSession(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return c, nil
}

// createSession creates a new Consul session.
func (c *Consul) createSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.Config.GetTTL()
	if ttl < 10*time.Second {
		ttl = 10 * time.Second
	}

	sessionEntry := &api.SessionEntry{
		Name:      fmt.Sprintf("patroni-%s-%s", c.Scope, c.Name()),
		TTL:       ttl.String(),
		Behavior:  api.SessionBehaviorDelete,
		LockDelay: 0,
	}

	id, _, err := c.session.Create(sessionEntry, nil)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	c.sessionID = id
	c.SetSession(id)

	log.Info().
		Str("session_id", id).
		Dur("ttl", ttl).
		Msg("Created Consul session")

	// Start session renewal
	go c.renewSession(ctx)

	return nil
}

// renewSession periodically renews the Consul session.
func (c *Consul) renewSession(ctx context.Context) {
	ttl := c.Config.GetTTL()
	renewInterval := ttl / 2

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			sessionID := c.sessionID
			c.mu.RUnlock()

			if sessionID == "" {
				continue
			}

			_, _, err := c.session.Renew(sessionID, nil)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to renew Consul session")
				// Try to recreate session
				if err := c.createSession(ctx); err != nil {
					log.Error().Err(err).Msg("Failed to recreate Consul session")
				}
			}
		}
	}
}

// Name returns the DCS implementation name.
func (c *Consul) Name() string {
	return "consul"
}

// Close closes the Consul client connection.
func (c *Consul) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sessionID != "" {
		c.session.Destroy(c.sessionID, nil)
	}
	return nil
}

// keyPath returns the full key path for a given key.
func (c *Consul) keyPath(key string) string {
	return strings.TrimPrefix(c.ClusterPath()+"/"+key, "/")
}

// GetCluster retrieves the current cluster state from Consul.
func (c *Consul) GetCluster(ctx context.Context) (*types.Cluster, error) {
	cluster := &types.Cluster{}

	// Get all keys under the cluster path
	prefix := strings.TrimPrefix(c.ClusterPath(), "/")
	pairs, _, err := c.kv.List(prefix, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster data: %w", err)
	}

	for _, kv := range pairs {
		key := strings.TrimPrefix(kv.Key, prefix+"/")
		value := string(kv.Value)
		version := int64(kv.ModifyIndex)

		switch {
		case key == "initialize":
			cluster.InitializeVersion = version

		case key == "config":
			config := &types.ClusterConfig{Version: version}
			if err := json.Unmarshal(kv.Value, &config.Data); err != nil {
				log.Warn().Err(err).Msg("Failed to parse cluster config")
			}
			cluster.Config = config

		case key == "leader":
			cluster.Leader = &types.Leader{
				Version:    version,
				MemberName: value,
				Session:    kv.Session,
			}

		case key == "sync":
			sync := &types.SyncState{Version: version}
			if err := json.Unmarshal(kv.Value, sync); err != nil {
				log.Warn().Err(err).Msg("Failed to parse sync state")
			}
			cluster.SyncState = sync

		case key == "failover":
			failover := &types.Failover{Version: version}
			if err := json.Unmarshal(kv.Value, failover); err != nil {
				log.Warn().Err(err).Msg("Failed to parse failover")
			}
			cluster.Failover = failover

		case key == "history":
			history := &types.TimelineHistory{Version: version}
			if err := json.Unmarshal(kv.Value, &history.Value); err != nil {
				log.Warn().Err(err).Msg("Failed to parse history")
			}
			cluster.History = history

		case key == "status":
			status := &types.Status{Version: version}
			if err := json.Unmarshal(kv.Value, status); err != nil {
				log.Warn().Err(err).Msg("Failed to parse status")
			}
			cluster.Status = status

		case strings.HasPrefix(key, "members/"):
			memberName := strings.TrimPrefix(key, "members/")
			member, _ := types.ParseMemberFromJSON(version, memberName, kv.Session, value)
			cluster.Members = append(cluster.Members, member)
		}
	}

	return cluster, nil
}

// TouchMember updates this member's entry in Consul.
func (c *Consul) TouchMember(ctx context.Context, data *types.MemberData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal member data: %w", err)
	}

	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	key := c.keyPath("members/" + c.Config.Name)
	p := &api.KVPair{
		Key:     key,
		Value:   jsonData,
		Session: sessionID,
	}

	_, err = c.kv.Put(p, nil)
	if err != nil {
		return fmt.Errorf("failed to update member: %w", err)
	}

	return nil
}

// TakeLock attempts to acquire the leader lock.
func (c *Consul) TakeLock(ctx context.Context) (bool, error) {
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	key := c.keyPath("leader")
	p := &api.KVPair{
		Key:     key,
		Value:   []byte(c.Config.Name),
		Session: sessionID,
	}

	acquired, _, err := c.kv.Acquire(p, nil)
	if err != nil {
		return false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	if acquired {
		log.Info().Msg("Successfully acquired leader lock")
	}

	return acquired, nil
}

// UpdateLeader updates the leader key.
func (c *Consul) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	// Consul lock is maintained by session, just update optime
	if leaderInfo != nil && leaderInfo.XlogLocation > 0 {
		key := c.keyPath("optime/leader")
		optimeData, _ := json.Marshal(map[string]int64{"optime": leaderInfo.XlogLocation})
		p := &api.KVPair{
			Key:   key,
			Value: optimeData,
		}
		_, err := c.kv.Put(p, nil)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to update leader optime")
		}
	}

	return nil
}

// DeleteLeader releases the leader lock.
func (c *Consul) DeleteLeader(ctx context.Context) error {
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	key := c.keyPath("leader")
	p := &api.KVPair{
		Key:     key,
		Session: sessionID,
	}

	_, _, err := c.kv.Release(p, nil)
	if err != nil {
		return fmt.Errorf("failed to release leader: %w", err)
	}

	log.Info().Msg("Released leader lock")
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (c *Consul) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	// Check current leader
	key := c.keyPath("leader")
	pair, _, err := c.kv.Get(key, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check leader: %w", err)
	}

	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	if pair != nil && pair.Session == sessionID {
		// We already hold the lock
		return true, nil
	}

	// Try to acquire
	return c.TakeLock(ctx)
}

// SetFailoverValue sets/updates the failover key.
func (c *Consul) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	data, err := json.Marshal(failover)
	if err != nil {
		return fmt.Errorf("failed to marshal failover: %w", err)
	}

	key := c.keyPath("failover")
	p := &api.KVPair{Key: key, Value: data}
	_, err = c.kv.Put(p, nil)
	return err
}

// DeleteFailover deletes the failover key.
func (c *Consul) DeleteFailover(ctx context.Context) error {
	key := c.keyPath("failover")
	_, err := c.kv.Delete(key, nil)
	return err
}

// SetConfigValue sets/updates the cluster configuration.
func (c *Consul) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	key := c.keyPath("config")
	p := &api.KVPair{Key: key, Value: data}
	_, err = c.kv.Put(p, nil)
	return err
}

// SetSyncState sets the synchronous replication state.
func (c *Consul) SetSyncState(ctx context.Context, state *types.SyncState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	key := c.keyPath("sync")
	p := &api.KVPair{Key: key, Value: data}
	_, err = c.kv.Put(p, nil)
	return err
}

// DeleteSyncState deletes the sync state.
func (c *Consul) DeleteSyncState(ctx context.Context) error {
	key := c.keyPath("sync")
	_, err := c.kv.Delete(key, nil)
	return err
}

// Initialize initializes the cluster in DCS.
func (c *Consul) Initialize(ctx context.Context, sysID string) (bool, error) {
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	key := c.keyPath("initialize")
	p := &api.KVPair{
		Key:     key,
		Value:   []byte(sysID),
		Session: sessionID,
	}

	// Use CAS to ensure atomicity
	success, _, err := c.kv.CAS(p, nil)
	if err != nil {
		return false, fmt.Errorf("failed to initialize: %w", err)
	}

	return success, nil
}

// SetHistoryValue sets the timeline history.
func (c *Consul) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	key := c.keyPath("history")
	p := &api.KVPair{Key: key, Value: data}
	_, err = c.kv.Put(p, nil)
	return err
}

// DeleteCluster removes all cluster data from DCS.
func (c *Consul) DeleteCluster(ctx context.Context) error {
	prefix := strings.TrimPrefix(c.ClusterPath(), "/")
	_, err := c.kv.DeleteTree(prefix, nil)
	return err
}

// Watch returns a channel that receives cluster updates.
func (c *Consul) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster, 1)

	go func() {
		defer close(ch)

		var lastIndex uint64
		prefix := strings.TrimPrefix(c.ClusterPath(), "/")

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			opts := &api.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  timeout,
			}

			pairs, meta, err := c.kv.List(prefix, opts)
			if err != nil {
				log.Error().Err(err).Msg("Watch error")
				time.Sleep(time.Second)
				continue
			}

			if meta.LastIndex > lastIndex {
				lastIndex = meta.LastIndex

				cluster, err := c.GetCluster(ctx)
				if err != nil {
					log.Error().Err(err).Msg("Failed to get cluster after watch")
					continue
				}

				select {
				case ch <- cluster:
				case <-ctx.Done():
					return
				default:
					// Channel full, skip
				}
			}

			// Avoid busy loop if no pairs
			if len(pairs) == 0 {
				time.Sleep(time.Second)
			}
		}
	}()

	return ch, nil
}
