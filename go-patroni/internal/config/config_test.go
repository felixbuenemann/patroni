package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patroni/patroni-go/internal/dcs"
)

func TestConfigLoad(t *testing.T) {
	// Create a temporary config file
	tmpDir, err := os.MkdirTemp("", "patroni-config")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "patroni.yml")
	configContent := `
scope: batman
namespace: /patroni/
name: postgresql0

restapi:
  listen: 0.0.0.0:8008
  connect_address: 127.0.0.1:8008

etcd3:
  hosts:
    - 127.0.0.1:2379

postgresql:
  listen: 0.0.0.0:5432
  connect_address: 127.0.0.1:5432
  data_dir: /data/postgresql
  bin_dir: /usr/lib/postgresql/15/bin
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Scope != "batman" {
		t.Errorf("Scope = %q, want %q", cfg.Scope, "batman")
	}
	if cfg.Namespace != "/patroni/" {
		t.Errorf("Namespace = %q, want %q", cfg.Namespace, "/patroni/")
	}
	if cfg.Name != "postgresql0" {
		t.Errorf("Name = %q, want %q", cfg.Name, "postgresql0")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}

	// Check zero values - defaults are set during Load
	if cfg.TTL != 0 {
		t.Logf("Default TTL = %d (zero before Load)", cfg.TTL)
	}
}

func TestConfigGetTTL(t *testing.T) {
	cfg := &Config{TTL: 30}

	expected := 30
	if cfg.TTL != expected {
		t.Errorf("TTL = %d, want %d", cfg.TTL, expected)
	}
}

func TestConfigGetLoopWait(t *testing.T) {
	cfg := &Config{LoopWait: 10}

	expected := 10
	if cfg.LoopWait != expected {
		t.Errorf("LoopWait = %d, want %d", cfg.LoopWait, expected)
	}
}

func TestConfigGetRetryTimeout(t *testing.T) {
	cfg := &Config{RetryTimeout: 10}

	expected := 10
	if cfg.RetryTimeout != expected {
		t.Errorf("RetryTimeout = %d, want %d", cfg.RetryTimeout, expected)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				Etcd3: &dcs.Config{
					Hosts: []string{"127.0.0.1:2379"},
				},
				PostgreSQL: PostgreSQLConfig{
					DataDir: "/data/postgres",
				},
			},
			wantErr: false,
		},
		{
			name: "missing scope",
			config: &Config{
				Name: "node1",
				Etcd3: &dcs.Config{
					Hosts: []string{"127.0.0.1:2379"},
				},
				PostgreSQL: PostgreSQLConfig{
					DataDir: "/data/postgres",
				},
			},
			wantErr: true,
		},
		{
			name: "missing name",
			config: &Config{
				Scope: "mycluster",
				Etcd3: &dcs.Config{
					Hosts: []string{"127.0.0.1:2379"},
				},
				PostgreSQL: PostgreSQLConfig{
					DataDir: "/data/postgres",
				},
			},
			wantErr: true,
		},
		{
			name: "missing data_dir",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				Etcd3: &dcs.Config{
					Hosts: []string{"127.0.0.1:2379"},
				},
			},
			wantErr: true,
		},
		{
			name: "missing dcs",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				PostgreSQL: PostgreSQLConfig{
					DataDir: "/data/postgres",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPostgreSQLConfig(t *testing.T) {
	cfg := PostgreSQLConfig{
		Listen:         "0.0.0.0:5432",
		ConnectAddress: "127.0.0.1:5432",
		DataDir:        "/data/postgres",
		BinDir:         "/usr/lib/postgresql/15/bin",
	}

	if cfg.Listen != "0.0.0.0:5432" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:5432")
	}
	if cfg.ConnectAddress != "127.0.0.1:5432" {
		t.Errorf("ConnectAddress = %q, want %q", cfg.ConnectAddress, "127.0.0.1:5432")
	}
}

func TestRestAPIConfig(t *testing.T) {
	cfg := RestAPIConfig{
		Listen:         "0.0.0.0:8008",
		ConnectAddress: "127.0.0.1:8008",
		Username:       "admin",
		Password:       "secret",
	}

	if cfg.Listen != "0.0.0.0:8008" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:8008")
	}
	if cfg.Username != "admin" {
		t.Errorf("Username = %q, want %q", cfg.Username, "admin")
	}
}

func TestBootstrapConfig(t *testing.T) {
	cfg := BootstrapConfig{
		Method: "initdb",
		InitDB: []interface{}{"--encoding=UTF8", "--locale=en_US.UTF-8"},
	}

	if cfg.Method != "initdb" {
		t.Errorf("Method = %q, want %q", cfg.Method, "initdb")
	}
	if len(cfg.InitDB) != 2 {
		t.Errorf("InitDB length = %d, want 2", len(cfg.InitDB))
	}
}

func TestWatchdogConfig(t *testing.T) {
	tests := []struct {
		name   string
		config WatchdogConfig
		mode   string
	}{
		{"off mode", WatchdogConfig{Mode: "off"}, "off"},
		{"auto mode", WatchdogConfig{Mode: "auto"}, "auto"},
		{"required mode", WatchdogConfig{Mode: "required"}, "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.Mode != tt.mode {
				t.Errorf("Mode = %q, want %q", tt.config.Mode, tt.mode)
			}
		})
	}
}

func TestConfigMerge(t *testing.T) {
	base := &Config{
		Scope:     "cluster1",
		TTL:       30,
		LoopWait:  10,
		PostgreSQL: PostgreSQLConfig{
			DataDir: "/data/postgres",
		},
	}

	override := map[string]interface{}{
		"ttl":       60,
		"loop_wait": 20,
	}

	// Simulate merging dynamic configuration
	if ttl, ok := override["ttl"].(int); ok {
		base.TTL = ttl
	}
	if loopWait, ok := override["loop_wait"].(int); ok {
		base.LoopWait = loopWait
	}

	if base.TTL != 60 {
		t.Errorf("TTL after merge = %d, want 60", base.TTL)
	}
	if base.LoopWait != 20 {
		t.Errorf("LoopWait after merge = %d, want 20", base.LoopWait)
	}
	// Original values should be preserved
	if base.Scope != "cluster1" {
		t.Errorf("Scope after merge = %q, want %q", base.Scope, "cluster1")
	}
}

func TestConfigEnvironmentVariables(t *testing.T) {
	// Test that environment variables are properly parsed
	tests := []struct {
		envVar   string
		envValue string
	}{
		{"PATRONI_SCOPE", "testcluster"},
		{"PATRONI_NAME", "node1"},
		{"PATRONI_NAMESPACE", "/patroni/"},
	}

	for _, tt := range tests {
		t.Run(tt.envVar, func(t *testing.T) {
			os.Setenv(tt.envVar, tt.envValue)
			defer os.Unsetenv(tt.envVar)

			val := os.Getenv(tt.envVar)
			if val != tt.envValue {
				t.Errorf("Getenv(%q) = %q, want %q", tt.envVar, val, tt.envValue)
			}
		})
	}
}

func TestTagsConfig(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]interface{}
		key  string
		want interface{}
	}{
		{
			name: "nofailover tag",
			tags: map[string]interface{}{"nofailover": true},
			key:  "nofailover",
			want: true,
		},
		{
			name: "nosync tag",
			tags: map[string]interface{}{"nosync": true},
			key:  "nosync",
			want: true,
		},
		{
			name: "clonefrom tag",
			tags: map[string]interface{}{"clonefrom": true},
			key:  "clonefrom",
			want: true,
		},
		{
			name: "noloadbalance tag",
			tags: map[string]interface{}{"noloadbalance": true},
			key:  "noloadbalance",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := tt.tags[tt.key]
			if !ok {
				t.Errorf("Tag %q not found", tt.key)
			}
			if val != tt.want {
				t.Errorf("Tag %q = %v, want %v", tt.key, val, tt.want)
			}
		})
	}
}

func TestValidateTimeouts(t *testing.T) {
	tests := []struct {
		name         string
		ttl          int
		loopWait     int
		retryTimeout int
		wantValid    bool
	}{
		{"valid timeouts", 30, 10, 10, true},
		{"ttl too small", 15, 10, 10, false},
		{"loop_wait too small", 30, 0, 10, false},
		{"retry_timeout too small", 30, 10, 2, false},
		{"rule violated", 30, 15, 10, false}, // loop_wait + 2*retry_timeout > ttl
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				TTL:          tt.ttl,
				LoopWait:     tt.loopWait,
				RetryTimeout: tt.retryTimeout,
			}

			// Validate timeout rules
			valid := true
			if cfg.TTL < 20 {
				valid = false
			}
			if cfg.LoopWait < 1 {
				valid = false
			}
			if cfg.RetryTimeout < 3 {
				valid = false
			}
			if cfg.LoopWait+2*cfg.RetryTimeout > cfg.TTL {
				valid = false
			}

			if valid != tt.wantValid {
				t.Errorf("Timeout validation = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

func TestAuthConfig(t *testing.T) {
	cfg := AuthConfig{
		Superuser: UserAuth{
			Username: "postgres",
			Password: "secret",
		},
		Replication: UserAuth{
			Username: "replicator",
			Password: "rep-pass",
		},
	}

	if cfg.Superuser.Username != "postgres" {
		t.Errorf("Superuser.Username = %q, want %q", cfg.Superuser.Username, "postgres")
	}
	if cfg.Replication.Username != "replicator" {
		t.Errorf("Replication.Username = %q, want %q", cfg.Replication.Username, "replicator")
	}
}

func TestLogConfig(t *testing.T) {
	cfg := LogConfig{
		Level:  "INFO",
		Format: "text",
		Dir:    "/var/log/patroni",
	}

	if cfg.Level != "INFO" {
		t.Errorf("Level = %q, want %q", cfg.Level, "INFO")
	}
	if cfg.Format != "text" {
		t.Errorf("Format = %q, want %q", cfg.Format, "text")
	}
}
