// Package etcd3 provides an etcd v3 API implementation of the DCS interface.
package etcd3

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

func init() {
	dcs.Register("etcd3", New)
}

// Etcd3 implements the DCS interface using etcd v3 API.
type Etcd3 struct {
	*dcs.BaseDCS
	client  *clientv3.Client
	lease   clientv3.Lease
	leaseID clientv3.LeaseID
	session *concurrency.Session
	mu      sync.RWMutex
}

// New creates a new Etcd3 DCS instance.
func New(config *dcs.Config) (dcs.DCS, error) {
	hosts := config.GetHosts()
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1:2379"}
	}

	// Normalize endpoints
	endpoints := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}
		endpoints = append(endpoints, host)
	}

	cfg := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	}

	// Configure authentication
	if config.Username != "" {
		cfg.Username = config.Username
		cfg.Password = config.Password
	}

	// Configure TLS
	if config.CACert != "" || config.Cert != "" {
		tlsConfig, err := newTLSConfig(config.CACert, config.Cert, config.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %w", err)
		}
		cfg.TLS = tlsConfig
	}

	client, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	e := &Etcd3{
		BaseDCS: dcs.NewBaseDCS(config),
		client:  client,
		lease:   clientv3.NewLease(client),
	}

	// Create a session with TTL
	if err := e.createSession(context.Background()); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return e, nil
}

// createSession creates a new etcd session with lease.
func (e *Etcd3) createSession(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ttl := int64(e.Config.GetTTL().Seconds())
	if ttl < 5 {
		ttl = 5
	}

	session, err := concurrency.NewSession(e.client, concurrency.WithTTL(int(ttl)))
	if err != nil {
		return fmt.Errorf("failed to create etcd session: %w", err)
	}

	e.session = session
	e.leaseID = session.Lease()
	e.SetSession(fmt.Sprintf("%d", e.leaseID))

	log.Info().
		Int64("lease_id", int64(e.leaseID)).
		Int64("ttl", ttl).
		Msg("Created etcd session")

	return nil
}

// Name returns the DCS implementation name.
func (e *Etcd3) Name() string {
	return "etcd3"
}

// Close closes the etcd client connection.
func (e *Etcd3) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.session != nil {
		e.session.Close()
	}
	return e.client.Close()
}

// GetCluster retrieves the current cluster state from etcd.
func (e *Etcd3) GetCluster(ctx context.Context) (*types.Cluster, error) {
	cluster := &types.Cluster{}

	// Get all keys under the cluster path
	resp, err := e.client.Get(ctx, e.ClusterPath(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster data: %w", err)
	}

	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		value := string(kv.Value)
		version := kv.ModRevision

		switch {
		case key == e.InitializePath():
			cluster.InitializeVersion = version

		case key == e.ConfigPath():
			config := &types.ClusterConfig{Version: version}
			if err := json.Unmarshal(kv.Value, &config.Data); err != nil {
				log.Warn().Err(err).Msg("Failed to parse cluster config")
			}
			cluster.Config = config

		case key == e.LeaderPath():
			cluster.Leader = &types.Leader{
				Version:    version,
				MemberName: value,
			}

		case key == e.SyncPath():
			sync := &types.SyncState{Version: version}
			if err := json.Unmarshal(kv.Value, sync); err != nil {
				log.Warn().Err(err).Msg("Failed to parse sync state")
			}
			cluster.SyncState = sync

		case key == e.FailoverPath():
			failover := &types.Failover{Version: version}
			if err := json.Unmarshal(kv.Value, failover); err != nil {
				log.Warn().Err(err).Msg("Failed to parse failover")
			}
			cluster.Failover = failover

		case key == e.HistoryPath():
			history := &types.TimelineHistory{Version: version}
			if err := json.Unmarshal(kv.Value, &history.Value); err != nil {
				log.Warn().Err(err).Msg("Failed to parse history")
			}
			cluster.History = history

		case key == e.StatusPath():
			status := &types.Status{Version: version}
			if err := json.Unmarshal(kv.Value, status); err != nil {
				log.Warn().Err(err).Msg("Failed to parse status")
			}
			cluster.Status = status

		case strings.HasPrefix(key, e.MembersPath()):
			memberName := strings.TrimPrefix(key, e.MembersPath())
			member, _ := types.ParseMemberFromJSON(version, memberName, "", value)
			cluster.Members = append(cluster.Members, member)
		}
	}

	// Get leader session info
	if cluster.Leader != nil {
		leaderKey := e.LeaderPath()
		resp, err := e.client.Get(ctx, leaderKey)
		if err == nil && len(resp.Kvs) > 0 {
			cluster.Leader.Session = fmt.Sprintf("%d", resp.Kvs[0].Lease)
		}
	}

	return cluster, nil
}

