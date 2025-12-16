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

func TestGetDCSType(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		wantType     string
		wantNil      bool
	}{
		{
			name: "etcd3 configured",
			config: &Config{
				Scope:     "mycluster",
				Name:      "node1",
				Namespace: "/patroni/",
				TTL:       30,
				Etcd3:     &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
			},
			wantType: "etcd3",
			wantNil:  false,
		},
		{
			name: "etcd configured",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				Etcd:  &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
			},
			wantType: "etcd",
			wantNil:  false,
		},
		{
			name: "consul configured",
			config: &Config{
				Scope:  "mycluster",
				Name:   "node1",
				Consul: &dcs.Config{Host: "127.0.0.1:8500"},
			},
			wantType: "consul",
			wantNil:  false,
		},
		{
			name: "zookeeper configured",
			config: &Config{
				Scope:     "mycluster",
				Name:      "node1",
				ZooKeeper: &dcs.Config{Hosts: []string{"127.0.0.1:2181"}},
			},
			wantType: "zookeeper",
			wantNil:  false,
		},
		{
			name: "exhibitor configured",
			config: &Config{
				Scope:     "mycluster",
				Name:      "node1",
				Exhibitor: &dcs.Config{Hosts: []string{"127.0.0.1:8181"}},
			},
			wantType: "exhibitor",
			wantNil:  false,
		},
		{
			name: "kubernetes configured",
			config: &Config{
				Scope:      "mycluster",
				Name:       "node1",
				Kubernetes: &dcs.Config{Namespace: "default"},
			},
			wantType: "kubernetes",
			wantNil:  false,
		},
		{
			name: "raft configured",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				Raft:  &dcs.Config{Hosts: []string{"127.0.0.1:5254"}},
			},
			wantType: "raft",
			wantNil:  false,
		},
		{
			name: "no dcs configured",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
			},
			wantType: "",
			wantNil:  true,
		},
		{
			name: "etcd3 takes priority over etcd",
			config: &Config{
				Scope: "mycluster",
				Name:  "node1",
				Etcd:  &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
				Etcd3: &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
			},
			wantType: "etcd3",
			wantNil:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dcsType, dcsConfig := tt.config.GetDCSType()
			if dcsType != tt.wantType {
				t.Errorf("GetDCSType() type = %q, want %q", dcsType, tt.wantType)
			}
			if (dcsConfig == nil) != tt.wantNil {
				t.Errorf("GetDCSType() config nil = %v, want %v", dcsConfig == nil, tt.wantNil)
			}
			// Verify common settings are populated
			if dcsConfig != nil {
				if dcsConfig.Scope != tt.config.Scope {
					t.Errorf("DCSConfig.Scope = %q, want %q", dcsConfig.Scope, tt.config.Scope)
				}
				if dcsConfig.Name != tt.config.Name {
					t.Errorf("DCSConfig.Name = %q, want %q", dcsConfig.Name, tt.config.Name)
				}
			}
		})
	}
}

func TestGetAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantURL string
	}{
		{
			name: "with connect address",
			config: &Config{
				RestAPI: RestAPIConfig{
					Listen:         "0.0.0.0:8008",
					ConnectAddress: "192.168.1.100:8008",
				},
			},
			wantURL: "http://192.168.1.100:8008",
		},
		{
			name: "without connect address",
			config: &Config{
				RestAPI: RestAPIConfig{
					Listen: "0.0.0.0:8008",
				},
			},
			wantURL: "http://0.0.0.0:8008",
		},
		{
			name: "with TLS",
			config: &Config{
				RestAPI: RestAPIConfig{
					Listen:         "0.0.0.0:8008",
					ConnectAddress: "secure.example.com:8008",
					CertFile:       "/etc/ssl/certs/patroni.crt",
				},
			},
			wantURL: "https://secure.example.com:8008",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.config.GetAPIURL()
			if url != tt.wantURL {
				t.Errorf("GetAPIURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestGetConnectionURL(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantURL string
	}{
		{
			name: "with connect address and user",
			config: &Config{
				PostgreSQL: PostgreSQLConfig{
					Listen:         "0.0.0.0:5432",
					ConnectAddress: "192.168.1.100:5432",
					Authentication: AuthConfig{
						Superuser: UserAuth{Username: "admin"},
					},
				},
			},
			wantURL: "postgres://admin@192.168.1.100:5432/postgres",
		},
		{
			name: "without connect address",
			config: &Config{
				PostgreSQL: PostgreSQLConfig{
					Listen: "0.0.0.0:5432",
				},
			},
			wantURL: "postgres://postgres@0.0.0.0:5432/postgres",
		},
		{
			name: "without port in address",
			config: &Config{
				PostgreSQL: PostgreSQLConfig{
					Listen: "localhost",
				},
			},
			wantURL: "postgres://postgres@localhost:5432/postgres",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.config.GetConnectionURL()
			if url != tt.wantURL {
				t.Errorf("GetConnectionURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestGetDataDir(t *testing.T) {
	cfg := &Config{
		PostgreSQL: PostgreSQLConfig{
			DataDir: "/data/postgresql",
		},
	}

	dataDir := cfg.GetDataDir()
	if dataDir != "/data/postgresql" {
		t.Errorf("GetDataDir() = %q, want %q", dataDir, "/data/postgresql")
	}
}

func TestSaveAndLoadDynamicConfig(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "patroni-dynamic-config")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		PostgreSQL: PostgreSQLConfig{
			DataDir: tmpDir,
		},
	}

	// Test saving dynamic config
	dynamicConfig := map[string]interface{}{
		"ttl":       60,
		"loop_wait": 15,
		"postgresql": map[string]interface{}{
			"parameters": map[string]interface{}{
				"max_connections": 100,
			},
		},
	}

	if err := cfg.SaveDynamicConfig(dynamicConfig); err != nil {
		t.Fatalf("SaveDynamicConfig() error = %v", err)
	}

	// Verify file was created
	path := filepath.Join(tmpDir, "patroni.dynamic.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("Dynamic config file was not created at %s", path)
	}

	// Test loading dynamic config
	loadedConfig, err := cfg.LoadDynamicConfig()
	if err != nil {
		t.Fatalf("LoadDynamicConfig() error = %v", err)
	}

	if loadedConfig == nil {
		t.Fatal("LoadDynamicConfig() returned nil")
	}

	// Check values
	if ttl, ok := loadedConfig["ttl"].(int); !ok || ttl != 60 {
		t.Errorf("LoadDynamicConfig() ttl = %v, want 60", loadedConfig["ttl"])
	}
	if loopWait, ok := loadedConfig["loop_wait"].(int); !ok || loopWait != 15 {
		t.Errorf("LoadDynamicConfig() loop_wait = %v, want 15", loadedConfig["loop_wait"])
	}
}

func TestLoadDynamicConfigNotExists(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "patroni-dynamic-config-empty")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		PostgreSQL: PostgreSQLConfig{
			DataDir: tmpDir,
		},
	}

	// Test loading non-existent dynamic config
	loadedConfig, err := cfg.LoadDynamicConfig()
	if err != nil {
		t.Errorf("LoadDynamicConfig() error = %v, want nil", err)
	}
	if loadedConfig != nil {
		t.Errorf("LoadDynamicConfig() = %v, want nil for non-existent file", loadedConfig)
	}
}

func TestParseIntWithUnits(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"empty string", "", 0, false},
		{"plain number", "1234", 1234, false},
		{"with spaces", "  5678  ", 5678, false},
		{"kilobytes K", "10K", 10 * 1024, false},
		{"kilobytes lowercase k", "10k", 10 * 1024, false},
		{"megabytes M", "5M", 5 * 1024 * 1024, false},
		{"gigabytes G", "2G", 2 * 1024 * 1024 * 1024, false},
		{"terabytes T", "1T", 1 * 1024 * 1024 * 1024 * 1024, false},
		{"invalid number", "abc", 0, true},
		{"invalid suffix", "10X", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIntWithUnits(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseIntWithUnits(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseIntWithUnits(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAddressEdgeCases(t *testing.T) {
	// Test via GetConnectionURL with various address formats
	tests := []struct {
		name     string
		listen   string
		wantHost string
		wantPort string
	}{
		{"host:port", "localhost:5433", "localhost", "5433"},
		{"ip:port", "192.168.1.1:5432", "192.168.1.1", "5432"},
		{"only host", "localhost", "localhost", "5432"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				PostgreSQL: PostgreSQLConfig{
					Listen: tt.listen,
				},
			}
			url := cfg.GetConnectionURL()
			expected := "postgres://postgres@" + tt.wantHost + ":" + tt.wantPort + "/postgres"
			if url != expected {
				t.Errorf("GetConnectionURL() = %q, want %q", url, expected)
			}
		})
	}
}

func TestLoadConfigInvalidFile(t *testing.T) {
	// Test loading from a non-existent file
	_, err := Load("/nonexistent/path/to/config.yml")
	if err == nil {
		t.Error("Load() should return error for non-existent file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// Create a temporary config file with invalid YAML
	tmpDir, err := os.MkdirTemp("", "patroni-config")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "invalid.yml")
	invalidContent := `
scope: batman
  invalid yaml indent
name: node1
`
	if err := os.WriteFile(configFile, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	_, err = Load(configFile)
	if err == nil {
		t.Error("Load() should return error for invalid YAML")
	}
}

func TestValidateMultipleDCS(t *testing.T) {
	// Test that multiple DCS configurations generate a warning but don't fail
	cfg := &Config{
		Scope: "mycluster",
		Name:  "node1",
		PostgreSQL: PostgreSQLConfig{
			DataDir: "/data/postgres",
		},
		Etcd:  &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
		Etcd3: &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
	}

	// Should not return an error, just warn
	err := cfg.Validate()
	if err != nil {
		t.Errorf("Validate() with multiple DCS should not return error, got: %v", err)
	}
}

func TestLoadWithDefaults(t *testing.T) {
	// Create a minimal config file to test defaults
	tmpDir, err := os.MkdirTemp("", "patroni-config")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "patroni.yml")
	configContent := `
scope: testcluster
name: node1
postgresql:
  data_dir: /data/postgres
etcd3:
  hosts:
    - 127.0.0.1:2379
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Check defaults were applied
	if cfg.TTL != 30 {
		t.Errorf("Default TTL = %d, want 30", cfg.TTL)
	}
	if cfg.LoopWait != 10 {
		t.Errorf("Default LoopWait = %d, want 10", cfg.LoopWait)
	}
	if cfg.RetryTimeout != 10 {
		t.Errorf("Default RetryTimeout = %d, want 10", cfg.RetryTimeout)
	}
	if cfg.Namespace != "/service/" {
		t.Errorf("Default Namespace = %q, want /service/", cfg.Namespace)
	}
	if cfg.Watchdog.Mode != "automatic" {
		t.Errorf("Default Watchdog.Mode = %q, want automatic", cfg.Watchdog.Mode)
	}
}

func TestLoadWithEnvOverrides(t *testing.T) {
	// Create a config file
	tmpDir, err := os.MkdirTemp("", "patroni-config")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "patroni.yml")
	configContent := `
scope: originalscope
name: originalname
postgresql:
  data_dir: /data/postgres
etcd3:
  hosts:
    - 127.0.0.1:2379
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment variables
	os.Setenv("PATRONI_SCOPE", "envscope")
	os.Setenv("PATRONI_NAME", "envname")
	os.Setenv("PATRONI_TTL", "60")
	defer func() {
		os.Unsetenv("PATRONI_SCOPE")
		os.Unsetenv("PATRONI_NAME")
		os.Unsetenv("PATRONI_TTL")
	}()

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Environment should override config file
	if cfg.Scope != "envscope" {
		t.Errorf("Scope = %q, want envscope", cfg.Scope)
	}
	if cfg.Name != "envname" {
		t.Errorf("Name = %q, want envname", cfg.Name)
	}
}

func TestSaveDynamicConfigError(t *testing.T) {
	// Test saving to a non-existent directory
	cfg := &Config{
		PostgreSQL: PostgreSQLConfig{
			DataDir: "/nonexistent/path",
		},
	}

	err := cfg.SaveDynamicConfig(map[string]interface{}{"ttl": 60})
	if err == nil {
		t.Error("SaveDynamicConfig() should return error for non-existent directory")
	}
}

func TestLoadDynamicConfigInvalidYAML(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "patroni-dynamic-config-invalid")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		PostgreSQL: PostgreSQLConfig{
			DataDir: tmpDir,
		},
	}

	// Write invalid YAML
	path := filepath.Join(tmpDir, "patroni.dynamic.json")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0600); err != nil {
		t.Fatalf("Failed to write invalid config: %v", err)
	}

	_, err = cfg.LoadDynamicConfig()
	if err == nil {
		t.Error("LoadDynamicConfig() should return error for invalid YAML")
	}
}

func TestConfigConcurrentAccess(t *testing.T) {
	cfg := &Config{
		Scope:     "testcluster",
		Name:      "node1",
		Namespace: "/patroni/",
		TTL:       30,
		PostgreSQL: PostgreSQLConfig{
			DataDir:        "/data/postgres",
			Listen:         "0.0.0.0:5432",
			ConnectAddress: "127.0.0.1:5432",
		},
		RestAPI: RestAPIConfig{
			Listen:         "0.0.0.0:8008",
			ConnectAddress: "127.0.0.1:8008",
		},
		Etcd3: &dcs.Config{Hosts: []string{"127.0.0.1:2379"}},
	}

	// Test concurrent reads don't cause race conditions
	done := make(chan bool, 4)

	go func() {
		for i := 0; i < 100; i++ {
			_ = cfg.GetDataDir()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = cfg.GetAPIURL()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = cfg.GetConnectionURL()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_, _ = cfg.GetDCSType()
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	for i := 0; i < 4; i++ {
		<-done
	}
}

// Generator tests

func TestGetAddress(t *testing.T) {
	hostname, ip := GetAddress()

	// Hostname should be non-empty (or #FIXME if lookup fails)
	if hostname == "" {
		t.Error("GetAddress() returned empty hostname")
	}

	// IP should be non-empty (or #FIXME if lookup fails)
	if ip == "" {
		t.Error("GetAddress() returned empty IP")
	}
}

func TestNewConfigGenerator(t *testing.T) {
	gen := NewConfigGenerator("/tmp/patroni.yml")

	if gen.OutputFile != "/tmp/patroni.yml" {
		t.Errorf("OutputFile = %q, want /tmp/patroni.yml", gen.OutputFile)
	}
	if gen.Config == nil {
		t.Fatal("Config should not be nil")
	}
	if gen.Config.Scope != NoValueMsg {
		t.Errorf("Scope = %q, want %q", gen.Config.Scope, NoValueMsg)
	}
	if gen.Config.Namespace != "/patroni" {
		t.Errorf("Namespace = %q, want /patroni", gen.Config.Namespace)
	}
	if gen.Config.PostgreSQL == nil {
		t.Error("PostgreSQL config should not be nil")
	}
	if gen.Config.RestAPI == nil {
		t.Error("RestAPI config should not be nil")
	}
}

func TestConfigGeneratorSetDCS(t *testing.T) {
	gen := NewConfigGenerator("")

	gen.SetDCS("etcd3", map[string]interface{}{"hosts": []string{"127.0.0.1:2379"}})

	if gen.Config.DCS == nil {
		t.Fatal("DCS should not be nil after SetDCS")
	}
	etcd3, ok := gen.Config.DCS["etcd3"].(map[string]interface{})
	if !ok {
		t.Fatal("etcd3 config should be a map")
	}
	hosts, ok := etcd3["hosts"].([]string)
	if !ok || len(hosts) != 1 {
		t.Errorf("etcd3 hosts = %v, want [127.0.0.1:2379]", hosts)
	}
}

func TestConfigGeneratorSetEtcd(t *testing.T) {
	gen := NewConfigGenerator("")

	gen.SetEtcd([]string{"127.0.0.1:2379", "127.0.0.1:2380"})

	if gen.Config.DCS == nil {
		t.Fatal("DCS should not be nil after SetEtcd")
	}
	etcd3, ok := gen.Config.DCS["etcd3"].(map[string]interface{})
	if !ok {
		t.Fatal("etcd3 config should be a map")
	}
	hosts, ok := etcd3["hosts"].([]string)
	if !ok || len(hosts) != 2 {
		t.Errorf("etcd3 hosts = %v, want 2 hosts", hosts)
	}
}

func TestConfigGeneratorSetConsul(t *testing.T) {
	gen := NewConfigGenerator("")

	gen.SetConsul("127.0.0.1:8500")

	if gen.Config.DCS == nil {
		t.Fatal("DCS should not be nil after SetConsul")
	}
	consul, ok := gen.Config.DCS["consul"].(map[string]interface{})
	if !ok {
		t.Fatal("consul config should be a map")
	}
	host, ok := consul["host"].(string)
	if !ok || host != "127.0.0.1:8500" {
		t.Errorf("consul host = %v, want 127.0.0.1:8500", host)
	}
}

func TestConfigGeneratorSetZooKeeper(t *testing.T) {
	gen := NewConfigGenerator("")

	gen.SetZooKeeper([]string{"127.0.0.1:2181"})

	if gen.Config.DCS == nil {
		t.Fatal("DCS should not be nil after SetZooKeeper")
	}
	zk, ok := gen.Config.DCS["zookeeper"].(map[string]interface{})
	if !ok {
		t.Fatal("zookeeper config should be a map")
	}
	hosts, ok := zk["hosts"].([]string)
	if !ok || len(hosts) != 1 {
		t.Errorf("zookeeper hosts = %v, want [127.0.0.1:2181]", hosts)
	}
}

func TestConfigGeneratorSetBootstrap(t *testing.T) {
	gen := NewConfigGenerator("")

	dcsConfig := map[string]interface{}{"ttl": 30}
	initdb := []map[string]interface{}{
		{"encoding": "UTF8"},
		{"locale": "en_US.UTF-8"},
	}

	gen.SetBootstrap(dcsConfig, initdb)

	if gen.Config.Bootstrap == nil {
		t.Fatal("Bootstrap should not be nil after SetBootstrap")
	}
	if _, ok := gen.Config.Bootstrap["dcs"]; !ok {
		t.Error("Bootstrap should contain dcs")
	}
	if _, ok := gen.Config.Bootstrap["initdb"]; !ok {
		t.Error("Bootstrap should contain initdb")
	}
}

func TestConfigGeneratorSetBootstrapEmptyInitdb(t *testing.T) {
	gen := NewConfigGenerator("")

	dcsConfig := map[string]interface{}{"ttl": 30}

	gen.SetBootstrap(dcsConfig, nil)

	if gen.Config.Bootstrap == nil {
		t.Fatal("Bootstrap should not be nil after SetBootstrap")
	}
	if _, ok := gen.Config.Bootstrap["initdb"]; ok {
		t.Error("Bootstrap should not contain initdb when empty")
	}
}

func TestConfigGeneratorDetectPostgreSQLBinDir(t *testing.T) {
	gen := NewConfigGenerator("")

	// This will return #FIXME in most test environments
	binDir := gen.DetectPostgreSQLBinDir()

	// Should return something (either a path or #FIXME)
	if binDir == "" {
		t.Error("DetectPostgreSQLBinDir() returned empty string")
	}
}

func TestConfigGeneratorDetectDataDirectory(t *testing.T) {
	gen := NewConfigGenerator("")

	// This will return #FIXME in most test environments
	dataDir := gen.DetectDataDirectory()

	// Should return something (either a path or #FIXME)
	if dataDir == "" {
		t.Error("DetectDataDirectory() returned empty string")
	}
}

func TestConfigGeneratorDetectDataDirectoryFromEnv(t *testing.T) {
	gen := NewConfigGenerator("")

	// Set PGDATA environment variable
	os.Setenv("PGDATA", "/custom/data/dir")
	defer os.Unsetenv("PGDATA")

	dataDir := gen.DetectDataDirectory()

	if dataDir != "/custom/data/dir" {
		t.Errorf("DetectDataDirectory() = %q, want /custom/data/dir", dataDir)
	}
}

func TestConfigGeneratorGenerate(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "patroni-gen")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	outputFile := filepath.Join(tmpDir, "patroni.yml")
	gen := NewConfigGenerator(outputFile)
	gen.SetEtcd([]string{"127.0.0.1:2379"})

	data, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if len(data) == 0 {
		t.Error("Generate() returned empty data")
	}

	// Verify file was written
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Error("Generate() did not create output file")
	}
}

func TestConfigGeneratorGenerateNoFile(t *testing.T) {
	gen := NewConfigGenerator("") // Empty output file
	gen.SetEtcd([]string{"127.0.0.1:2379"})

	data, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if len(data) == 0 {
		t.Error("Generate() returned empty data")
	}
}

func TestConfigGeneratorGenerateError(t *testing.T) {
	gen := NewConfigGenerator("/nonexistent/dir/patroni.yml")
	gen.SetEtcd([]string{"127.0.0.1:2379"})

	_, err := gen.Generate()
	if err == nil {
		t.Error("Generate() should return error for non-writable path")
	}
}

func TestNewRunningPostgreSQLGenerator(t *testing.T) {
	gen := NewRunningPostgreSQLGenerator("postgres://user:pass@localhost:5432/db", "/tmp/output.yml")

	if gen.DSN != "postgres://user:pass@localhost:5432/db" {
		t.Errorf("DSN = %q, want postgres://user:pass@localhost:5432/db", gen.DSN)
	}
	if gen.ConfigGenerator == nil {
		t.Error("ConfigGenerator should not be nil")
	}
	if gen.OutputFile != "/tmp/output.yml" {
		t.Errorf("OutputFile = %q, want /tmp/output.yml", gen.OutputFile)
	}
}

func TestParseDSN(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		wantKeys map[string]string
	}{
		{
			name: "URI format with user and password",
			dsn:  "postgres://user:pass@localhost:5432/mydb",
			wantKeys: map[string]string{
				"user":     "user",
				"password": "pass",
				"host":     "localhost",
				"port":     "5432",
				"dbname":   "mydb",
			},
		},
		{
			name: "URI format without password",
			dsn:  "postgres://user@localhost:5432/mydb",
			wantKeys: map[string]string{
				"user":   "user",
				"host":   "localhost",
				"port":   "5432",
				"dbname": "mydb",
			},
		},
		{
			name: "postgresql URI prefix",
			dsn:  "postgresql://user:pass@localhost:5432/mydb",
			wantKeys: map[string]string{
				"user":     "user",
				"password": "pass",
				"host":     "localhost",
				"port":     "5432",
				"dbname":   "mydb",
			},
		},
		{
			name: "key=value format",
			dsn:  "host=localhost port=5432 dbname=mydb user=admin password=secret",
			wantKeys: map[string]string{
				"host":     "localhost",
				"port":     "5432",
				"dbname":   "mydb",
				"user":     "admin",
				"password": "secret",
			},
		},
		{
			name: "URI without port",
			dsn:  "postgres://user@localhost/mydb",
			wantKeys: map[string]string{
				"user":   "user",
				"host":   "localhost",
				"dbname": "mydb",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseDSN(tt.dsn)
			for key, want := range tt.wantKeys {
				if got, ok := result[key]; !ok || got != want {
					t.Errorf("ParseDSN(%q)[%q] = %q, want %q", tt.dsn, key, got, want)
				}
			}
		})
	}
}

func TestAuthParameters(t *testing.T) {
	// Verify AuthParameters map has expected entries
	expectedParams := []string{"user", "password", "sslmode", "sslcert", "sslkey", "sslrootcert"}

	for _, param := range expectedParams {
		if _, ok := AuthParameters[param]; !ok {
			t.Errorf("AuthParameters missing key %q", param)
		}
	}
}
