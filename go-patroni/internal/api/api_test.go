package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/pkg/types"
)

// MockHA implements HAInterface for testing.
type MockHA struct {
	isLeader         bool
	isPaused         bool
	isBusy           bool
	cluster          *types.Cluster
	config           map[string]interface{}
	effectiveTags    types.Tags
	scheduledRestart *ScheduledRestart

	// Error injection
	reinitializeErr       error
	manualFailoverErr     error
	cancelFailoverErr     error
	setConfigErr          error
	scheduleRestartErr    error
	cancelRestartErr      error

	// Call tracking
	reinitializeCalls     int
	manualFailoverCalls   int
	cancelFailoverCalls   int
	setConfigCalls        int
	scheduleRestartCalls  int
	cancelRestartCalls    int
}

func (m *MockHA) IsLeader() bool                              { return m.isLeader }
func (m *MockHA) IsPaused() bool                              { return m.isPaused }
func (m *MockHA) IsBusy() bool                                { return m.isBusy }
func (m *MockHA) GetCluster() *types.Cluster                  { return m.cluster }
func (m *MockHA) GetConfig() map[string]interface{}           { return m.config }
func (m *MockHA) GetEffectiveTags() types.Tags                { return m.effectiveTags }
func (m *MockHA) GetScheduledRestart() *ScheduledRestart      { return m.scheduledRestart }

func (m *MockHA) ScheduleRestart(schedule time.Time, postmasterStartTime time.Time) error {
	m.scheduleRestartCalls++
	if m.scheduleRestartErr != nil {
		return m.scheduleRestartErr
	}
	m.scheduledRestart = &ScheduledRestart{Schedule: schedule, PostmasterStartTime: postmasterStartTime, Pending: true}
	return nil
}

func (m *MockHA) CancelScheduledRestart() error {
	m.cancelRestartCalls++
	if m.cancelRestartErr != nil {
		return m.cancelRestartErr
	}
	m.scheduledRestart = nil
	return nil
}

func (m *MockHA) Reinitialize(ctx context.Context, force bool) error {
	m.reinitializeCalls++
	return m.reinitializeErr
}

func (m *MockHA) ManualFailover(ctx context.Context, leader, candidate string, scheduledAt *time.Time) error {
	m.manualFailoverCalls++
	return m.manualFailoverErr
}

func (m *MockHA) CancelFailover(ctx context.Context) error {
	m.cancelFailoverCalls++
	return m.cancelFailoverErr
}

func (m *MockHA) SetConfig(ctx context.Context, cfg map[string]interface{}) error {
	m.setConfigCalls++
	if m.setConfigErr != nil {
		return m.setConfigErr
	}
	m.config = cfg
	return nil
}

// MockPostgreSQL implements PostgreSQLInterface for testing.
type MockPostgreSQL struct {
	state          types.PostgresqlState
	role           types.PostgresqlRole
	running        bool
	primary        bool
	majorVersion   int
	sysID          string
	pendingRestart bool
	restartReason  map[string]interface{}
	timeline       int
	walPosition    int64

	// Error injection
	restartErr error
	reloadErr  error

	// Call tracking
	restartCalls int
	reloadCalls  int
}

func (m *MockPostgreSQL) State() types.PostgresqlState                { return m.state }
func (m *MockPostgreSQL) Role() types.PostgresqlRole                  { return m.role }
func (m *MockPostgreSQL) IsRunning() bool                             { return m.running }
func (m *MockPostgreSQL) IsPrimary() bool                             { return m.primary }
func (m *MockPostgreSQL) MajorVersion() int                           { return m.majorVersion }
func (m *MockPostgreSQL) SysID() string                               { return m.sysID }
func (m *MockPostgreSQL) IsPendingRestart() bool                      { return m.pendingRestart }
func (m *MockPostgreSQL) PendingRestartReason() map[string]interface{} { return m.restartReason }
func (m *MockPostgreSQL) TimelineWALPosition() (int, int64)           { return m.timeline, m.walPosition }

func (m *MockPostgreSQL) Restart(ctx context.Context) error {
	m.restartCalls++
	return m.restartErr
}

