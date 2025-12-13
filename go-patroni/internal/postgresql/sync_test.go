package postgresql

import (
	"testing"

	"github.com/patroni/patroni-go/pkg/types"
)

func TestDefaultSyncConfig(t *testing.T) {
	config := DefaultSyncConfig()

	if config.Mode != SyncModeOff {
		t.Errorf("Mode = %v, want SyncModeOff", config.Mode)
	}
	if config.StrictMode {
		t.Error("StrictMode should default to false")
	}
	if config.SyncNodeCount != 1 {
		t.Errorf("SyncNodeCount = %d, want 1", config.SyncNodeCount)
	}
	if config.MaxLagOnSyncNode != -1 {
		t.Errorf("MaxLagOnSyncNode = %d, want -1", config.MaxLagOnSyncNode)
	}
}

func TestGenerateSynchronousStandbyNames(t *testing.T) {
	tests := []struct {
		name      string
		standbys  []string
		mode      SyncMode
		nodeCount int
		expected  string
	}{
		{
			name:      "empty standbys",
			standbys:  []string{},
			mode:      SyncModeOn,
			nodeCount: 1,
			expected:  "",
		},
		{
			name:      "single standby",
			standbys:  []string{"node1"},
			mode:      SyncModeOn,
			nodeCount: 1,
			expected:  `"node1"`,
		},
		{
			name:      "multiple standbys with FIRST 1",
			standbys:  []string{"node1", "node2"},
			mode:      SyncModeOn,
			nodeCount: 1,
			expected:  `FIRST 1 ("node1", "node2")`,
		},
		{
			name:      "multiple standbys with FIRST 2",
			standbys:  []string{"node1", "node2", "node3"},
			mode:      SyncModeQuorum,
			nodeCount: 2,
			expected:  `FIRST 2 ("node1", "node2", "node3")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &SyncConfig{
				Mode:          tt.mode,
				SyncNodeCount: tt.nodeCount,
			}
			sr := &SyncReplication{config: config}
			got := sr.GenerateSynchronousStandbyNames(tt.standbys)
			if got != tt.expected {
				t.Errorf("GenerateSynchronousStandbyNames() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseSynchronousStandbyNames(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		expectedNames []string
		expectedCount int
		wantErr       bool
	}{
		{
			name:          "empty string",
			value:         "",
			expectedNames: nil,
			expectedCount: 0,
			wantErr:       false,
		},
		{
			name:          "single name",
			value:         `"node1"`,
			expectedNames: []string{"node1"},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name:          "single name unquoted",
			value:         "node1",
			expectedNames: []string{"node1"},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name:          "FIRST syntax",
			value:         `FIRST 2 ("node1", "node2", "node3")`,
			expectedNames: []string{"node1", "node2", "node3"},
			expectedCount: 2,
			wantErr:       false,
		},
		{
			name:          "ANY syntax",
			value:         `ANY 1 ("node1", "node2")`,
			expectedNames: []string{"node1", "node2"},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name:          "simple list",
			value:         `"node1", "node2"`,
			expectedNames: []string{"node1", "node2"},
			expectedCount: 1,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names, count, err := ParseSynchronousStandbyNames(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSynchronousStandbyNames() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(names) != len(tt.expectedNames) {
					t.Errorf("ParseSynchronousStandbyNames() names length = %d, want %d", len(names), len(tt.expectedNames))
					return
				}
				for i := range names {
					if names[i] != tt.expectedNames[i] {
						t.Errorf("ParseSynchronousStandbyNames() names[%d] = %q, want %q", i, names[i], tt.expectedNames[i])
					}
				}
				if count != tt.expectedCount {
					t.Errorf("ParseSynchronousStandbyNames() count = %d, want %d", count, tt.expectedCount)
				}
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", `"simple"`},
		{"with-dash", `"with-dash"`},
		{"with.dot", `"with.dot"`},
		{`with"quote`, `"with""quote"`},
		{"node1", `"node1"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := quoteIdentifier(tt.input); got != tt.expected {
				t.Errorf("quoteIdentifier(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSplitQuotedList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{`"a", "b", "c"`, []string{`"a"`, ` "b"`, ` "c"`}},
		{`a, b, c`, []string{`a`, ` b`, ` c`}},
		{`"a,b", c`, []string{`"a,b"`, ` c`}},
		// Doubled quotes inside a quoted string are collapsed to single quote
		{`"a""b", c`, []string{`"a"b"`, ` c`}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitQuotedList(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitQuotedList() length = %d, want %d", len(got), len(tt.expected))
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("splitQuotedList()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestPickSynchronousStandbys(t *testing.T) {
	tests := []struct {
		name     string
		config   *SyncConfig
		cluster  *types.Cluster
		expected []string
	}{
		{
			name:   "sync mode off",
			config: &SyncConfig{Mode: SyncModeOff},
			cluster: &types.Cluster{
				Members: []*types.Member{
					{Name: "node1", Data: types.MemberData{XlogLocation: 100}},
				},
			},
			expected: nil,
		},
		{
			name:   "no eligible members",
			config: &SyncConfig{Mode: SyncModeOn, SyncNodeCount: 1},
			cluster: &types.Cluster{
				Leader:  &types.Leader{MemberName: "leader"},
				Members: []*types.Member{},
			},
			expected: nil,
		},
		{
			name:   "pick single standby",
			config: &SyncConfig{Mode: SyncModeOn, SyncNodeCount: 1, MaxLagOnSyncNode: -1},
			cluster: &types.Cluster{
				Leader: &types.Leader{MemberName: "leader"},
				Members: []*types.Member{
					{Name: "leader", Data: types.MemberData{XlogLocation: 100}},
					{Name: "node1", Data: types.MemberData{XlogLocation: 90}},
					{Name: "node2", Data: types.MemberData{XlogLocation: 80}},
				},
			},
			expected: []string{"node1"},
		},
		{
			name:   "skip nosync members",
			config: &SyncConfig{Mode: SyncModeOn, SyncNodeCount: 1, MaxLagOnSyncNode: -1},
			cluster: &types.Cluster{
				Leader: &types.Leader{MemberName: "leader"},
				Members: []*types.Member{
					{Name: "leader", Data: types.MemberData{XlogLocation: 100}},
					{Name: "node1", Data: types.MemberData{XlogLocation: 90, Tags: types.Tags{NoSync: true}}},
					{Name: "node2", Data: types.MemberData{XlogLocation: 80}},
				},
			},
			expected: []string{"node2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr := &SyncReplication{config: tt.config}
			got := sr.PickSynchronousStandbys(tt.cluster)

			if len(got) != len(tt.expected) {
				t.Errorf("PickSynchronousStandbys() length = %d, want %d", len(got), len(tt.expected))
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("PickSynchronousStandbys()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestCalculateLag(t *testing.T) {
	sr := &SyncReplication{config: DefaultSyncConfig()}

	tests := []struct {
		name       string
		leaderLSN  int64
		memberLSN  int64
		expected   int64
	}{
		{"no lag", 100, 100, 0},
		{"some lag", 100, 90, 10},
		{"large lag", 1000, 500, 500},
		{"member ahead", 100, 110, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leader := &types.Leader{
				Member: &types.Member{
					Data: types.MemberData{XlogLocation: tt.leaderLSN},
				},
			}
			member := &types.Member{
				Data: types.MemberData{XlogLocation: tt.memberLSN},
			}
			got := sr.calculateLag(leader, member)
			if got != tt.expected {
				t.Errorf("calculateLag() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestBuildSyncState(t *testing.T) {
	sr := &SyncReplication{config: DefaultSyncConfig()}

	state := sr.BuildSyncState("leader1", []string{"node1", "node2"})

	if state.Leader != "leader1" {
		t.Errorf("Leader = %q, want %q", state.Leader, "leader1")
	}
	if len(state.SyncStandby) != 2 {
		t.Errorf("SyncStandby length = %d, want 2", len(state.SyncStandby))
	}
	if state.SyncStandby[0] != "node1" || state.SyncStandby[1] != "node2" {
		t.Errorf("SyncStandby = %v, want [node1, node2]", state.SyncStandby)
	}
}
