// Package testutil provides mock implementations for testing.
package testutil

import (
	"context"
	"sync"
	"time"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

// Ensure MockDCS implements dcs.DCS interface.
var _ dcs.DCS = (*MockDCS)(nil)

// MockDCS is a mock implementation of dcs.DCS for testing.
type MockDCS struct {
	mu sync.RWMutex

	// Configurable state
	Cluster       *types.Cluster
	ConfigData    map[string]interface{}
	Initialized   bool
	IsLocked      bool
	LeaderName    string
	SyncStateData *types.SyncState
	FailoverData  *types.Failover
	HistoryData   []types.TimelineEntry

	// Error injection
	GetClusterErr         error
	TouchMemberErr        error
	TakeLockErr           error
	UpdateLeaderErr       error
	DeleteLeaderErr       error
	AcquireRenewLockErr   error
	SetFailoverErr        error
	DeleteFailoverErr     error
	SetConfigErr          error
	SetSyncStateErr       error
	DeleteSyncStateErr    error
	InitializeErr         error
	SetHistoryErr         error
	DeleteClusterErr      error
	WatchErr              error

	// Call tracking
	GetClusterCalls         int
	TouchMemberCalls        int
	TakeLockCalls           int
	UpdateLeaderCalls       int
	DeleteLeaderCalls       int
	AcquireRenewLockCalls   int
	SetFailoverCalls        int
	DeleteFailoverCalls     int
	SetConfigCalls          int
	SetSyncStateCalls       int
	DeleteSyncStateCalls    int
	InitializeCalls         int
	SetHistoryCalls         int
	DeleteClusterCalls      int
	WatchCalls              int
	CloseCalls              int

	// Last call arguments
	LastMemberData  *types.MemberData
	LastLeaderInfo  *types.MemberData
	LastFailover    *types.Failover
	LastConfig      map[string]interface{}
	LastSyncState   *types.SyncState
	LastHistory     []types.TimelineEntry
	LastSysID       string
}

// NewMockDCS creates a new MockDCS with sensible defaults.
func NewMockDCS() *MockDCS {
	return &MockDCS{
		Cluster: &types.Cluster{
			InitializeVersion: 1,
			Members:           []*types.Member{},
		},
		ConfigData: make(map[string]interface{}),
	}
}

// NewMockDCSWithCluster creates a MockDCS with a pre-configured cluster.
func NewMockDCSWithCluster(cluster *types.Cluster) *MockDCS {
	return &MockDCS{
		Cluster:    cluster,
		ConfigData: make(map[string]interface{}),
	}
}

// GetCluster retrieves the current cluster state from DCS.
func (m *MockDCS) GetCluster(ctx context.Context) (*types.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetClusterCalls++

	if m.GetClusterErr != nil {
		return nil, m.GetClusterErr
	}
	return m.Cluster, nil
}

// TouchMember updates this member's entry in DCS with current state.
func (m *MockDCS) TouchMember(ctx context.Context, data *types.MemberData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TouchMemberCalls++
	m.LastMemberData = data

	if m.TouchMemberErr != nil {
		return m.TouchMemberErr
	}
	return nil
}

// TakeLock attempts to acquire the leader lock.
func (m *MockDCS) TakeLock(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TakeLockCalls++

	if m.TakeLockErr != nil {
		return false, m.TakeLockErr
	}

	if m.IsLocked {
		return false, nil
	}

	m.IsLocked = true
	return true, nil
}

// UpdateLeader updates the leader key with new information.
func (m *MockDCS) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateLeaderCalls++
	m.LastLeaderInfo = leaderInfo

	if m.UpdateLeaderErr != nil {
		return m.UpdateLeaderErr
	}
	return nil
}

// DeleteLeader releases the leader lock.
func (m *MockDCS) DeleteLeader(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteLeaderCalls++

	if m.DeleteLeaderErr != nil {
		return m.DeleteLeaderErr
	}

	m.IsLocked = false
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (m *MockDCS) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AcquireRenewLockCalls++

	if m.AcquireRenewLockErr != nil {
		return false, m.AcquireRenewLockErr
	}

	m.IsLocked = true
	return true, nil
}

// SetFailoverValue sets/updates the failover key.
func (m *MockDCS) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetFailoverCalls++
	m.LastFailover = failover
	m.FailoverData = failover

	if m.SetFailoverErr != nil {
		return m.SetFailoverErr
	}
	return nil
}

