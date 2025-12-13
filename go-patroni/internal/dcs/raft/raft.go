// Package raft provides a Raft consensus-based implementation of the DCS interface.
package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

func init() {
	dcs.Register("raft", New)
}

// command types for the Raft FSM
const (
	cmdSetMember     = "set_member"
	cmdDeleteMember  = "delete_member"
	cmdSetLeader     = "set_leader"
	cmdDeleteLeader  = "delete_leader"
	cmdSetConfig     = "set_config"
	cmdSetSync       = "set_sync"
	cmdDeleteSync    = "delete_sync"
	cmdSetFailover   = "set_failover"
	cmdDeleteFailover = "delete_failover"
	cmdSetHistory    = "set_history"
	cmdInitialize    = "initialize"
	cmdDeleteCluster = "delete_cluster"
	cmdSetStatus     = "set_status"
)

// command represents a Raft log entry command.
type command struct {
	Type  string          `json:"type"`
	Key   string          `json:"key,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Raft implements the DCS interface using Raft consensus.
type Raft struct {
	*dcs.BaseDCS
	raft     *raft.Raft
	fsm      *fsm
	dataDir  string
	bindAddr string
	peers    []string
	mu       sync.RWMutex
}

// fsm implements the raft.FSM interface.
type fsm struct {
	mu       sync.RWMutex
	members  map[string]*types.Member
	leader   *types.Leader
	config   *types.ClusterConfig
	sync     *types.SyncState
	failover *types.Failover
	history  *types.TimelineHistory
	status   *types.Status
	initVer  int64
	version  int64
}

// New creates a new Raft DCS instance.
func New(config *dcs.Config) (dcs.DCS, error) {
	hosts := config.GetHosts()
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1:2222"}
	}

	// Get data directory from config
	dataDir := config.Namespace
	if dataDir == "" {
		dataDir = "/var/lib/patroni/raft"
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	r := &Raft{
		BaseDCS:  dcs.NewBaseDCS(config),
		dataDir:  dataDir,
		bindAddr: hosts[0],
		peers:    hosts[1:],
		fsm: &fsm{
			members: make(map[string]*types.Member),
		},
	}

	if err := r.setupRaft(); err != nil {
		return nil, fmt.Errorf("failed to setup raft: %w", err)
	}

	return r, nil
}

// setupRaft configures and starts the Raft node.
func (r *Raft) setupRaft() error {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(r.Config.Name)
	config.HeartbeatTimeout = 1000 * time.Millisecond
	config.ElectionTimeout = 1000 * time.Millisecond
	config.LeaderLeaseTimeout = 500 * time.Millisecond
	config.CommitTimeout = 50 * time.Millisecond

	// Setup Raft communication
	addr, err := net.ResolveTCPAddr("tcp", r.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve bind address: %w", err)
	}

	transport, err := raft.NewTCPTransport(r.bindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	// Create the snapshot store
	snapshots, err := raft.NewFileSnapshotStore(r.dataDir, 2, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Create the log store and stable store
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(r.dataDir, "raft.db"))
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}

	// Check if this is a new cluster
	hasState, err := raft.HasExistingState(logStore, logStore, snapshots)
	if err != nil {
		return fmt.Errorf("failed to check existing state: %w", err)
	}

	// Create the Raft system
	ra, err := raft.NewRaft(config, r.fsm, logStore, logStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}
	r.raft = ra

	// Bootstrap cluster if this is new
	if !hasState {
		servers := []raft.Server{
			{
				ID:      config.LocalID,
				Address: raft.ServerAddress(r.bindAddr),
			},
		}
		for _, peer := range r.peers {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(peer),
				Address: raft.ServerAddress(peer),
			})
		}

		configuration := raft.Configuration{Servers: servers}
		ra.BootstrapCluster(configuration)
	}

	return nil
}

// Name returns the DCS implementation name.
func (r *Raft) Name() string {
	return "raft"
}

// Close closes the Raft node.
func (r *Raft) Close() error {
	if r.raft != nil {
		future := r.raft.Shutdown()
		return future.Error()
	}
	return nil
}

// GetCluster retrieves the current cluster state from the FSM.
func (r *Raft) GetCluster(ctx context.Context) (*types.Cluster, error) {
	r.fsm.mu.RLock()
	defer r.fsm.mu.RUnlock()

	cluster := &types.Cluster{
		InitializeVersion: r.fsm.initVer,
	}

	// Copy members
	for _, m := range r.fsm.members {
		memberCopy := *m
		cluster.Members = append(cluster.Members, &memberCopy)
	}

	if r.fsm.leader != nil {
		leaderCopy := *r.fsm.leader
		cluster.Leader = &leaderCopy
	}

	if r.fsm.config != nil {
		configCopy := *r.fsm.config
		cluster.Config = &configCopy
	}

	if r.fsm.sync != nil {
		syncCopy := *r.fsm.sync
		cluster.SyncState = &syncCopy
	}

	if r.fsm.failover != nil {
		failoverCopy := *r.fsm.failover
		cluster.Failover = &failoverCopy
	}

	if r.fsm.history != nil {
		historyCopy := *r.fsm.history
		cluster.History = &historyCopy
	}

	if r.fsm.status != nil {
		statusCopy := *r.fsm.status
		cluster.Status = &statusCopy
	}

	return cluster, nil
}

// applyCommand applies a command to the Raft log.
func (r *Raft) applyCommand(cmd *command) error {
	if r.raft.State() != raft.Leader {
		// Forward to leader or return error
		return fmt.Errorf("not the leader")
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	timeout := 5 * time.Second
	future := r.raft.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}

	return nil
}

// TouchMember updates this member's entry.
func (r *Raft) TouchMember(ctx context.Context, data *types.MemberData) error {
	value, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal member data: %w", err)
	}

	cmd := &command{
		Type:  cmdSetMember,
		Key:   r.Config.Name,
		Value: value,
	}

	return r.applyCommand(cmd)
}

// TakeLock attempts to acquire the leader lock.
func (r *Raft) TakeLock(ctx context.Context) (bool, error) {
	// In Raft, the Raft leader automatically becomes the Patroni leader
	if r.raft.State() != raft.Leader {
		return false, nil
	}

	value, err := json.Marshal(r.Config.Name)
	if err != nil {
		return false, err
	}

	cmd := &command{
		Type:  cmdSetLeader,
		Value: value,
	}

	if err := r.applyCommand(cmd); err != nil {
		return false, err
	}

	log.Info().Msg("Successfully acquired leader lock")
	return true, nil
}

// UpdateLeader updates the leader information.
func (r *Raft) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	// Just touch member with updated info
	if leaderInfo != nil {
		return r.TouchMember(ctx, leaderInfo)
	}
	return nil
}

// DeleteLeader releases the leader lock.
func (r *Raft) DeleteLeader(ctx context.Context) error {
	cmd := &command{
		Type: cmdDeleteLeader,
	}

	if err := r.applyCommand(cmd); err != nil {
		return err
	}

	log.Info().Msg("Released leader lock")
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (r *Raft) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	// In Raft, check if we're the Raft leader
	if r.raft.State() == raft.Leader {
		return true, nil
	}

	// Wait for potential leadership
	select {
	case <-r.raft.LeaderCh():
		return r.TakeLock(ctx)
	case <-time.After(100 * time.Millisecond):
		return false, nil
	}
}

// SetFailoverValue sets/updates the failover key.
func (r *Raft) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	value, err := json.Marshal(failover)
	if err != nil {
		return fmt.Errorf("failed to marshal failover: %w", err)
	}

	cmd := &command{
		Type:  cmdSetFailover,
		Value: value,
	}

	return r.applyCommand(cmd)
}

// DeleteFailover deletes the failover key.
func (r *Raft) DeleteFailover(ctx context.Context) error {
	cmd := &command{
		Type: cmdDeleteFailover,
	}

	return r.applyCommand(cmd)
}

// SetConfigValue sets/updates the cluster configuration.
func (r *Raft) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	value, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	cmd := &command{
		Type:  cmdSetConfig,
		Value: value,
	}

	return r.applyCommand(cmd)
}

// SetSyncState sets the synchronous replication state.
func (r *Raft) SetSyncState(ctx context.Context, state *types.SyncState) error {
	value, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	cmd := &command{
		Type:  cmdSetSync,
		Value: value,
	}

	return r.applyCommand(cmd)
}

// DeleteSyncState deletes the sync state.
func (r *Raft) DeleteSyncState(ctx context.Context) error {
	cmd := &command{
		Type: cmdDeleteSync,
	}

	return r.applyCommand(cmd)
}

// Initialize initializes the cluster.
func (r *Raft) Initialize(ctx context.Context, sysID string) (bool, error) {
	r.fsm.mu.RLock()
	initialized := r.fsm.initVer > 0
	r.fsm.mu.RUnlock()

	if initialized {
		return false, nil
	}

	value, err := json.Marshal(sysID)
	if err != nil {
		return false, err
	}

	cmd := &command{
		Type:  cmdInitialize,
		Value: value,
	}

	if err := r.applyCommand(cmd); err != nil {
		return false, err
	}

	return true, nil
}

// SetHistoryValue sets the timeline history.
func (r *Raft) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	value, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	cmd := &command{
		Type:  cmdSetHistory,
		Value: value,
	}

	return r.applyCommand(cmd)
}

// DeleteCluster removes all cluster data.
func (r *Raft) DeleteCluster(ctx context.Context) error {
	cmd := &command{
		Type: cmdDeleteCluster,
	}

	return r.applyCommand(cmd)
}

// Watch returns a channel that receives cluster updates.
func (r *Raft) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster, 1)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var lastVersion int64

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.fsm.mu.RLock()
				version := r.fsm.version
				r.fsm.mu.RUnlock()

				if version > lastVersion {
					lastVersion = version

					cluster, err := r.GetCluster(ctx)
					if err != nil {
						log.Error().Err(err).Msg("Failed to get cluster")
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
			}
		}
	}()

	return ch, nil
}

// IsLeader returns true if this node is the Raft leader.
func (r *Raft) IsLeader() bool {
	return r.raft.State() == raft.Leader
}

// GetLeaderAddress returns the address of the current Raft leader.
func (r *Raft) GetLeaderAddress() string {
	return string(r.raft.Leader())
}

// AddPeer adds a new peer to the Raft cluster.
func (r *Raft) AddPeer(id, addr string) error {
	if r.raft.State() != raft.Leader {
		return fmt.Errorf("not the leader")
	}

	future := r.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second)
	return future.Error()
}

// RemovePeer removes a peer from the Raft cluster.
func (r *Raft) RemovePeer(id string) error {
	if r.raft.State() != raft.Leader {
		return fmt.Errorf("not the leader")
	}

	future := r.raft.RemoveServer(raft.ServerID(id), 0, 10*time.Second)
	return future.Error()
}

// FSM implementation

// Apply applies a Raft log entry to the FSM.
func (f *fsm) Apply(l *raft.Log) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	var cmd command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal command")
		return err
	}

	f.version++

	switch cmd.Type {
	case cmdSetMember:
		var data types.MemberData
		if err := json.Unmarshal(cmd.Value, &data); err != nil {
			return err
		}
		f.members[cmd.Key] = &types.Member{
			Name:    cmd.Key,
			Version: f.version,
			Data:    data,
		}

	case cmdDeleteMember:
		delete(f.members, cmd.Key)

	case cmdSetLeader:
		var name string
		if err := json.Unmarshal(cmd.Value, &name); err != nil {
			return err
		}
		f.leader = &types.Leader{
			Version:    f.version,
			MemberName: name,
		}
		// Link to member if exists
		if m, ok := f.members[name]; ok {
			f.leader.Member = m
		}

	case cmdDeleteLeader:
		f.leader = nil

	case cmdSetConfig:
		var data map[string]interface{}
		if err := json.Unmarshal(cmd.Value, &data); err != nil {
			return err
		}
		f.config = &types.ClusterConfig{
			Version: f.version,
			Data:    data,
		}

	case cmdSetSync:
		var state types.SyncState
		if err := json.Unmarshal(cmd.Value, &state); err != nil {
			return err
		}
		state.Version = f.version
		f.sync = &state

	case cmdDeleteSync:
		f.sync = nil

	case cmdSetFailover:
		var failover types.Failover
		if err := json.Unmarshal(cmd.Value, &failover); err != nil {
			return err
		}
		failover.Version = f.version
		f.failover = &failover

	case cmdDeleteFailover:
		f.failover = nil

	case cmdSetHistory:
		var entries []types.TimelineEntry
		if err := json.Unmarshal(cmd.Value, &entries); err != nil {
			return err
		}
		f.history = &types.TimelineHistory{
			Version: f.version,
			Value:   entries,
		}

	case cmdInitialize:
		f.initVer = f.version

	case cmdDeleteCluster:
		f.members = make(map[string]*types.Member)
		f.leader = nil
		f.config = nil
		f.sync = nil
		f.failover = nil
		f.history = nil
		f.status = nil
		f.initVer = 0

	case cmdSetStatus:
		var status types.Status
		if err := json.Unmarshal(cmd.Value, &status); err != nil {
			return err
		}
		status.Version = f.version
		f.status = &status
	}

	return nil
}

// Snapshot returns a snapshot of the FSM.
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Create a deep copy of the state
	state := &fsmSnapshot{
		Members:  make(map[string]*types.Member),
		InitVer:  f.initVer,
		Version:  f.version,
	}

	for k, v := range f.members {
		memberCopy := *v
		state.Members[k] = &memberCopy
	}

	if f.leader != nil {
		leaderCopy := *f.leader
		state.Leader = &leaderCopy
	}

	if f.config != nil {
		configCopy := *f.config
		state.Config = &configCopy
	}

	if f.sync != nil {
		syncCopy := *f.sync
		state.Sync = &syncCopy
	}

	if f.failover != nil {
		failoverCopy := *f.failover
		state.Failover = &failoverCopy
	}

	if f.history != nil {
		historyCopy := *f.history
		state.History = &historyCopy
	}

	if f.status != nil {
		statusCopy := *f.status
		state.Status = &statusCopy
	}

	return state, nil
}

// Restore restores the FSM from a snapshot.
func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	var state fsmSnapshot
	if err := json.NewDecoder(rc).Decode(&state); err != nil {
		return fmt.Errorf("failed to decode snapshot: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.members = state.Members
	f.leader = state.Leader
	f.config = state.Config
	f.sync = state.Sync
	f.failover = state.Failover
	f.history = state.History
	f.status = state.Status
	f.initVer = state.InitVer
	f.version = state.Version

	return nil
}

// fsmSnapshot implements raft.FSMSnapshot.
type fsmSnapshot struct {
	Members  map[string]*types.Member    `json:"members"`
	Leader   *types.Leader               `json:"leader,omitempty"`
	Config   *types.ClusterConfig        `json:"config,omitempty"`
	Sync     *types.SyncState            `json:"sync,omitempty"`
	Failover *types.Failover             `json:"failover,omitempty"`
	History  *types.TimelineHistory      `json:"history,omitempty"`
	Status   *types.Status               `json:"status,omitempty"`
	InitVer  int64                       `json:"init_ver"`
	Version  int64                       `json:"version"`
}

// Persist writes the snapshot to the given sink.
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	err := func() error {
		data, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("failed to marshal snapshot: %w", err)
		}

		if _, err := sink.Write(data); err != nil {
			return fmt.Errorf("failed to write snapshot: %w", err)
		}

		return nil
	}()

	if err != nil {
		sink.Cancel()
		return err
	}

	return sink.Close()
}

// Release releases the snapshot resources.
func (s *fsmSnapshot) Release() {}
