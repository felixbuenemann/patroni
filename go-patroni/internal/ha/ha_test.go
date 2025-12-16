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

// TestHAReinitialize moved to integration tests section below

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

// ============= Integration-style Tests with Mocks =============

func TestHARunCycleWithMockDCS(t *testing.T) {
	cfg := &config.Config{
		Name:     "node1",
		Scope:    "mycluster",
		LoopWait: 10,
	}
	mockDCS := testutil.NewMockDCS()
	mockDCS.Cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary", State: "running"}},
		},
	}

	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(true)
	mockPG.SetWALPosition(16777216)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.setState(HAStateRunning)

	ctx := context.Background()
	ha.runCycle(ctx)

	// Verify touchMember was called
	if mockDCS.TouchMemberCalls != 1 {
		t.Errorf("TouchMember calls = %d, want 1", mockDCS.TouchMemberCalls)
	}

	// Verify cluster state was updated
	if ha.cluster == nil {
		t.Error("Cluster should be set after runCycle")
	}
}

func TestHARunCycleWhenPaused(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.setState(HAStatePaused)

	ctx := context.Background()
	ha.runCycle(ctx)

	// Verify no DCS calls when paused
	if mockDCS.GetClusterCalls != 0 {
		t.Error("GetCluster should not be called when paused")
	}
}

func TestHARunCycleDCSError(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.GetClusterErr = context.DeadlineExceeded
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.setState(HAStateRunning)

	ctx := context.Background()
	ha.runCycle(ctx)

	// Verify GetCluster was called but touchMember was not (due to error)
	if mockDCS.GetClusterCalls != 1 {
		t.Errorf("GetCluster calls = %d, want 1", mockDCS.GetClusterCalls)
	}
	if mockDCS.TouchMemberCalls != 0 {
		t.Error("TouchMember should not be called when GetCluster fails")
	}
}

func TestHADetermineActionNoCluster(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetDataDir("/var/lib/postgresql/data")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{InitializeVersion: 0} // Uninitialized cluster

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Uninitialized cluster + uninitialized pg + data dir = bootstrap
	if action != ActionBootstrap {
		t.Errorf("Action = %s, want %s", action, ActionBootstrap)
	}
}

func TestHADetermineActionUninitializedCluster(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(false)
	mockPG.SetRole(types.PostgresqlRoleReplica) // Not uninitialized

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{InitializeVersion: 0}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Uninitialized cluster but PG not uninitialized = none
	if action != ActionNone {
		t.Errorf("Action = %s, want %s", action, ActionNone)
	}
}

func TestHADetermineActionAsPrimary(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Running as primary and we are leader = renew lock
	if action != ActionRenewLock {
		t.Errorf("Action = %s, want %s", action, ActionRenewLock)
	}

	// Verify isLeader was set
	if !ha.isLeader {
		t.Error("isLeader should be true when we are the leader")
	}
}

