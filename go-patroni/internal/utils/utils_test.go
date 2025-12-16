package utils

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseBool(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected *bool
	}{
		// Boolean inputs
		{"bool true", true, boolPtr(true)},
		{"bool false", false, boolPtr(false)},

		// String inputs - true variants
		{"string on", "on", boolPtr(true)},
		{"string ON", "ON", boolPtr(true)},
		{"string true", "true", boolPtr(true)},
		{"string TRUE", "TRUE", boolPtr(true)},
		{"string yes", "yes", boolPtr(true)},
		{"string YES", "YES", boolPtr(true)},
		{"string 1", "1", boolPtr(true)},

		// String inputs - false variants
		{"string off", "off", boolPtr(false)},
		{"string OFF", "OFF", boolPtr(false)},
		{"string false", "false", boolPtr(false)},
		{"string FALSE", "FALSE", boolPtr(false)},
		{"string no", "no", boolPtr(false)},
		{"string NO", "NO", boolPtr(false)},
		{"string 0", "0", boolPtr(false)},

		// Invalid string inputs
		{"string invalid", "invalid", nil},
		{"string empty", "", nil},
		{"string maybe", "maybe", nil},

		// Numeric inputs
		{"int 1", 1, boolPtr(true)},
		{"int 0", 0, boolPtr(false)},
		{"int 42", 42, boolPtr(true)},
		{"int -1", -1, boolPtr(true)},
		{"int64 1", int64(1), boolPtr(true)},
		{"int64 0", int64(0), boolPtr(false)},
		{"float64 1.0", float64(1.0), boolPtr(true)},
		{"float64 0.0", float64(0.0), boolPtr(false)},
		{"float64 0.5", float64(0.5), boolPtr(false)}, // truncates to 0

		// Invalid types
		{"slice", []string{"a"}, nil},
		{"map", map[string]string{}, nil},
		{"nil", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseBool(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("ParseBool(%v) = %v, want nil", tt.input, *result)
				}
			} else {
				if result == nil {
					t.Errorf("ParseBool(%v) = nil, want %v", tt.input, *tt.expected)
				} else if *result != *tt.expected {
					t.Errorf("ParseBool(%v) = %v, want %v", tt.input, *result, *tt.expected)
				}
			}
		})
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		name      string
		input     interface{}
		expected  int64
		expectErr bool
	}{
		// Direct numeric types
		{"int", 42, 42, false},
		{"int64", int64(1000), 1000, false},
		{"float64", float64(3.14), 3, false},

		// Plain string numbers
		{"string plain", "123", 123, false},
		{"string negative", "-456", -456, false},
		{"string zero", "0", 0, false},
		{"string empty", "", 0, false},
		{"string whitespace", "  100  ", 100, false},

		// Memory units
		{"string B", "100B", 100, false},
		{"string KB", "1KB", 1024, false},
		{"string K", "2K", 2048, false},
		{"string MB", "1MB", 1024 * 1024, false},
		{"string M", "1M", 1024 * 1024, false},
		{"string GB", "1GB", 1024 * 1024 * 1024, false},
		{"string G", "1G", 1024 * 1024 * 1024, false},
		{"string TB", "1TB", 1024 * 1024 * 1024 * 1024, false},
		{"string T", "1T", 1024 * 1024 * 1024 * 1024, false},

		// Time units
		{"string US", "1US", 1, false},
		{"string MS", "100MS", 100000, false},
		{"string S", "1S", 1000000, false},
		{"string H", "1H", 1000 * 1000 * 60 * 60, false},
		{"string D", "1D", 1000 * 1000 * 60 * 60 * 24, false},

		// Case insensitivity
		{"string lowercase kb", "1kb", 1024, false},
		{"string mixed KB", "1Kb", 1024, false},

		// With spaces between number and unit
		{"string space unit", "10 MB", 10 * 1024 * 1024, false},

		// Invalid inputs
		{"string invalid", "abc", 0, true},
		{"invalid type slice", []int{1, 2}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseInt(tt.input)

			if tt.expectErr {
				if err == nil {
					t.Errorf("ParseInt(%v) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseInt(%v) unexpected error: %v", tt.input, err)
				} else if result != tt.expected {
					t.Errorf("ParseInt(%v) = %d, want %d", tt.input, result, tt.expected)
				}
			}
		})
	}
}

