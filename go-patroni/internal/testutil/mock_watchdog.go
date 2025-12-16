// Package testutil provides mock implementations for testing.
package testutil

import (
	"context"
	"sync"
	"time"
)

// MockWatchdog is a mock implementation of Watchdog for testing.
type MockWatchdog struct {
	mu sync.Mutex

	// Configurable state
	device       string
	mode         string
	timeout      time.Duration
	safetyMargin time.Duration
	isActive     bool
	timeLeft     time.Duration

	// Error injection
	OpenErr       error
	CloseErr      error
	KeepaliveErr  error
	SetTimeoutErr error
	GetTimeLeftErr error
	GetInfoErr    error
	DisableErr    error
	EnableErr     error

	// Call tracking
	OpenCalls          int
	CloseCalls         int
	KeepaliveCalls     int
	SetTimeoutCalls    int
	GetTimeoutCalls    int
	GetTimeLeftCalls   int
	GetInfoCalls       int
	DisableCalls       int
	EnableCalls        int
	StartKeepaliveCalls int
	CalculateIntervalCalls int
	ModeCalls          int
	IsActiveCalls      int

	// Last call arguments
	LastTimeout  time.Duration
	LastInterval time.Duration
	LastLoopWait time.Duration
}

// WatchdogInfo represents the watchdog device info.
type WatchdogInfo struct {
	Options         uint32
	FirmwareVersion uint32
	Identity        [32]byte
}

// NewMockWatchdog creates a new MockWatchdog with sensible defaults.
func NewMockWatchdog() *MockWatchdog {
	return &MockWatchdog{
		device:       "/dev/watchdog",
		mode:         "automatic",
		timeout:      60 * time.Second,
		safetyMargin: 5 * time.Second,
		isActive:     false,
		timeLeft:     60 * time.Second,
	}
}

// Open opens and activates the watchdog device.
func (m *MockWatchdog) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OpenCalls++

	if m.OpenErr != nil {
		return m.OpenErr
	}

	if m.mode != "off" {
		m.isActive = true
	}
	return nil
}

// Close closes the watchdog device.
func (m *MockWatchdog) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCalls++

	if m.CloseErr != nil {
		return m.CloseErr
	}

	m.isActive = false
	return nil
}

// IsActive returns true if the watchdog is currently active.
func (m *MockWatchdog) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IsActiveCalls++
	return m.isActive
}

// Keepalive sends a keepalive signal to the watchdog.
func (m *MockWatchdog) Keepalive() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.KeepaliveCalls++

	if m.KeepaliveErr != nil {
		return m.KeepaliveErr
	}

	// Reset time left on keepalive
	m.timeLeft = m.timeout
	return nil
}

// SetTimeout sets the watchdog timeout.
func (m *MockWatchdog) SetTimeout(timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetTimeoutCalls++
	m.LastTimeout = timeout

	if m.SetTimeoutErr != nil {
		return m.SetTimeoutErr
	}

	m.timeout = timeout
	return nil
}

// GetTimeout returns the current watchdog timeout.
func (m *MockWatchdog) GetTimeout() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetTimeoutCalls++
	return m.timeout
}

// GetTimeLeft returns the time left before the watchdog fires.
func (m *MockWatchdog) GetTimeLeft() (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetTimeLeftCalls++

	if m.GetTimeLeftErr != nil {
		return 0, m.GetTimeLeftErr
	}

	return m.timeLeft, nil
}

// GetInfo returns information about the watchdog device.
func (m *MockWatchdog) GetInfo() (*WatchdogInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetInfoCalls++

	if m.GetInfoErr != nil {
		return nil, m.GetInfoErr
	}

	info := &WatchdogInfo{
		Options:         0x8000, // WDIOF_MAGICCLOSE
		FirmwareVersion: 1,
	}
	copy(info.Identity[:], "Mock Watchdog")
	return info, nil
}

// Disable disables the watchdog (if supported).
func (m *MockWatchdog) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DisableCalls++

	if m.DisableErr != nil {
		return m.DisableErr
	}

	m.isActive = false
	return nil
}

// Enable enables the watchdog (if supported).
func (m *MockWatchdog) Enable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EnableCalls++

	if m.EnableErr != nil {
		return m.EnableErr
	}

	m.isActive = true
	return nil
}

// StartKeepalive starts a background goroutine that sends keepalives.
func (m *MockWatchdog) StartKeepalive(ctx context.Context, interval time.Duration) {
	m.mu.Lock()
	m.StartKeepaliveCalls++
	m.LastInterval = interval
	m.mu.Unlock()

	// Start background keepalive goroutine
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Keepalive()
			}
		}
	}()
}

