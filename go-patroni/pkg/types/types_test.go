package types

import (
	"encoding/json"
	"testing"
)

func TestPostgresqlStateString(t *testing.T) {
	tests := []struct {
		state    PostgresqlState
		expected string
	}{
		{PostgresqlStateStopped, "stopped"},
		{PostgresqlStateStarting, "starting"},
		{PostgresqlStateRunning, "running"},
		{PostgresqlStateStopping, "stopping"},
		{PostgresqlStateCrashed, "crashed"},
		{PostgresqlStateStartFailed, "start failed"},
		{PostgresqlStateStopFailed, "stop failed"},
		{PostgresqlStateRestarting, "restarting"},
		{PostgresqlStateRestartFailed, "restart failed"},
		{PostgresqlStateCreatingReplica, "creating replica"},
		{PostgresqlStateBootstrapStarting, "bootstrap starting"},
		{PostgresqlState(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("PostgresqlState.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPostgresqlRoleString(t *testing.T) {
	tests := []struct {
		role     PostgresqlRole
		expected string
	}{
		{PostgresqlRoleUninitialized, "uninitialized"},
		{PostgresqlRolePrimary, "primary"},
		{PostgresqlRoleReplica, "replica"},
		{PostgresqlRoleDemoted, "demoted"},
		{PostgresqlRolePromoted, "promoted"},
		{PostgresqlRoleStandbyLeader, "standby_leader"},
		{PostgresqlRole(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.role.String(); got != tt.expected {
				t.Errorf("PostgresqlRole.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseMemberFromJSON(t *testing.T) {
	tests := []struct {
		name       string
		version    int64
		memberName string
		session    string
		value      string
		checkName  string
		checkData  bool
	}{
		{
			name:       "valid member",
			version:    1,
			memberName: "node1",
			session:    "session1",
			value:      `{"conn_url":"postgres://localhost:5432","api_url":"http://localhost:8008","state":"running","role":"primary"}`,
			checkName:  "node1",
			checkData:  true,
		},
		{
			name:       "member with tags",
			version:    2,
			memberName: "node2",
			session:    "session2",
			value:      `{"conn_url":"postgres://localhost:5433","state":"running","tags":{"nofailover":true,"nosync":true}}`,
			checkName:  "node2",
			checkData:  true,
		},
		{
			name:       "invalid json returns member with empty data",
			version:    1,
			memberName: "node3",
			session:    "session3",
			value:      `{invalid`,
			checkName:  "node3",
			checkData:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			member, err := ParseMemberFromJSON(tt.version, tt.memberName, tt.session, tt.value)
			if err != nil {
				t.Errorf("ParseMemberFromJSON() unexpected error = %v", err)
				return
			}
			if member.Name != tt.checkName {
				t.Errorf("ParseMemberFromJSON() name = %v, want %v", member.Name, tt.checkName)
			}
			if tt.checkData && member.Data.ConnURL == "" {
				t.Errorf("ParseMemberFromJSON() expected data to be populated")
			}
		})
	}
}

func TestParseLSN(t *testing.T) {
	tests := []struct {
		name     string
		lsn      string
		expected LSN
		wantErr  bool
	}{
		{"valid lsn", "0/16B3748", LSN(0x16B3748), false},  // 23803720
		{"valid lsn 2", "1/0", LSN(4294967296), false},     // 0x100000000
		{"valid lsn 3", "0/0", LSN(0), false},
		{"valid lsn 4", "0/1000", LSN(0x1000), false},      // 4096
		{"invalid format", "invalid", LSN(0), true},
		{"missing slash", "12345", LSN(0), true},
		{"empty string", "", LSN(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLSN(tt.lsn)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLSN() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ParseLSN() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLSNString(t *testing.T) {
	tests := []struct {
		lsn      int64
		expected string
	}{
		{0, "0/0"},
		{0x16B3748, "0/16B3748"},   // 23803720
		{4294967296, "1/0"},        // 0x100000000
		{0x100001000, "1/1000"},    // high=1, low=0x1000
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			l := LSN(tt.lsn)
			if got := l.String(); got != tt.expected {
				t.Errorf("LSN.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSlotNameFromMemberName(t *testing.T) {
	tests := []struct {
		memberName string
		expected   string
	}{
		{"node1", "node1"},
		{"node-1", "node_1"},
		{"Node.1", "node_1"},
		{"NODE-test.server", "node_test_server"},
		{"123-start", "123_start"},
	}

	for _, tt := range tests {
		t.Run(tt.memberName, func(t *testing.T) {
			if got := SlotNameFromMemberName(tt.memberName); got != tt.expected {
				t.Errorf("SlotNameFromMemberName() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMemberDataJSON(t *testing.T) {
	data := MemberData{
		ConnURL:      "postgres://localhost:5432",
		APIURL:       "http://localhost:8008",
		State:        "running",
		Role:         "primary",
		XlogLocation: 12345,
		Timeline:     1,
		Tags: Tags{
			NoFailover:    true,
			NoLoadbalance: false,
		},
	}

	// Test marshaling
	jsonData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Failed to marshal MemberData: %v", err)
	}

	// Test unmarshaling
	var decoded MemberData
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal MemberData: %v", err)
	}

	if decoded.ConnURL != data.ConnURL {
		t.Errorf("ConnURL mismatch: got %v, want %v", decoded.ConnURL, data.ConnURL)
	}
	if decoded.Tags.NoFailover != data.Tags.NoFailover {
		t.Errorf("Tags.NoFailover mismatch: got %v, want %v", decoded.Tags.NoFailover, data.Tags.NoFailover)
	}
}

func TestClusterHasLeader(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *Cluster
		expected bool
	}{
		{
			name:     "nil leader",
			cluster:  &Cluster{Leader: nil},
			expected: false,
		},
		{
			name: "empty leader name",
			cluster: &Cluster{
				Leader: &Leader{MemberName: ""},
			},
			expected: false,
		},
		{
			name: "valid leader",
			cluster: &Cluster{
				Leader: &Leader{MemberName: "node1"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cluster.HasLeader(); got != tt.expected {
				t.Errorf("Cluster.HasLeader() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestClusterGetMember(t *testing.T) {
	cluster := &Cluster{
		Members: []*Member{
			{Name: "node1"},
			{Name: "node2"},
			{Name: "node3"},
		},
	}

	tests := []struct {
		name       string
		memberName string
		found      bool
	}{
		{"existing member", "node1", true},
		{"another member", "node2", true},
		{"non-existing", "node4", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			member := cluster.GetMember(tt.memberName)
			if (member != nil) != tt.found {
				t.Errorf("Cluster.GetMember() found = %v, want %v", member != nil, tt.found)
			}
		})
	}
}

func TestSyncStateEmpty(t *testing.T) {
	state := SyncState{}

	if state.Leader != "" {
		t.Errorf("Empty SyncState should have empty Leader")
	}
	if len(state.SyncStandby) != 0 {
		t.Errorf("Empty SyncState should have empty SyncStandby")
	}
}

func TestFailoverJSON(t *testing.T) {
	failover := &Failover{
		Version:   1,
		Leader:    "node1",
		Candidate: "node2",
	}

	jsonData, err := json.Marshal(failover)
	if err != nil {
		t.Fatalf("Failed to marshal Failover: %v", err)
	}

	var decoded Failover
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Failover: %v", err)
	}

	if decoded.Leader != failover.Leader {
		t.Errorf("Leader mismatch: got %v, want %v", decoded.Leader, failover.Leader)
	}
	if decoded.Candidate != failover.Candidate {
		t.Errorf("Candidate mismatch: got %v, want %v", decoded.Candidate, failover.Candidate)
	}
}

func TestTagsDefaults(t *testing.T) {
	tags := Tags{}

	// All boolean fields should be false by default
	if tags.NoFailover {
		t.Error("NoFailover should default to false")
	}
	if tags.NoLoadbalance {
		t.Error("NoLoadbalance should default to false")
	}
	if tags.CloneFrom {
		t.Error("CloneFrom should default to false")
	}
	if tags.NoSync {
		t.Error("NoSync should default to false")
	}
	if tags.NoStream {
		t.Error("NoStream should default to false")
	}

	// Int fields should be 0 by default
	if tags.FailoverPriority != 0 {
		t.Error("FailoverPriority should default to 0")
	}
	if tags.SyncPriority != 0 {
		t.Error("SyncPriority should default to 0")
	}
}
