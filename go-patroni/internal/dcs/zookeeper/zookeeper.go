// Package zookeeper provides a ZooKeeper implementation of the DCS interface.
package zookeeper

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/samuel/go-zookeeper/zk"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

func init() {
	dcs.Register("zookeeper", New)
}

// ZooKeeper implements the DCS interface using Apache ZooKeeper.
type ZooKeeper struct {
	*dcs.BaseDCS
	conn      *zk.Conn
	eventCh   <-chan zk.Event
	sessionID int64
	mu        sync.RWMutex
}

// New creates a new ZooKeeper DCS instance.
func New(config *dcs.Config) (dcs.DCS, error) {
	hosts := config.GetHosts()
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1:2181"}
	}

	// Normalize hosts
	normalizedHosts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimPrefix(host, "zk://")
		host = strings.TrimPrefix(host, "zookeeper://")
		normalizedHosts = append(normalizedHosts, host)
	}

	ttl := config.GetTTL()
	if ttl < 5*time.Second {
		ttl = 5 * time.Second
	}

	conn, eventCh, err := zk.Connect(normalizedHosts, ttl)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ZooKeeper: %w", err)
	}

	z := &ZooKeeper{
		BaseDCS: dcs.NewBaseDCS(config),
		conn:    conn,
		eventCh: eventCh,
	}

	// Wait for connection
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event := <-eventCh:
			if event.State == zk.StateConnected || event.State == zk.StateHasSession {
				z.sessionID = conn.SessionID()
				z.SetSession(fmt.Sprintf("%d", z.sessionID))
				log.Info().
					Int64("session_id", z.sessionID).
					Msg("Connected to ZooKeeper")
				goto connected
			}
		case <-timeout:
			conn.Close()
			return nil, fmt.Errorf("timeout connecting to ZooKeeper")
		}
	}
connected:

	// Ensure base paths exist
	if err := z.ensureBasePaths(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create base paths: %w", err)
	}

	// Start event handler
	go z.handleEvents()

	return z, nil
}

// ensureBasePaths creates the necessary ZooKeeper paths.
func (z *ZooKeeper) ensureBasePaths() error {
	paths := []string{
		z.ClusterPath(),
		z.MembersPath(),
	}

	for _, p := range paths {
		if err := z.ensurePath(p); err != nil {
			return err
		}
	}

	return nil
}

// ensurePath ensures a path exists, creating parent nodes as needed.
func (z *ZooKeeper) ensurePath(p string) error {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	current := ""

	for _, part := range parts {
		current = current + "/" + part
		exists, _, err := z.conn.Exists(current)
		if err != nil {
			return err
		}
		if !exists {
			_, err := z.conn.Create(current, nil, 0, zk.WorldACL(zk.PermAll))
			if err != nil && err != zk.ErrNodeExists {
				return err
			}
		}
	}

	return nil
}

// handleEvents processes ZooKeeper events.
func (z *ZooKeeper) handleEvents() {
	for event := range z.eventCh {
		switch event.State {
		case zk.StateDisconnected:
			log.Warn().Msg("ZooKeeper disconnected")
		case zk.StateConnected:
			log.Info().Msg("ZooKeeper reconnected")
		case zk.StateExpired:
			log.Error().Msg("ZooKeeper session expired")
			// Session expired, need to handle this
		}
	}
}

// Name returns the DCS implementation name.
func (z *ZooKeeper) Name() string {
	return "zookeeper"
}

// Close closes the ZooKeeper connection.
func (z *ZooKeeper) Close() error {
	z.conn.Close()
	return nil
}