// CalculateKeepaliveInterval calculates the optimal keepalive interval.
func (m *MockWatchdog) CalculateKeepaliveInterval(loopWait time.Duration) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CalculateIntervalCalls++
	m.LastLoopWait = loopWait

	if m.timeout == 0 {
		return loopWait
	}

	// Keepalive should be at least safety_margin seconds before timeout
	maxInterval := m.timeout - m.safetyMargin
	if maxInterval < time.Second {
		maxInterval = time.Second
	}

	// Use the smaller of loopWait and maxInterval
	if loopWait < maxInterval {
		return loopWait
	}
	return maxInterval
}

// Mode returns the current watchdog mode.
func (m *MockWatchdog) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ModeCalls++
	return m.mode
}

// SetMode sets the watchdog mode for testing.
func (m *MockWatchdog) SetMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
}

// SetActive sets the active state for testing.
func (m *MockWatchdog) SetActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isActive = active
}

// SetTimeLeft sets the time left for testing.
func (m *MockWatchdog) SetTimeLeft(timeLeft time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeLeft = timeLeft
}

// SetSafetyMargin sets the safety margin for testing.
func (m *MockWatchdog) SetSafetyMargin(margin time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.safetyMargin = margin
}

// Reset resets all call counts and error injections.
func (m *MockWatchdog) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.OpenCalls = 0
	m.CloseCalls = 0
	m.KeepaliveCalls = 0
	m.SetTimeoutCalls = 0
	m.GetTimeoutCalls = 0
	m.GetTimeLeftCalls = 0
	m.GetInfoCalls = 0
	m.DisableCalls = 0
	m.EnableCalls = 0
	m.StartKeepaliveCalls = 0
	m.CalculateIntervalCalls = 0
	m.ModeCalls = 0
	m.IsActiveCalls = 0

	m.OpenErr = nil
	m.CloseErr = nil
	m.KeepaliveErr = nil
	m.SetTimeoutErr = nil
	m.GetTimeLeftErr = nil
	m.GetInfoErr = nil
	m.DisableErr = nil
	m.EnableErr = nil
}

// MockWatchdogMonitor is a mock implementation of WatchdogMonitor for testing.
type MockWatchdogMonitor struct {
	mu sync.Mutex

	// Embedded mock watchdog
	Watchdog *MockWatchdog

	// State
	isLeader bool

	// Call tracking
	OpenCalls           int
	CloseCalls          int
	SetLeaderStateCalls int
	KeepaliveCalls      int
	IsActiveCalls       int

	// Last call arguments
	LastLeaderState bool

	// Error injection
	OpenErr  error
	CloseErr error
}

// NewMockWatchdogMonitor creates a new MockWatchdogMonitor.
func NewMockWatchdogMonitor() *MockWatchdogMonitor {
	return &MockWatchdogMonitor{
		Watchdog: NewMockWatchdog(),
		isLeader: false,
	}
}

// Open opens the watchdog device.
func (m *MockWatchdogMonitor) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OpenCalls++

	if m.OpenErr != nil {
		return m.OpenErr
	}

	return m.Watchdog.Open()
}

// Close closes the watchdog device.
func (m *MockWatchdogMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCalls++

	if m.CloseErr != nil {
		return m.CloseErr
	}

	return m.Watchdog.Close()
}

// SetLeaderState updates the leader state and manages watchdog accordingly.
func (m *MockWatchdogMonitor) SetLeaderState(isLeader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetLeaderStateCalls++
	m.LastLeaderState = isLeader

	if m.isLeader == isLeader {
		return
	}

	m.isLeader = isLeader

	if isLeader {
		// Becoming leader - activate watchdog
		if m.Watchdog.isActive {
			m.Watchdog.Keepalive()
		}
	} else {
		// Lost leadership - disable watchdog
		if m.Watchdog.isActive {
			m.Watchdog.Disable()
		}
	}
}

// Keepalive sends a keepalive if we're the leader.
func (m *MockWatchdogMonitor) Keepalive() error {
	m.mu.Lock()
	isLeader := m.isLeader
	m.KeepaliveCalls++
	m.mu.Unlock()

	if !isLeader {
		return nil
	}

	return m.Watchdog.Keepalive()
}

// IsActive returns true if the watchdog is active.
func (m *MockWatchdogMonitor) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IsActiveCalls++
	return m.Watchdog.isActive
}

// Reset resets all call counts.
func (m *MockWatchdogMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.OpenCalls = 0
	m.CloseCalls = 0
	m.SetLeaderStateCalls = 0
	m.KeepaliveCalls = 0
	m.IsActiveCalls = 0

	m.OpenErr = nil
	m.CloseErr = nil

	m.Watchdog.Reset()
}