// DeleteFailover deletes the failover key.
func (m *MockDCS) DeleteFailover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteFailoverCalls++

	if m.DeleteFailoverErr != nil {
		return m.DeleteFailoverErr
	}

	m.FailoverData = nil
	return nil
}

// SetConfigValue sets/updates the cluster configuration.
func (m *MockDCS) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetConfigCalls++
	m.LastConfig = config

	if m.SetConfigErr != nil {
		return m.SetConfigErr
	}

	m.ConfigData = config
	return nil
}

// SetSyncState sets the synchronous replication state.
func (m *MockDCS) SetSyncState(ctx context.Context, state *types.SyncState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetSyncStateCalls++
	m.LastSyncState = state

	if m.SetSyncStateErr != nil {
		return m.SetSyncStateErr
	}

	m.SyncStateData = state
	return nil
}

// DeleteSyncState deletes the sync state.
func (m *MockDCS) DeleteSyncState(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteSyncStateCalls++

	if m.DeleteSyncStateErr != nil {
		return m.DeleteSyncStateErr
	}

	m.SyncStateData = nil
	return nil
}

// Initialize initializes the cluster in DCS.
func (m *MockDCS) Initialize(ctx context.Context, sysID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InitializeCalls++
	m.LastSysID = sysID

	if m.InitializeErr != nil {
		return false, m.InitializeErr
	}

	if m.Initialized {
		return false, nil
	}

	m.Initialized = true
	return true, nil
}

// SetHistoryValue sets the timeline history.
func (m *MockDCS) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetHistoryCalls++
	m.LastHistory = history

	if m.SetHistoryErr != nil {
		return m.SetHistoryErr
	}

	m.HistoryData = history
	return nil
}

// DeleteCluster removes all cluster data from DCS.
func (m *MockDCS) DeleteCluster(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteClusterCalls++

	if m.DeleteClusterErr != nil {
		return m.DeleteClusterErr
	}

	m.Cluster = nil
	m.ConfigData = nil
	m.Initialized = false
	m.IsLocked = false
	return nil
}

// Watch returns a channel that receives cluster updates.
func (m *MockDCS) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WatchCalls++

	if m.WatchErr != nil {
		return nil, m.WatchErr
	}

	ch := make(chan *types.Cluster)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case <-time.After(timeout):
			return
		}
	}()

	return ch, nil
}

// Close closes the DCS connection.
func (m *MockDCS) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCalls++
	return nil
}

// Name returns the name of this DCS implementation.
func (m *MockDCS) Name() string {
	return "mock"
}

// SetCluster sets the cluster state for testing.
func (m *MockDCS) SetCluster(cluster *types.Cluster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Cluster = cluster
}

// SetLeader sets the leader for the cluster.
func (m *MockDCS) SetLeader(name string, member *types.Member) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Cluster == nil {
		m.Cluster = &types.Cluster{}
	}
	m.Cluster.Leader = &types.Leader{
		MemberName: name,
		Member:     member,
	}
	m.LeaderName = name
}

// AddMember adds a member to the cluster.
func (m *MockDCS) AddMember(member *types.Member) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Cluster == nil {
		m.Cluster = &types.Cluster{}
	}
	m.Cluster.Members = append(m.Cluster.Members, member)
}

// Reset resets all call counts and error injections.
func (m *MockDCS) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.GetClusterCalls = 0
	m.TouchMemberCalls = 0
	m.TakeLockCalls = 0
	m.UpdateLeaderCalls = 0
	m.DeleteLeaderCalls = 0
	m.AcquireRenewLockCalls = 0
	m.SetFailoverCalls = 0
	m.DeleteFailoverCalls = 0
	m.SetConfigCalls = 0
	m.SetSyncStateCalls = 0
	m.DeleteSyncStateCalls = 0
	m.InitializeCalls = 0
	m.SetHistoryCalls = 0
	m.DeleteClusterCalls = 0
	m.WatchCalls = 0
	m.CloseCalls = 0

	m.GetClusterErr = nil
	m.TouchMemberErr = nil
	m.TakeLockErr = nil
	m.UpdateLeaderErr = nil
	m.DeleteLeaderErr = nil
	m.AcquireRenewLockErr = nil
	m.SetFailoverErr = nil
	m.DeleteFailoverErr = nil
	m.SetConfigErr = nil
	m.SetSyncStateErr = nil
	m.DeleteSyncStateErr = nil
	m.InitializeErr = nil
	m.SetHistoryErr = nil
	m.DeleteClusterErr = nil
	m.WatchErr = nil
}
