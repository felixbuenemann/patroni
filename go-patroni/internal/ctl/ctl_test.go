package ctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

// MockDCS implements dcs.DCS interface for testing.
type MockDCS struct {
	cluster   *types.Cluster
	getErr    error
	touchErr  error
	lockErr   error
	locked    bool
	configErr error
}

func (m *MockDCS) GetCluster(ctx context.Context) (*types.Cluster, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.cluster, nil
}

func (m *MockDCS) TouchMember(ctx context.Context, data *types.MemberData) error {
	return m.touchErr
}

func (m *MockDCS) TakeLock(ctx context.Context) (bool, error) {
	return m.locked, m.lockErr
}

func (m *MockDCS) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	return nil
}

func (m *MockDCS) DeleteLeader(ctx context.Context) error {
	return nil
}

func (m *MockDCS) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	return m.locked, m.lockErr
}

func (m *MockDCS) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	return nil
}

func (m *MockDCS) DeleteFailover(ctx context.Context) error {
	return nil
}

func (m *MockDCS) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	return m.configErr
}

func (m *MockDCS) SetSyncState(ctx context.Context, state *types.SyncState) error {
	return nil
}

func (m *MockDCS) DeleteMember(ctx context.Context) error {
	return nil
}

func (m *MockDCS) DeleteSyncState(ctx context.Context) error {
	return nil
}

func (m *MockDCS) Initialize(ctx context.Context, sysID string) (bool, error) {
	return true, nil
}

func (m *MockDCS) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	return nil
}

func (m *MockDCS) DeleteCluster(ctx context.Context) error {
	return nil
}

func (m *MockDCS) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster)
	close(ch)
	return ch, nil
}

func (m *MockDCS) Name() string {
	return "mock"
}

func (m *MockDCS) Close() error {
	return nil
}

// Verify MockDCS implements dcs.DCS
var _ dcs.DCS = (*MockDCS)(nil)

func TestOutputFormatConstants(t *testing.T) {
	formats := map[OutputFormat]string{
		FormatTable: "table",
		FormatJSON:  "json",
		FormatYAML:  "yaml",
		FormatTSV:   "tsv",
	}

	for format, expected := range formats {
		if string(format) != expected {
			t.Errorf("Format %v = %q, want %q", format, string(format), expected)
		}
	}
}

func TestMemberStateConstants(t *testing.T) {
	states := map[MemberState]string{
		StateRunning:  "running",
		StateStarting: "starting",
		StateStopped:  "stopped",
		StateUnknown:  "unknown",
	}

	for state, expected := range states {
		if string(state) != expected {
			t.Errorf("State %v = %q, want %q", state, string(state), expected)
		}
	}
}

func TestFlushTargetConstants(t *testing.T) {
	targets := map[FlushTarget]string{
		FlushRestart:    "restart",
		FlushSwitchover: "switchover",
	}

	for target, expected := range targets {
		if string(target) != expected {
			t.Errorf("Target %v = %q, want %q", target, string(target), expected)
		}
	}
}

func TestNewClient(t *testing.T) {
	cfg := &Config{
		Scope:   "mycluster",
		Timeout: 5 * time.Second,
	}

	client := NewClient(cfg)
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
	if client.scope != "mycluster" {
		t.Errorf("scope = %q, want %q", client.scope, "mycluster")
	}
	if client.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want %v", client.timeout, 5*time.Second)
	}
	if client.httpClient == nil {
		t.Error("httpClient should be initialized")
	}
}

