// Package testutil provides mock implementations for testing.
package testutil

import (
	"context"
	"sync"

	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/pkg/types"
)

// MockPostgresql is a mock implementation of PostgreSQL for testing.
type MockPostgresql struct {
	mu sync.RWMutex

	// Configurable state
	name         string
	scope        string
	dataDir      string
	majorVersion int
	sysID        string
	state        types.PostgresqlState
	role         types.PostgresqlRole
	running      bool
	primary      bool
	timeline     int
	walPosition  int64
	memberData   *types.MemberData

	// Pending restart
	pendingRestart       bool
	pendingRestartReason map[string]interface{}

	// Error injection
	StartErr    error
	StopErr     error
	RestartErr  error
	ReloadErr   error
	PromoteErr  error
	DemoteErr   error
	BootstrapErr error
	CloneErr    error
	QueryErr    error
	ExecErr     error

	// Call tracking
	StartCalls     int
	StopCalls      int
	RestartCalls   int
	ReloadCalls    int
	PromoteCalls   int
	DemoteCalls    int
	BootstrapCalls int
	CloneCalls     int
	QueryCalls     int
	ExecCalls      int
	KillCalls      int
	CloseCalls     int

	// Last call arguments
	LastStopMode        string
	LastPrimaryConnInfo string
	LastQuery           string
}

// NewMockPostgresql creates a new MockPostgresql with sensible defaults.
func NewMockPostgresql(name, scope string) *MockPostgresql {
	return &MockPostgresql{
		name:                 name,
		scope:                scope,
		dataDir:              "/var/lib/postgresql/data",
		majorVersion:         150000,
		sysID:                "1234567890123456789",
		state:                types.PostgresqlStateStopped,
		role:                 types.PostgresqlRoleUninitialized,
		running:              false,
		primary:              false,
		timeline:             1,
		walPosition:          0,
		pendingRestartReason: make(map[string]interface{}),
	}
}

// Name returns the node name.
func (m *MockPostgresql) Name() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.name
}

// Scope returns the cluster scope.
func (m *MockPostgresql) Scope() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scope
}

// DataDir returns the data directory path.
func (m *MockPostgresql) DataDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dataDir
}

// MajorVersion returns the PostgreSQL major version.
func (m *MockPostgresql) MajorVersion() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.majorVersion
}

// SysID returns the database system identifier.
func (m *MockPostgresql) SysID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sysID
}

// State returns the current PostgreSQL state.
func (m *MockPostgresql) State() types.PostgresqlState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Role returns the current PostgreSQL role.
func (m *MockPostgresql) Role() types.PostgresqlRole {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.role
}

// IsRunning returns true if PostgreSQL is running.
func (m *MockPostgresql) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// IsPrimary returns true if this node is the primary.
func (m *MockPostgresql) IsPrimary() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.primary
}

// Start starts PostgreSQL.
func (m *MockPostgresql) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StartCalls++

	if m.StartErr != nil {
		return m.StartErr
	}

	m.running = true
	m.state = types.PostgresqlStateRunning
	return nil
}

// Stop stops PostgreSQL.
func (m *MockPostgresql) Stop(ctx context.Context, mode postgresql.StopMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StopCalls++
	m.LastStopMode = string(mode)

	if m.StopErr != nil {
		return m.StopErr
	}

	m.running = false
	m.state = types.PostgresqlStateStopped
	return nil
}

// Restart restarts PostgreSQL.
func (m *MockPostgresql) Restart(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RestartCalls++

	if m.RestartErr != nil {
		return m.RestartErr
	}

	m.running = true
	m.state = types.PostgresqlStateRunning
	m.pendingRestart = false
	return nil
}

// Reload reloads PostgreSQL configuration.
func (m *MockPostgresql) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReloadCalls++

	if m.ReloadErr != nil {
		return m.ReloadErr
	}
	return nil
}

// Promote promotes this node to primary.
func (m *MockPostgresql) Promote(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PromoteCalls++

	if m.PromoteErr != nil {
		return m.PromoteErr
	}

	m.role = types.PostgresqlRolePromoted
	m.primary = true
	return nil
}

// Demote demotes this node to replica.
func (m *MockPostgresql) Demote(ctx context.Context, primaryConnInfo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DemoteCalls++
	m.LastPrimaryConnInfo = primaryConnInfo

	if m.DemoteErr != nil {
		return m.DemoteErr
	}

	m.role = types.PostgresqlRoleDemoted
	m.primary = false
	return nil
}

// Bootstrap initializes a new PostgreSQL cluster.
func (m *MockPostgresql) Bootstrap(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BootstrapCalls++

	if m.BootstrapErr != nil {
		return m.BootstrapErr
	}

	m.role = types.PostgresqlRolePrimary
	m.running = true
	m.state = types.PostgresqlStateRunning
	m.primary = true
	return nil
}

