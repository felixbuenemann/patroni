// Package testutil provides mock implementations for testing.
package testutil

import (
	"context"
	"sync"
	"time"

	"github.com/patroni/patroni-go/pkg/types"
)

// HAState represents the current state of the HA state machine.
type HAState int

const (
	HAStateStarting HAState = iota
	HAStateRunning
	HAStatePaused
	HAStateStopped
)

func (s HAState) String() string {
	states := []string{"starting", "running", "paused", "stopped"}
	if int(s) < len(states) {
		return states[s]
	}
	return "unknown"
}

// ScheduledRestart represents a scheduled restart.
type ScheduledRestart struct {
	Schedule            time.Time `json:"schedule"`
	PostmasterStartTime time.Time `json:"postmaster_start_time,omitempty"`
	Pending             bool      `json:"pending"`
}

// MockHA is a mock implementation of HA for testing.
type MockHA struct {
	mu sync.RWMutex

	// Configurable state
	state            HAState
	isLeader         bool
	isPaused         bool
	isBusy           bool
	cluster          *types.Cluster
	config           map[string]interface{}
	scheduledRestart *ScheduledRestart
	effectiveTags    types.Tags

	// Error injection
	RunErr                   error
	ReinitializeErr          error
	ManualFailoverErr        error
	CancelFailoverErr        error
	SetConfigErr             error
	ScheduleRestartErr       error
	CancelScheduledRestartErr error

	// Call tracking
	RunCalls                   int
	StopCalls                  int
	PauseCalls                 int
	ResumeCalls                int
	WakeupCalls                int
	ReinitializeCalls          int
	ManualFailoverCalls        int
	CancelFailoverCalls        int
	SetConfigCalls             int
	GetConfigCalls             int
	ScheduleRestartCalls       int
	CancelScheduledRestartCalls int
	GetScheduledRestartCalls   int
	GetEffectiveTagsCalls      int
	GetClusterCalls            int

	// Last call arguments
	LastReinitForce     bool
	LastFailoverLeader  string
	LastFailoverCandidate string
	LastFailoverSchedule *time.Time
	LastConfig          map[string]interface{}
	LastRestartSchedule time.Time
	LastRestartPostmaster time.Time
}

// NewMockHA creates a new MockHA with sensible defaults.
func NewMockHA() *MockHA {
	return &MockHA{
		state:    HAStateStarting,
		isLeader: false,
		isPaused: false,
		isBusy:   false,
		cluster: &types.Cluster{
			InitializeVersion: 1,
			Members:           []*types.Member{},
		},
		config:        make(map[string]interface{}),
		effectiveTags: types.Tags{},
	}
}

// Run starts the HA main loop (mock version just returns).
func (m *MockHA) Run(ctx context.Context) error {
	m.mu.Lock()
	m.RunCalls++
	m.state = HAStateRunning
	m.mu.Unlock()

	if m.RunErr != nil {
		return m.RunErr
	}

	// Wait for context cancellation
	<-ctx.Done()

	m.mu.Lock()
	m.state = HAStateStopped
	m.mu.Unlock()

	return ctx.Err()
}

// State returns the current HA state.
func (m *MockHA) State() HAState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// IsLeader returns true if this node is the leader.
func (m *MockHA) IsLeader() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isLeader
}

// IsPaused returns true if the HA loop is paused.
func (m *MockHA) IsPaused() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isPaused
}

// IsBusy returns true if the HA is busy with an operation.
func (m *MockHA) IsBusy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isBusy
}

// Stop stops the HA loop.
func (m *MockHA) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StopCalls++
	m.state = HAStateStopped
}

// Pause pauses the HA loop.
func (m *MockHA) Pause() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PauseCalls++
	m.state = HAStatePaused
	m.isPaused = true
}

// Resume resumes the HA loop.
func (m *MockHA) Resume() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ResumeCalls++
	m.state = HAStateRunning
	m.isPaused = false
}

// Wakeup wakes up the HA loop to run immediately.
func (m *MockHA) Wakeup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WakeupCalls++
}

// GetCluster returns the current cluster state.
func (m *MockHA) GetCluster() *types.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.GetClusterCalls++
	return m.cluster
}

// GetConfig returns the current cluster configuration.
func (m *MockHA) GetConfig() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.GetConfigCalls++
	return m.config
}

