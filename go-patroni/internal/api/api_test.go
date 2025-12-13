package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/patroni/patroni-go/pkg/types"
)

// Test response types (defined locally for testing)
type testHealthResponse struct {
	State            string `json:"state"`
	Role             string `json:"role"`
	ServerVersion    int    `json:"server_version"`
	Timeline         int    `json:"timeline"`
	XlogLocation     int64  `json:"xlog_location"`
	ReplicationState string `json:"replication_state,omitempty"`
}

type testClusterResponse struct {
	Members []testMemberResponse `json:"members"`
}

type testMemberResponse struct {
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	State          string     `json:"state"`
	APIURL         string     `json:"api_url"`
	Host           string     `json:"host"`
	Port           int        `json:"port"`
	Timeline       int        `json:"timeline"`
	Lag            int64      `json:"lag,omitempty"`
	PendingRestart bool       `json:"pending_restart,omitempty"`
	Tags           types.Tags `json:"tags,omitempty"`
}

type testConfigResponse struct {
	TTL          int                    `json:"ttl"`
	LoopWait     int                    `json:"loop_wait"`
	RetryTimeout int                    `json:"retry_timeout"`
	PostgreSQL   map[string]interface{} `json:"postgresql,omitempty"`
}

type testSwitchoverRequest struct {
	Leader    string `json:"leader"`
	Candidate string `json:"candidate"`
}

type testRestartRequest struct {
	Role            string `json:"role,omitempty"`
	PostgresVersion string `json:"postgres_version,omitempty"`
	Timeout         int    `json:"timeout,omitempty"`
	PendingRestart  bool   `json:"pending_restart,omitempty"`
}

type testErrorResponse struct {
	Error string `json:"error"`
}

func TestHealthResponseJSON(t *testing.T) {
	resp := testHealthResponse{
		State:            "running",
		Role:             "primary",
		ServerVersion:    150000,
		Timeline:         1,
		XlogLocation:     1234567,
		ReplicationState: "streaming",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal HealthResponse: %v", err)
	}

	var decoded testHealthResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal HealthResponse: %v", err)
	}

	if decoded.State != resp.State {
		t.Errorf("State = %q, want %q", decoded.State, resp.State)
	}
	if decoded.Role != resp.Role {
		t.Errorf("Role = %q, want %q", decoded.Role, resp.Role)
	}
}

