package watchdog

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}

	if cfg.Mode != ModeAutomatic {
		t.Errorf("DefaultConfig().Mode = %q, want %q", cfg.Mode, ModeAutomatic)
	}

	if cfg.Device != "/dev/watchdog" {
		t.Errorf("DefaultConfig().Device = %q, want %q", cfg.Device, "/dev/watchdog")
	}

	if cfg.SafetyMargin != 5*time.Second {
		t.Errorf("DefaultConfig().SafetyMargin = %v, want %v", cfg.SafetyMargin, 5*time.Second)
	}
}

func TestNew(t *testing.T) {
	t.Run("with nil config", func(t *testing.T) {
		w, err := New(nil)
		if err != nil {
			t.Fatalf("New(nil) error = %v", err)
		}

		if w.device != "/dev/watchdog" {
			t.Errorf("New(nil).device = %q, want /dev/watchdog", w.device)
		}

		if w.mode != ModeAutomatic {
			t.Errorf("New(nil).mode = %q, want %q", w.mode, ModeAutomatic)
		}

		if w.safetyMargin != 5*time.Second {
			t.Errorf("New(nil).safetyMargin = %v, want %v", w.safetyMargin, 5*time.Second)
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := &Config{
			Mode:         ModeRequired,
			Device:       "/dev/watchdog0",
			SafetyMargin: 10 * time.Second,
		}

		w, err := New(cfg)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if w.device != "/dev/watchdog0" {
			t.Errorf("New().device = %q, want /dev/watchdog0", w.device)
		}

		if w.mode != ModeRequired {
			t.Errorf("New().mode = %q, want %q", w.mode, ModeRequired)
		}

		if w.safetyMargin != 10*time.Second {
			t.Errorf("New().safetyMargin = %v, want %v", w.safetyMargin, 10*time.Second)
		}
	})

	t.Run("with empty device defaults", func(t *testing.T) {
		cfg := &Config{
			Mode:   ModeOff,
			Device: "",
		}

		w, err := New(cfg)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if w.device != "/dev/watchdog" {
			t.Errorf("New().device = %q, want /dev/watchdog", w.device)
		}
	})

	t.Run("with zero safety margin defaults", func(t *testing.T) {
		cfg := &Config{
			Mode:         ModeAutomatic,
			SafetyMargin: 0,
		}

		w, err := New(cfg)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if w.safetyMargin != 5*time.Second {
			t.Errorf("New().safetyMargin = %v, want %v", w.safetyMargin, 5*time.Second)
		}
	})
}

func TestWatchdogModeConstants(t *testing.T) {
	if ModeOff != "off" {
		t.Errorf("ModeOff = %q, want off", ModeOff)
	}

	if ModeAutomatic != "automatic" {
		t.Errorf("ModeAutomatic = %q, want automatic", ModeAutomatic)
	}

	if ModeRequired != "required" {
		t.Errorf("ModeRequired = %q, want required", ModeRequired)
	}
}

func TestWatchdogOpenOff(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	err := w.Open()
	if err != nil {
		t.Errorf("Open() with mode off should not error, got %v", err)
	}

	if w.IsActive() {
		t.Error("Watchdog should not be active when mode is off")
	}
}

func TestWatchdogOpenNotAvailable(t *testing.T) {
	// Test with a non-existent device and automatic mode
	w, _ := New(&Config{
		Mode:   ModeAutomatic,
		Device: "/dev/nonexistent_watchdog_device",
	})

	err := w.Open()
	// Should not error in automatic mode
	if err != nil {
		t.Errorf("Open() with automatic mode and unavailable device should not error, got %v", err)
	}

	if w.IsActive() {
		t.Error("Watchdog should not be active when device is unavailable")
	}
}

func TestWatchdogOpenRequired(t *testing.T) {
	// Test with a non-existent device and required mode
	w, _ := New(&Config{
		Mode:   ModeRequired,
		Device: "/dev/nonexistent_watchdog_device",
	})

	err := w.Open()
	// Should error in required mode
	if err == nil {
		t.Error("Open() with required mode and unavailable device should error")
	}
}

func TestWatchdogClose(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	// Close should not error even when not open
	err := w.Close()
	if err != nil {
		t.Errorf("Close() should not error, got %v", err)
	}

	if w.IsActive() {
		t.Error("Watchdog should not be active after close")
	}
}

func TestWatchdogIsActive(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	// Initially not active
	if w.IsActive() {
		t.Error("Watchdog should not be active initially")
	}
}

func TestWatchdogMode(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{ModeOff, ModeOff},
		{ModeAutomatic, ModeAutomatic},
		{ModeRequired, ModeRequired},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			w, _ := New(&Config{Mode: tt.mode})
			if w.Mode() != tt.expected {
				t.Errorf("Mode() = %q, want %q", w.Mode(), tt.expected)
			}
		})
	}
}

func TestWatchdogGetTimeout(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	// Initially timeout is 0
	timeout := w.GetTimeout()
	if timeout != 0 {
		t.Errorf("GetTimeout() = %v, want 0", timeout)
	}
}

func TestWatchdogKeepaliveWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	// Keepalive should not error when not open
	err := w.Keepalive()
	if err != nil {
		t.Errorf("Keepalive() when not open should not error, got %v", err)
	}
}

func TestWatchdogSetTimeoutWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	err := w.SetTimeout(30 * time.Second)
	if err == nil {
		t.Error("SetTimeout() when not open should error")
	}
}

func TestWatchdogGetTimeLeftWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	_, err := w.GetTimeLeft()
	if err == nil {
		t.Error("GetTimeLeft() when not open should error")
	}
}

func TestWatchdogGetInfoWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	_, err := w.GetInfo()
	if err == nil {
		t.Error("GetInfo() when not open should error")
	}
}

func TestWatchdogDisableWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	// Disable should not error when not open
	err := w.Disable()
	if err != nil {
		t.Errorf("Disable() when not open should not error, got %v", err)
	}
}

func TestWatchdogEnableWhenNotOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	err := w.Enable()
	if err == nil {
		t.Error("Enable() when not open should error")
	}
}

func TestCalculateKeepaliveInterval(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		safetyMargin time.Duration
		loopWait     time.Duration
		expected     time.Duration
	}{
		{
			"zero timeout uses loopWait",
			0,
			5 * time.Second,
			10 * time.Second,
			10 * time.Second,
		},
		{
			"loopWait smaller than maxInterval",
			30 * time.Second,
			5 * time.Second,
			10 * time.Second,
			10 * time.Second,
		},
		{
			"loopWait larger than maxInterval",
			30 * time.Second,
			5 * time.Second,
			30 * time.Second,
			25 * time.Second,
		},
		{
			"small timeout with safety margin",
			6 * time.Second,
			5 * time.Second,
			10 * time.Second,
			1 * time.Second, // minimum 1 second
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, _ := New(&Config{
				Mode:         ModeOff,
				SafetyMargin: tt.safetyMargin,
			})
			w.timeout = tt.timeout

			result := w.CalculateKeepaliveInterval(tt.loopWait)
			if result != tt.expected {
				t.Errorf("CalculateKeepaliveInterval(%v) = %v, want %v",
					tt.loopWait, result, tt.expected)
			}
		})
	}
}

func TestIsAvailable(t *testing.T) {
	// Test with default device
	available := IsAvailable("")
	// Usually not available in test environment
	t.Logf("IsAvailable(\"\") = %v", available)

	// Test with non-existent device
	available = IsAvailable("/dev/nonexistent_watchdog")
	if available {
		t.Error("IsAvailable() should return false for non-existent device")
	}

	// Test with /dev/null (exists but not a watchdog)
	available = IsAvailable("/dev/null")
	if !available {
		t.Log("IsAvailable(\"/dev/null\") = false (file stat check only)")
	}
}

func TestSoftdogLoader(t *testing.T) {
	loader := NewSoftdogLoader()
	if loader == nil {
		t.Fatal("NewSoftdogLoader() returned nil")
	}

	// Load should not error (it just logs)
	err := loader.Load(60)
	if err != nil {
		t.Errorf("SoftdogLoader.Load() error = %v", err)
	}
}

func TestNewWatchdogMonitor(t *testing.T) {
	cfg := &Config{Mode: ModeOff}

	m, err := NewWatchdogMonitor(cfg)
	if err != nil {
		t.Fatalf("NewWatchdogMonitor() error = %v", err)
	}

	if m == nil {
		t.Fatal("NewWatchdogMonitor() returned nil")
	}

	if m.watchdog == nil {
		t.Error("WatchdogMonitor.watchdog should not be nil")
	}
}

func TestWatchdogMonitorOpen(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	err := m.Open()
	if err != nil {
		t.Errorf("WatchdogMonitor.Open() error = %v", err)
	}
}

func TestWatchdogMonitorClose(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	err := m.Close()
	if err != nil {
		t.Errorf("WatchdogMonitor.Close() error = %v", err)
	}
}

func TestWatchdogMonitorIsActive(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	if m.IsActive() {
		t.Error("WatchdogMonitor should not be active initially")
	}
}

func TestWatchdogMonitorSetLeaderState(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	// Set leader state to true
	m.SetLeaderState(true)

	// Set again to same state (should do nothing)
	m.SetLeaderState(true)

	// Set to false
	m.SetLeaderState(false)

	// Verify no panic
}

func TestWatchdogMonitorKeepalive(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	// Keepalive when not leader should return nil
	err := m.Keepalive()
	if err != nil {
		t.Errorf("WatchdogMonitor.Keepalive() when not leader should not error, got %v", err)
	}

	// Set as leader and try keepalive
	m.SetLeaderState(true)
	err = m.Keepalive()
	// Should still not error since watchdog isn't actually open
	if err != nil {
		t.Errorf("WatchdogMonitor.Keepalive() as leader should not error, got %v", err)
	}
}