func TestDeepCompare(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]interface{}
		b        map[string]interface{}
		expected bool
	}{
		{
			"identical empty maps",
			map[string]interface{}{},
			map[string]interface{}{},
			true,
		},
		{
			"identical simple maps",
			map[string]interface{}{"key": "value"},
			map[string]interface{}{"key": "value"},
			true,
		},
		{
			"different values",
			map[string]interface{}{"key": "value1"},
			map[string]interface{}{"key": "value2"},
			false,
		},
		{
			"different keys",
			map[string]interface{}{"key1": "value"},
			map[string]interface{}{"key2": "value"},
			false,
		},
		{
			"different lengths",
			map[string]interface{}{"key1": "value1", "key2": "value2"},
			map[string]interface{}{"key1": "value1"},
			false,
		},
		{
			"nested maps identical",
			map[string]interface{}{
				"nested": map[string]interface{}{"inner": "value"},
			},
			map[string]interface{}{
				"nested": map[string]interface{}{"inner": "value"},
			},
			true,
		},
		{
			"nested maps different",
			map[string]interface{}{
				"nested": map[string]interface{}{"inner": "value1"},
			},
			map[string]interface{}{
				"nested": map[string]interface{}{"inner": "value2"},
			},
			false,
		},
		{
			"numeric values",
			map[string]interface{}{"num": 42},
			map[string]interface{}{"num": 42},
			true,
		},
		{
			"missing key in b",
			map[string]interface{}{"key1": "value1", "key2": "value2"},
			map[string]interface{}{"key1": "value1", "key3": "value2"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DeepCompare(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("DeepCompare(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestPatchConfig(t *testing.T) {
	tests := []struct {
		name            string
		config          map[string]interface{}
		patch           map[string]interface{}
		expectedConfig  map[string]interface{}
		expectedChanged bool
	}{
		{
			"add new key",
			map[string]interface{}{"existing": "value"},
			map[string]interface{}{"new": "added"},
			map[string]interface{}{"existing": "value", "new": "added"},
			true,
		},
		{
			"update existing key",
			map[string]interface{}{"key": "old"},
			map[string]interface{}{"key": "new"},
			map[string]interface{}{"key": "new"},
			true,
		},
		{
			"delete key with nil",
			map[string]interface{}{"key": "value", "delete": "me"},
			map[string]interface{}{"delete": nil},
			map[string]interface{}{"key": "value"},
			true,
		},
		{
			"no change same value",
			map[string]interface{}{"key": "same"},
			map[string]interface{}{"key": "same"},
			map[string]interface{}{"key": "same"},
			false,
		},
		{
			"delete non-existent key",
			map[string]interface{}{"key": "value"},
			map[string]interface{}{"nonexistent": nil},
			map[string]interface{}{"key": "value"},
			false,
		},
		{
			"nested patch",
			map[string]interface{}{
				"nested": map[string]interface{}{"a": "1", "b": "2"},
			},
			map[string]interface{}{
				"nested": map[string]interface{}{"b": "changed"},
			},
			map[string]interface{}{
				"nested": map[string]interface{}{"a": "1", "b": "changed"},
			},
			true,
		},
		{
			"empty patch",
			map[string]interface{}{"key": "value"},
			map[string]interface{}{},
			map[string]interface{}{"key": "value"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a deep copy to avoid modifying test data
			config := deepCopyMap(tt.config)

			changed := PatchConfig(config, tt.patch)

			if changed != tt.expectedChanged {
				t.Errorf("PatchConfig() changed = %v, want %v", changed, tt.expectedChanged)
			}

			if !DeepCompare(config, tt.expectedConfig) {
				t.Errorf("PatchConfig() config = %v, want %v", config, tt.expectedConfig)
			}
		})
	}
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		input        string
		expectedHost string
		expectedPort string
	}{
		{"localhost:5432", "localhost", "5432"},
		{"127.0.0.1:8080", "127.0.0.1", "8080"},
		{"[::1]:5432", "::1", "5432"},
		{"hostname", "hostname", ""},
		{"localhost:", "localhost", ""},
		{":5432", "", "5432"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			host, port := SplitHostPort(tt.input)
			if host != tt.expectedHost || port != tt.expectedPort {
				t.Errorf("SplitHostPort(%q) = (%q, %q), want (%q, %q)",
					tt.input, host, port, tt.expectedHost, tt.expectedPort)
			}
		})
	}
}

func TestURI(t *testing.T) {
	tests := []struct {
		scheme   string
		hostport string
		expected string
	}{
		{"http", "localhost:8080", "http://localhost:8080"},
		{"https", "example.com:443", "https://example.com:443"},
		{"postgres", "127.0.0.1:5432", "postgres://127.0.0.1:5432"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := URI(tt.scheme, tt.hostport)
			if result != tt.expected {
				t.Errorf("URI(%q, %q) = %q, want %q", tt.scheme, tt.hostport, result, tt.expected)
			}
		})
	}
}

func TestRetry(t *testing.T) {
	t.Run("max tries reached", func(t *testing.T) {
		// MaxTries=3 means attempts 1 and 2 return true, attempt 3 returns false
		retry := NewRetry(3, 0, time.Second)
		err := errors.New("test error")

		// First call: attempts becomes 1, 1 < 3, returns true
		if !retry.ShouldRetry(err) {
			t.Error("ShouldRetry() should return true on first attempt")
		}
		// Second call: attempts becomes 2, 2 < 3, returns true
		if !retry.ShouldRetry(err) {
			t.Error("ShouldRetry() should return true on second attempt")
		}
		// Third call: attempts becomes 3, 3 >= 3, returns false
		if retry.ShouldRetry(err) {
			t.Error("ShouldRetry() should return false when attempts >= MaxTries")
		}
	})

	t.Run("deadline reached", func(t *testing.T) {
		retry := NewRetry(100, 10*time.Millisecond, time.Second)
		err := errors.New("test error")

		// Wait for deadline to pass
		time.Sleep(20 * time.Millisecond)

		if retry.ShouldRetry(err) {
			t.Error("ShouldRetry() should return false after deadline")
		}
	})

	t.Run("no error no retry", func(t *testing.T) {
		retry := NewRetry(3, time.Hour, time.Second)

		if retry.ShouldRetry(nil) {
			t.Error("ShouldRetry(nil) should return false")
		}
	})

	t.Run("delay increases exponentially", func(t *testing.T) {
		retry := NewRetry(10, time.Hour, time.Second)
		err := errors.New("test error")

		expectedDelays := []time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
			800 * time.Millisecond,
			time.Second, // capped at MaxDelay
		}

		for i, expected := range expectedDelays {
			retry.ShouldRetry(err)
			delay := retry.Delay()
			if delay != expected {
				t.Errorf("Delay() on attempt %d = %v, want %v", i+1, delay, expected)
			}
		}
	})
}

