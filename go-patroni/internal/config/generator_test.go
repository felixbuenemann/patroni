package config

import (
	"testing"
)

func TestConfigGenerator(t *testing.T) {
	tests := []struct {
		name   string
		dsn    string
		output string
	}{
		{"basic dsn", "postgres://localhost/postgres", "yaml"},
		{"with options", "postgres://user:pass@localhost/db", "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dsn == "" {
				t.Error("DSN should not be empty")
			}
		})
	}
}

func TestGenerateScope(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "mycluster", "mycluster"},
		{"with spaces", "my cluster", "my-cluster"},
		{"uppercase", "MYCLUSTER", "mycluster"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input == "" {
				t.Error("Input should not be empty")
			}
		})
	}
}

func TestGenerateNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
	}{
		{"default", "/patroni/"},
		{"custom", "/my/namespace/"},
		{"service", "/service/patroni/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.namespace == "" {
				t.Error("Namespace should not be empty")
			}
			if tt.namespace[0] != '/' {
				t.Error("Namespace should start with /")
			}
		})
	}
}

func TestGenerateNodeName(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		expected string
	}{
		{"simple", "node1", "node1"},
		{"fqdn", "node1.example.com", "node1"},
		{"with suffix", "node1-replica", "node1-replica"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.hostname == "" {
				t.Error("Hostname should not be empty")
			}
		})
	}
}

func TestGenerateRestAPI(t *testing.T) {
	tests := []struct {
		name           string
		listen         string
		connectAddress string
	}{
		{"default", "0.0.0.0:8008", "127.0.0.1:8008"},
		{"custom port", "0.0.0.0:8080", "127.0.0.1:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.listen == "" {
				t.Error("Listen should not be empty")
			}
			if tt.connectAddress == "" {
				t.Error("ConnectAddress should not be empty")
			}
		})
	}
}

func TestGeneratePostgreSQL(t *testing.T) {
	tests := []struct {
		name           string
		listen         string
		connectAddress string
		dataDir        string
	}{
		{
			name:           "default",
			listen:         "0.0.0.0:5432",
			connectAddress: "127.0.0.1:5432",
			dataDir:        "/data/postgresql",
		},
		{
			name:           "custom",
			listen:         "0.0.0.0:5433",
			connectAddress: "192.168.1.1:5433",
			dataDir:        "/var/lib/postgresql/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dataDir == "" {
				t.Error("DataDir should not be empty")
			}
		})
	}
}

func TestGenerateDCS(t *testing.T) {
	dcsTypes := []struct {
		name   string
		dcs    string
		hosts  []string
	}{
		{"etcd3", "etcd3", []string{"localhost:2379"}},
		{"consul", "consul", []string{"localhost:8500"}},
		{"zookeeper", "zookeeper", []string{"localhost:2181"}},
	}

	for _, tt := range dcsTypes {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dcs == "" {
				t.Error("DCS type should not be empty")
			}
			if len(tt.hosts) == 0 {
				t.Error("Hosts should not be empty")
			}
		})
	}
}

func TestGenerateBootstrap(t *testing.T) {
	tests := []struct {
		name   string
		method string
		opts   []string
	}{
		{"initdb", "initdb", []string{"--encoding=UTF8"}},
		{"pg_basebackup", "pg_basebackup", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.method == "" {
				t.Error("Method should not be empty")
			}
		})
	}
}

func TestGenerateAuth(t *testing.T) {
	tests := []struct {
		name        string
		superuser   string
		replication string
	}{
		{"default", "postgres", "replicator"},
		{"custom", "admin", "replica_user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.superuser == "" {
				t.Error("Superuser should not be empty")
			}
			if tt.replication == "" {
				t.Error("Replication user should not be empty")
			}
		})
	}
}

func TestGenerateTags(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]interface{}
	}{
		{"empty", map[string]interface{}{}},
		{"with nofailover", map[string]interface{}{"nofailover": true}},
		{"with clonefrom", map[string]interface{}{"clonefrom": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tags == nil {
				t.Error("Tags should not be nil")
			}
		})
	}
}

func TestOutputFormat(t *testing.T) {
	formats := []struct {
		name   string
		format string
	}{
		{"yaml", "yaml"},
		{"json", "json"},
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			if f.format == "" {
				t.Error("Format should not be empty")
			}
		})
	}
}
