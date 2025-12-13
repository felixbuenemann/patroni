package ha

import (
	"testing"
	"time"

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