// GetCluster retrieves the current cluster state from ZooKeeper.
func (z *ZooKeeper) GetCluster(ctx context.Context) (*types.Cluster, error) {
	cluster := &types.Cluster{}

	basePath := z.ClusterPath()

	// Get initialize
	if data, stat, err := z.conn.Get(basePath + "/initialize"); err == nil {
		cluster.InitializeVersion = int64(stat.Version)
		_ = data // sysID stored here
	}

	// Get config
	if data, stat, err := z.conn.Get(basePath + "/config"); err == nil {
		config := &types.ClusterConfig{Version: int64(stat.Version)}
		if err := json.Unmarshal(data, &config.Data); err != nil {
			log.Warn().Err(err).Msg("Failed to parse cluster config")
		}
		cluster.Config = config
	}

	// Get leader
	if data, stat, err := z.conn.Get(basePath + "/leader"); err == nil {
		cluster.Leader = &types.Leader{
			Version:    int64(stat.Version),
			MemberName: string(data),
			Session:    fmt.Sprintf("%d", stat.EphemeralOwner),
		}
	}

	// Get sync state
	if data, stat, err := z.conn.Get(basePath + "/sync"); err == nil {
		sync := &types.SyncState{Version: int64(stat.Version)}
		if err := json.Unmarshal(data, sync); err != nil {
			log.Warn().Err(err).Msg("Failed to parse sync state")
		}
		cluster.SyncState = sync
	}

	// Get failover
	if data, stat, err := z.conn.Get(basePath + "/failover"); err == nil {
		failover := &types.Failover{Version: int64(stat.Version)}
		if err := json.Unmarshal(data, failover); err != nil {
			log.Warn().Err(err).Msg("Failed to parse failover")
		}
		cluster.Failover = failover
	}

	// Get history
	if data, stat, err := z.conn.Get(basePath + "/history"); err == nil {
		history := &types.TimelineHistory{Version: int64(stat.Version)}
		if err := json.Unmarshal(data, &history.Value); err != nil {
			log.Warn().Err(err).Msg("Failed to parse history")
		}
		cluster.History = history
	}

	// Get status
	if data, stat, err := z.conn.Get(basePath + "/status"); err == nil {
		status := &types.Status{Version: int64(stat.Version)}
		if err := json.Unmarshal(data, status); err != nil {
			log.Warn().Err(err).Msg("Failed to parse status")
		}
		cluster.Status = status
	}

	// Get members
	membersPath := z.MembersPath()
	if children, _, err := z.conn.Children(membersPath); err == nil {
		for _, name := range children {
			memberPath := path.Join(membersPath, name)
			if data, stat, err := z.conn.Get(memberPath); err == nil {
				member, _ := types.ParseMemberFromJSON(
					int64(stat.Version),
					name,
					fmt.Sprintf("%d", stat.EphemeralOwner),
					string(data),
				)
				cluster.Members = append(cluster.Members, member)
			}
		}
	}

	return cluster, nil
}

// TouchMember updates this member's entry in ZooKeeper.
func (z *ZooKeeper) TouchMember(ctx context.Context, data *types.MemberData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal member data: %w", err)
	}

	memberPath := path.Join(z.MembersPath(), z.Config.Name)

	// Try to set data on existing node
	_, err = z.conn.Set(memberPath, jsonData, -1)
	if err == zk.ErrNoNode {
		// Create ephemeral node
		_, err = z.conn.Create(memberPath, jsonData, zk.FlagEphemeral, zk.WorldACL(zk.PermAll))
	}

	if err != nil {
		return fmt.Errorf("failed to update member: %w", err)
	}

	return nil
}

// TakeLock attempts to acquire the leader lock.
func (z *ZooKeeper) TakeLock(ctx context.Context) (bool, error) {
	leaderPath := z.LeaderPath()

	// Try to create ephemeral node
	_, err := z.conn.Create(leaderPath, []byte(z.Config.Name), zk.FlagEphemeral, zk.WorldACL(zk.PermAll))
	if err == nil {
		log.Info().Msg("Successfully acquired leader lock")
		return true, nil
	}

	if err == zk.ErrNodeExists {
		// Check if we own it
		_, stat, err := z.conn.Get(leaderPath)
		if err != nil {
			return false, fmt.Errorf("failed to check leader: %w", err)
		}

		z.mu.RLock()
		sessionID := z.sessionID
		z.mu.RUnlock()

		if stat.EphemeralOwner == sessionID {
			return true, nil
		}
		return false, nil
	}

	return false, fmt.Errorf("failed to acquire lock: %w", err)
}

// UpdateLeader updates the leader key.
func (z *ZooKeeper) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	if leaderInfo != nil && leaderInfo.XlogLocation > 0 {
		optimePath := z.LeaderOpTimePath()
		optimeData, _ := json.Marshal(map[string]int64{"optime": leaderInfo.XlogLocation})

		_, err := z.conn.Set(optimePath, optimeData, -1)
		if err == zk.ErrNoNode {
			z.ensurePath(path.Dir(optimePath))
			_, err = z.conn.Create(optimePath, optimeData, 0, zk.WorldACL(zk.PermAll))
		}
		if err != nil {
			log.Warn().Err(err).Msg("Failed to update leader optime")
		}
	}

	return nil
}

// DeleteLeader releases the leader lock.
func (z *ZooKeeper) DeleteLeader(ctx context.Context) error {
	leaderPath := z.LeaderPath()

	// Check if we own the leader node
	_, stat, err := z.conn.Get(leaderPath)
	if err != nil {
		if err == zk.ErrNoNode {
			return nil
		}
		return fmt.Errorf("failed to check leader: %w", err)
	}

	z.mu.RLock()
	sessionID := z.sessionID
	z.mu.RUnlock()

	if stat.EphemeralOwner != sessionID {
		return nil // Not our lock
	}

	err = z.conn.Delete(leaderPath, stat.Version)
	if err != nil && err != zk.ErrNoNode {
		return fmt.Errorf("failed to delete leader: %w", err)
	}

	log.Info().Msg("Released leader lock")
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (z *ZooKeeper) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	leaderPath := z.LeaderPath()

	// Check if we already hold the lock
	_, stat, err := z.conn.Get(leaderPath)
	if err == nil {
		z.mu.RLock()
		sessionID := z.sessionID
		z.mu.RUnlock()

		if stat.EphemeralOwner == sessionID {
			// We hold the lock, touch it to keep it fresh
			_, err := z.conn.Set(leaderPath, []byte(z.Config.Name), stat.Version)
			if err == nil {
				return true, nil
			}
		}
		return false, nil
	}

	if err == zk.ErrNoNode {
		return z.TakeLock(ctx)
	}

	return false, fmt.Errorf("failed to check leader: %w", err)
}