func (m *MockPostgreSQL) Reload(ctx context.Context) error {
	m.reloadCalls++
	return m.reloadErr
}

// newTestableServer creates a real Server with mock interfaces for testing.
// This ensures tests exercise the actual Server implementation code.
func newTestableServer(ha HAInterface, pg PostgreSQLInterface, cfg *config.RestAPIConfig) *Server {
	return NewServerWithInterfaces(ha, pg, cfg)
}

// Test helpers
func newMockHA() *MockHA {
	return &MockHA{
		cluster: &types.Cluster{
			InitializeVersion: 1,
			Members:           []*types.Member{},
		},
		config: make(map[string]interface{}),
	}
}

func newMockPG() *MockPostgreSQL {
	return &MockPostgreSQL{
		state:        types.PostgresqlStateRunning,
		role:         types.PostgresqlRolePrimary,
		running:      true,
		primary:      true,
		majorVersion: 150000,
		sysID:        "1234567890123456789",
		timeline:     1,
		walPosition:  16777216,
	}
}

// ============= Tests =============

func TestServerRoutes(t *testing.T) {
	s := &Server{
		failsafe: make(map[string]string),
	}
	s.setupRoutes()

	if s.router == nil {
		t.Error("Router should be initialized after setupRoutes()")
	}
}

func TestWriteJSON(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()

	data := map[string]string{"message": "test"}
	s.writeJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["message"] != "test" {
		t.Errorf("Response message = %q, want %q", resp["message"], "test")
	}
}

func TestWriteError(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()

	s.writeError(w, http.StatusBadRequest, "invalid request")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["error"] != "invalid request" {
		t.Errorf("Response error = %q, want %q", resp["error"], "invalid request")
	}
}

func TestNewServer(t *testing.T) {
	cfg := &config.RestAPIConfig{
		Listen: ":8008",
	}

	s := NewServer(nil, nil, cfg)
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
	if s.router == nil {
		t.Error("Server.router should be initialized")
	}
	if s.failsafe == nil {
		t.Error("Server.failsafe should be initialized")
	}
}

func TestNewServerNilConfig(t *testing.T) {
	s := NewServer(nil, nil, nil)
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
	if s.router == nil {
		t.Error("Server.router should be initialized even with nil config")
	}
}

func TestServerStop(t *testing.T) {
	s := &Server{}
	err := s.Stop()
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}