func TestHADetermineActionAsReplica(t *testing.T) {
	cfg := &config.Config{
		Name:  "node2",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(false) // We are a replica

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
			{Name: "node2", Data: types.MemberData{Role: "replica"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Running as replica with a leader = follow leader
	if action != ActionFollowLeader {
		t.Errorf("Action = %s, want %s", action, ActionFollowLeader)
	}

	// Verify isLeader was set to false
	if ha.isLeader {
		t.Error("isLeader should be false when we are not the leader")
	}
}

func TestHADetermineActionNeedDemote(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(true) // We think we're primary

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node2"}, // But node2 is leader
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
			{Name: "node2", Data: types.MemberData{Role: "primary"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Running as primary but not leader = demote
	if action != ActionDemote {
		t.Errorf("Action = %s, want %s", action, ActionDemote)
	}
}

func TestHADetermineActionNoLeaderCanPromote(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(false) // We are a replica
	mockPG.SetTimeline(1)
	mockPG.SetWALPosition(16777216)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            nil, // No leader
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "replica"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// No leader and we can promote = promote
	if action != ActionPromote {
		t.Errorf("Action = %s, want %s", action, ActionPromote)
	}
}

func TestHADetermineActionNoLeaderCantPromote(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
		Tags:  map[string]interface{}{"nofailover": true},
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(false)
	mockPG.SetTimeline(1)
	mockPG.SetWALPosition(16777216)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            nil,
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "replica"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// No leader but nofailover tag = none (can't promote)
	if action != ActionNone {
		t.Errorf("Action = %s, want %s", action, ActionNone)
	}
}

func TestHADetermineActionNotRunningNoLeader(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(false)
	mockPG.SetRole(types.PostgresqlRolePrimary)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            nil,
		Members:           []*types.Member{},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Not running, no leader = try to start as primary
	if action != ActionStartAsPrimary {
		t.Errorf("Action = %s, want %s", action, ActionStartAsPrimary)
	}
}

func TestHADetermineActionNotRunningWithLeader(t *testing.T) {
	cfg := &config.Config{
		Name:  "node2",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")
	mockPG.SetRunning(false)
	mockPG.SetRole(types.PostgresqlRoleReplica)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Not running, has leader, we are not leader = start as replica
	if action != ActionStartAsReplica {
		t.Errorf("Action = %s, want %s", action, ActionStartAsReplica)
	}
}

func TestHADetermineActionNotRunningWeAreLeader(t *testing.T) {
	cfg := &config.Config{
		Name:  "node1",
		Scope: "mycluster",
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(false)
	mockPG.SetRole(types.PostgresqlRolePrimary)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
		},
	}

	ctx := context.Background()
	action := ha.determineAction(ctx)

	// Not running, we are leader = start as primary
	if action != ActionStartAsPrimary {
		t.Errorf("Action = %s, want %s", action, ActionStartAsPrimary)
	}
}

// ============= Execute Action Tests =============

func TestHAExecuteActionNone(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionNone)

	if err != nil {
		t.Errorf("executeAction(ActionNone) error = %v", err)
	}
}

func TestHAExecuteActionBootstrap(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionBootstrap)

	if err != nil {
		t.Errorf("executeAction(ActionBootstrap) error = %v", err)
	}

	// Verify bootstrap was called
	if mockPG.BootstrapCalls != 1 {
		t.Errorf("Bootstrap calls = %d, want 1", mockPG.BootstrapCalls)
	}

	// Verify DCS initialize was called
	if mockDCS.InitializeCalls != 1 {
		t.Errorf("Initialize calls = %d, want 1", mockDCS.InitializeCalls)
	}
}

func TestHAExecuteActionStartAsPrimary(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionStartAsPrimary)

	if err != nil {
		t.Errorf("executeAction(ActionStartAsPrimary) error = %v", err)
	}

	// Verify start was called
	if mockPG.StartCalls != 1 {
		t.Errorf("Start calls = %d, want 1", mockPG.StartCalls)
	}

	// Verify lock was attempted
	if mockDCS.TakeLockCalls != 1 {
		t.Errorf("TakeLock calls = %d, want 1", mockDCS.TakeLockCalls)
	}
}

func TestHAExecuteActionStartAsReplica(t *testing.T) {
	cfg := &config.Config{
		LoopWait: 10,
		PostgreSQL: config.PostgreSQLConfig{
			Authentication: config.AuthConfig{
				Replication: config.UserAuth{
					Username: "replicator",
					Password: "secret",
				},
			},
		},
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")
	mockPG.SetRole(types.PostgresqlRoleUninitialized)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					ConnURL: "postgres://node1:5432/postgres",
				},
			},
		},
	}

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionStartAsReplica)

	if err != nil {
		t.Errorf("executeAction(ActionStartAsReplica) error = %v", err)
	}

	// Verify clone was called (since role was uninitialized)
	if mockPG.CloneCalls != 1 {
		t.Errorf("Clone calls = %d, want 1", mockPG.CloneCalls)
	}

	// Verify start was called
	if mockPG.StartCalls != 1 {
		t.Errorf("Start calls = %d, want 1", mockPG.StartCalls)
	}
}

func TestHAExecuteActionAcquireLock(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionAcquireLock)

	if err != nil {
		t.Errorf("executeAction(ActionAcquireLock) error = %v", err)
	}

	// Verify lock was attempted
	if mockDCS.TakeLockCalls != 1 {
		t.Errorf("TakeLock calls = %d, want 1", mockDCS.TakeLockCalls)
	}

	// Verify leader was updated
	if mockDCS.UpdateLeaderCalls != 1 {
		t.Errorf("UpdateLeader calls = %d, want 1", mockDCS.UpdateLeaderCalls)
	}

	// Verify isLeader was set
	if !ha.isLeader {
		t.Error("isLeader should be true after acquiring lock")
	}
}