func TestPollingLoop(t *testing.T) {
	t.Run("success on first try", func(t *testing.T) {
		ctx := context.Background()
		result := PollingLoop(ctx, time.Second, 10*time.Millisecond, func() bool {
			return true
		})

		if !result {
			t.Error("PollingLoop() should return true when fn succeeds")
		}
	})

	t.Run("success after retry", func(t *testing.T) {
		ctx := context.Background()
		attempts := 0

		result := PollingLoop(ctx, time.Second, 10*time.Millisecond, func() bool {
			attempts++
			return attempts >= 3
		})

		if !result {
			t.Error("PollingLoop() should return true when fn eventually succeeds")
		}
		if attempts < 3 {
			t.Errorf("PollingLoop() attempts = %d, want >= 3", attempts)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx := context.Background()
		result := PollingLoop(ctx, 50*time.Millisecond, 10*time.Millisecond, func() bool {
			return false
		})

		if result {
			t.Error("PollingLoop() should return false on timeout")
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		result := PollingLoop(ctx, time.Second, 10*time.Millisecond, func() bool {
			return false
		})

		if result {
			t.Error("PollingLoop() should return false when context is cancelled")
		}
	})
}

func TestNewRetry(t *testing.T) {
	r := NewRetry(5, 30*time.Second, 2*time.Second)

	if r.MaxTries != 5 {
		t.Errorf("NewRetry().MaxTries = %d, want 5", r.MaxTries)
	}
	if r.Deadline != 30*time.Second {
		t.Errorf("NewRetry().Deadline = %v, want 30s", r.Deadline)
	}
	if r.MaxDelay != 2*time.Second {
		t.Errorf("NewRetry().MaxDelay = %v, want 2s", r.MaxDelay)
	}
	if r.deadline.IsZero() {
		t.Error("NewRetry().deadline should not be zero")
	}
}

func TestRetryWithNoMaxTries(t *testing.T) {
	retry := NewRetry(0, time.Hour, time.Second)
	err := errors.New("test error")

	// With MaxTries=0, only deadline should limit retries
	for i := 0; i < 100; i++ {
		if !retry.ShouldRetry(err) {
			t.Errorf("ShouldRetry() returned false on attempt %d with MaxTries=0", i)
			break
		}
	}
}

func TestRetryWithNoDeadline(t *testing.T) {
	retry := NewRetry(3, 0, time.Second)
	err := errors.New("test error")

	// With Deadline=0, only MaxTries should limit
	// MaxTries=3: attempts 1 and 2 return true, attempt 3 returns false
	retry.ShouldRetry(err) // attempts = 1
	retry.ShouldRetry(err) // attempts = 2

	// Third call should return false since attempts will be 3 >= MaxTries
	if retry.ShouldRetry(err) {
		t.Error("ShouldRetry() should return false after max tries even with no deadline")
	}
}

// Helper function to create a bool pointer
func boolPtr(b bool) *bool {
	return &b
}

// Helper function to deep copy a map
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			result[k] = deepCopyMap(nested)
		} else {
			result[k] = v
		}
	}
	return result
}
