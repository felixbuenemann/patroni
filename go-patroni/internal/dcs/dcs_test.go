package dcs

import (
	"testing"
)

func TestRegisterAndNew(t *testing.T) {
	// Test registering a factory
	called := false
	testFactory := func(config *Config) (DCS, error) {
		called = true
		return nil, nil
	}

	Register("test_backend", testFactory)

	// Verify registration
	if _, exists := factories["test_backend"]; !exists {
		t.Error("Factory was not registered")
	}

	// Test New with registered backend
	config := &Config{
		Namespace: "test",
		Scope:     "cluster1",
		Name:      "node1",
		Hosts:     []string{"localhost:2379"},
	}

	_, err := New("test_backend", config)
	if err != nil {
		t.Errorf("New() returned error: %v", err)
	}
	if !called {
		t.Error("Factory was not called")
	}

	// Test New with unregistered backend
	_, err = New("nonexistent", config)
	if err == nil {
		t.Error("New() should return error for unregistered backend")
	}
}

func TestBaseDCS(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
		TTL:       30,
		LoopWait:  10,
	}

	base := NewBaseDCS(config)

	// Test path methods
	tests := []struct {
		method   func() string
		expected string
	}{
		{base.MemberPath, "/patroni/cluster1/members/node1"},
		{base.LeaderPath, "/patroni/cluster1/leader"},
		{base.ConfigPath, "/patroni/cluster1/config"},
		{base.SyncPath, "/patroni/cluster1/sync"},
		{base.FailoverPath, "/patroni/cluster1/failover"},
		{base.HistoryPath, "/patroni/cluster1/history"},
		{base.InitializePath, "/patroni/cluster1/initialize"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.method(); got != tt.expected {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBaseDCSClusterPath(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "mycluster",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	if got := base.ClusterPath(); got != "/patroni/mycluster" {
		t.Errorf("ClusterPath() = %v, want /patroni/mycluster", got)
	}
}

func TestBaseDCSMembersPath(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "mycluster",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	if got := base.MembersPath(); got != "/patroni/mycluster/members/" {
		t.Errorf("MembersPath() = %v, want /patroni/mycluster/members/", got)
	}
}

func TestConfigGetHosts(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected []string
	}{
		{
			name: "multiple hosts",
			config: &Config{
				Hosts: []string{"host1:2379", "host2:2379"},
			},
			expected: []string{"host1:2379", "host2:2379"},
		},
		{
			name: "single host",
			config: &Config{
				Host: "localhost:2379",
			},
			expected: []string{"localhost:2379"},
		},
		{
			name: "hosts takes precedence",
			config: &Config{
				Hosts: []string{"host1:2379"},
				Host:  "localhost:2379",
			},
			expected: []string{"host1:2379"},
		},
		{
			name:     "no hosts",
			config:   &Config{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetHosts()
			if len(got) != len(tt.expected) {
				t.Errorf("GetHosts() length = %v, want %v", len(got), len(tt.expected))
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("GetHosts()[%d] = %v, want %v", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestConfigNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		scope     string
		expected  string
	}{
		{"with leading slash", "/patroni", "mycluster", "/patroni/mycluster"},
		{"without leading slash", "patroni", "mycluster", "patroni/mycluster"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Namespace: tt.namespace,
				Scope:     tt.scope,
			}
			base := NewBaseDCS(config)
			got := base.ClusterPath()
			if got != tt.expected {
				t.Errorf("ClusterPath() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDCSConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		valid  bool
	}{
		{
			name: "valid config",
			config: &Config{
				Namespace: "/patroni",
				Scope:     "cluster1",
				Name:      "node1",
				Hosts:     []string{"localhost:2379"},
			},
			valid: true,
		},
		{
			name: "missing scope",
			config: &Config{
				Namespace: "/patroni",
				Name:      "node1",
			},
			valid: false,
		},
		{
			name: "missing name",
			config: &Config{
				Namespace: "/patroni",
				Scope:     "cluster1",
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation
			valid := tt.config.Scope != "" && tt.config.Name != ""
			if valid != tt.valid {
				t.Errorf("Config validation = %v, want %v", valid, tt.valid)
			}
		})
	}
}
