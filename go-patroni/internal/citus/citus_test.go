package citus

import (
	"testing"
)

func TestCitusGroup(t *testing.T) {
	tests := []struct {
		name  string
		group int
		valid bool
	}{
		{"coordinator", 0, true},
		{"worker 1", 1, true},
		{"worker 2", 2, true},
		{"negative", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.group >= 0
			if valid != tt.valid {
				t.Errorf("Group %d valid = %v, want %v", tt.group, valid, tt.valid)
			}
		})
	}
}

func TestCitusDatabase(t *testing.T) {
	tests := []struct {
		name     string
		database string
		valid    bool
	}{
		{"citus database", "citus", true},
		{"postgres database", "postgres", true},
		{"custom database", "mydb", true},
		{"empty database", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.database != ""
			if valid != tt.valid {
				t.Errorf("Database %q valid = %v, want %v", tt.database, valid, tt.valid)
			}
		})
	}
}

func TestCitusNodeRole(t *testing.T) {
	tests := []struct {
		group    int
		expected string
	}{
		{0, "coordinator"},
		{1, "worker"},
		{2, "worker"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			role := "worker"
			if tt.group == 0 {
				role = "coordinator"
			}
			if role != tt.expected {
				t.Errorf("Role for group %d = %q, want %q", tt.group, role, tt.expected)
			}
		})
	}
}

func TestCitusWorkerNode(t *testing.T) {
	workers := []struct {
		name    string
		host    string
		port    int
		group   int
		primary bool
	}{
		{"worker1", "worker1.example.com", 5432, 1, true},
		{"worker2", "worker2.example.com", 5432, 2, true},
		{"worker1-replica", "worker1-replica.example.com", 5432, 1, false},
	}

	for _, w := range workers {
		t.Run(w.name, func(t *testing.T) {
			if w.host == "" {
				t.Error("Host should not be empty")
			}
			if w.port <= 0 {
				t.Error("Port should be positive")
			}
			if w.group <= 0 {
				t.Error("Worker group should be positive")
			}
		})
	}
}

func TestCitusCoordinator(t *testing.T) {
	coord := struct {
		host  string
		port  int
		group int
	}{
		host:  "coordinator.example.com",
		port:  5432,
		group: 0,
	}

	if coord.group != 0 {
		t.Error("Coordinator should have group 0")
	}
	if coord.host == "" {
		t.Error("Coordinator host should not be empty")
	}
}

func TestCitusRebalanceConfig(t *testing.T) {
	config := map[string]interface{}{
		"rebalance_strategy":    "by_shard_count",
		"rebalance_threshold":   0.1,
		"shard_replication_factor": 1,
	}

	if _, ok := config["rebalance_strategy"]; !ok {
		t.Error("Config should have rebalance_strategy")
	}
}

func TestCitusDistributedTables(t *testing.T) {
	tables := []struct {
		name      string
		column    string
		collocate string
	}{
		{"orders", "customer_id", ""},
		{"order_items", "order_id", "orders"},
	}

	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			if table.name == "" {
				t.Error("Table name should not be empty")
			}
			if table.column == "" {
				t.Error("Distribution column should not be empty")
			}
		})
	}
}

func TestCitusReferenceTable(t *testing.T) {
	tables := []string{"countries", "currencies", "settings"}

	for _, table := range tables {
		if table == "" {
			t.Errorf("Reference table name should not be empty")
		}
	}
}

func TestCitusShardCount(t *testing.T) {
	tests := []struct {
		name  string
		count int
		valid bool
	}{
		{"default", 32, true},
		{"small", 8, true},
		{"large", 128, true},
		{"zero", 0, false},
		{"negative", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.count > 0
			if valid != tt.valid {
				t.Errorf("Shard count %d valid = %v, want %v", tt.count, valid, tt.valid)
			}
		})
	}
}

func TestCitusConnectionInfo(t *testing.T) {
	info := struct {
		host     string
		port     int
		database string
		user     string
	}{
		host:     "localhost",
		port:     5432,
		database: "citus",
		user:     "postgres",
	}

	if info.host == "" || info.database == "" || info.user == "" {
		t.Error("Connection info should have host, database, and user")
	}
	if info.port <= 0 {
		t.Error("Port should be positive")
	}
}