func TestHAExecuteActionRenewLock(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.IsLocked = true // We already have the lock
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.isLeader = true

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionRenewLock)

	if err != nil {
		t.Errorf("executeAction(ActionRenewLock) error = %v", err)
	}

	// Verify lock renewal was attempted
	if mockDCS.AcquireRenewLockCalls != 1 {
		t.Errorf("AttemptToAcquireOrRenewLock calls = %d, want 1", mockDCS.AcquireRenewLockCalls)
	}

	// Verify leader was updated
	if mockDCS.UpdateLeaderCalls != 1 {
		t.Errorf("UpdateLeader calls = %d, want 1", mockDCS.UpdateLeaderCalls)
	}
}

func TestHAExecuteActionRenewLockLost(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.AcquireRenewLockErr = nil
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)

	// Set up cluster with another leader to allow demote
	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.isLeader = true
	ha.cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node2"},
		Members: []*types.Member{
			{Name: "node2", Data: types.MemberData{ConnURL: "postgres://node2:5432/postgres"}},
		},
	}

	// Simulate lock not renewed (another node took it)
	mockDCS.IsLocked = false // Force AttemptToAcquireOrRenewLock to return false

	// Override the mock to simulate losing the lock
	mockDCS.AcquireRenewLockErr = nil

	ctx := context.Background()
	err := ha.doRenewLock(ctx)

	// The error comes from demote, which should succeed in mock
	if err != nil {
		t.Errorf("doRenewLock error = %v", err)
	}
}

func TestHAExecuteActionFollowLeader(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
		},
	}

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionFollowLeader)

	if err != nil {
		t.Errorf("executeAction(ActionFollowLeader) error = %v", err)
	}
}

func TestHAExecuteActionPromote(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionPromote)

	if err != nil {
		t.Errorf("executeAction(ActionPromote) error = %v", err)
	}

	// Verify lock was acquired
	if mockDCS.TakeLockCalls != 1 {
		t.Errorf("TakeLock calls = %d, want 1", mockDCS.TakeLockCalls)
	}

	// Verify promote was called
	if mockPG.PromoteCalls != 1 {
		t.Errorf("Promote calls = %d, want 1", mockPG.PromoteCalls)
	}

	// Verify isLeader was set
	if !ha.isLeader {
		t.Error("isLeader should be true after promote")
	}
}

func TestHAExecuteActionPromoteLockFailed(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.IsLocked = true // Someone else has the lock
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.doPromote(ctx)

	if err != nil {
		t.Errorf("doPromote error = %v", err)
	}

	// Verify promote was NOT called since we couldn't get the lock
	if mockPG.PromoteCalls != 0 {
		t.Errorf("Promote calls = %d, want 0", mockPG.PromoteCalls)
	}
}

func TestHAExecuteActionDemote(t *testing.T) {
	cfg := &config.Config{
		LoopWait: 10,
		PostgreSQL: config.PostgreSQLConfig{
			Authentication: config.AuthConfig{
				Replication: config.UserAuth{
					Username: "replicator",
				},
			},
		},
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)
	mockPG.SetPrimary(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.isLeader = true
	ha.cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node2"},
		Members: []*types.Member{
			{
				Name: "node2",
				Data: types.MemberData{
					ConnURL: "postgres://node2:5432/postgres",
				},
			},
		},
	}

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionDemote)

	if err != nil {
		t.Errorf("executeAction(ActionDemote) error = %v", err)
	}

	// Verify demote was called
	if mockPG.DemoteCalls != 1 {
		t.Errorf("Demote calls = %d, want 1", mockPG.DemoteCalls)
	}

	// Verify isLeader was cleared
	if ha.isLeader {
		t.Error("isLeader should be false after demote")
	}
}

func TestHAExecuteActionRestart(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, ActionRestart)

	if err != nil {
		t.Errorf("executeAction(ActionRestart) error = %v", err)
	}

	// Verify restart was called
	if mockPG.RestartCalls != 1 {
		t.Errorf("Restart calls = %d, want 1", mockPG.RestartCalls)
	}
}

func TestHAExecuteActionUnknown(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.executeAction(ctx, Action("unknown"))

	if err == nil {
		t.Error("executeAction(unknown) should return error")
	}
}

// ============= Action Handler Tests =============

func TestHADoBootstrapAlreadyInitialized(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.Initialized = true // Already initialized
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.doBootstrap(ctx)

	if err != nil {
		t.Errorf("doBootstrap error = %v", err)
	}

	// Verify bootstrap was NOT called since DCS was already initialized
	if mockPG.BootstrapCalls != 0 {
		t.Errorf("Bootstrap calls = %d, want 0", mockPG.BootstrapCalls)
	}
}

