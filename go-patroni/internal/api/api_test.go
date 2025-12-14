package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/pkg/types"
)

// TestServerRoutes tests that the server routes are set up correctly.
func TestServerRoutes(t *testing.T) {
	s := &Server{
		failsafe: make(map[string]string),
	}
	s.setupRoutes()

	if s.router == nil {
		t.Error("Router should be initialized after setupRoutes()")
	}
}

// TestWriteJSON tests the JSON writing helper.
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

// TestWriteError tests the error writing helper.
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

// TestNewServer tests server creation.
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

// TestNewServerNilConfig tests server creation with nil config.
func TestNewServerNilConfig(t *testing.T) {
	s := NewServer(nil, nil, nil)
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
	if s.router == nil {
		t.Error("Server.router should be initialized even with nil config")
	}
}

// TestServerStop tests stopping a server that hasn't started.
func TestServerStop(t *testing.T) {
	s := &Server{}
	err := s.Stop()
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}

// TestReloadConfig tests configuration reloading.
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

// TestReloadConfigNil tests reloading config when server has no config.
func TestReloadConfigNil(t *testing.T) {
	s := &Server{}
	// Should not panic
	s.ReloadConfig(&config.RestAPIConfig{Listen: ":8008"})
}

// TestAuthMiddleware tests the authentication middleware.
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
		{
			name:       "no auth",
			auth:       false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong username",
			auth:       true,
			username:   "wrong",
			password:   "pass",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong password",
			auth:       true,
			username:   "test",
			password:   "wrong",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "correct auth",
			auth:       true,
			username:   "test",
			password:   "pass",
			wantStatus: http.StatusOK,
		},
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

