package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// RestartSchedule represents a scheduled restart.
type RestartSchedule struct {
	Schedule   time.Time              `json:"schedule"`
	Role       string                 `json:"role,omitempty"`       // "master", "replica", or empty for any
	PendingRestart bool               `json:"pending_restart,omitempty"` // Only restart if pending_restart is true
	PostgresVersion string            `json:"postgres_version,omitempty"` // Expected PG version
	Timeout    int                    `json:"timeout,omitempty"`    // Restart timeout in seconds
}

// Scheduler manages scheduled operations for Patroni.
type Scheduler struct {
	mu              sync.RWMutex
	restartSchedule *RestartSchedule
	callbacks       []SchedulerCallback
}

// SchedulerCallback is called when a scheduled operation should be executed.
type SchedulerCallback func(schedule *RestartSchedule) error

// NewScheduler creates a new Scheduler instance.
func NewScheduler() *Scheduler {
	return &Scheduler{}
}

// SetRestartSchedule sets the scheduled restart time.
func (s *Scheduler) SetRestartSchedule(schedule *RestartSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if schedule.Schedule.Before(time.Now()) {
		return fmt.Errorf("scheduled time is in the past")
	}

	s.restartSchedule = schedule
	log.Info().
		Time("schedule", schedule.Schedule).
		Str("role", schedule.Role).
		Msg("Restart scheduled")

	return nil
}

// GetRestartSchedule returns the current restart schedule.
func (s *Scheduler) GetRestartSchedule() *RestartSchedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.restartSchedule
}

// CancelRestartSchedule cancels any scheduled restart.
func (s *Scheduler) CancelRestartSchedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartSchedule = nil
	log.Info().Msg("Scheduled restart cancelled")
}

// ShouldRestart checks if a restart should be performed now.
func (s *Scheduler) ShouldRestart(currentRole string, hasPendingRestart bool) bool {
	s.mu.RLock()
	schedule := s.restartSchedule
	s.mu.RUnlock()

	if schedule == nil {
		return false
	}

	// Check if it's time
	if time.Now().Before(schedule.Schedule) {
		return false
	}

	// Check role constraint
	if schedule.Role != "" {
		if schedule.Role == "master" && currentRole != "master" {
			return false
		}
		if schedule.Role == "replica" && currentRole == "master" {
			return false
		}
	}

	// Check pending restart constraint
	if schedule.PendingRestart && !hasPendingRestart {
		return false
	}

	return true
}

// ClearIfExecuted clears the schedule after execution.
func (s *Scheduler) ClearIfExecuted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartSchedule = nil
}

// OnScheduleCallback registers a callback for schedule events.
func (s *Scheduler) OnScheduleCallback(cb SchedulerCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, cb)
}

// Run starts the scheduler loop.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkSchedule()
		}
	}
}

// checkSchedule checks if any scheduled operation should run.
func (s *Scheduler) checkSchedule() {
	s.mu.RLock()
	schedule := s.restartSchedule
	callbacks := s.callbacks
	s.mu.RUnlock()

	if schedule == nil {
		return
	}

	if time.Now().Before(schedule.Schedule) {
		return
	}

	// Execute callbacks
	for _, cb := range callbacks {
		if err := cb(schedule); err != nil {
			log.Error().Err(err).Msg("Scheduler callback failed")
		}
	}
}

// MarshalJSON implements custom JSON marshaling.
func (r *RestartSchedule) MarshalJSON() ([]byte, error) {
	type Alias RestartSchedule
	return json.Marshal(&struct {
		Schedule string `json:"schedule"`
		*Alias
	}{
		Schedule: r.Schedule.Format(time.RFC3339),
		Alias:    (*Alias)(r),
	})
}