// Clone clones from primary.
func (m *MockPostgresql) Clone(ctx context.Context, primaryConnInfo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloneCalls++
	m.LastPrimaryConnInfo = primaryConnInfo

	if m.CloneErr != nil {
		return m.CloneErr
	}

	m.role = types.PostgresqlRoleReplica
	return nil
}

// GetMemberData returns the member data for DCS updates.
func (m *MockPostgresql) GetMemberData() *types.MemberData {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.memberData != nil {
		return m.memberData
	}

	return &types.MemberData{
		ConnURL:      "postgres://localhost:5432/postgres",
		APIURL:       "http://localhost:8008/patroni",
		State:        m.state.String(),
		Role:         m.role.String(),
		Timeline:     m.timeline,
		XlogLocation: m.walPosition,
		Version:      types.Version,
	}
}

// TimelineWALPosition returns the timeline and WAL position.
func (m *MockPostgresql) TimelineWALPosition() (int, int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.timeline, m.walPosition
}

// LastOperation returns the last WAL position.
func (m *MockPostgresql) LastOperation() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.walPosition
}

// GetVersion returns the PostgreSQL major version as an integer.
func (m *MockPostgresql) GetVersion() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.majorVersion
}

// GetControlData returns the pg_controldata output as a map.
func (m *MockPostgresql) GetControlData() (map[string]string, error) {
	return map[string]string{
		"Database system identifier": m.sysID,
		"pg_control version number":  "1300",
		"Catalog version number":     "202107181",
	}, nil
}

// SupportsTimelines returns true if this PostgreSQL version supports timelines.
func (m *MockPostgresql) SupportsTimelines() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.majorVersion >= 90300
}

// IsPendingRestart returns true if a restart is pending.
func (m *MockPostgresql) IsPendingRestart() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingRestart
}

// SetPendingRestart sets the pending restart flag.
func (m *MockPostgresql) SetPendingRestart(pending bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingRestart = pending
}

// PendingRestartReason returns the reason for pending restart.
func (m *MockPostgresql) PendingRestartReason() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingRestartReason
}

// Kill sends a signal to the PostgreSQL process.
func (m *MockPostgresql) Kill(signal int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.KillCalls++
	return nil
}

// Close closes the PostgreSQL manager.
func (m *MockPostgresql) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCalls++
	return nil
}

// CloseConnections closes all database connections.
func (m *MockPostgresql) CloseConnections() {
	// No-op for mock
}

// SetState sets the PostgreSQL state for testing.
func (m *MockPostgresql) SetState(state types.PostgresqlState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

// SetRole sets the PostgreSQL role for testing.
func (m *MockPostgresql) SetRole(role types.PostgresqlRole) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.role = role
}

// SetRunning sets the running state for testing.
func (m *MockPostgresql) SetRunning(running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = running
	if running {
		m.state = types.PostgresqlStateRunning
	} else {
		m.state = types.PostgresqlStateStopped
	}
}

// SetPrimary sets the primary state for testing.
func (m *MockPostgresql) SetPrimary(primary bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.primary = primary
	if primary {
		m.role = types.PostgresqlRolePrimary
	} else {
		m.role = types.PostgresqlRoleReplica
	}
}

// SetTimeline sets the timeline for testing.
func (m *MockPostgresql) SetTimeline(timeline int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeline = timeline
}

// SetWALPosition sets the WAL position for testing.
func (m *MockPostgresql) SetWALPosition(pos int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.walPosition = pos
}

// SetMemberData sets custom member data for testing.
func (m *MockPostgresql) SetMemberData(data *types.MemberData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memberData = data
}

// SetDataDir sets the data directory for testing.
func (m *MockPostgresql) SetDataDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dataDir = dir
}

// SetMajorVersion sets the major version for testing.
func (m *MockPostgresql) SetMajorVersion(version int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.majorVersion = version
}

// Reset resets all call counts and error injections.
func (m *MockPostgresql) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.StartCalls = 0
	m.StopCalls = 0
	m.RestartCalls = 0
	m.ReloadCalls = 0
	m.PromoteCalls = 0
	m.DemoteCalls = 0
	m.BootstrapCalls = 0
	m.CloneCalls = 0
	m.QueryCalls = 0
	m.ExecCalls = 0
	m.KillCalls = 0
	m.CloseCalls = 0

	m.StartErr = nil
	m.StopErr = nil
	m.RestartErr = nil
	m.ReloadErr = nil
	m.PromoteErr = nil
	m.DemoteErr = nil
	m.BootstrapErr = nil
	m.CloneErr = nil
	m.QueryErr = nil
	m.ExecErr = nil
}