// TestAuthMiddlewareNoConfig tests auth middleware skips when not configured.
func TestAuthMiddlewareNoConfig(t *testing.T) {
	s := NewServer(nil, nil, nil)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := s.authMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestAuthMiddlewareEmptyUsername tests auth skips with empty username.
func TestAuthMiddlewareEmptyUsername(t *testing.T) {
	cfg := &config.RestAPIConfig{
		Listen:   ":8008",
		Username: "",
		Password: "pass",
	}
	s := NewServer(nil, nil, cfg)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := s.authMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should pass without auth when username is empty
	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestCheckAccess tests the IP allowlist checking.
func TestCheckAccess(t *testing.T) {
	tests := []struct {
		name       string
		allowlist  []string
		remoteAddr string
		expected   bool
	}{
		{
			name:       "no allowlist",
			allowlist:  nil,
			remoteAddr: "192.168.1.1:54321",
			expected:   true,
		},
		{
			name:       "empty allowlist",
			allowlist:  []string{},
			remoteAddr: "192.168.1.1:54321",
			expected:   true,
		},
		{
			name:       "exact match",
			allowlist:  []string{"192.168.1.1"},
			remoteAddr: "192.168.1.1:54321",
			expected:   true,
		},
		{
			name:       "no match",
			allowlist:  []string{"192.168.1.2"},
			remoteAddr: "192.168.1.1:54321",
			expected:   false,
		},
		{
			name:       "CIDR match",
			allowlist:  []string{"192.168.1.0/24"},
			remoteAddr: "192.168.1.100:54321",
			expected:   true,
		},
		{
			name:       "CIDR no match",
			allowlist:  []string{"192.168.1.0/24"},
			remoteAddr: "192.168.2.1:54321",
			expected:   false,
		},
		{
			name:       "IPv6 match",
			allowlist:  []string{"::1"},
			remoteAddr: "[::1]:54321",
			expected:   true,
		},
		{
			name:       "localhost",
			allowlist:  []string{"127.0.0.1"},
			remoteAddr: "127.0.0.1:54321",
			expected:   true,
		},
		{
			name:       "multiple rules first match",
			allowlist:  []string{"192.168.1.1", "10.0.0.0/8"},
			remoteAddr: "192.168.1.1:54321",
			expected:   true,
		},
		{
			name:       "multiple rules second match",
			allowlist:  []string{"192.168.1.1", "10.0.0.0/8"},
			remoteAddr: "10.1.2.3:54321",
			expected:   true,
		},
		{
			name:       "invalid IP",
			allowlist:  []string{"192.168.1.1"},
			remoteAddr: "invalid:54321",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.RestAPIConfig{
				Allowlist: tt.allowlist,
			}
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

// TestCheckAccessNilConfig tests CheckAccess with nil config.
func TestCheckAccessNilConfig(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:54321"

	// Should allow when no config
	result := s.CheckAccess(req)
	if !result {
		t.Error("CheckAccess() should return true with nil config")
	}
}

// TestGetFailsafeState tests the failsafe state getter.
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

// TestGetFailsafeStateEmpty tests empty failsafe state.
func TestGetFailsafeStateEmpty(t *testing.T) {
	s := &Server{
		failsafe: make(map[string]string),
	}

	state := s.GetFailsafeState()
	if len(state) != 0 {
		t.Errorf("Failsafe state length = %d, want 0", len(state))
	}
}

// TestGetScope tests the scope getter.
func TestGetScope(t *testing.T) {
	// With nil config
	s := &Server{}
	if scope := s.getScope(); scope != "" {
		t.Errorf("getScope() = %q, want empty", scope)
	}

	// With config
	s.config = &config.Config{Scope: "mycluster"}
	if scope := s.getScope(); scope != "mycluster" {
		t.Errorf("getScope() = %q, want %q", scope, "mycluster")
	}
}

// TestGetName tests the name getter.
func TestGetName(t *testing.T) {
	// With nil config
	s := &Server{}
	if name := s.getName(); name != "" {
		t.Errorf("getName() = %q, want empty", name)
	}

	// With config
	s.config = &config.Config{Name: "node1"}
	if name := s.getName(); name != "node1" {
		t.Errorf("getName() = %q, want %q", name, "node1")
	}
}

// TestLivenessEndpoint tests the liveness probe endpoint.
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

// Test JSON response types
func TestHealthResponseJSON(t *testing.T) {
	resp := map[string]interface{}{
		"state":          "running",
		"role":           "primary",
		"server_version": 150000,
		"timeline":       1,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if decoded["state"] != "running" {
		t.Errorf("state = %v, want 'running'", decoded["state"])
	}
	if decoded["role"] != "primary" {
		t.Errorf("role = %v, want 'primary'", decoded["role"])
	}
}

func TestClusterResponseJSON(t *testing.T) {
	resp := map[string]interface{}{
		"scope": "mycluster",
		"members": []map[string]interface{}{
			{
				"name":    "node1",
				"role":    "leader",
				"state":   "running",
				"api_url": "http://node1:8008",
			},
			{
				"name":    "node2",
				"role":    "replica",
				"state":   "running",
				"api_url": "http://node2:8008",
				"lag":     1024,
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	if !strings.Contains(string(data), "mycluster") {
		t.Error("JSON should contain scope")
	}
	if !strings.Contains(string(data), "node1") {
		t.Error("JSON should contain member names")
	}
}

func TestErrorResponseJSON(t *testing.T) {
	resp := map[string]string{
		"error": "something went wrong",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	if !strings.Contains(string(data), "something went wrong") {
		t.Error("JSON should contain error message")
	}
}

func TestSwitchoverRequestJSON(t *testing.T) {
	req := struct {
		Leader    string `json:"leader"`
		Candidate string `json:"candidate"`
	}{
		Leader:    "node1",
		Candidate: "node2",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var decoded struct {
		Leader    string `json:"leader"`
		Candidate string `json:"candidate"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}

	if decoded.Leader != "node1" {
		t.Errorf("Leader = %q, want %q", decoded.Leader, "node1")
	}
	if decoded.Candidate != "node2" {
		t.Errorf("Candidate = %q, want %q", decoded.Candidate, "node2")
	}
}

func TestRestartRequestJSON(t *testing.T) {
	req := struct {
		Role            string `json:"role,omitempty"`
		PostgresVersion string `json:"postgres_version,omitempty"`
		Timeout         int    `json:"timeout,omitempty"`
		PendingRestart  bool   `json:"pending_restart,omitempty"`
	}{
		Role:            "replica",
		PostgresVersion: "15.0",
		Timeout:         30,
		PendingRestart:  true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	if !strings.Contains(string(data), "replica") {
		t.Error("JSON should contain role")
	}
	if !strings.Contains(string(data), "pending_restart") {
		t.Error("JSON should contain pending_restart")
	}
}

func TestReinitializeRequestJSON(t *testing.T) {
	req := struct {
		Force bool `json:"force,omitempty"`
	}{
		Force: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	if !strings.Contains(string(data), "true") {
		t.Error("JSON should contain force:true")
	}
}

func TestScheduledSwitchoverRequestJSON(t *testing.T) {
	req := struct {
		Leader      string `json:"leader,omitempty"`
		Candidate   string `json:"candidate,omitempty"`
		ScheduledAt string `json:"scheduled_at,omitempty"`
	}{
		Leader:      "node1",
		Candidate:   "node2",
		ScheduledAt: "2024-01-15T10:30:00Z",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var decoded struct {
		Leader      string `json:"leader,omitempty"`
		Candidate   string `json:"candidate,omitempty"`
		ScheduledAt string `json:"scheduled_at,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}

	if decoded.ScheduledAt != "2024-01-15T10:30:00Z" {
		t.Errorf("ScheduledAt = %q, want %q", decoded.ScheduledAt, "2024-01-15T10:30:00Z")
	}
}

// TestTagsType tests the tags type from types package.
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
	if !tags.CloneFrom {
		t.Error("CloneFrom should be true")
	}
	if tags.FailoverPriority != 1 {
		t.Errorf("FailoverPriority = %d, want 1", tags.FailoverPriority)
	}
}

// TestMethodNotAllowed tests that wrong HTTP methods are rejected.
func TestMethodNotAllowed(t *testing.T) {
	s := NewServer(nil, nil, nil)

	// GET endpoint shouldn't accept POST
	req := httptest.NewRequest("POST", "/liveness", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestContentTypeJSON tests that JSON endpoints return proper content type.
func TestContentTypeJSON(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()

	s.writeJSON(w, http.StatusOK, map[string]string{"test": "value"})

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

// TestWriteJSONWithDifferentTypes tests JSON writing with various types.
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

// TestWriteErrorWithDifferentCodes tests error writing with various status codes.
func TestWriteErrorWithDifferentCodes(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		msg    string
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

// TestConfigStruct tests the config structure.
func TestConfigStruct(t *testing.T) {
	cfg := config.RestAPIConfig{
		Listen:   ":8008",
		Username: "admin",
		Password: "secret",
		CertFile: "/path/to/cert.pem",
		KeyFile:  "/path/to/key.pem",
		Allowlist: []string{"127.0.0.1", "192.168.0.0/16"},
	}

	if cfg.Listen != ":8008" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":8008")
	}
	if cfg.Username != "admin" {
		t.Errorf("Username = %q, want %q", cfg.Username, "admin")
	}
	if len(cfg.Allowlist) != 2 {
		t.Errorf("Allowlist length = %d, want 2", len(cfg.Allowlist))
	}
}

// Test that failsafe can be modified
func TestFailsafeModification(t *testing.T) {
	s := &Server{
		failsafe: make(map[string]string),
	}

	// Initial state should be empty
	if len(s.failsafe) != 0 {
		t.Error("Initial failsafe should be empty")
	}

	// Add entries
	s.failsafe["node1"] = "http://node1:8008"
	s.failsafe["node2"] = "http://node2:8008"

	state := s.GetFailsafeState()
	if len(state) != 2 {
		t.Errorf("Failsafe should have 2 entries, got %d", len(state))
	}

	// Remove entry
	delete(s.failsafe, "node1")
	state = s.GetFailsafeState()
	if len(state) != 1 {
		t.Errorf("Failsafe should have 1 entry, got %d", len(state))
	}
}

// TestFailsafeEndpointRoute tests that failsafe route exists.
func TestFailsafeEndpointRoute(t *testing.T) {
	s := NewServer(nil, nil, nil)

	// Test that POST /failsafe exists (but will fail due to nil PG)
	body := bytes.NewBufferString(`{"node1": "http://node1:8008"}`)
	req := httptest.NewRequest("POST", "/failsafe", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	// Should not be 404 (route exists)
	if w.Code == http.StatusNotFound {
		t.Error("Failsafe endpoint should exist")
	}
}

// Benchmark tests
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

func BenchmarkAuthMiddleware(b *testing.B) {
	cfg := &config.RestAPIConfig{
		Listen:   ":8008",
		Username: "test",
		Password: "pass",
	}
	s := NewServer(nil, nil, cfg)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := s.authMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.SetBasicAuth("test", "pass")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}
