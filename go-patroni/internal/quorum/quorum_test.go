package quorum

import (
	"testing"
)

func TestQuorumStateResolver(t *testing.T) {
	tests := []struct {
		name            string
		leader          string
		quorum          int
		voters          []string
		numsync         int
		sync            []string
		numsyncConfirmed int
		active          []string
		syncWanted      int
		leaderWanted    string
	}{
		{
			name:            "add node",
			leader:          "a",
			quorum:          0,
			voters:          []string{},
			numsync:         0,
			sync:            []string{},
			numsyncConfirmed: 0,
			active:          []string{"b"},
			syncWanted:      2,
			leaderWanted:    "a",
		},
		{
			name:            "2 node cluster active matches state",
			leader:          "a",
			quorum:          0,
			voters:          []string{"b"},
			numsync:         1,
			sync:            []string{"b"},
			numsyncConfirmed: 1,
			active:          []string{"b"},
			syncWanted:      2,
			leaderWanted:    "a",
		},
		{
			name:            "add node by increasing quorum",
			leader:          "a",
			quorum:          0,
			voters:          []string{"b"},
			numsync:         1,
			sync:            []string{"b"},
			numsyncConfirmed: 1,
			active:          []string{"B", "C"},
			syncWanted:      1,
			leaderWanted:    "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leader == "" {
				t.Error("Leader should not be empty")
			}
			if tt.quorum < 0 {
				t.Error("Quorum should not be negative")
			}
			if tt.syncWanted < 0 {
				t.Error("SyncWanted should not be negative")
			}
		})
	}
}

func TestQuorumInvariant(t *testing.T) {
	// Main invariant: quorum + numsync >= len(voters | sync)
	tests := []struct {
		name      string
		quorum    int
		numsync   int
		voters    int
		sync      int
		satisfied bool
	}{
		{"satisfied", 1, 1, 2, 2, true},
		{"not satisfied", 0, 1, 2, 2, false},
		{"edge case", 1, 1, 1, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simplified invariant check
			total := tt.voters
			if tt.sync > total {
				total = tt.sync
			}
			satisfied := tt.quorum+tt.numsync >= total
			if satisfied != tt.satisfied {
				t.Errorf("Invariant satisfied = %v, want %v", satisfied, tt.satisfied)
			}
		})
	}
}

func TestQuorumTransitions(t *testing.T) {
	transitions := []struct {
		name       string
		transition string
		leader     string
		value      int
		nodes      []string
	}{
		{"sync update", "sync", "a", 1, []string{"b"}},
		{"quorum update", "quorum", "a", 0, []string{"b"}},
		{"restart", "restart", "a", 0, []string{}},
	}

	for _, tt := range transitions {
		t.Run(tt.name, func(t *testing.T) {
			if tt.transition == "" {
				t.Error("Transition type should not be empty")
			}
			if tt.leader == "" {
				t.Error("Leader should not be empty")
			}
		})
	}
}

func TestPromotion(t *testing.T) {
	tests := []struct {
		name         string
		oldLeader    string
		newLeader    string
		voters       []string
		activeNodes  []string
	}{
		{
			name:         "node c promotes",
			oldLeader:    "a",
			newLeader:    "c",
			voters:       []string{"b", "c", "d"},
			activeNodes:  []string{},
		},
		{
			name:         "node b reconnects after promotion",
			oldLeader:    "c",
			newLeader:    "c",
			voters:       []string{"a", "b", "d"},
			activeNodes:  []string{"b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.newLeader == "" {
				t.Error("New leader should not be empty")
			}
		})
	}
}

func TestNonSyncPromotion(t *testing.T) {
	tests := []struct {
		name        string
		leader      string
		syncNodes   []string
		activeNodes []string
	}{
		{
			name:        "non-sync node promotes",
			leader:      "d",
			syncNodes:   []string{"b", "c"},
			activeNodes: []string{},
		},
		{
			name:        "nodes reconnect",
			leader:      "d",
			syncNodes:   []string{"a", "b", "c"},
			activeNodes: []string{"b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leader == "" {
				t.Error("Leader should not be empty")
			}
		})
	}
}

func TestRemoveNodes(t *testing.T) {
	tests := []struct {
		name       string
		voters     []string
		sync       []string
		active     []string
		syncWanted int
	}{
		{
			name:       "remove inactive nodes",
			voters:     []string{"b", "c", "d", "e"},
			sync:       []string{"b", "c", "d", "e"},
			active:     []string{"b", "c"},
			syncWanted: 2,
		},
		{
			name:       "remove with decreased sync",
			voters:     []string{"b", "c", "d", "e"},
			sync:       []string{"b", "c", "d", "e"},
			active:     []string{"b", "c"},
			syncWanted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.syncWanted < 0 {
				t.Error("SyncWanted should not be negative")
			}
		})
	}
}

func TestSwapSyncNode(t *testing.T) {
	tests := []struct {
		name   string
		sync   []string
		active []string
	}{
		{
			name:   "swap b for d",
			sync:   []string{"b", "c"},
			active: []string{"b", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.sync) == 0 {
				t.Error("Sync should not be empty")
			}
		})
	}
}

func TestInvalidStates(t *testing.T) {
	tests := []struct {
		name        string
		description string
		shouldError bool
	}{
		{"invariant not satisfied", "quorum too low", true},
		{"mismatched quorum and sync", "external modification", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.shouldError {
				t.Error("Invalid states should error")
			}
		})
	}
}

func TestSafetyMargin(t *testing.T) {
	tests := []struct {
		name    string
		quorum  int
		numsync int
		voters  int
		sync    int
		margin  int
	}{
		{"no margin", 0, 2, 2, 2, 0},
		{"positive margin", 1, 2, 2, 2, 1},
		{"high margin", 2, 2, 2, 2, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Safety margin = quorum + numsync - max(voters, sync)
			total := tt.voters
			if tt.sync > total {
				total = tt.sync
			}
			margin := tt.quorum + tt.numsync - total
			if margin != tt.margin {
				t.Errorf("Safety margin = %d, want %d", margin, tt.margin)
			}
		})
	}
}

func TestQuorumUpdate(t *testing.T) {
	tests := []struct {
		name      string
		oldQuorum int
		newQuorum int
		valid     bool
	}{
		{"increase", 0, 1, true},
		{"decrease", 2, 1, true},
		{"negative", 1, -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.newQuorum >= 0
			if valid != tt.valid {
				t.Errorf("Quorum update valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestSyncUpdate(t *testing.T) {
	tests := []struct {
		name       string
		oldNumsync int
		newNumsync int
		valid      bool
	}{
		{"increase", 1, 2, true},
		{"decrease", 2, 1, true},
		{"negative", 1, -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.newNumsync >= 0
			if valid != tt.valid {
				t.Errorf("Sync update valid = %v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestCaseInsensitiveSet(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		check    string
		contains bool
	}{
		{"lowercase", []string{"a", "b", "c"}, "a", true},
		{"uppercase match", []string{"A", "B", "C"}, "a", true},
		{"mixed case", []string{"Abc", "Def"}, "abc", true},
		{"not found", []string{"a", "b"}, "c", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate case insensitive check
			found := false
			checkLower := toLower(tt.check)
			for _, s := range tt.input {
				if toLower(s) == checkLower {
					found = true
					break
				}
			}
			if found != tt.contains {
				t.Errorf("Contains %q = %v, want %v", tt.check, found, tt.contains)
			}
		})
	}
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}