func TestNewClientDefaultTimeout(t *testing.T) {
	cfg := &Config{
		Scope: "mycluster",
	}

	client := NewClient(cfg)
	if client.timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want %v", client.timeout, 10*time.Second)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   interface{}
		wantErr bool
	}{
		{"valid ttl", "ttl", 30, false},
		{"invalid ttl", "ttl", 5, true},
		{"boundary ttl", "ttl", 10, false},
		{"valid loop_wait", "loop_wait", 10, false},
		{"invalid loop_wait", "loop_wait", 0, true},
		{"boundary loop_wait", "loop_wait", 1, false},
		{"valid retry_timeout", "retry_timeout", 10, false},
		{"invalid retry_timeout", "retry_timeout", 0, true},
		{"boundary retry_timeout", "retry_timeout", 1, false},
		{"unknown key", "unknown", "value", false},
		{"non-int ttl", "ttl", "30", false}, // not int, so no validation
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig(%q, %v) error = %v, wantErr %v",
					tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestParseDCSURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantType    string
		wantHostLen int
		wantErr     bool
	}{
		{"etcd url", "etcd://localhost:2379", "etcd", 1, false},
		{"etcd3 url", "etcd3://localhost:2379", "etcd3", 1, false},
		{"consul url", "consul://localhost:8500", "consul", 1, false},
		{"zookeeper url", "zookeeper://localhost:2181", "zookeeper", 1, false},
		{"kubernetes url", "kubernetes://localhost", "kubernetes", 1, false},
		{"invalid url", "://invalid", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dcsType, hosts, err := ParseDCSURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDCSURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if err == nil {
				if dcsType != tt.wantType {
					t.Errorf("dcsType = %q, want %q", dcsType, tt.wantType)
				}
				if len(hosts) != tt.wantHostLen {
					t.Errorf("hosts len = %d, want %d", len(hosts), tt.wantHostLen)
				}
			}
		})
	}
}

func TestClusterMemberJSON(t *testing.T) {
	member := &ClusterMember{
		Name:     "node1",
		Host:     "192.168.1.1",
		Port:     5432,
		Role:     "Leader",
		State:    StateRunning,
		Timeline: 1,
		Lag:      0,
		APIURL:   "http://192.168.1.1:8008",
	}

	data, err := json.Marshal(member)
	if err != nil {
		t.Fatalf("Failed to marshal ClusterMember: %v", err)
	}

	var decoded ClusterMember
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ClusterMember: %v", err)
	}

	if decoded.Name != "node1" {
		t.Errorf("Name = %q, want %q", decoded.Name, "node1")
	}
	if decoded.Role != "Leader" {
		t.Errorf("Role = %q, want %q", decoded.Role, "Leader")
	}
	if decoded.State != StateRunning {
		t.Errorf("State = %q, want %q", decoded.State, StateRunning)
	}
}

func TestClusterInfoJSON(t *testing.T) {
	info := &ClusterInfo{
		Name:   "mycluster",
		Scope:  "mycluster",
		Leader: "node1",
		Paused: false,
		Members: []*ClusterMember{
			{Name: "node1", Role: "Leader", State: StateRunning},
			{Name: "node2", Role: "Replica", State: StateRunning, Lag: 1024},
		},
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Failed to marshal ClusterInfo: %v", err)
	}

	var decoded ClusterInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ClusterInfo: %v", err)
	}

	if decoded.Leader != "node1" {
		t.Errorf("Leader = %q, want %q", decoded.Leader, "node1")
	}
	if len(decoded.Members) != 2 {
		t.Errorf("Members len = %d, want 2", len(decoded.Members))
	}
}

func TestSwitchoverRequestJSON(t *testing.T) {
	now := time.Now()
	req := &SwitchoverRequest{
		Leader:      "node1",
		Candidate:   "node2",
		ScheduledAt: &now,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal SwitchoverRequest: %v", err)
	}

	var decoded SwitchoverRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal SwitchoverRequest: %v", err)
	}

	if decoded.Leader != "node1" {
		t.Errorf("Leader = %q, want %q", decoded.Leader, "node1")
	}
	if decoded.Candidate != "node2" {
		t.Errorf("Candidate = %q, want %q", decoded.Candidate, "node2")
	}
}

func TestFailoverRequestJSON(t *testing.T) {
	req := &FailoverRequest{
		Candidate: "node2",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal FailoverRequest: %v", err)
	}

	var decoded FailoverRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal FailoverRequest: %v", err)
	}

	if decoded.Candidate != "node2" {
		t.Errorf("Candidate = %q, want %q", decoded.Candidate, "node2")
	}
}