// TouchMember updates this member's entry in etcd.
func (e *Etcd3) TouchMember(ctx context.Context, data *types.MemberData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal member data: %w", err)
	}

	e.mu.RLock()
	leaseID := e.leaseID
	e.mu.RUnlock()

	_, err = e.client.Put(ctx, e.MemberPath(), string(jsonData), clientv3.WithLease(leaseID))
	if err != nil {
		return fmt.Errorf("failed to update member: %w", err)
	}

	return nil
}

// TakeLock attempts to acquire the leader lock.
func (e *Etcd3) TakeLock(ctx context.Context) (bool, error) {
	e.mu.RLock()
	leaseID := e.leaseID
	e.mu.RUnlock()

	// Use a transaction to atomically check and set the leader key
	txn := e.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(e.LeaderPath()), "=", 0)).
		Then(clientv3.OpPut(e.LeaderPath(), e.Config.Name, clientv3.WithLease(leaseID))).
		Else(clientv3.OpGet(e.LeaderPath()))

	resp, err := txn.Commit()
	if err != nil {
		return false, fmt.Errorf("failed to take lock: %w", err)
	}

	if resp.Succeeded {
		log.Info().Msg("Successfully acquired leader lock")
		return true, nil
	}

	// Check if we already own the lock
	if len(resp.Responses) > 0 {
		getResp := resp.Responses[0].GetResponseRange()
		if len(getResp.Kvs) > 0 {
			existingLeader := string(getResp.Kvs[0].Value)
			existingLease := getResp.Kvs[0].Lease
			if existingLeader == e.Config.Name && existingLease == int64(leaseID) {
				return true, nil
			}
		}
	}

	return false, nil
}

// UpdateLeader updates the leader key with new information.
func (e *Etcd3) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	e.mu.RLock()
	leaseID := e.leaseID
	e.mu.RUnlock()

	// Update the leader key (keep-alive)
	_, err := e.client.Put(ctx, e.LeaderPath(), e.Config.Name, clientv3.WithLease(leaseID))
	if err != nil {
		return fmt.Errorf("failed to update leader: %w", err)
	}

	// Update leader optime if provided
	if leaderInfo != nil && leaderInfo.XlogLocation > 0 {
		optimeData, _ := json.Marshal(map[string]int64{"optime": leaderInfo.XlogLocation})
		_, err = e.client.Put(ctx, e.LeaderOpTimePath(), string(optimeData))
		if err != nil {
			log.Warn().Err(err).Msg("Failed to update leader optime")
		}
	}

	return nil
}

// DeleteLeader releases the leader lock.
func (e *Etcd3) DeleteLeader(ctx context.Context) error {
	e.mu.RLock()
	leaseID := e.leaseID
	e.mu.RUnlock()

	// Only delete if we own the lock
	txn := e.client.Txn(ctx).
		If(clientv3.Compare(clientv3.LeaseValue(e.LeaderPath()), "=", leaseID)).
		Then(clientv3.OpDelete(e.LeaderPath()))

	_, err := txn.Commit()
	if err != nil {
		return fmt.Errorf("failed to delete leader: %w", err)
	}

	log.Info().Msg("Released leader lock")
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (e *Etcd3) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	// First check if we already hold the lock
	resp, err := e.client.Get(ctx, e.LeaderPath())
	if err != nil {
		return false, fmt.Errorf("failed to check leader: %w", err)
	}

	e.mu.RLock()
	leaseID := e.leaseID
	e.mu.RUnlock()

	if len(resp.Kvs) > 0 {
		existingLeader := string(resp.Kvs[0].Value)
		existingLease := resp.Kvs[0].Lease

		if existingLeader == e.Config.Name && existingLease == int64(leaseID) {
			// We already hold the lock, just refresh it
			_, _, err := e.lease.KeepAliveOnce(ctx, leaseID)
			if err != nil {
				return false, fmt.Errorf("failed to refresh lease: %w", err)
			}
			return true, nil
		}
		// Someone else holds the lock
		return false, nil
	}

	// No leader, try to acquire
	return e.TakeLock(ctx)
}

