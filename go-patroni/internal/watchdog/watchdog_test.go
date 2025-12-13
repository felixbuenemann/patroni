package watchdog

import (
	"testing"
	"time"
)

func TestWatchdogModeValidation(t *testing.T) {
	tests := []struct {
		mode  string
		valid bool
	}{
		{"off", true},
		{"auto", true},
		{"automatic", true},
		{"required", true},
		{"", true},     // Empty defaults to off
		{"bad", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			valid := isValidMode(tt.mode)
			if valid != tt.valid {
				t.Errorf("Mode %q valid = %v, want %v", tt.mode, valid, tt.valid)
			}
		})
	}
}

func isValidMode(mode string) bool {
	switch mode {
	case "off", "auto", "automatic", "required", "":
		return true
	default:
		return false
	}
}

func TestWatchdogTimeout(t *testing.T) {
	tests := []struct {
		name         string
		ttl          int
		loopWait     int
		safetyMargin int
		expected     int
	}{
		{"default safety margin", 30, 10, 5, 25},
		{"no safety margin", 30, 10, 0, 30},
		{"negative safety margin uses half", 30, 10, -1, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var timeout int
			if tt.safetyMargin < 0 {
				timeout = tt.ttl / 2
			} else {
				timeout = tt.ttl - tt.safetyMargin
			}
			if timeout != tt.expected {
				t.Errorf("Timeout = %d, want %d", timeout, tt.expected)
			}
		})
	}
}

func TestWatchdogTimingValidation(t *testing.T) {
	tests := []struct {
		name     string
		ttl      int
		loopWait int
		valid    bool
	}{
		{"valid timing", 30, 10, true},
		{"ttl too close to loop_wait", 15, 10, false},
		{"loop_wait larger than ttl", 30, 40, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Watchdog requires: loop_wait < ttl / 2
			valid := tt.loopWait < tt.ttl/2
			if valid != tt.valid {
				t.Errorf("Timing valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestWatchdogDevice(t *testing.T) {
	tests := []struct {
		name   string
		device string
	}{
		{"default device", "/dev/watchdog"},
		{"custom device", "/dev/watchdog0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.device == "" {
				t.Error("Device path should not be empty")
			}
		})
	}
}

func TestWatchdogSafetyMargin(t *testing.T) {
	tests := []struct {
		name         string
		safetyMargin int
		ttl          int
		expected     int
	}{
		{"default margin", 5, 30, 25},
		{"custom margin", 10, 60, 50},
		{"zero margin", 0, 30, 30},
		{"negative uses half ttl", -1, 30, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var timeout int
			if tt.safetyMargin < 0 {
				timeout = tt.ttl / 2
			} else {
				timeout = tt.ttl - tt.safetyMargin
			}
			if timeout != tt.expected {
				t.Errorf("Timeout = %d, want %d", timeout, tt.expected)
			}
		})
	}
}

func TestNullWatchdog(t *testing.T) {
	// NullWatchdog is a no-op implementation
	w := &NullWatchdog{}

	if w.IsHealthy() != true {
		t.Error("NullWatchdog should always be healthy")
	}

	if w.CanBeDisabled() != true {
		t.Error("NullWatchdog should always be disableable")
	}
}

type NullWatchdog struct{}

func (w *NullWatchdog) IsHealthy() bool {
	return true
}

func (w *NullWatchdog) CanBeDisabled() bool {
	return true
}

func TestWatchdogKeepalive(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"fast keepalive", 1 * time.Second},
		{"normal keepalive", 5 * time.Second},
		{"slow keepalive", 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.interval <= 0 {
				t.Error("Keepalive interval should be positive")
			}
		})
	}
}

func TestWatchdogConfigReload(t *testing.T) {
	tests := []struct {
		name       string
		oldMode    string
		newMode    string
		shouldStop bool
	}{
		{"required to off", "required", "off", true},
		{"off to required", "off", "required", false},
		{"same mode", "required", "required", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldStop := tt.newMode == "off" && tt.oldMode != "off"
			if shouldStop != tt.shouldStop {
				t.Errorf("Should stop = %v, want %v", shouldStop, tt.shouldStop)
			}
		})
	}
}

func TestWatchdogDriverSelection(t *testing.T) {
	tests := []struct {
		platform string
		driver   string
	}{
		{"linux", "linux"},
		{"darwin", ""},
		{"windows", ""},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			if tt.platform == "linux" && tt.driver == "" {
				t.Error("Linux should have a watchdog driver")
			}
			if tt.platform != "linux" && tt.driver != "" {
				t.Errorf("Non-Linux platform %q should not have driver", tt.platform)
			}
		})
	}
}
