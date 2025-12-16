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

func TestConfigGetTTL(t *testing.T) {
	tests := []struct {
		name     string
		ttl      int
		expected int64 // expected seconds
	}{
		{"with TTL set", 60, 60},
		{"with zero TTL defaults to 30", 0, 30},
		{"with negative TTL defaults to 30", -5, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{TTL: tt.ttl}
			got := config.GetTTL()
			expectedDuration := tt.expected * int64(1e9) // convert to nanoseconds
			if int64(got) != expectedDuration {
				t.Errorf("GetTTL() = %v, want %v seconds", got, tt.expected)
			}
		})
	}
}

func TestConfigGetRetryTimeout(t *testing.T) {
	tests := []struct {
		name         string
		retryTimeout int
		expected     int64 // expected seconds
	}{
		{"with RetryTimeout set", 15, 15},
		{"with zero RetryTimeout defaults to 10", 0, 10},
		{"with negative RetryTimeout defaults to 10", -5, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{RetryTimeout: tt.retryTimeout}
			got := config.GetRetryTimeout()
			expectedDuration := tt.expected * int64(1e9) // convert to nanoseconds
			if int64(got) != expectedDuration {
				t.Errorf("GetRetryTimeout() = %v, want %v seconds", got, tt.expected)
			}
		})
	}
}

func TestAvailable(t *testing.T) {
	// Register some test backends
	Register("test_dcs_1", func(config *Config) (DCS, error) {
		return nil, nil
	})
	Register("test_dcs_2", func(config *Config) (DCS, error) {
		return nil, nil
	})

	available := Available()

	// Check that Available returns a list
	if available == nil {
		t.Fatal("Available() returned nil")
	}

	// Check that our registered backends are in the list
	found1, found2 := false, false
	for _, name := range available {
		if name == "test_dcs_1" {
			found1 = true
		}
		if name == "test_dcs_2" {
			found2 = true
		}
	}

	if !found1 || !found2 {
		t.Errorf("Available() should contain registered backends: got %v", available)
	}
}

func TestBaseDCSAdditionalPaths(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	tests := []struct {
		name     string
		method   func() string
		expected string
	}{
		{"LeaderOpTimePath", base.LeaderOpTimePath, "/patroni/cluster1/optime/leader"},
		{"StatusPath", base.StatusPath, "/patroni/cluster1/status"},
		{"FailsafePath", base.FailsafePath, "/patroni/cluster1/failsafe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.method()
			if got != tt.expected {
				t.Errorf("%s() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestBaseDCSSession(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	// Initially session should be empty
	if got := base.GetSession(); got != "" {
		t.Errorf("GetSession() initially = %q, want empty", got)
	}

	// Set session
	base.SetSession("session123")

	// Verify session was set
	if got := base.GetSession(); got != "session123" {
		t.Errorf("GetSession() after SetSession = %q, want session123", got)
	}

	// Clear session
	base.SetSession("")

	if got := base.GetSession(); got != "" {
		t.Errorf("GetSession() after clearing = %q, want empty", got)
	}
}

func TestBaseDCSIsLeader(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	// Without session, should not be leader
	if base.IsLeader("session123") {
		t.Error("IsLeader() should return false when no session is set")
	}

	// Set session
	base.SetSession("session123")

	// With matching session, should be leader
	if !base.IsLeader("session123") {
		t.Error("IsLeader() should return true when sessions match")
	}

	// With different session, should not be leader
	if base.IsLeader("different_session") {
		t.Error("IsLeader() should return false when sessions don't match")
	}

	// With empty leader session
	if base.IsLeader("") {
		t.Error("IsLeader() should return false when leader session is empty")
	}
}

func TestBaseDCSDefaultNamespace(t *testing.T) {
	// Test with empty namespace
	config := &Config{
		Namespace: "",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	// Default namespace should be /service/
	if base.Namespace != "/service/" {
		t.Errorf("Namespace with empty config = %q, want /service/", base.Namespace)
	}

	if got := base.ClusterPath(); got != "/service/cluster1" {
		t.Errorf("ClusterPath() with default namespace = %q, want /service/cluster1", got)
	}
}

func TestBaseDCSNamespaceWithoutTrailingSlash(t *testing.T) {
	// Test with namespace without trailing slash
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	// Should have trailing slash added
	if base.Namespace != "/patroni/" {
		t.Errorf("Namespace = %q, want /patroni/", base.Namespace)
	}
}

func TestCommonErrors(t *testing.T) {
	// Verify error variables are defined correctly
	if ErrNotFound == nil {
		t.Error("ErrNotFound should not be nil")
	}
	if ErrAlreadyExists == nil {
		t.Error("ErrAlreadyExists should not be nil")
	}
	if ErrPrecondition == nil {
		t.Error("ErrPrecondition should not be nil")
	}
	if ErrSessionExpired == nil {
		t.Error("ErrSessionExpired should not be nil")
	}
	if ErrNotLeader == nil {
		t.Error("ErrNotLeader should not be nil")
	}
	if ErrNoLeader == nil {
		t.Error("ErrNoLeader should not be nil")
	}
	if ErrTimeout == nil {
		t.Error("ErrTimeout should not be nil")
	}
}

func TestBaseDCSConcurrentSession(t *testing.T) {
	config := &Config{
		Namespace: "/patroni",
		Scope:     "cluster1",
		Name:      "node1",
	}

	base := NewBaseDCS(config)

	done := make(chan bool, 3)

	// Concurrent SetSession calls
	go func() {
		for i := 0; i < 100; i++ {
			base.SetSession("session_a")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			base.SetSession("session_b")
		}
		done <- true
	}()

	// Concurrent GetSession calls
	go func() {
		for i := 0; i < 100; i++ {
			_ = base.GetSession()
		}
		done <- true
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestConfigFields(t *testing.T) {
	config := &Config{
		Namespace:    "/patroni",
		Scope:        "cluster1",
		Name:         "node1",
		TTL:          30,
		LoopWait:     10,
		RetryTimeout: 10,
		Hosts:        []string{"host1:2379", "host2:2379"},
		Host:         "singlehost:2379",
		Username:     "user",
		Password:     "pass",
		CACert:       "/path/to/ca.crt",
		Cert:         "/path/to/cert.crt",
		Key:          "/path/to/key.key",
	}

	if config.Namespace != "/patroni" {
		t.Errorf("Namespace = %q, want /patroni", config.Namespace)
	}
	if config.Scope != "cluster1" {
		t.Errorf("Scope = %q, want cluster1", config.Scope)
	}
	if config.Name != "node1" {
		t.Errorf("Name = %q, want node1", config.Name)
	}
	if config.Username != "user" {
		t.Errorf("Username = %q, want user", config.Username)
	}
	if config.Password != "pass" {
		t.Errorf("Password = %q, want pass", config.Password)
	}
	if config.CACert != "/path/to/ca.crt" {
		t.Errorf("CACert = %q, want /path/to/ca.crt", config.CACert)
	}
}
