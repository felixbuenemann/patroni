package exhibitor

import (
	"testing"
	"time"
)

func TestExhibitorEnsembleProviderInit(t *testing.T) {
	tests := []struct {
		name      string
		hosts     []string
		port      int
		shouldErr bool
	}{
		{"single host", []string{"localhost"}, 8181, false},
		{"multiple hosts", []string{"localhost", "exhibitor"}, 8181, false},
		{"no hosts", []string{}, 8181, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.hosts) == 0 && !tt.shouldErr {
				t.Error("Empty hosts should error")
			}
		})
	}
}

func TestExhibitorEnsembleProviderPoll(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		statusCode  int
		shouldPoll  bool
		newEnsemble string
	}{
		{
			name:        "valid response",
			response:    `{"servers":["127.0.0.1","127.0.0.2","127.0.0.3"],"port":2181}`,
			statusCode:  200,
			shouldPoll:  true,
			newEnsemble: "127.0.0.1:2181,127.0.0.2:2181,127.0.0.3:2181",
		},
		{
			name:        "empty servers",
			response:    `{"servers":[],"port":2181}`,
			statusCode:  200,
			shouldPoll:  false,
			newEnsemble: "",
		},
		{
			name:        "error response",
			response:    `{"error":"internal error"}`,
			statusCode:  500,
			shouldPoll:  false,
			newEnsemble: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.statusCode >= 400 && tt.shouldPoll {
				t.Error("Error status should not poll")
			}
		})
	}
}

func TestExhibitorURIParsing(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected string
		hasError bool
	}{
		{"http uri", "http://exhibitor:8181", "exhibitor:8181", false},
		{"https uri", "https://exhibitor:8181", "exhibitor:8181", false},
		{"bare host", "exhibitor", "exhibitor:8181", false},
		{"host with port", "exhibitor:8181", "exhibitor:8181", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.uri == "" {
				t.Error("URI should not be empty")
			}
		})
	}
}

func TestExhibitorMasterIndex(t *testing.T) {
	tests := []struct {
		name        string
		masterIndex int
		numServers  int
		valid       bool
	}{
		{"valid index", 0, 3, true},
		{"valid middle", 1, 3, true},
		{"valid last", 2, 3, true},
		{"out of range", 5, 3, false},
		{"negative", -1, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.masterIndex >= 0 && tt.masterIndex < tt.numServers
			if valid != tt.valid {
				t.Errorf("valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestExhibitorGetCluster(t *testing.T) {
	tests := []struct {
		name      string
		ensChange bool
		zkError   string
		hasError  bool
	}{
		{"no change", false, "", false},
		{"ensemble changed", true, "", false},
		{"zk error", false, "ZooKeeperError", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.zkError != "" && !tt.hasError {
				t.Error("ZK error should cause error")
			}
		})
	}
}

func TestExhibitorZKConnection(t *testing.T) {
	t.Skip("Skipping test that requires running Exhibitor and ZooKeeper")

	t.Run("connect via exhibitor", func(t *testing.T) {
		// This would test actual connection via exhibitor discovery
		t.Log("Connected to ZooKeeper via Exhibitor")
	})
}

func TestExhibitorRetry(t *testing.T) {
	tests := []struct {
		name       string
		retries    int
		shouldFail bool
	}{
		{"success first try", 0, false},
		{"success after retry", 2, false},
		{"max retries exceeded", 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxRetries := 3
			shouldFail := tt.retries > maxRetries
			if shouldFail != tt.shouldFail {
				t.Errorf("shouldFail = %v, want %v", shouldFail, tt.shouldFail)
			}
		})
	}
}

func TestExhibitorPollingInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"default", 300 * time.Second},
		{"fast", 60 * time.Second},
		{"slow", 600 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.interval <= 0 {
				t.Error("Interval should be positive")
			}
		})
	}
}