// SetFailoverValue sets/updates the failover key.
func (e *Etcd3) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	data, err := json.Marshal(failover)
	if err != nil {
		return fmt.Errorf("failed to marshal failover: %w", err)
	}

	_, err = e.client.Put(ctx, e.FailoverPath(), string(data))
	if err != nil {
		return fmt.Errorf("failed to set failover: %w", err)
	}

	return nil
}

// DeleteFailover deletes the failover key.
func (e *Etcd3) DeleteFailover(ctx context.Context) error {
	_, err := e.client.Delete(ctx, e.FailoverPath())
	if err != nil {
		return fmt.Errorf("failed to delete failover: %w", err)
	}
	return nil
}

// SetConfigValue sets/updates the cluster configuration.
func (e *Etcd3) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	_, err = e.client.Put(ctx, e.ConfigPath(), string(data))
	if err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}

	return nil
}

// SetSyncState sets the synchronous replication state.
func (e *Etcd3) SetSyncState(ctx context.Context, state *types.SyncState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	_, err = e.client.Put(ctx, e.SyncPath(), string(data))
	if err != nil {
		return fmt.Errorf("failed to set sync state: %w", err)
	}

	return nil
}

// DeleteSyncState deletes the sync state.
func (e *Etcd3) DeleteSyncState(ctx context.Context) error {
	_, err := e.client.Delete(ctx, e.SyncPath())
	if err != nil {
		return fmt.Errorf("failed to delete sync state: %w", err)
	}
	return nil
}

// Initialize initializes the cluster in DCS.
func (e *Etcd3) Initialize(ctx context.Context, sysID string) (bool, error) {
	// Use a transaction to atomically check and set the initialize key
	txn := e.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(e.InitializePath()), "=", 0)).
		Then(clientv3.OpPut(e.InitializePath(), sysID))

	resp, err := txn.Commit()
	if err != nil {
		return false, fmt.Errorf("failed to initialize: %w", err)
	}

	return resp.Succeeded, nil
}

// SetHistoryValue sets the timeline history.
func (e *Etcd3) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	_, err = e.client.Put(ctx, e.HistoryPath(), string(data))
	if err != nil {
		return fmt.Errorf("failed to set history: %w", err)
	}

	return nil
}

// DeleteCluster removes all cluster data from DCS.
func (e *Etcd3) DeleteCluster(ctx context.Context) error {
	_, err := e.client.Delete(ctx, e.ClusterPath(), clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}
	return nil
}

// Watch returns a channel that receives cluster updates.
func (e *Etcd3) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster, 1)

	go func() {
		defer close(ch)

		watchCh := e.client.Watch(ctx, e.ClusterPath(), clientv3.WithPrefix())

		// Send initial cluster state
		cluster, err := e.GetCluster(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get initial cluster state")
			return
		}
		select {
		case ch <- cluster:
		case <-ctx.Done():
			return
		}

		// Watch for changes
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-watchCh:
				if !ok {
					return
				}
				if resp.Err() != nil {
					log.Error().Err(resp.Err()).Msg("Watch error")
					continue
				}

				// Fetch the full cluster state on any change
				cluster, err := e.GetCluster(ctx)
				if err != nil {
					log.Error().Err(err).Msg("Failed to get cluster state after watch event")
					continue
				}

				select {
				case ch <- cluster:
				case <-ctx.Done():
					return
				default:
					// Channel full, skip this update
				}
			}
		}
	}()

	return ch, nil
}
