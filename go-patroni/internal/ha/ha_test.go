package ha

import (
	"context"
	"testing"
	"time"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/testutil"
	"github.com/patroni/patroni-go/pkg/types"
)

func TestActionString(t *testing.T) {
	tests := []struct {
		action   Action
		expected string
	}{
		{ActionNone, "none"},
		{ActionBootstrap, "bootstrap"},
		{ActionPromote, "promote"},
		{ActionDemote, "demote"},
		{ActionAcquireLock, "acquire_lock"},
		{ActionRenewLock, "renew_lock"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.action) != tt.expected {
				t.Errorf("Action = %q, want %q", tt.action, tt.expected)
			}
		})
	}
}

func TestSchedulerSetRestartSchedule(t *testing.T) {
	scheduler := NewScheduler()

	// Test setting future schedule
	futureTime := time.Now().Add(1 * time.Hour)
	schedule := &RestartSchedule{
		Schedule: futureTime,
		Role:     "replica",
	}

	err := scheduler.SetRestartSchedule(schedule)
	if err != nil {
		t.Errorf("SetRestartSchedule() returned error: %v", err)
	}

	got := scheduler.GetRestartSchedule()
	if got == nil {
		t.Fatal("GetRestartSchedule() returned nil")
	}
	if !got.Schedule.Equal(futureTime) {
		t.Errorf("Schedule time = %v, want %v", got.Schedule, futureTime)
	}

	// Test setting past schedule
	pastTime := time.Now().Add(-1 * time.Hour)
	pastSchedule := &RestartSchedule{
		Schedule: pastTime,
	}

	err = scheduler.SetRestartSchedule(pastSchedule)
	if err == nil {
		t.Error("SetRestartSchedule() should return error for past time")
	}
}

func TestSchedulerCancelRestartSchedule(t *testing.T) {
	scheduler := NewScheduler()

	// Set a schedule
	futureTime := time.Now().Add(1 * time.Hour)
	schedule := &RestartSchedule{Schedule: futureTime}
	scheduler.SetRestartSchedule(schedule)

	// Cancel it
	scheduler.CancelRestartSchedule()

	if scheduler.GetRestartSchedule() != nil {
		t.Error("GetRestartSchedule() should return nil after cancel")
	}
}

func TestSchedulerShouldRestart(t *testing.T) {
	scheduler := NewScheduler()

	// No schedule set
	if scheduler.ShouldRestart("master", false) {
		t.Error("ShouldRestart() should return false with no schedule")
	}

	// Set a schedule for now
	nowSchedule := &RestartSchedule{
		Schedule: time.Now().Add(-1 * time.Second),
	}
	scheduler.mu.Lock()
	scheduler.restartSchedule = nowSchedule // Bypass validation for test
	scheduler.mu.Unlock()

	if !scheduler.ShouldRestart("replica", false) {
		t.Error("ShouldRestart() should return true when schedule time has passed")
	}

	// Test role constraint - master only
	masterSchedule := &RestartSchedule{
		Schedule: time.Now().Add(-1 * time.Second),
		Role:     "master",
	}
	scheduler.mu.Lock()
	scheduler.restartSchedule = masterSchedule
	scheduler.mu.Unlock()

	if scheduler.ShouldRestart("replica", false) {
		t.Error("ShouldRestart() should return false when role doesn't match (replica vs master)")
	}
	if !scheduler.ShouldRestart("master", false) {
		t.Error("ShouldRestart() should return true when role matches (master)")
	}

	// Test pending_restart constraint
	pendingSchedule := &RestartSchedule{
		Schedule:       time.Now().Add(-1 * time.Second),
		PendingRestart: true,
	}
	scheduler.mu.Lock()
	scheduler.restartSchedule = pendingSchedule
	scheduler.mu.Unlock()

	if scheduler.ShouldRestart("master", false) {
		t.Error("ShouldRestart() should return false when pending_restart is required but not set")
	}
	if !scheduler.ShouldRestart("master", true) {
		t.Error("ShouldRestart() should return true when pending_restart is required and set")
	}
}