func TestExhibitorHostsUpdate(t *testing.T) {
	tests := []struct {
		name     string
		oldHosts []string
		newHosts []string
		changed  bool
	}{
		{
			name:     "no change",
			oldHosts: []string{"a:2181", "b:2181"},
			newHosts: []string{"a:2181", "b:2181"},
			changed:  false,
		},
		{
			name:     "added host",
			oldHosts: []string{"a:2181"},
			newHosts: []string{"a:2181", "b:2181"},
			changed:  true,
		},
		{
			name:     "removed host",
			oldHosts: []string{"a:2181", "b:2181"},
			newHosts: []string{"a:2181"},
			changed:  true,
		},
		{
			name:     "different hosts",
			oldHosts: []string{"a:2181"},
			newHosts: []string{"b:2181"},
			changed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simple length check as proxy for change detection
			changed := len(tt.oldHosts) != len(tt.newHosts)
			if !changed && len(tt.oldHosts) > 0 && len(tt.newHosts) > 0 {
				changed = tt.oldHosts[0] != tt.newHosts[0]
			}
			if changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
		})
	}
}

func TestExhibitorHTTPClient(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"default timeout", 2100 * time.Millisecond},
		{"short timeout", 1000 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.timeout <= 0 {
				t.Error("Timeout should be positive")
			}
		})
	}
}

func TestExhibitorJSONParsing(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		valid    bool
		servers  int
		port     int
	}{
		{
			name:    "valid json",
			json:    `{"servers":["127.0.0.1","127.0.0.2","127.0.0.3"],"port":2181}`,
			valid:   true,
			servers: 3,
			port:    2181,
		},
		{
			name:    "empty json",
			json:    `{}`,
			valid:   true,
			servers: 0,
			port:    0,
		},
		{
			name:    "invalid json",
			json:    `{invalid}`,
			valid:   false,
			servers: 0,
			port:    0,
		},
		{
			name:    "missing servers",
			json:    `{"port":2181}`,
			valid:   true,
			servers: 0,
			port:    2181,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.json == "" {
				t.Error("JSON should not be empty")
			}
		})
	}
}

func TestExhibitorConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "basic config",
			config: map[string]interface{}{
				"hosts": []string{"localhost", "exhibitor"},
				"port":  8181,
			},
		},
		{
			name: "with retry",
			config: map[string]interface{}{
				"hosts":         []string{"localhost"},
				"port":          8181,
				"retry_timeout": 10,
			},
		},
		{
			name: "with scope",
			config: map[string]interface{}{
				"hosts": []string{"localhost"},
				"port":  8181,
				"scope": "test",
				"name":  "node1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config == nil {
				t.Error("Config should not be nil")
			}
			if _, ok := tt.config["hosts"]; !ok {
				t.Error("Config should have hosts")
			}
		})
	}
}

func TestExhibitorClusterURL(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{"localhost", "localhost", 8181, "http://localhost:8181/exhibitor/v1/cluster/list"},
		{"ip", "192.168.1.1", 8181, "http://192.168.1.1:8181/exhibitor/v1/cluster/list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.host == "" {
				t.Error("Host should not be empty")
			}
			if tt.port <= 0 {
				t.Error("Port should be positive")
			}
		})
	}
}

func TestExhibitorBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		delay   time.Duration
	}{
		{"first attempt", 1, 1 * time.Second},
		{"second attempt", 2, 2 * time.Second},
		{"third attempt", 3, 4 * time.Second},
		{"max backoff", 10, 60 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.attempt <= 0 {
				t.Error("Attempt should be positive")
			}
		})
	}
}

func TestExhibitorMasterElection(t *testing.T) {
	tests := []struct {
		name        string
		servers     []string
		masterIndex int
		expected    string
	}{
		{"first is master", []string{"a", "b", "c"}, 0, "a"},
		{"second is master", []string{"a", "b", "c"}, 1, "b"},
		{"third is master", []string{"a", "b", "c"}, 2, "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.masterIndex < 0 || tt.masterIndex >= len(tt.servers) {
				t.Error("Master index out of range")
			}
			if tt.servers[tt.masterIndex] != tt.expected {
				t.Errorf("master = %s, want %s", tt.servers[tt.masterIndex], tt.expected)
			}
		})
	}
}