func TestReloadConfig(t *testing.T) {
	cfg := &config.RestAPIConfig{
		Listen:   ":8008",
		Username: "test",
		Password: "pass",
	}
	s := NewServer(nil, nil, cfg)

	newCfg := &config.RestAPIConfig{
		Listen:   ":8009",
		Username: "newuser",
		Password: "newpass",
	}
	s.ReloadConfig(newCfg)

	if s.config.RestAPI.Username != "newuser" {
		t.Errorf("Username = %q, want %q", s.config.RestAPI.Username, "newuser")
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.RestAPIConfig{
		Listen:   ":8008",
		Username: "test",
		Password: "pass",
	}
	s := NewServer(nil, nil, cfg)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	handler := s.authMiddleware(testHandler)

	tests := []struct {
		name       string
		auth       bool
		username   string
		password   string
		wantStatus int
	}{
		{"no auth", false, "", "", http.StatusUnauthorized},
		{"wrong username", true, "wrong", "pass", http.StatusUnauthorized},
		{"wrong password", true, "test", "wrong", http.StatusUnauthorized},
		{"correct auth", true, "test", "pass", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.auth {
				req.SetBasicAuth(tt.username, tt.password)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCheckAccess(t *testing.T) {
	tests := []struct {
		name       string
		allowlist  []string
		remoteAddr string
		expected   bool
	}{
		{"no allowlist", nil, "192.168.1.1:54321", true},
		{"empty allowlist", []string{}, "192.168.1.1:54321", true},
		{"exact match", []string{"192.168.1.1"}, "192.168.1.1:54321", true},
		{"no match", []string{"192.168.1.2"}, "192.168.1.1:54321", false},
		{"CIDR match", []string{"192.168.1.0/24"}, "192.168.1.100:54321", true},
		{"CIDR no match", []string{"192.168.1.0/24"}, "192.168.2.1:54321", false},
		{"IPv6 match", []string{"::1"}, "[::1]:54321", true},
		{"localhost", []string{"127.0.0.1"}, "127.0.0.1:54321", true},
		{"invalid IP", []string{"192.168.1.1"}, "invalid:54321", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.RestAPIConfig{Allowlist: tt.allowlist}
			s := NewServer(nil, nil, cfg)

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr

			result := s.CheckAccess(req)
			if result != tt.expected {
				t.Errorf("CheckAccess() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetFailsafeState(t *testing.T) {
	s := &Server{
		failsafe: map[string]string{
			"node1": "http://node1:8008",
			"node2": "http://node2:8008",
		},
	}

	state := s.GetFailsafeState()
	if len(state) != 2 {
		t.Errorf("Failsafe state length = %d, want 2", len(state))
	}
	if state["node1"] != "http://node1:8008" {
		t.Errorf("node1 = %q, want %q", state["node1"], "http://node1:8008")
	}
}

func TestGetScope(t *testing.T) {
	s := &Server{}
	if scope := s.getScope(); scope != "" {
		t.Errorf("getScope() = %q, want empty", scope)
	}

	s.config = &config.Config{Scope: "mycluster"}
	if scope := s.getScope(); scope != "mycluster" {
		t.Errorf("getScope() = %q, want %q", scope, "mycluster")
	}
}

func TestGetName(t *testing.T) {
	s := &Server{}
	if name := s.getName(); name != "" {
		t.Errorf("getName() = %q, want empty", name)
	}

	s.config = &config.Config{Name: "node1"}
	if name := s.getName(); name != "node1" {
		t.Errorf("getName() = %q, want %q", name, "node1")
	}
}

func TestLivenessEndpoint(t *testing.T) {
	s := NewServer(nil, nil, nil)

	req := httptest.NewRequest("GET", "/liveness", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if body != "OK" {
		t.Errorf("Body = %q, want %q", body, "OK")
	}
}

// ============= Tests using TestableServer with Mocks =============

func TestHealthEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		running    bool
		wantStatus int
	}{
		{"running", true, http.StatusOK},
		{"not running", false, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockPG := newMockPG()
			mockPG.running = tt.running

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestPrimaryEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		isPrimary  bool
		wantStatus int
	}{
		{"leader and primary", true, true, http.StatusOK},
		{"not leader", false, true, http.StatusServiceUnavailable},
		{"not primary", true, false, http.StatusServiceUnavailable},
		{"neither", false, false, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockPG := newMockPG()
			mockPG.primary = tt.isPrimary

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/primary", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestReplicaEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name          string
		isLeader      bool
		running       bool
		primary       bool
		noLoadbalance bool
		wantStatus    int
	}{
		{"valid replica", false, true, false, false, http.StatusOK},
		{"is leader", true, true, false, false, http.StatusServiceUnavailable},
		{"not running", false, false, false, false, http.StatusServiceUnavailable},
		{"is primary", false, true, true, false, http.StatusServiceUnavailable},
		{"noloadbalance", false, true, false, true, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockHA.effectiveTags.NoLoadbalance = tt.noLoadbalance
			mockPG := newMockPG()
			mockPG.running = tt.running
			mockPG.primary = tt.primary

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/replica", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestLeaderEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		wantStatus int
	}{
		{"is leader", true, http.StatusOK},
		{"not leader", false, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockPG := newMockPG()

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/leader", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestStandbyLeaderEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		isPrimary  bool
		wantStatus int
	}{
		{"standby leader", true, false, http.StatusOK},
		{"normal leader", true, true, http.StatusServiceUnavailable},
		{"not leader", false, false, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockPG := newMockPG()
			mockPG.primary = tt.isPrimary

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/standby-leader", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestReadOnlyEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name          string
		running       bool
		noLoadbalance bool
		wantStatus    int
	}{
		{"running readable", true, false, http.StatusOK},
		{"not running", false, false, http.StatusServiceUnavailable},
		{"noloadbalance", true, true, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.effectiveTags.NoLoadbalance = tt.noLoadbalance
			mockPG := newMockPG()
			mockPG.running = tt.running

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/read-only", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestReadinessEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		running    bool
		state      types.PostgresqlState
		wantStatus int
	}{
		{"running and ready", true, types.PostgresqlStateRunning, http.StatusOK},
		{"not running", false, types.PostgresqlStateStopped, http.StatusServiceUnavailable},
		{"starting", true, types.PostgresqlStateStarting, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockPG := newMockPG()
			mockPG.running = tt.running
			mockPG.state = tt.state

			s := newTestableServer(mockHA, mockPG, nil)

			req := httptest.NewRequest("GET", "/readiness", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestClusterEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader: &types.Leader{
			MemberName: "node1",
		},
		Members: []*types.Member{
			{
				Name: "node1",
				Data: types.MemberData{
					Role:         "primary",
					State:        "running",
					APIURL:       "http://node1:8008",
					Timeline:     1,
					XlogLocation: 16777216,
				},
			},
			{
				Name: "node2",
				Data: types.MemberData{
					Role:         "replica",
					State:        "running",
					APIURL:       "http://node2:8008",
					Timeline:     1,
					XlogLocation: 16777000,
				},
			},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, &config.RestAPIConfig{})
	s.config = &config.Config{Scope: "mycluster"}

	req := httptest.NewRequest("GET", "/cluster", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["scope"] != "mycluster" {
		t.Errorf("scope = %v, want 'mycluster'", resp["scope"])
	}
	if resp["leader"] != "node1" {
		t.Errorf("leader = %v, want 'node1'", resp["leader"])
	}

	members := resp["members"].([]interface{})
	if len(members) != 2 {
		t.Errorf("members count = %d, want 2", len(members))
	}
}

func TestClusterEndpointNoCluster(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = nil
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("GET", "/cluster", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHistoryEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		History: &types.TimelineHistory{
			Value: []types.TimelineEntry{
				{Timeline: 1, LSN: 16777216, Reason: "new primary"},
			},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("GET", "/history", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp []types.TimelineEntry
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp) != 1 {
		t.Errorf("history entries = %d, want 1", len(resp))
	}
}

func TestConfigEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Config: &types.ClusterConfig{
			Data: map[string]interface{}{
				"loop_wait":     10,
				"ttl":           30,
				"maximum_lag_on_failover": 1048576,
			},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["loop_wait"].(float64) != 10 {
		t.Errorf("loop_wait = %v, want 10", resp["loop_wait"])
	}
}

func TestRestartEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("POST", "/restart", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockPG.restartCalls != 1 {
		t.Errorf("restart calls = %d, want 1", mockPG.restartCalls)
	}
}

func TestRestartScheduledEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	// Schedule a restart for 1 hour from now
	scheduleTime := time.Now().Add(time.Hour).Format(time.RFC3339)
	body := bytes.NewBufferString(`{"schedule":"` + scheduleTime + `"}`)
	req := httptest.NewRequest("POST", "/restart", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusAccepted)
	}

	if mockHA.scheduleRestartCalls != 1 {
		t.Errorf("scheduleRestart calls = %d, want 1", mockHA.scheduleRestartCalls)
	}

	// restart should not be called for scheduled restart
	if mockPG.restartCalls != 0 {
		t.Errorf("restart calls = %d, want 0", mockPG.restartCalls)
	}
}

func TestDeleteRestartEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("DELETE", "/restart", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockHA.cancelRestartCalls != 1 {
		t.Errorf("cancelRestart calls = %d, want 1", mockHA.cancelRestartCalls)
	}
}

func TestReloadEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("POST", "/reload", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockPG.reloadCalls != 1 {
		t.Errorf("reload calls = %d, want 1", mockPG.reloadCalls)
	}
}

func TestReinitializeEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		isBusy     bool
		force      bool
		wantStatus int
	}{
		{"non-leader success", false, false, false, http.StatusAccepted},
		{"leader forbidden", true, false, false, http.StatusForbidden},
		{"busy without force", false, true, false, http.StatusConflict},
		{"busy with force", false, true, true, http.StatusAccepted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockHA.isBusy = tt.isBusy
			mockPG := newMockPG()

			s := newTestableServer(mockHA, mockPG, nil)

			var body *bytes.Buffer
			if tt.force {
				body = bytes.NewBufferString(`{"force":true}`)
			} else {
				body = bytes.NewBufferString(`{}`)
			}

			req := httptest.NewRequest("POST", "/reinitialize", body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestSwitchoverEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.isLeader = true
	mockHA.cluster = &types.Cluster{
		InitializeVersion: 1,
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
			{Name: "node2", Data: types.MemberData{Role: "replica"}},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)
	s.config = &config.Config{Name: "node1"}

	body := bytes.NewBufferString(`{"leader":"node1","candidate":"node2"}`)
	req := httptest.NewRequest("POST", "/switchover", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusAccepted)
	}

	if mockHA.manualFailoverCalls != 1 {
		t.Errorf("manualFailover calls = %d, want 1", mockHA.manualFailoverCalls)
	}
}

func TestSwitchoverEndpointNotLeader(t *testing.T) {
	mockHA := newMockHA()
	mockHA.isLeader = false
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{"candidate":"node2"}`)
	req := httptest.NewRequest("POST", "/switchover", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestFailoverEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		InitializeVersion: 1,
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary"}},
			{Name: "node2", Data: types.MemberData{Role: "replica"}},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{"candidate":"node2"}`)
	req := httptest.NewRequest("POST", "/failover", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusAccepted)
	}

	if mockHA.manualFailoverCalls != 1 {
		t.Errorf("manualFailover calls = %d, want 1", mockHA.manualFailoverCalls)
	}
}

func TestFailoverEndpointNoCandidate(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/failover", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPatchConfigEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Config: &types.ClusterConfig{
			Data: map[string]interface{}{
				"loop_wait": 10,
				"ttl":       30,
			},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{"loop_wait":15,"new_param":"value"}`)
	req := httptest.NewRequest("PATCH", "/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockHA.setConfigCalls != 1 {
		t.Errorf("setConfig calls = %d, want 1", mockHA.setConfigCalls)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["loop_wait"].(float64) != 15 {
		t.Errorf("loop_wait = %v, want 15", resp["loop_wait"])
	}
	if resp["new_param"] != "value" {
		t.Errorf("new_param = %v, want 'value'", resp["new_param"])
	}
}

func TestPutConfigEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Config: &types.ClusterConfig{
			Data: map[string]interface{}{
				"old_param": "old_value",
			},
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{"loop_wait":20,"ttl":60}`)
	req := httptest.NewRequest("PUT", "/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockHA.setConfigCalls != 1 {
		t.Errorf("setConfig calls = %d, want 1", mockHA.setConfigCalls)
	}
}

func TestFailsafeEndpointWithMocks(t *testing.T) {
	mockHA := newMockHA()
	mockPG := newMockPG()
	mockPG.walPosition = 16777216

	s := newTestableServer(mockHA, mockPG, nil)

	body := bytes.NewBufferString(`{"node1":"http://node1:8008"}`)
	req := httptest.NewRequest("POST", "/failsafe", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["accepted"] != true {
		t.Errorf("accepted = %v, want true", resp["accepted"])
	}
	if resp["lsn"].(float64) != 16777216 {
		t.Errorf("lsn = %v, want 16777216", resp["lsn"])
	}

	if s.failsafe["node1"] != "http://node1:8008" {
		t.Errorf("failsafe node1 = %v, want 'http://node1:8008'", s.failsafe["node1"])
	}
}

func TestSynchronousEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		running    bool
		primary    bool
		syncState  *types.SyncState
		nodeName   string
		wantStatus int
	}{
		{
			name:     "is sync standby via SyncStandby",
			isLeader: false,
			running:  true,
			primary:  false,
			syncState: &types.SyncState{
				SyncStandby: []string{"node2"},
			},
			nodeName:   "node2",
			wantStatus: http.StatusOK,
		},
		{
			name:     "is sync standby via Sync",
			isLeader: false,
			running:  true,
			primary:  false,
			syncState: &types.SyncState{
				Sync: "node2",
			},
			nodeName:   "node2",
			wantStatus: http.StatusOK,
		},
		{
			name:     "not sync standby",
			isLeader: false,
			running:  true,
			primary:  false,
			syncState: &types.SyncState{
				SyncStandby: []string{"node3"},
			},
			nodeName:   "node2",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "is leader",
			isLeader:   true,
			running:    true,
			primary:    true,
			syncState:  nil,
			nodeName:   "node1",
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockHA.cluster = &types.Cluster{
				SyncState: tt.syncState,
			}
			mockPG := newMockPG()
			mockPG.running = tt.running
			mockPG.primary = tt.primary

			s := newTestableServer(mockHA, mockPG, nil)
			s.config = &config.Config{Name: tt.nodeName}

			req := httptest.NewRequest("GET", "/synchronous", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestAsynchronousEndpointWithMocks(t *testing.T) {
	tests := []struct {
		name       string
		isLeader   bool
		running    bool
		primary    bool
		syncState  *types.SyncState
		nodeName   string
		wantStatus int
	}{
		{
			name:       "is async standby",
			isLeader:   false,
			running:    true,
			primary:    false,
			syncState:  &types.SyncState{SyncStandby: []string{"node3"}},
			nodeName:   "node2",
			wantStatus: http.StatusOK,
		},
		{
			name:       "is sync standby (rejected)",
			isLeader:   false,
			running:    true,
			primary:    false,
			syncState:  &types.SyncState{SyncStandby: []string{"node2"}},
			nodeName:   "node2",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "is leader",
			isLeader:   true,
			running:    true,
			primary:    true,
			syncState:  nil,
			nodeName:   "node1",
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := newMockHA()
			mockHA.isLeader = tt.isLeader
			mockHA.cluster = &types.Cluster{
				SyncState: tt.syncState,
			}
			mockPG := newMockPG()
			mockPG.running = tt.running
			mockPG.primary = tt.primary

			s := newTestableServer(mockHA, mockPG, nil)
			s.config = &config.Config{Name: tt.nodeName}

			req := httptest.NewRequest("GET", "/asynchronous", nil)
			w := httptest.NewRecorder()

			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestDeleteSwitchoverEndpointWithMocks(t *testing.T) {
	scheduledTime := time.Now().Add(time.Hour)
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Failover: &types.Failover{
			ScheduledAt: scheduledTime,
		},
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("DELETE", "/switchover", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	if mockHA.cancelFailoverCalls != 1 {
		t.Errorf("cancelFailover calls = %d, want 1", mockHA.cancelFailoverCalls)
	}
}

func TestDeleteSwitchoverEndpointNoScheduled(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Failover: nil,
	}
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	req := httptest.NewRequest("DELETE", "/switchover", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetPostgreSQLStatusWithTags(t *testing.T) {
	mockHA := newMockHA()
	mockHA.effectiveTags = types.Tags{
		NoFailover:    true,
		NoLoadbalance: true,
		CloneFrom:     true,
	}
	mockPG := newMockPG()
	mockPG.pendingRestart = true
	mockPG.restartReason = map[string]interface{}{"param": "changed"}

	s := newTestableServer(mockHA, mockPG, nil)

	status := s.getPostgreSQLStatus()

	if tags, ok := status["tags"].(types.Tags); !ok || !tags.NoFailover {
		t.Error("tags should include NoFailover=true")
	}

	if status["pending_restart"] != true {
		t.Error("pending_restart should be true")
	}

	if reason, ok := status["pending_restart_reason"].(map[string]interface{}); !ok || reason["param"] != "changed" {
		t.Error("pending_restart_reason should contain the reason")
	}
}

func TestCalculateLag(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		Leader: &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{XlogLocation: 1000}},
			{Name: "node2", Data: types.MemberData{XlogLocation: 800}},
		},
	}
	mockPG := newMockPG()
	mockPG.walPosition = 800

	s := newTestableServer(mockHA, mockPG, nil)

	lag := s.calculateLag()
	if lag != 200 {
		t.Errorf("lag = %d, want 200", lag)
	}
}

func TestCalculateLagNoCluster(t *testing.T) {
	mockHA := newMockHA()
	mockHA.cluster = nil
	mockPG := newMockPG()

	s := newTestableServer(mockHA, mockPG, nil)

	lag := s.calculateLag()
	if lag != 0 {
		t.Errorf("lag = %d, want 0", lag)
	}
}

// ============= Type and JSON Tests =============

func TestTagsType(t *testing.T) {
	tags := types.Tags{
		NoFailover:       true,
		NoLoadbalance:    false,
		CloneFrom:        true,
		FailoverPriority: 1,
	}

	if !tags.NoFailover {
		t.Error("NoFailover should be true")
	}
	if tags.NoLoadbalance {
		t.Error("NoLoadbalance should be false")
	}
}

func TestWriteJSONWithDifferentTypes(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{"map", map[string]string{"key": "value"}},
		{"slice", []string{"a", "b", "c"}},
		{"struct", struct{ Name string }{"test"}},
		{"int", 42},
		{"string", "test"},
		{"bool", true},
		{"nil", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			w := httptest.NewRecorder()

			s.writeJSON(w, http.StatusOK, tt.data)

			if w.Code != http.StatusOK {
				t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
			}
		})
	}
}

func TestWriteErrorWithDifferentCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
		msg  string
	}{
		{"bad request", http.StatusBadRequest, "bad request"},
		{"unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"forbidden", http.StatusForbidden, "forbidden"},
		{"not found", http.StatusNotFound, "not found"},
		{"conflict", http.StatusConflict, "conflict"},
		{"internal error", http.StatusInternalServerError, "internal error"},
		{"service unavailable", http.StatusServiceUnavailable, "service unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			w := httptest.NewRecorder()

			s.writeError(w, tt.code, tt.msg)

			if w.Code != tt.code {
				t.Errorf("Status code = %d, want %d", w.Code, tt.code)
			}

			var resp map[string]string
			json.NewDecoder(w.Body).Decode(&resp)
			if resp["error"] != tt.msg {
				t.Errorf("Error message = %q, want %q", resp["error"], tt.msg)
			}
		})
	}
}

// ============= Benchmark Tests =============

func BenchmarkWriteJSON(b *testing.B) {
	s := &Server{}
	data := map[string]interface{}{
		"state":          "running",
		"role":           "primary",
		"server_version": 150000,
		"timeline":       1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		s.writeJSON(w, http.StatusOK, data)
	}
}

func BenchmarkCheckAccess(b *testing.B) {
	cfg := &config.RestAPIConfig{
		Allowlist: []string{"127.0.0.1", "192.168.0.0/16", "::1"},
	}
	s := NewServer(nil, nil, cfg)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:54321"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.CheckAccess(req)
	}
}

func BenchmarkHealthEndpoint(b *testing.B) {
	mockHA := newMockHA()
	mockPG := newMockPG()
	s := newTestableServer(mockHA, mockPG, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
	}
}

func BenchmarkClusterEndpoint(b *testing.B) {
	mockHA := newMockHA()
	mockHA.cluster = &types.Cluster{
		InitializeVersion: 1,
		Leader:            &types.Leader{MemberName: "node1"},
		Members: []*types.Member{
			{Name: "node1", Data: types.MemberData{Role: "primary", State: "running", XlogLocation: 16777216}},
			{Name: "node2", Data: types.MemberData{Role: "replica", State: "running", XlogLocation: 16777000}},
			{Name: "node3", Data: types.MemberData{Role: "replica", State: "running", XlogLocation: 16776000}},
		},
	}
	mockPG := newMockPG()
	s := newTestableServer(mockHA, mockPG, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/cluster", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
	}
}