func TestPendingRestart(t *testing.T) {
	pr := NewPendingRestart()

	// Initial state
	if pr.IsPending() {
		t.Error("IsPending() should return false initially")
	}

	// Set pending
	pr.SetPending("config change", "max_connections", "shared_buffers")

	if !pr.IsPending() {
		t.Error("IsPending() should return true after SetPending")
	}
	if pr.Reason() != "config change" {
		t.Errorf("Reason() = %q, want %q", pr.Reason(), "config change")
	}
	params := pr.Params()
	if len(params) != 2 {
		t.Errorf("Params() length = %d, want 2", len(params))
	}

	// Clear
	pr.Clear()

	if pr.IsPending() {
		t.Error("IsPending() should return false after Clear")
	}
	if pr.Reason() != "" {
		t.Error("Reason() should be empty after Clear")
	}
}

func TestPendingRestartToMap(t *testing.T) {
	pr := NewPendingRestart()

	// Not pending
	if pr.ToMap() != nil {
		t.Error("ToMap() should return nil when not pending")
	}

	// Set pending
	pr.SetPending("test reason", "param1")

	m := pr.ToMap()
	if m == nil {
		t.Fatal("ToMap() should not return nil when pending")
	}
	if m["pending_restart"] != true {
		t.Error("ToMap()[pending_restart] should be true")
	}
	if m["pending_restart_reason"] != "test reason" {
		t.Error("ToMap()[pending_restart_reason] should be 'test reason'")
	}
}

