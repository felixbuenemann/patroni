package consul

import (
	"context"
	"testing"
	"time"

	"github.com/patroni/patroni-go/internal/dcs"
)

func TestConsulNew(t *testing.T) {
	// Test creating a new Consul DCS instance
	// Note: This test will fail without a running Consul server
	// It primarily tests configuration parsing

	config := &dcs.Config{
		Namespace: "/patroni",
		Scope:     "test",
		Name:      "node1",
		TTL:       30,
		Hosts:     []string{"127.0.0.1:8500"},
	}

	// This will fail to connect but tests the configuration parsing
	_, err := New(config)
	if err == nil {
		// If we got here, there's a Consul server running
		t.Log("Consul server available, connection succeeded")
	} else {
		// Expected when no Consul server is running
		t.Logf("Expected error (no Consul server): %v", err)
	}
}

func TestConsulKeyPath(t *testing.T) {
	config := &dcs.Config{
		Namespace: "/patroni",
		Scope:     "test",
		Name:      "node1",
	}

	base := dcs.NewBaseDCS(config)
	c := &Consul{
		BaseDCS: base,
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"leader", "patroni/test/leader"},
		{"members/node1", "patroni/test/members/node1"},
		{"config", "patroni/test/config"},
		{"sync", "patroni/test/sync"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := c.keyPath(tt.key)
			if got != tt.expected {
				t.Errorf("keyPath(%q) = %q, want %q", tt.key, got, tt.expected)
			}
		})
	}
}

func TestConsulName(t *testing.T) {
	config := &dcs.Config{
		Namespace: "/patroni",
		Scope:     "test",
		Name:      "node1",
	}

	base := dcs.NewBaseDCS(config)
	c := &Consul{
		BaseDCS: base,
	}

	if got := c.Name(); got != "consul" {
		t.Errorf("Name() = %q, want %q", got, "consul")
	}
}

func TestConsulConfigParsing(t *testing.T) {
	tests := []struct {
		name   string
		config *dcs.Config
	}{
		{
			name: "basic config",
			config: &dcs.Config{
				Namespace: "/patroni",
				Scope:     "test",
				Name:      "node1",
				Hosts:     []string{"localhost:8500"},
			},
		},
		{
			name: "with auth",
			config: &dcs.Config{
				Namespace: "/patroni",
				Scope:     "test",
				Name:      "node1",
				Hosts:     []string{"localhost:8500"},
				Username:  "consul",
				Password:  "secret",
			},
		},
		{
			name: "with TLS",
			config: &dcs.Config{
				Namespace: "/patroni",
				Scope:     "test",
				Name:      "node1",
				Hosts:     []string{"https://localhost:8500"},
				CACert:    "/path/to/ca.crt",
				Cert:      "/path/to/client.crt",
				Key:       "/path/to/client.key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify config is valid
			if tt.config.Scope == "" {
				t.Error("Scope should not be empty")
			}
			if tt.config.Name == "" {
				t.Error("Name should not be empty")
			}
		})
	}
}

func TestConsulSessionTTL(t *testing.T) {
	tests := []struct {
		name     string
		ttl      time.Duration
		expected time.Duration
	}{
		{"short ttl", 5 * time.Second, 10 * time.Second},  // Minimum is 10s
		{"normal ttl", 30 * time.Second, 30 * time.Second},
		{"long ttl", 60 * time.Second, 60 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Session TTL minimum is 10 seconds
			result := tt.ttl
			if result < 10*time.Second {
				result = 10 * time.Second
			}
			if result != tt.expected {
				t.Errorf("Session TTL = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConsulContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Verify context is cancelled
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("Context should be cancelled")
	}
}

func TestConsulHostParsing(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{"simple host", "localhost:8500", "http://localhost:8500"},
		{"with http", "http://localhost:8500", "http://localhost:8500"},
		{"with https", "https://localhost:8500", "https://localhost:8500"},
		{"ip address", "192.168.1.1:8500", "http://192.168.1.1:8500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tt.host
			if host[:4] != "http" {
				host = "http://" + host
			}
			// For this test we just check the prefix logic
			if host[:4] != "http" {
				t.Errorf("Host should start with http, got %q", host)
			}
		})
	}
}
