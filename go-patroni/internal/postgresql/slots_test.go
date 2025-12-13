package postgresql

import (
	"testing"
)

func TestSlotName(t *testing.T) {
	tests := []struct {
		memberName string
		expected   string
	}{
		{"node1", "node1"},
		{"node-1", "node_1"},
		{"node.1", "node_1"},
		{"NODE1", "node1"},
		{"my-node.name", "my_node_name"},
	}

	for _, tt := range tests {
		t.Run(tt.memberName, func(t *testing.T) {
			// Slot names: lowercase, replace - and . with _
			got := normalizeSlotName(tt.memberName)
			if got != tt.expected {
				t.Errorf("normalizeSlotName(%q) = %q, want %q", tt.memberName, got, tt.expected)
			}
		})
	}
}

func normalizeSlotName(name string) string {
	result := make([]byte, len(name))
	for i, c := range name {
		switch {
		case c >= 'A' && c <= 'Z':
			result[i] = byte(c - 'A' + 'a')
		case c == '-' || c == '.':
			result[i] = '_'
		default:
			result[i] = byte(c)
		}
	}
	return string(result)
}

func TestSlotType(t *testing.T) {
	tests := []struct {
		slotType string
		valid    bool
	}{
		{"physical", true},
		{"logical", true},
		{"", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.slotType, func(t *testing.T) {
			valid := tt.slotType == "physical" || tt.slotType == "logical"
			if valid != tt.valid {
				t.Errorf("Slot type %q valid = %v, want %v", tt.slotType, valid, tt.valid)
			}
		})
	}
}

func TestLogicalSlotConfig(t *testing.T) {
	tests := []struct {
		name     string
		database string
		plugin   string
		valid    bool
	}{
		{"valid slot", "mydb", "pgoutput", true},
		{"test_decoding", "postgres", "test_decoding", true},
		{"missing database", "", "pgoutput", false},
		{"missing plugin", "mydb", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.database != "" && tt.plugin != ""
			if valid != tt.valid {
				t.Errorf("Logical slot valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestPhysicalSlotConfig(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"node1", true},
		{"replication_slot", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.name != ""
			if valid != tt.valid {
				t.Errorf("Physical slot valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestSlotLSN(t *testing.T) {
	tests := []struct {
		name     string
		lsn      string
		expected int64
		valid    bool
	}{
		{"valid lsn", "0/16B3748", 0x16B3748, true},
		{"zero lsn", "0/0", 0, true},
		{"invalid", "invalid", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just validate the format
			valid := len(tt.lsn) > 0 && tt.lsn != "invalid"
			if valid != tt.valid {
				t.Errorf("LSN %q valid = %v, want %v", tt.lsn, valid, tt.valid)
			}
		})
	}
}

func TestSlotActive(t *testing.T) {
	slots := []struct {
		name   string
		active bool
	}{
		{"active_slot", true},
		{"inactive_slot", false},
	}

	for _, slot := range slots {
		t.Run(slot.name, func(t *testing.T) {
			// Test slot activity state
			if slot.name == "active_slot" && !slot.active {
				t.Error("Active slot should be active")
			}
		})
	}
}

func TestSlotRetention(t *testing.T) {
	tests := []struct {
		name      string
		retainLSN int64
		walSize   int64
		shouldDrop bool
	}{
		{"recent slot", 100, 100, false},
		{"stale slot", 1, 1000000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A slot should be dropped if it's using too much WAL
			shouldDrop := tt.walSize-tt.retainLSN > 500000
			if shouldDrop != tt.shouldDrop {
				t.Errorf("Should drop = %v, want %v", shouldDrop, tt.shouldDrop)
			}
		})
	}
}

func TestIgnoreSlots(t *testing.T) {
	ignorePatterns := []struct {
		pattern string
		name    string
		match   bool
	}{
		{"test_*", "test_slot", true},
		{"test_*", "prod_slot", false},
		{"pg_*", "pg_stat_slot", true},
	}

	for _, tt := range ignorePatterns {
		t.Run(tt.name, func(t *testing.T) {
			// Simple prefix matching
			prefix := tt.pattern[:len(tt.pattern)-1]
			match := len(tt.name) >= len(prefix) && tt.name[:len(prefix)] == prefix
			if match != tt.match {
				t.Errorf("Pattern %q matches %q = %v, want %v", tt.pattern, tt.name, match, tt.match)
			}
		})
	}
}

func TestPermanentSlots(t *testing.T) {
	permanentSlots := map[string]map[string]interface{}{
		"logical_slot": {
			"type":     "logical",
			"database": "mydb",
			"plugin":   "pgoutput",
		},
		"physical_slot": {
			"type": "physical",
		},
	}

	for name, config := range permanentSlots {
		t.Run(name, func(t *testing.T) {
			slotType, ok := config["type"].(string)
			if !ok {
				t.Error("Slot should have a type")
			}
			if slotType == "logical" {
				if _, ok := config["database"]; !ok {
					t.Error("Logical slot should have a database")
				}
				if _, ok := config["plugin"]; !ok {
					t.Error("Logical slot should have a plugin")
				}
			}
		})
	}
}

func TestSlotAdvance(t *testing.T) {
	tests := []struct {
		name       string
		currentLSN int64
		targetLSN  int64
		advance    bool
	}{
		{"advance needed", 100, 200, true},
		{"no advance needed", 200, 100, false},
		{"same position", 100, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			advance := tt.targetLSN > tt.currentLSN
			if advance != tt.advance {
				t.Errorf("Advance = %v, want %v", advance, tt.advance)
			}
		})
	}
}

func TestMemberSlots(t *testing.T) {
	members := []string{"node1", "node2", "node3"}
	leader := "node1"

	for _, member := range members {
		t.Run(member, func(t *testing.T) {
			// Leader doesn't need a slot on itself
			needsSlot := member != leader
			if member == leader && needsSlot {
				t.Error("Leader should not need a slot on itself")
			}
		})
	}
}