// SetConfig sets the cluster configuration.
func (m *MockHA) SetConfig(ctx context.Context, cfg map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetConfigCalls++
	m.LastConfig = cfg

	if m.SetConfigErr != nil {
		return m.SetConfigErr
	}

	m.config = cfg
	return nil
}

// Reinitialize triggers a reinitialize of this member.
func (m *MockHA) Reinitialize(ctx context.Context, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReinitializeCalls++
	m.LastReinitForce = force

	if m.ReinitializeErr != nil {
		return m.ReinitializeErr
	}
	return nil
}

// ManualFailover triggers a manual failover.
func (m *MockHA) ManualFailover(ctx context.Context, leader, candidate string, scheduledAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ManualFailoverCalls++
	m.LastFailoverLeader = leader
	m.LastFailoverCandidate = candidate
	m.LastFailoverSchedule = scheduledAt

	if m.ManualFailoverErr != nil {
		return m.ManualFailoverErr
	}
	return nil
}

// CancelFailover cancels a pending failover.
func (m *MockHA) CancelFailover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CancelFailoverCalls++

	if m.CancelFailoverErr != nil {
		return m.CancelFailoverErr
	}
	return nil
}

// ScheduleRestart schedules a restart at the specified time.
func (m *MockHA) ScheduleRestart(schedule time.Time, postmasterStartTime time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ScheduleRestartCalls++
	m.LastRestartSchedule = schedule
	m.LastRestartPostmaster = postmasterStartTime

	if m.ScheduleRestartErr != nil {
		return m.ScheduleRestartErr
	}

	m.scheduledRestart = &ScheduledRestart{
		Schedule:            schedule,
		PostmasterStartTime: postmasterStartTime,
		Pending:             true,
	}
	return nil
}

// CancelScheduledRestart cancels any scheduled restart.
func (m *MockHA) CancelScheduledRestart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CancelScheduledRestartCalls++

	if m.CancelScheduledRestartErr != nil {
		return m.CancelScheduledRestartErr
	}

	m.scheduledRestart = nil
	return nil
}

// GetScheduledRestart returns the scheduled restart info.
func (m *MockHA) GetScheduledRestart() *ScheduledRestart {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.GetScheduledRestartCalls++
	return m.scheduledRestart
}

// GetEffectiveTags returns the effective tags for this node.
func (m *MockHA) GetEffectiveTags() types.Tags {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.GetEffectiveTagsCalls++
	return m.effectiveTags
}

// SetState sets the HA state for testing.
func (m *MockHA) SetState(state HAState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

// SetLeader sets the leader state for testing.
func (m *MockHA) SetLeader(isLeader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isLeader = isLeader
}

// SetPaused sets the paused state for testing.
func (m *MockHA) SetPaused(isPaused bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isPaused = isPaused
	if isPaused {
		m.state = HAStatePaused
	} else {
		m.state = HAStateRunning
	}
}

// SetBusy sets the busy state for testing.
func (m *MockHA) SetBusy(isBusy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isBusy = isBusy
}

// SetCluster sets the cluster state for testing.
func (m *MockHA) SetCluster(cluster *types.Cluster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cluster = cluster
}

// SetEffectiveTags sets the effective tags for testing.
func (m *MockHA) SetEffectiveTags(tags types.Tags) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.effectiveTags = tags
}

// SetScheduledRestart sets the scheduled restart for testing.
func (m *MockHA) SetScheduledRestart(restart *ScheduledRestart) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduledRestart = restart
}

// Reset resets all call counts and error injections.
func (m *MockHA) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RunCalls = 0
	m.StopCalls = 0
	m.PauseCalls = 0
	m.ResumeCalls = 0
	m.WakeupCalls = 0
	m.ReinitializeCalls = 0
	m.ManualFailoverCalls = 0
	m.CancelFailoverCalls = 0
	m.SetConfigCalls = 0
	m.GetConfigCalls = 0
	m.ScheduleRestartCalls = 0
	m.CancelScheduledRestartCalls = 0
	m.GetScheduledRestartCalls = 0
	m.GetEffectiveTagsCalls = 0
	m.GetClusterCalls = 0

	m.RunErr = nil
	m.ReinitializeErr = nil
	m.ManualFailoverErr = nil
	m.CancelFailoverErr = nil
	m.SetConfigErr = nil
	m.ScheduleRestartErr = nil
	m.CancelScheduledRestartErr = nil
}