// UnmarshalJSON implements custom JSON unmarshaling.
func (r *RestartSchedule) UnmarshalJSON(data []byte) error {
	type Alias RestartSchedule
	aux := &struct {
		Schedule string `json:"schedule"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	t, err := time.Parse(time.RFC3339, aux.Schedule)
	if err != nil {
		return fmt.Errorf("invalid schedule time format: %w", err)
	}

	r.Schedule = t
	return nil
}

// PendingRestart tracks whether a PostgreSQL restart is pending.
type PendingRestart struct {
	mu       sync.RWMutex
	pending  bool
	reason   string
	params   []string // Parameters requiring restart
}

// NewPendingRestart creates a new PendingRestart tracker.
func NewPendingRestart() *PendingRestart {
	return &PendingRestart{}
}

// SetPending marks a restart as pending.
func (p *PendingRestart) SetPending(reason string, params ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = true
	p.reason = reason
	p.params = params
	log.Info().
		Str("reason", reason).
		Strs("params", params).
		Msg("PostgreSQL restart pending")
}

// Clear clears the pending restart flag.
func (p *PendingRestart) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = false
	p.reason = ""
	p.params = nil
}

// IsPending returns whether a restart is pending.
func (p *PendingRestart) IsPending() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pending
}

// Reason returns the reason for the pending restart.
func (p *PendingRestart) Reason() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.reason
}

// Params returns the parameters requiring restart.
func (p *PendingRestart) Params() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.params
}

// ToMap returns a map representation for API responses.
func (p *PendingRestart) ToMap() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.pending {
		return nil
	}

	return map[string]interface{}{
		"pending_restart":        true,
		"pending_restart_reason": p.reason,
		"pending_restart_params": p.params,
	}
}

// MaintenanceWindow represents a maintenance window for scheduled operations.
type MaintenanceWindow struct {
	Enabled   bool          `yaml:"enabled" json:"enabled"`
	StartTime string        `yaml:"start_time" json:"start_time"` // HH:MM format
	EndTime   string        `yaml:"end_time" json:"end_time"`     // HH:MM format
	Days      []time.Weekday `yaml:"days" json:"days"`
}

// IsActive checks if the maintenance window is currently active.
func (m *MaintenanceWindow) IsActive() bool {
	if !m.Enabled {
		return false
	}

	now := time.Now()
	currentDay := now.Weekday()

	// Check if today is a valid day
	dayValid := false
	for _, d := range m.Days {
		if d == currentDay {
			dayValid = true
			break
		}
	}
	if !dayValid {
		return false
	}

	// Parse start and end times
	startH, startM := parseTime(m.StartTime)
	endH, endM := parseTime(m.EndTime)

	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM

	// Handle overnight windows (e.g., 23:00 - 05:00)
	if endMinutes < startMinutes {
		return currentMinutes >= startMinutes || currentMinutes < endMinutes
	}

	return currentMinutes >= startMinutes && currentMinutes < endMinutes
}

// NextWindowStart returns the next time the maintenance window starts.
func (m *MaintenanceWindow) NextWindowStart() time.Time {
	if !m.Enabled || len(m.Days) == 0 {
		return time.Time{}
	}

	now := time.Now()
	startH, startM := parseTime(m.StartTime)

	// Find the next valid day
	for i := 0; i < 8; i++ {
		checkDay := now.AddDate(0, 0, i)
		for _, validDay := range m.Days {
			if checkDay.Weekday() == validDay {
				candidateTime := time.Date(
					checkDay.Year(), checkDay.Month(), checkDay.Day(),
					startH, startM, 0, 0,
					checkDay.Location(),
				)
				if candidateTime.After(now) {
					return candidateTime
				}
			}
		}
	}

	return time.Time{}
}

// parseTime parses a HH:MM time string.
func parseTime(s string) (int, int) {
	var h, m int
	fmt.Sscanf(s, "%d:%d", &h, &m)
	return h, m
}

// RestartCoordinator coordinates restarts across the cluster.
type RestartCoordinator struct {
	mu            sync.Mutex
	pendingNodes  map[string]time.Time
	restartWindow *MaintenanceWindow
}

// NewRestartCoordinator creates a new RestartCoordinator.
func NewRestartCoordinator() *RestartCoordinator {
	return &RestartCoordinator{
		pendingNodes: make(map[string]time.Time),
	}
}

// SetMaintenanceWindow sets the maintenance window.
func (c *RestartCoordinator) SetMaintenanceWindow(window *MaintenanceWindow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restartWindow = window
}

// RequestRestart requests a restart for a node.
func (c *RestartCoordinator) RequestRestart(nodeName string, requestTime time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingNodes[nodeName] = requestTime
}

// ClearRestart clears a restart request for a node.
func (c *RestartCoordinator) ClearRestart(nodeName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingNodes, nodeName)
}

// GetPendingRestarts returns all pending restart requests.
func (c *RestartCoordinator) GetPendingRestarts() map[string]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make(map[string]time.Time)
	for k, v := range c.pendingNodes {
		result[k] = v
	}
	return result
}

// CanRestart checks if a node can restart now.
func (c *RestartCoordinator) CanRestart(nodeName string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if there's a pending request
	_, pending := c.pendingNodes[nodeName]
	if !pending {
		return false
	}

	// Check maintenance window
	if c.restartWindow != nil && c.restartWindow.Enabled {
		return c.restartWindow.IsActive()
	}

	return true
}