func TestHADoStartAsReplicaNoLeader(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.cluster = &types.Cluster{
		Leader:  nil, // No leader
		Members: []*types.Member{},
	}

	ctx := context.Background()
	err := ha.doStartAsReplica(ctx)

	if err == nil {
		t.Error("doStartAsReplica should return error when no leader")
	}
}

func TestHAHandleDCSErrorAsLeaderWithFailsafe(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.isLeader = true
	ha.failsafe.Update(map[string]interface{}{
		"name":     "node1",
		"conn_url": "postgres://localhost:5432",
	})

	ctx := context.Background()
	ha.handleDCSError(ctx)

	// With failsafe active, should continue as leader (no action needed)
	// Just verify no panic and function completes
}

func TestHAHandleDCSErrorAsLeaderNoFailsafe(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)
	ha.isLeader = true

	ctx := context.Background()
	ha.handleDCSError(ctx)

	// Without failsafe, function just logs warning
}

func TestHACanPromoteNoWAL(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetTimeline(0)
	mockPG.SetWALPosition(0)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	canPromote := ha.canPromote(ctx)

	if canPromote {
		t.Error("canPromote should return false when timeline/WAL is 0")
	}
}

func TestHACanPromoteWithNoFailoverTag(t *testing.T) {
	cfg := &config.Config{
		LoopWait: 10,
		Tags:     map[string]interface{}{"nofailover": true},
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetTimeline(1)
	mockPG.SetWALPosition(16777216)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	canPromote := ha.canPromote(ctx)

	if canPromote {
		t.Error("canPromote should return false with nofailover tag")
	}
}

func TestHACanPromoteValid(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetTimeline(1)
	mockPG.SetWALPosition(16777216)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	canPromote := ha.canPromote(ctx)

	if !canPromote {
		t.Error("canPromote should return true for valid candidate")
	}
}

func TestHABuildPrimaryConnInfo(t *testing.T) {
	cfg := &config.Config{
		Name: "node2",
		PostgreSQL: config.PostgreSQLConfig{
			Authentication: config.AuthConfig{
				Replication: config.UserAuth{
					Username: "replicator",
					Password: "secret",
					SSLMode:  "require",
				},
			},
		},
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	leader := &types.Member{
		Name: "node1",
		Data: types.MemberData{
			ConnURL: "postgres://node1:5432/postgres",
		},
	}

	connInfo := ha.buildPrimaryConnInfo(leader)

	if connInfo == "" {
		t.Error("buildPrimaryConnInfo should return non-empty string")
	}

	// Check that it contains expected parts
	expectedParts := []string{"host=", "port=", "user=replicator", "application_name=node2", "password=secret", "sslmode=require"}
	for _, part := range expectedParts {
		if !containsString(connInfo, part) {
			t.Errorf("connInfo missing %q: %s", part, connInfo)
		}
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsString(s[1:], substr) || s[:len(substr)] == substr)
}

func TestHAReinitialize(t *testing.T) {
	cfg := &config.Config{
		LoopWait: 10,
		PostgreSQL: config.PostgreSQLConfig{
			Authentication: config.AuthConfig{
				Replication: config.UserAuth{
					Username: "replicator",
				},
			},
		},
	}
	mockDCS := testutil.NewMockDCS()
	mockDCS.Cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					ConnURL: "postgres://node1:5432/postgres",
				},
			},
		},
	}
	mockPG := testutil.NewMockPostgresql("node2", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.Reinitialize(ctx, false)

	if err != nil {
		t.Errorf("Reinitialize error = %v", err)
	}

	// Verify stop was called
	if mockPG.StopCalls != 1 {
		t.Errorf("Stop calls = %d, want 1", mockPG.StopCalls)
	}

	// Verify clone was called
	if mockPG.CloneCalls != 1 {
		t.Errorf("Clone calls = %d, want 1", mockPG.CloneCalls)
	}

	// Verify start was called
	if mockPG.StartCalls != 1 {
		t.Errorf("Start calls = %d, want 1", mockPG.StartCalls)
	}

	// Verify busy was set and cleared
	if ha.IsBusy() {
		t.Error("IsBusy should be false after Reinitialize completes")
	}
}

func TestHAReinitializeNoLeader(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockDCS.Cluster = &types.Cluster{
		Leader:  nil,
		Members: []*types.Member{},
	}
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()
	err := ha.Reinitialize(ctx, false)

	if err == nil {
		t.Error("Reinitialize should return error when no leader")
	}
}