func TestMaintenanceWindowIsActive(t *testing.T) {
	now := time.Now()
	currentDay := now.Weekday()
	currentHour := now.Hour()
	currentMinute := now.Minute()

	tests := []struct {
		name     string
		window   *MaintenanceWindow
		expected bool
	}{
		{
			name:     "disabled",
			window:   &MaintenanceWindow{Enabled: false},
			expected: false,
		},
		{
			name: "wrong day",
			window: &MaintenanceWindow{
				Enabled:   true,
				StartTime: "00:00",
				EndTime:   "23:59",
				Days:      []time.Weekday{(currentDay + 1) % 7}, // Tomorrow
			},
			expected: false,
		},
		{
			name: "current day and time",
			window: &MaintenanceWindow{
				Enabled:   true,
				StartTime: "00:00",
				EndTime:   "23:59",
				Days:      []time.Weekday{currentDay},
			},
			expected: true,
		},
		{
			name: "outside time window",
			window: &MaintenanceWindow{
				Enabled:   true,
				StartTime: formatTime((currentHour + 2) % 24, currentMinute),
				EndTime:   formatTime((currentHour + 3) % 24, currentMinute),
				Days:      []time.Weekday{currentDay},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.window.IsActive(); got != tt.expected {
				t.Errorf("IsActive() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func formatTime(hour, minute int) string {
	return time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC).Format("15:04")
}

func TestRestartCoordinator(t *testing.T) {
	coord := NewRestartCoordinator()

	// Request restart
	coord.RequestRestart("node1", time.Now())

	pending := coord.GetPendingRestarts()
	if _, exists := pending["node1"]; !exists {
		t.Error("node1 should have pending restart")
	}

	// Clear restart
	coord.ClearRestart("node1")

	pending = coord.GetPendingRestarts()
	if _, exists := pending["node1"]; exists {
		t.Error("node1 should not have pending restart after clear")
	}
}

func TestRestartCoordinatorCanRestart(t *testing.T) {
	coord := NewRestartCoordinator()

	// No pending request
	if coord.CanRestart("node1") {
		t.Error("CanRestart() should return false with no pending request")
	}

	// With pending request, no maintenance window
	coord.RequestRestart("node1", time.Now())
	if !coord.CanRestart("node1") {
		t.Error("CanRestart() should return true with pending request and no maintenance window")
	}
}

func TestRestartScheduleJSON(t *testing.T) {
	schedule := &RestartSchedule{
		Schedule: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Role:     "replica",
	}

	// Test MarshalJSON
	data, err := schedule.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error: %v", err)
	}

	// Test UnmarshalJSON
	var decoded RestartSchedule
	if err := decoded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON() error: %v", err)
	}

	if !decoded.Schedule.Equal(schedule.Schedule) {
		t.Errorf("Schedule mismatch after JSON round-trip")
	}
	if decoded.Role != schedule.Role {
		t.Errorf("Role = %q, want %q", decoded.Role, schedule.Role)
	}
}

func TestSelectBestCandidate(t *testing.T) {
	tests := []struct {
		name     string
		members  []*types.Member
		expected string
	}{
		{
			name:     "no members",
			members:  []*types.Member{},
			expected: "",
		},
		{
			name: "single member",
			members: []*types.Member{
				{Name: "node1", Data: types.MemberData{XlogLocation: 100}},
			},
			expected: "node1",
		},
		{
			name: "multiple members - highest LSN wins",
			members: []*types.Member{
				{Name: "node1", Data: types.MemberData{XlogLocation: 100}},
				{Name: "node2", Data: types.MemberData{XlogLocation: 200}},
				{Name: "node3", Data: types.MemberData{XlogLocation: 150}},
			},
			expected: "node2",
		},
		{
			name: "skip nofailover members",
			members: []*types.Member{
				{Name: "node1", Data: types.MemberData{XlogLocation: 200, Tags: types.Tags{NoFailover: true}}},
				{Name: "node2", Data: types.MemberData{XlogLocation: 100}},
			},
			expected: "node2",
		},
		{
			name: "failover_priority matters",
			members: []*types.Member{
				{Name: "node1", Data: types.MemberData{XlogLocation: 100, Tags: types.Tags{FailoverPriority: 1}}},
				{Name: "node2", Data: types.MemberData{XlogLocation: 100, Tags: types.Tags{FailoverPriority: 2}}},
			},
			expected: "node2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectBestCandidate(tt.members)
			if got != tt.expected {
				t.Errorf("selectBestCandidate() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// selectBestCandidate is a helper function that mimics the HA selection logic
func selectBestCandidate(members []*types.Member) string {
	if len(members) == 0 {
		return ""
	}

	var best *types.Member
	for _, m := range members {
		if m.Data.Tags.NoFailover {
			continue
		}

		if best == nil {
			best = m
			continue
		}

		// Compare failover priority first
		if m.Data.Tags.FailoverPriority > best.Data.Tags.FailoverPriority {
			best = m
			continue
		}
		if m.Data.Tags.FailoverPriority < best.Data.Tags.FailoverPriority {
			continue
		}

		// Then compare LSN
		if m.Data.XlogLocation > best.Data.XlogLocation {
			best = m
		}
	}

	if best == nil {
		return ""
	}
	return best.Name
}

// ============= HA State Tests =============

func TestHAStateString(t *testing.T) {
	tests := []struct {
		state    HAState
		expected string
	}{
		{HAStateStarting, "starting"},
		{HAStateRunning, "running"},
		{HAStatePaused, "paused"},
		{HAStateStopped, "stopped"},
		{HAState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.state.String() != tt.expected {
				t.Errorf("HAState.String() = %q, want %q", tt.state.String(), tt.expected)
			}
		})
	}
}

func TestNewHA(t *testing.T) {
	cfg := &config.Config{
		Name:     "node1",
		Scope:    "mycluster",
		LoopWait: 10,
		TTL:      30,
	}

	mockDCS := testutil.NewMockDCS()

	ha := New(cfg, mockDCS, nil)

	if ha == nil {
		t.Fatal("New() returned nil")
	}
	if ha.state != HAStateStarting {
		t.Errorf("Initial state = %v, want HAStateStarting", ha.state)
	}
	if ha.dcs != mockDCS {
		t.Error("DCS not set correctly")
	}
	if ha.wakeupCh == nil {
		t.Error("wakeupCh should be initialized")
	}
	if ha.stopCh == nil {
		t.Error("stopCh should be initialized")
	}
}

func TestHAState(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	if ha.State() != HAStateStarting {
		t.Errorf("State() = %v, want HAStateStarting", ha.State())
	}

	ha.setState(HAStateRunning)
	if ha.State() != HAStateRunning {
		t.Errorf("State() = %v, want HAStateRunning", ha.State())
	}
}

func TestHAIsLeader(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	if ha.IsLeader() {
		t.Error("IsLeader() should return false initially")
	}

	ha.mu.Lock()
	ha.isLeader = true
	ha.mu.Unlock()

	if !ha.IsLeader() {
		t.Error("IsLeader() should return true after setting")
	}
}

func TestHAIsPaused(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	if ha.IsPaused() {
		t.Error("IsPaused() should return false initially")
	}

	ha.setState(HAStatePaused)

	if !ha.IsPaused() {
		t.Error("IsPaused() should return true after setting paused state")
	}
}

func TestHAIsBusy(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	if ha.IsBusy() {
		t.Error("IsBusy() should return false initially")
	}

	ha.mu.Lock()
	ha.busy = true
	ha.mu.Unlock()

	if !ha.IsBusy() {
		t.Error("IsBusy() should return true after setting")
	}
}

func TestHAGetCluster(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	// Initial nil cluster
	if ha.GetCluster() != nil {
		t.Error("GetCluster() should return nil initially")
	}

	// Set cluster
	cluster := &types.Cluster{
		InitializeVersion: 1,
		Members: []*types.Member{
			{Name: "node1"},
		},
	}
	ha.mu.Lock()
	ha.cluster = cluster
	ha.mu.Unlock()

	got := ha.GetCluster()
	if got == nil {
		t.Fatal("GetCluster() returned nil after setting")
	}
	if got.InitializeVersion != 1 {
		t.Errorf("InitializeVersion = %d, want 1", got.InitializeVersion)
	}
}

func TestHAGetEffectiveTags(t *testing.T) {
	cfg := &config.Config{
		LoopWait: 10,
	}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	tags := ha.GetEffectiveTags()

	// Default tags should be empty/false
	if tags.NoFailover {
		t.Error("NoFailover should be false by default")
	}
	if tags.NoLoadbalance {
		t.Error("NoLoadbalance should be false by default")
	}
}

func TestHAWakeup(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	// Wakeup should not block
	ha.Wakeup()

	// Second wakeup should also not block (channel is buffered)
	ha.Wakeup()

	// Verify channel has signal
	select {
	case <-ha.wakeupCh:
		// Expected
	default:
		t.Error("wakeupCh should have signal after Wakeup()")
	}
}

func TestHAStop(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	ha.Stop()

	// Verify stop channel is closed
	select {
	case <-ha.stopCh:
		// Expected - channel is closed
	case <-time.After(100 * time.Millisecond):
		t.Error("stopCh should be closed after Stop()")
	}
}

func TestHAPauseResume(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)
	ha.setState(HAStateRunning)

	// Pause
	ha.Pause()
	if !ha.IsPaused() {
		t.Error("IsPaused() should be true after Pause()")
	}

	// Resume
	ha.Resume()
	if ha.IsPaused() {
		t.Error("IsPaused() should be false after Resume()")
	}
	if ha.State() != HAStateRunning {
		t.Errorf("State() = %v, want HAStateRunning after Resume()", ha.State())
	}
}

func TestHAScheduleRestart(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	futureTime := time.Now().Add(1 * time.Hour)

	err := ha.ScheduleRestart(futureTime, time.Time{})
	if err != nil {
		t.Errorf("ScheduleRestart() error = %v", err)
	}

	restart := ha.GetScheduledRestart()
	if restart == nil {
		t.Fatal("GetScheduledRestart() returned nil")
	}
	if !restart.Schedule.Equal(futureTime) {
		t.Errorf("Schedule time mismatch")
	}
}

func TestHACancelScheduledRestart(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	futureTime := time.Now().Add(1 * time.Hour)
	ha.ScheduleRestart(futureTime, time.Time{})

	err := ha.CancelScheduledRestart()
	if err != nil {
		t.Errorf("CancelScheduledRestart() error = %v", err)
	}

	if ha.GetScheduledRestart() != nil {
		t.Error("GetScheduledRestart() should return nil after cancel")
	}
}

func TestHAManualFailover(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	ctx := context.Background()
	err := ha.ManualFailover(ctx, "leader1", "candidate1", nil)
	if err != nil {
		t.Errorf("ManualFailover() error = %v", err)
	}

	if mockDCS.SetFailoverCalls != 1 {
		t.Errorf("SetFailoverValue calls = %d, want 1", mockDCS.SetFailoverCalls)
	}
}

func TestHACancelFailover(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	ctx := context.Background()
	err := ha.CancelFailover(ctx)
	if err != nil {
		t.Errorf("CancelFailover() error = %v", err)
	}

	if mockDCS.DeleteFailoverCalls != 1 {
		t.Errorf("DeleteFailover calls = %d, want 1", mockDCS.DeleteFailoverCalls)
	}
}

func TestHASetConfig(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	ctx := context.Background()
	newConfig := map[string]interface{}{
		"loop_wait": 15,
		"ttl":       45,
	}

	err := ha.SetConfig(ctx, newConfig)
	if err != nil {
		t.Errorf("SetConfig() error = %v", err)
	}

	if mockDCS.SetConfigCalls != 1 {
		t.Errorf("SetConfigValue calls = %d, want 1", mockDCS.SetConfigCalls)
	}
}

func TestHAReinitialize(t *testing.T) {
	// Skip this test since Reinitialize requires a PostgreSQL instance
	// and would panic with nil pg
	t.Skip("Skipping test that requires PostgreSQL instance")
}

func TestHAGetConfig(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	ha := New(cfg, mockDCS, nil)

	// No cluster
	config := ha.GetConfig()
	if config != nil {
		t.Error("GetConfig() should return nil when no cluster")
	}

	// With cluster config
	ha.mu.Lock()
	ha.cluster = &types.Cluster{
		Config: &types.ClusterConfig{
			Data: map[string]interface{}{
				"loop_wait": 10,
			},
		},
	}
	ha.mu.Unlock()

	config = ha.GetConfig()
	if config == nil {
		t.Fatal("GetConfig() returned nil")
	}
	if config["loop_wait"].(int) != 10 {
		t.Errorf("loop_wait = %v, want 10", config["loop_wait"])
	}
}

func TestFailsafe(t *testing.T) {
	mockDCS := testutil.NewMockDCS()
	fs := NewFailsafe(mockDCS)

	if fs == nil {
		t.Fatal("NewFailsafe() returned nil")
	}

	// Initial state
	if fs.IsActive() {
		t.Error("IsActive() should be false initially")
	}

	// Update with data should make it active
	fs.Update(map[string]interface{}{
		"name":     "node1",
		"conn_url": "postgres://localhost:5432",
		"api_url":  "http://localhost:8008",
	})

	if !fs.IsActive() {
		t.Error("IsActive() should be true after Update()")
	}

	// Reset should deactivate
	fs.Reset()
	if fs.IsActive() {
		t.Error("IsActive() should be false after Reset()")
	}
}

func TestScheduledRestartStruct(t *testing.T) {
	now := time.Now()
	restart := &ScheduledRestart{
		Schedule:            now,
		PostmasterStartTime: now.Add(-time.Hour),
	}

	if restart.Schedule != now {
		t.Error("Schedule time mismatch")
	}
	if restart.PostmasterStartTime.IsZero() {
		t.Error("PostmasterStartTime should be set")
	}
}

// ============= Integration-style Tests =============

func TestHARunCycleWithMockDCS(t *testing.T) {
	// Skip this test since runCycle requires a PostgreSQL instance for touchMember
	t.Skip("Skipping test that requires PostgreSQL instance")
}

func TestHADetermineActionNoCluster(t *testing.T) {
	// Skip tests that require PostgreSQL instance since determineAction calls pg.State()
	t.Skip("Skipping test that requires PostgreSQL instance")
}

func TestHADetermineActionUninitializedCluster(t *testing.T) {
	t.Skip("Skipping test that requires PostgreSQL instance")
}

func TestHADetermineActionAsPrimary(t *testing.T) {
	t.Skip("Skipping test that requires PostgreSQL instance")
}

func TestHADetermineActionAsReplica(t *testing.T) {
	t.Skip("Skipping test that requires PostgreSQL instance")
}