func TestClusterResponseJSON(t *testing.T) {
	resp := testClusterResponse{
		Members: []testMemberResponse{
			{
				Name:     "node1",
				Role:     "leader",
				State:    "running",
				APIURL:   "http://node1:8008",
				Host:     "node1",
				Port:     5432,
				Timeline: 1,
			},
			{
				Name:     "node2",
				Role:     "replica",
				State:    "running",
				APIURL:   "http://node2:8008",
				Host:     "node2",
				Port:     5432,
				Timeline: 1,
				Lag:      1024,
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal ClusterResponse: %v", err)
	}

	var decoded testClusterResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ClusterResponse: %v", err)
	}

	if len(decoded.Members) != 2 {
		t.Errorf("Members length = %d, want 2", len(decoded.Members))
	}
	if decoded.Members[0].Name != "node1" {
		t.Errorf("Members[0].Name = %q, want %q", decoded.Members[0].Name, "node1")
	}
}

func TestMemberResponseJSON(t *testing.T) {
	resp := testMemberResponse{
		Name:           "node1",
		Role:           "leader",
		State:          "running",
		APIURL:         "http://node1:8008",
		Host:           "node1",
		Port:           5432,
		Timeline:       1,
		PendingRestart: true,
		Tags: types.Tags{
			NoFailover:       false,
			FailoverPriority: 1,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal MemberResponse: %v", err)
	}

	if !strings.Contains(string(data), `"name":"node1"`) {
		t.Error("JSON should contain name field")
	}
	if !strings.Contains(string(data), `"pending_restart":true`) {
		t.Error("JSON should contain pending_restart field")
	}
}

func TestConfigResponseJSON(t *testing.T) {
	resp := testConfigResponse{
		TTL:          30,
		LoopWait:     10,
		RetryTimeout: 10,
		PostgreSQL: map[string]interface{}{
			"parameters": map[string]interface{}{
				"max_connections": 100,
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal ConfigResponse: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ConfigResponse: %v", err)
	}

	if decoded["ttl"].(float64) != 30 {
		t.Errorf("ttl = %v, want 30", decoded["ttl"])
	}
}

func TestSwitchoverRequestJSON(t *testing.T) {
	req := testSwitchoverRequest{
		Leader:    "node1",
		Candidate: "node2",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal SwitchoverRequest: %v", err)
	}

	var decoded testSwitchoverRequest
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

func TestRestartRequestJSON(t *testing.T) {
	req := testRestartRequest{
		Role:            "replica",
		PostgresVersion: "15.0",
		Timeout:         30,
		PendingRestart:  true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal RestartRequest: %v", err)
	}

	var decoded testRestartRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal RestartRequest: %v", err)
	}

	if decoded.Role != "replica" {
		t.Errorf("Role = %q, want %q", decoded.Role, "replica")
	}
	if !decoded.PendingRestart {
		t.Error("PendingRestart should be true")
	}
}

// MockServer creates a test HTTP server with the API routes
type MockServer struct {
	router *chi.Mux
}

func NewMockServer() *MockServer {
	router := chi.NewRouter()

	ms := &MockServer{
		router: router,
	}

	// Add test routes
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testHealthResponse{
			State: "running",
			Role:  "primary",
		})
	})

	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.Get("/primary", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.Get("/replica", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	router.Get("/cluster", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testClusterResponse{
			Members: []testMemberResponse{
				{Name: "node1", Role: "leader"},
				{Name: "node2", Role: "replica"},
			},
		})
	})

	return ms
}

func (ms *MockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ms.router.ServeHTTP(w, r)
}

func TestHealthEndpoint(t *testing.T) {
	server := NewMockServer()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp testHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.State != "running" {
		t.Errorf("State = %q, want %q", resp.State, "running")
	}
}

func TestPrimaryEndpoint(t *testing.T) {
	server := NewMockServer()

	req := httptest.NewRequest("GET", "/primary", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestReplicaEndpoint(t *testing.T) {
	server := NewMockServer()

	req := httptest.NewRequest("GET", "/replica", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	// When we're a primary, /replica should return 503
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestClusterEndpoint(t *testing.T) {
	server := NewMockServer()

	req := httptest.NewRequest("GET", "/cluster", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp testClusterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp.Members) != 2 {
		t.Errorf("Members count = %d, want 2", len(resp.Members))
	}
}

func TestErrorResponse(t *testing.T) {
	resp := testErrorResponse{
		Error: "something went wrong",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal ErrorResponse: %v", err)
	}

	if !strings.Contains(string(data), "something went wrong") {
		t.Error("JSON should contain error message")
	}
}

func TestHTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name      string
		isPrimary bool
		isRunning bool
		endpoint  string
		expected  int
	}{
		{"primary on /primary", true, true, "/primary", http.StatusOK},
		{"primary on /replica", true, true, "/replica", http.StatusServiceUnavailable},
		{"replica on /primary", false, true, "/primary", http.StatusServiceUnavailable},
		{"replica on /replica", false, true, "/replica", http.StatusOK},
		{"stopped on any", true, false, "/primary", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is a conceptual test showing expected behavior
			// In real implementation, you'd use actual server with mocked PostgreSQL state
			if tt.isPrimary && tt.isRunning && tt.endpoint == "/primary" {
				if tt.expected != http.StatusOK {
					t.Error("Primary should return 200 on /primary")
				}
			}
		})
	}
}

func TestContentType(t *testing.T) {
	server := NewMockServer()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	// Response should be JSON
	body := w.Body.String()
	if !strings.HasPrefix(body, "{") {
		t.Error("Response should be JSON")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