// ============= Run Loop Tests =============

func TestHARunWithContextCancel(t *testing.T) {
	cfg := &config.Config{LoopWait: 1}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := ha.Run(ctx)

	if err != context.DeadlineExceeded {
		t.Errorf("Run error = %v, want context.DeadlineExceeded", err)
	}

	if ha.State() != HAStateStopped {
		t.Errorf("State = %v, want HAStateStopped", ha.State())
	}
}

func TestHARunWithStop(t *testing.T) {
	cfg := &config.Config{LoopWait: 10}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx := context.Background()

	// Stop after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		ha.Stop()
	}()

	err := ha.Run(ctx)

	if err != nil {
		t.Errorf("Run error = %v, want nil", err)
	}

	if ha.State() != HAStateStopped {
		t.Errorf("State = %v, want HAStateStopped", ha.State())
	}
}

func TestHARunWithWakeup(t *testing.T) {
	cfg := &config.Config{LoopWait: 60} // Long loop wait
	mockDCS := testutil.NewMockDCS()
	mockDCS.Cluster = &types.Cluster{InitializeVersion: 1}
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")
	mockPG.SetRunning(true)

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Wakeup immediately to trigger a cycle
	go func() {
		time.Sleep(10 * time.Millisecond)
		ha.Wakeup()
	}()

	ha.Run(ctx)

	// Verify cycle was run (GetCluster was called)
	if mockDCS.GetClusterCalls < 1 {
		t.Error("GetCluster should be called after Wakeup")
	}
}

// ============= getEffectiveTags Tests =============

func TestGetEffectiveTags(t *testing.T) {
	tests := []struct {
		name           string
		tags           map[string]interface{}
		wantNoFailover bool
		wantNoLB       bool
		wantCloneFrom  bool
		wantNoSync     bool
	}{
		{
			name:           "nil tags",
			tags:           nil,
			wantNoFailover: false,
			wantNoLB:       false,
		},
		{
			name: "nofailover true",
			tags: map[string]interface{}{"nofailover": true},
			wantNoFailover: true,
		},
		{
			name: "noloadbalance true",
			tags: map[string]interface{}{"noloadbalance": true},
			wantNoLB: true,
		},
		{
			name: "clonefrom true",
			tags: map[string]interface{}{"clonefrom": true},
			wantCloneFrom: true,
		},
		{
			name: "nosync true",
			tags: map[string]interface{}{"nosync": true},
			wantNoSync: true,
		},
		{
			name: "all tags",
			tags: map[string]interface{}{
				"nofailover":    true,
				"noloadbalance": true,
				"clonefrom":     true,
				"nosync":        true,
			},
			wantNoFailover: true,
			wantNoLB:       true,
			wantCloneFrom:  true,
			wantNoSync:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LoopWait: 10,
				Tags:     tt.tags,
			}
			mockDCS := testutil.NewMockDCS()
			mockPG := testutil.NewMockPostgresql("node1", "mycluster")

			ha := NewWithInterface(cfg, mockDCS, mockPG)

			tags := ha.GetEffectiveTags()

			if tags.NoFailover != tt.wantNoFailover {
				t.Errorf("NoFailover = %v, want %v", tags.NoFailover, tt.wantNoFailover)
			}
			if tags.NoLoadbalance != tt.wantNoLB {
				t.Errorf("NoLoadbalance = %v, want %v", tags.NoLoadbalance, tt.wantNoLB)
			}
			if tags.CloneFrom != tt.wantCloneFrom {
				t.Errorf("CloneFrom = %v, want %v", tags.CloneFrom, tt.wantCloneFrom)
			}
			if tags.NoSync != tt.wantNoSync {
				t.Errorf("NoSync = %v, want %v", tags.NoSync, tt.wantNoSync)
			}
		})
	}
}

// ============= NewWithInterface Test =============

func TestNewWithInterface(t *testing.T) {
	cfg := &config.Config{
		Name:     "node1",
		Scope:    "mycluster",
		LoopWait: 10,
	}
	mockDCS := testutil.NewMockDCS()
	mockPG := testutil.NewMockPostgresql("node1", "mycluster")

	ha := NewWithInterface(cfg, mockDCS, mockPG)

	if ha == nil {
		t.Fatal("NewWithInterface returned nil")
	}

	if ha.pg != mockPG {
		t.Error("PostgreSQL interface not set correctly")
	}

	if ha.dcs != mockDCS {
		t.Error("DCS not set correctly")
	}
}