// SetFailoverValue sets/updates the failover key.
func (z *ZooKeeper) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	data, err := json.Marshal(failover)
	if err != nil {
		return fmt.Errorf("failed to marshal failover: %w", err)
	}

	failoverPath := z.FailoverPath()
	_, err = z.conn.Set(failoverPath, data, -1)
	if err == zk.ErrNoNode {
		_, err = z.conn.Create(failoverPath, data, 0, zk.WorldACL(zk.PermAll))
	}
	return err
}

// DeleteFailover deletes the failover key.
func (z *ZooKeeper) DeleteFailover(ctx context.Context) error {
	failoverPath := z.FailoverPath()
	err := z.conn.Delete(failoverPath, -1)
	if err == zk.ErrNoNode {
		return nil
	}
	return err
}

// SetConfigValue sets/updates the cluster configuration.
func (z *ZooKeeper) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	configPath := z.ConfigPath()
	_, err = z.conn.Set(configPath, data, -1)
	if err == zk.ErrNoNode {
		_, err = z.conn.Create(configPath, data, 0, zk.WorldACL(zk.PermAll))
	}
	return err
}

// SetSyncState sets the synchronous replication state.
func (z *ZooKeeper) SetSyncState(ctx context.Context, state *types.SyncState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	syncPath := z.SyncPath()
	_, err = z.conn.Set(syncPath, data, -1)
	if err == zk.ErrNoNode {
		_, err = z.conn.Create(syncPath, data, 0, zk.WorldACL(zk.PermAll))
	}
	return err
}

// DeleteSyncState deletes the sync state.
func (z *ZooKeeper) DeleteSyncState(ctx context.Context) error {
	syncPath := z.SyncPath()
	err := z.conn.Delete(syncPath, -1)
	if err == zk.ErrNoNode {
		return nil
	}
	return err
}

// Initialize initializes the cluster in DCS.
func (z *ZooKeeper) Initialize(ctx context.Context, sysID string) (bool, error) {
	initPath := z.InitializePath()

	_, err := z.conn.Create(initPath, []byte(sysID), 0, zk.WorldACL(zk.PermAll))
	if err == nil {
		return true, nil
	}
	if err == zk.ErrNodeExists {
		return false, nil
	}
	return false, fmt.Errorf("failed to initialize: %w", err)
}

// SetHistoryValue sets the timeline history.
func (z *ZooKeeper) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	historyPath := z.HistoryPath()
	_, err = z.conn.Set(historyPath, data, -1)
	if err == zk.ErrNoNode {
		_, err = z.conn.Create(historyPath, data, 0, zk.WorldACL(zk.PermAll))
	}
	return err
}

// DeleteCluster removes all cluster data from DCS.
func (z *ZooKeeper) DeleteCluster(ctx context.Context) error {
	return z.deleteRecursive(z.ClusterPath())
}

// deleteRecursive deletes a node and all its children.
func (z *ZooKeeper) deleteRecursive(p string) error {
	children, _, err := z.conn.Children(p)
	if err != nil {
		if err == zk.ErrNoNode {
			return nil
		}
		return err
	}

	for _, child := range children {
		if err := z.deleteRecursive(path.Join(p, child)); err != nil {
			return err
		}
	}

	return z.conn.Delete(p, -1)
}

// Watch returns a channel that receives cluster updates.
func (z *ZooKeeper) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster, 1)

	go func() {
		defer close(ch)

		// Send initial cluster state
		cluster, err := z.GetCluster(ctx)
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
		basePath := z.ClusterPath()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Set watch on children
			_, _, eventCh, err := z.conn.ChildrenW(basePath)
			if err != nil {
				log.Error().Err(err).Msg("Failed to set watch")
				time.Sleep(time.Second)
				continue
			}

			select {
			case <-ctx.Done():
				return
			case <-eventCh:
				cluster, err := z.GetCluster(ctx)
				if err != nil {
					log.Error().Err(err).Msg("Failed to get cluster after watch")
					continue
				}

				select {
				case ch <- cluster:
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}()

	return ch, nil
}
