package patroni

import (
	"testing"
	"time"
)

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
	if Version != "4.0.0-go" {
		t.Errorf("Version = %q, want %q", Version, "4.0.0-go")
	}
}

func TestScheduledRestart(t *testing.T) {
	tests := []struct {
		name    string
		restart *ScheduledRestart
	}{
		{
			name: "pending restart",
			restart: &ScheduledRestart{
				Schedule:            time.Now().Add(time.Hour),
				PostmasterStartTime: time.Now(),
				Pending:             true,
			},
		},
		{
			name: "completed restart",
			restart: &ScheduledRestart{
				Schedule:            time.Now().Add(-time.Hour),
				PostmasterStartTime: time.Now(),
				Pending:             false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.restart.Schedule.IsZero() {
				t.Error("Schedule should not be zero")
			}
		})
	}
}

func TestOptions(t *testing.T) {
	opts := Options{
		ConfigFile: "/etc/patroni/postgres0.yml",
		Validate:   true,
	}

	if opts.ConfigFile == "" {
		t.Error("ConfigFile should be set")
	}
	if !opts.Validate {
		t.Error("Validate should be true")
	}
}

// TestFilterTags tests the filterTags function through a Patroni instance
func TestFilterTags(t *testing.T) {
	p := &Patroni{
		tags: make(map[string]interface{}),
	}

	tests := []struct {
		name     string
		tags     map[string]interface{}
		contains string
		absent   string
	}{
		{
			name:     "remove false noloadbalance",
			tags:     map[string]interface{}{"noloadbalance": false, "smth": "random"},
			contains: "smth",
			absent:   "noloadbalance",
		},
		{
			name:     "keep true noloadbalance",
			tags:     map[string]interface{}{"noloadbalance": true, "other": "value"},
			contains: "noloadbalance",
		},
		{
			name:     "keep non-bool noloadbalance",
			tags:     map[string]interface{}{"noloadbalance": "maybe", "test": true},
			contains: "noloadbalance",
		},
		{
			name:     "nil tags",
			tags:     nil,
			contains: "",
		},
		{
			name:     "empty tags",
			tags:     map[string]interface{}{},
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.filterTags(tt.tags)

			if result == nil {
				t.Fatal("filterTags should not return nil")
			}

			if tt.contains != "" {
				if _, ok := result[tt.contains]; !ok {
					t.Errorf("result should contain %q", tt.contains)
				}
			}

			if tt.absent != "" {
				if _, ok := result[tt.absent]; ok {
					t.Errorf("result should not contain %q", tt.absent)
				}
			}
		})
	}
}

// TestPatroniStructFields tests the Patroni struct fields
func TestPatroniStructFields(t *testing.T) {
	p := &Patroni{
		tags:     make(map[string]interface{}),
		nextRun:  time.Now(),
		running:  false,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		reloadCh: make(chan struct{}, 1),
	}

	if p.tags == nil {
		t.Error("tags should be initialized")
	}
	if p.nextRun.IsZero() {
		t.Error("nextRun should be set")
	}
	if p.running {
		t.Error("running should be false initially")
	}
	if p.stopCh == nil {
		t.Error("stopCh should be initialized")
	}
	if p.doneCh == nil {
		t.Error("doneCh should be initialized")
	}
	if p.reloadCh == nil {
		t.Error("reloadCh should be initialized")
	}
}

// TestTags tests the Tags method
func TestTags(t *testing.T) {
	p := &Patroni{
		tags: map[string]interface{}{
			"nofailover": true,
			"clonefrom":  true,
		},
	}

	tags := p.Tags()
	if tags == nil {
		t.Fatal("Tags() should not return nil")
	}
	if tags["nofailover"] != true {
		t.Error("nofailover tag should be true")
	}
	if tags["clonefrom"] != true {
		t.Error("clonefrom tag should be true")
	}
}

// TestIsRunning tests the IsRunning method
func TestIsRunning(t *testing.T) {
	p := &Patroni{
		running: false,
	}

	if p.IsRunning() {
		t.Error("IsRunning() should return false initially")
	}

	p.running = true
	if !p.IsRunning() {
		t.Error("IsRunning() should return true after setting running")
	}
}

// TestWakeupHA tests the wakeupHA method
func TestWakeupHA(t *testing.T) {
	p := &Patroni{
		reloadCh: make(chan struct{}, 1),
	}

	// First call should succeed
	p.wakeupHA()

	// Check channel has one message
	select {
	case <-p.reloadCh:
		// Expected
	default:
		t.Error("wakeupHA should have sent a message")
	}

	// Call again and check it doesn't block
	p.wakeupHA()
	p.wakeupHA() // Second call when channel might be full should not block

	// Verify channel has at most one message
	select {
	case <-p.reloadCh:
		// Expected
	default:
		// Also OK if channel was empty
	}
}

// Test filterTags with various noloadbalance values
func TestFilterTagsNoLoadBalance(t *testing.T) {
	p := &Patroni{}

	tests := []struct {
		name   string
		input  interface{}
		expect bool
	}{
		{"true bool", true, true},
		{"false bool", false, false},
		{"string true", "true", true},
		{"int zero", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := map[string]interface{}{"noloadbalance": tt.input}
			result := p.filterTags(tags)

			_, present := result["noloadbalance"]
			if tt.expect != present {
				t.Errorf("noloadbalance presence = %v, want %v", present, tt.expect)
			}
		})
	}
}

// Test concurrent access to Tags
func TestTagsConcurrent(t *testing.T) {
	p := &Patroni{
		tags: map[string]interface{}{
			"key": "value",
		},
	}

	done := make(chan bool)

	// Start multiple goroutines reading tags
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = p.Tags()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Test concurrent access to IsRunning
func TestIsRunningConcurrent(t *testing.T) {
	p := &Patroni{}

	done := make(chan bool)

	// Start multiple goroutines
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = p.IsRunning()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Benchmark tests
func BenchmarkFilterTags(b *testing.B) {
	p := &Patroni{}
	tags := map[string]interface{}{
		"noloadbalance": false,
		"nofailover":    true,
		"clonefrom":     true,
		"custom":        "value",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.filterTags(tags)
	}
}

func BenchmarkTags(b *testing.B) {
	p := &Patroni{
		tags: map[string]interface{}{
			"nofailover": true,
			"clonefrom":  true,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Tags()
	}
}

func BenchmarkIsRunning(b *testing.B) {
	p := &Patroni{running: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.IsRunning()
	}
}