func TestReinitRequestJSON(t *testing.T) {
	req := &ReinitRequest{
		Force: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal ReinitRequest: %v", err)
	}

	var decoded ReinitRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ReinitRequest: %v", err)
	}

	if !decoded.Force {
		t.Error("Force should be true")
	}
}

func TestConfigStruct(t *testing.T) {
	cfg := Config{
		Scope:   "testcluster",
		Timeout: 30 * time.Second,
	}

	if cfg.Scope != "testcluster" {
		t.Errorf("Scope = %q, want %q", cfg.Scope, "testcluster")
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, 30*time.Second)
	}
}

func TestClientSwitchoverNoLeader(t *testing.T) {
	client := NewClient(&Config{Scope: "test"})

	req := &SwitchoverRequest{
		Candidate: "node2",
	}

	err := client.Switchover(context.Background(), req)
	if err == nil {
		t.Error("Switchover without leader should return error")
	}
	if err.Error() != "leader is required for switchover" {
		t.Errorf("error = %q, want %q", err.Error(), "leader is required for switchover")
	}
}

func TestClientFailoverNoCandidate(t *testing.T) {
	client := NewClient(&Config{Scope: "test"})

	req := &FailoverRequest{}

	err := client.Failover(context.Background(), req)
	if err == nil {
		t.Error("Failover without candidate should return error")
	}
	if err.Error() != "candidate is required for failover" {
		t.Errorf("error = %q, want %q", err.Error(), "candidate is required for failover")
	}
}