func TestWatchdogInfoStruct(t *testing.T) {
	info := WatchdogInfo{
		Options:         0x8180,
		FirmwareVersion: 1,
	}
	copy(info.Identity[:], "test watchdog")

	if info.Options != 0x8180 {
		t.Errorf("WatchdogInfo.Options = %x, want 0x8180", info.Options)
	}

	if info.FirmwareVersion != 1 {
		t.Errorf("WatchdogInfo.FirmwareVersion = %d, want 1", info.FirmwareVersion)
	}

	identity := string(info.Identity[:13])
	if identity != "test watchdog" {
		t.Errorf("WatchdogInfo.Identity = %q, want %q", identity, "test watchdog")
	}
}

func TestIoctlConstants(t *testing.T) {
	// Verify ioctl constants are defined
	if WDIOC_GETSUPPORT == 0 {
		t.Error("WDIOC_GETSUPPORT should not be 0")
	}

	if WDIOC_KEEPALIVE == 0 {
		t.Error("WDIOC_KEEPALIVE should not be 0")
	}

	if WDIOC_SETTIMEOUT == 0 {
		t.Error("WDIOC_SETTIMEOUT should not be 0")
	}

	if WDIOC_GETTIMEOUT == 0 {
		t.Error("WDIOC_GETTIMEOUT should not be 0")
	}

	if WDIOS_DISABLECARD == 0 {
		t.Error("WDIOS_DISABLECARD should not be 0")
	}

	if WDIOS_ENABLECARD == 0 {
		t.Error("WDIOS_ENABLECARD should not be 0")
	}
}

func TestConfigStruct(t *testing.T) {
	cfg := Config{
		Mode:         ModeAutomatic,
		Device:       "/dev/watchdog0",
		SafetyMargin: 10 * time.Second,
	}

	if cfg.Mode != ModeAutomatic {
		t.Errorf("Config.Mode = %q, want %q", cfg.Mode, ModeAutomatic)
	}

	if cfg.Device != "/dev/watchdog0" {
		t.Errorf("Config.Device = %q, want /dev/watchdog0", cfg.Device)
	}

	if cfg.SafetyMargin != 10*time.Second {
		t.Errorf("Config.SafetyMargin = %v, want %v", cfg.SafetyMargin, 10*time.Second)
	}
}

func TestCalculateKeepaliveIntervalVerySmallTimeout(t *testing.T) {
	w, _ := New(&Config{
		Mode:         ModeOff,
		SafetyMargin: 3 * time.Second,
	})
	// Set timeout smaller than safety margin - should result in 1 second minimum
	w.timeout = 2 * time.Second

	result := w.CalculateKeepaliveInterval(10 * time.Second)
	if result != 1*time.Second {
		t.Errorf("CalculateKeepaliveInterval() with small timeout = %v, want %v", result, 1*time.Second)
	}
}

func TestWatchdogCloseWithoutOpen(t *testing.T) {
	w, _ := New(&Config{Mode: ModeAutomatic})

	// Close without ever opening
	err := w.Close()
	if err != nil {
		t.Errorf("Close() without open should not error, got %v", err)
	}
}

func TestWatchdogMonitorSetLeaderStateSameState(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	// Initially not leader
	m.SetLeaderState(false)
	// Set to false again - should be no-op
	m.SetLeaderState(false)

	// Now set to true
	m.SetLeaderState(true)
	// Set to true again - should be no-op
	m.SetLeaderState(true)

	// Back to false
	m.SetLeaderState(false)
}

func TestWatchdogMonitorNilConfig(t *testing.T) {
	m, err := NewWatchdogMonitor(nil)
	if err != nil {
		t.Fatalf("NewWatchdogMonitor(nil) error = %v", err)
	}

	if m == nil {
		t.Fatal("NewWatchdogMonitor(nil) returned nil")
	}

	if m.watchdog == nil {
		t.Error("WatchdogMonitor.watchdog should not be nil with nil config")
	}
}

func TestWatchdogConcurrentAccess(t *testing.T) {
	w, _ := New(&Config{Mode: ModeOff})

	done := make(chan bool, 4)

	go func() {
		for i := 0; i < 100; i++ {
			_ = w.IsActive()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = w.GetTimeout()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = w.Mode()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = w.CalculateKeepaliveInterval(10 * time.Second)
		}
		done <- true
	}()

	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestWatchdogMonitorConcurrentAccess(t *testing.T) {
	cfg := &Config{Mode: ModeOff}
	m, _ := NewWatchdogMonitor(cfg)

	done := make(chan bool, 3)

	go func() {
		for i := 0; i < 100; i++ {
			m.SetLeaderState(i%2 == 0)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = m.IsActive()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = m.Keepalive()
		}
		done <- true
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestIsAvailableWithDevNull(t *testing.T) {
	// /dev/null exists on Linux
	available := IsAvailable("/dev/null")
	if !available {
		t.Error("IsAvailable(\"/dev/null\") should return true since file exists")
	}
}

func TestWatchdogAllModes(t *testing.T) {
	modes := []string{ModeOff, ModeAutomatic, ModeRequired, "custom"}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			cfg := &Config{
				Mode:   mode,
				Device: "/dev/nonexistent_watchdog",
			}

			w, err := New(cfg)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if w.Mode() != mode {
				t.Errorf("Mode() = %q, want %q", w.Mode(), mode)
			}
		})
	}
}