// Test HTTP request helpers with mock server
func TestPostRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want %q", r.Header.Get("Content-Type"), "application/json")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewClient(&Config{Scope: "test"})
	resp, err := client.postRequest(context.Background(), server.URL, []byte(`{"test":true}`))
	if err != nil {
		t.Fatalf("postRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestPutRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPut)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(&Config{Scope: "test"})
	resp, err := client.putRequest(context.Background(), server.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("putRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestPatchRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPatch)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(&Config{Scope: "test"})
	resp, err := client.patchRequest(context.Background(), server.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("patchRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestClusterMemberWithTags(t *testing.T) {
	member := &ClusterMember{
		Name:  "node1",
		Role:  "Replica",
		State: StateRunning,
		Tags: types.Tags{
			NoFailover:       true,
			NoLoadbalance:    false,
			FailoverPriority: 2,
		},
	}

	data, err := json.Marshal(member)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded ClusterMember
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if !decoded.Tags.NoFailover {
		t.Error("Tags.NoFailover should be true")
	}
	if decoded.Tags.FailoverPriority != 2 {
		t.Errorf("Tags.FailoverPriority = %d, want 2", decoded.Tags.FailoverPriority)
	}
}

func TestMemberStateValues(t *testing.T) {
	tests := []struct {
		input MemberState
		valid bool
	}{
		{StateRunning, true},
		{StateStarting, true},
		{StateStopped, true},
		{StateUnknown, true},
		{MemberState("invalid"), false},
	}

	validStates := map[MemberState]bool{
		StateRunning:  true,
		StateStarting: true,
		StateStopped:  true,
		StateUnknown:  true,
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			_, valid := validStates[tt.input]
			if valid != tt.valid {
				t.Errorf("state %q valid = %v, want %v", tt.input, valid, tt.valid)
			}
		})
	}
}

func TestOutputFormatValues(t *testing.T) {
	tests := []struct {
		input OutputFormat
		valid bool
	}{
		{FormatTable, true},
		{FormatJSON, true},
		{FormatYAML, true},
		{FormatTSV, true},
		{OutputFormat("invalid"), false},
	}

	validFormats := map[OutputFormat]bool{
		FormatTable: true,
		FormatJSON:  true,
		FormatYAML:  true,
		FormatTSV:   true,
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			_, valid := validFormats[tt.input]
			if valid != tt.valid {
				t.Errorf("format %q valid = %v, want %v", tt.input, valid, tt.valid)
			}
		})
	}
}

func TestScheduledSwitchoverRequest(t *testing.T) {
	scheduledTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	req := &SwitchoverRequest{
		Leader:      "node1",
		Candidate:   "node2",
		ScheduledAt: &scheduledTime,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded SwitchoverRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.ScheduledAt == nil {
		t.Error("ScheduledAt should not be nil")
	} else if decoded.ScheduledAt.Year() != 2025 {
		t.Errorf("ScheduledAt year = %d, want 2025", decoded.ScheduledAt.Year())
	}
}

func TestValidateConfigErrorMessages(t *testing.T) {
	tests := []struct {
		key       string
		value     interface{}
		errSubstr string
	}{
		{"ttl", 5, "ttl must be at least 10"},
		{"loop_wait", 0, "loop_wait must be at least 1"},
		{"retry_timeout", 0, "retry_timeout must be at least 1"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := ValidateConfig(tt.key, tt.value)
			if err == nil {
				t.Error("expected error")
				return
			}
			if err.Error() != tt.errSubstr {
				t.Errorf("error = %q, want %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

func TestClientHTTPClientTimeout(t *testing.T) {
	timeout := 15 * time.Second
	cfg := &Config{
		Scope:   "test",
		Timeout: timeout,
	}

	client := NewClient(cfg)
	if client.httpClient.Timeout != timeout {
		t.Errorf("httpClient.Timeout = %v, want %v", client.httpClient.Timeout, timeout)
	}
}

func TestClusterInfoEmpty(t *testing.T) {
	info := &ClusterInfo{
		Scope:   "emptycluster",
		Members: []*ClusterMember{},
	}

	if len(info.Members) != 0 {
		t.Errorf("Members len = %d, want 0", len(info.Members))
	}
	if info.Leader != "" {
		t.Errorf("Leader = %q, want empty", info.Leader)
	}
}

func TestClusterInfoPaused(t *testing.T) {
	info := &ClusterInfo{
		Scope:  "pausedcluster",
		Paused: true,
	}

	if !info.Paused {
		t.Error("Paused should be true")
	}
}

// Test Client.List with mock DCS
func TestClientList(t *testing.T) {
	mockCluster := &types.Cluster{
		Leader: &types.Leader{
			MemberName: "node1",
		},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					Role:     "primary",
					State:    "running",
					Timeline: 1,
					ConnURL:  "postgres://192.168.1.1:5432/postgres",
					APIURL:   "http://192.168.1.1:8008",
				},
			},
			{
				Name: "node2",
				Data: types.MemberData{
					Role:     "replica",
					State:    "running",
					Timeline: 1,
					ConnURL:  "postgres://192.168.1.2:5432/postgres",
					APIURL:   "http://192.168.1.2:8008",
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "mycluster",
	})

	info, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if info.Leader != "node1" {
		t.Errorf("Leader = %q, want %q", info.Leader, "node1")
	}
	if len(info.Members) != 2 {
		t.Fatalf("Members len = %d, want 2", len(info.Members))
	}
	// Leader should be first due to sorting
	if info.Members[0].Name != "node1" {
		t.Errorf("First member = %q, want %q (leader)", info.Members[0].Name, "node1")
	}
	if info.Members[0].Role != "Leader" {
		t.Errorf("Leader role = %q, want %q", info.Members[0].Role, "Leader")
	}
	if info.Members[0].Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", info.Members[0].Host, "192.168.1.1")
	}
	if info.Members[0].Port != 5432 {
		t.Errorf("Port = %d, want %d", info.Members[0].Port, 5432)
	}
}

func TestClientListEmpty(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "empty",
	})

	info, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(info.Members) != 0 {
		t.Errorf("Members len = %d, want 0", len(info.Members))
	}
}

func TestClientListError(t *testing.T) {
	mockDCS := &MockDCS{
		getErr: dcs.ErrNotFound,
	}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	_, err := client.List(context.Background())
	if err == nil {
		t.Error("List() should return error")
	}
}

// Test Client.GetConfig with mock DCS
func TestClientGetConfig(t *testing.T) {
	mockCluster := &types.Cluster{
		Config: &types.ClusterConfig{
			Data: map[string]interface{}{
				"ttl":       30,
				"loop_wait": 10,
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	cfg, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	if cfg["ttl"] != 30 {
		t.Errorf("ttl = %v, want 30", cfg["ttl"])
	}
	if cfg["loop_wait"] != 10 {
		t.Errorf("loop_wait = %v, want 10", cfg["loop_wait"])
	}
}

func TestClientGetConfigEmpty(t *testing.T) {
	mockCluster := &types.Cluster{}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	cfg, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	if len(cfg) != 0 {
		t.Errorf("config len = %d, want 0", len(cfg))
	}
}

func TestClientGetConfigNilData(t *testing.T) {
	mockCluster := &types.Cluster{
		Config: &types.ClusterConfig{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	cfg, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	if len(cfg) != 0 {
		t.Errorf("config len = %d, want 0", len(cfg))
	}
}

// Test Client.History with mock DCS
func TestClientHistory(t *testing.T) {
	mockCluster := &types.Cluster{
		History: &types.TimelineHistory{
			Value: []types.TimelineEntry{
				{Timeline: 1, LSN: 0, Reason: "initial"},
				{Timeline: 2, LSN: 1000, Reason: "failover"},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	history, err := client.History(context.Background())
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}

	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[0].Timeline != 1 {
		t.Errorf("history[0].Timeline = %d, want 1", history[0].Timeline)
	}
	if history[1].Timeline != 2 {
		t.Errorf("history[1].Timeline = %d, want 2", history[1].Timeline)
	}
}

func TestClientHistoryEmpty(t *testing.T) {
	mockCluster := &types.Cluster{}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	history, err := client.History(context.Background())
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}

	if len(history) != 0 {
		t.Errorf("history len = %d, want 0", len(history))
	}
}

// Test Client.Switchover with mock server
func TestClientSwitchoverSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/switchover" {
			t.Errorf("Path = %q, want /switchover", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Switchover(context.Background(), &SwitchoverRequest{
		Leader:    "node1",
		Candidate: "node2",
	})
	if err != nil {
		t.Errorf("Switchover() error = %v", err)
	}
}

func TestClientSwitchoverLeaderNotFound(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node2",
				Data: types.MemberData{
					APIURL: "http://localhost:8008",
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Switchover(context.Background(), &SwitchoverRequest{
		Leader:    "node1",
		Candidate: "node2",
	})
	if err == nil {
		t.Error("Switchover() should return error when leader not found")
	}
}

// Test Client.Failover with mock server
func TestClientFailoverSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/failover" {
			t.Errorf("Path = %q, want /failover", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Failover(context.Background(), &FailoverRequest{
		Candidate: "node2",
	})
	if err != nil {
		t.Errorf("Failover() error = %v", err)
	}
}

func TestClientFailoverNoMembers(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Failover(context.Background(), &FailoverRequest{
		Candidate: "node2",
	})
	if err == nil {
		t.Error("Failover() should return error when no members found")
	}
}

// Test Client.Reinit with mock server
func TestClientReinitSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reinitialize" {
			t.Errorf("Path = %q, want /reinitialize", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Reinit(context.Background(), "node1", &ReinitRequest{Force: true})
	if err != nil {
		t.Errorf("Reinit() error = %v", err)
	}
}

func TestClientReinitMemberNotFound(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Reinit(context.Background(), "node1", &ReinitRequest{})
	if err == nil {
		t.Error("Reinit() should return error when member not found")
	}
}

// Test Client.Restart with mock server
func TestClientRestartSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/restart" {
			t.Errorf("Path = %q, want /restart", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Restart(context.Background(), "node1", nil)
	if err != nil {
		t.Errorf("Restart() error = %v", err)
	}
}

func TestClientRestartWithSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	scheduled := time.Now().Add(time.Hour)
	err := client.Restart(context.Background(), "node1", &scheduled)
	if err != nil {
		t.Errorf("Restart() with schedule error = %v", err)
	}
}

// Test Client.Reload with mock server
func TestClientReloadSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reload" {
			t.Errorf("Path = %q, want /reload", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Reload(context.Background(), "node1")
	if err != nil {
		t.Errorf("Reload() error = %v", err)
	}
}

// Test Client.Pause and Resume with mock server
func TestClientPauseSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Errorf("Path = %q, want /config", r.URL.Path)
		}
		if r.Method != http.MethodPatch {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPatch)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Pause(context.Background(), false)
	if err != nil {
		t.Errorf("Pause() error = %v", err)
	}
}

func TestClientResumeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Resume(context.Background(), false)
	if err != nil {
		t.Errorf("Resume() error = %v", err)
	}
}

// Test Client.SetConfig with mock server
func TestClientSetConfigSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Errorf("Path = %q, want /config", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPut)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.SetConfig(context.Background(), map[string]interface{}{"ttl": 30})
	if err != nil {
		t.Errorf("SetConfig() error = %v", err)
	}
}

func TestClientSetConfigNoLeader(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.SetConfig(context.Background(), map[string]interface{}{"ttl": 30})
	if err == nil {
		t.Error("SetConfig() should return error when no leader")
	}
}

// Test Client.PatchConfig with mock server
func TestClientPatchConfigSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPatch)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.PatchConfig(context.Background(), map[string]interface{}{"loop_wait": 15})
	if err != nil {
		t.Errorf("PatchConfig() error = %v", err)
	}
}

// Test Client.Remove with mock server
func TestClientRemoveSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/patroni" {
			t.Errorf("Path = %q, want /patroni", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodDelete)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Remove(context.Background(), "node1")
	if err != nil {
		t.Errorf("Remove() error = %v", err)
	}
}

// Test Client.Flush with mock server
func TestClientFlushRestartSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/restart" {
			t.Errorf("Path = %q, want /restart", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodDelete)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Flush(context.Background(), "node1", FlushRestart)
	if err != nil {
		t.Errorf("Flush() error = %v", err)
	}
}

func TestClientFlushSwitchoverSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/switchover" {
			t.Errorf("Path = %q, want /switchover", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Flush(context.Background(), "node1", FlushSwitchover)
	if err != nil {
		t.Errorf("Flush() error = %v", err)
	}
}

func TestClientFlushUnknownTarget(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: "http://localhost:8008",
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Flush(context.Background(), "node1", FlushTarget("unknown"))
	if err == nil {
		t.Error("Flush() should return error for unknown target")
	}
}

// Test error responses from API
func TestClientSwitchoverAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte("switchover in progress"))
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Switchover(context.Background(), &SwitchoverRequest{
		Leader:    "node1",
		Candidate: "node2",
	})
	if err == nil {
		t.Error("Switchover() should return error on API error")
	}
}

func TestClientReloadAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Reload(context.Background(), "node1")
	if err == nil {
		t.Error("Reload() should return error on API error")
	}
}

func TestClientPauseNoMembers(t *testing.T) {
	mockCluster := &types.Cluster{
		Members: []*types.Member{},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Pause(context.Background(), false)
	if err == nil {
		t.Error("Pause() should return error when no members")
	}
}

func TestClientPauseUsesFirstMemberWhenNoLeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mockCluster := &types.Cluster{
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					APIURL: server.URL,
				},
			},
		},
	}

	mockDCS := &MockDCS{cluster: mockCluster}
	client := NewClient(&Config{
		DCS:   mockDCS,
		Scope: "test",
	})

	err := client.Pause(context.Background(), false)
	if err != nil {
		t.Errorf("Pause() error = %v", err)
	}
}

// Benchmark tests
func BenchmarkNewClient(b *testing.B) {
	cfg := &Config{
		Scope:   "benchmark",
		Timeout: 10 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewClient(cfg)
	}
}

func BenchmarkValidateConfig(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidateConfig("ttl", 30)
	}
}

func BenchmarkParseDCSURL(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseDCSURL("etcd3://localhost:2379")
	}
}

func BenchmarkClusterMemberMarshal(b *testing.B) {
	member := &ClusterMember{
		Name:     "node1",
		Host:     "192.168.1.1",
		Port:     5432,
		Role:     "Leader",
		State:    StateRunning,
		Timeline: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(member)
	}
}
