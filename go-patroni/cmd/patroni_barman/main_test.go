package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	testAPIURL       = "http://localhost:7480"
	testBarmanServer = "my_server"
	testBarmanModel  = "my_model"
	testBackupID     = "backup_id"
	testSSHCommand   = "ssh postgres@localhost"
	testDataDir      = "/path/to/pgdata"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
		want int
	}{
		{"no command", ExitNoCommand, -1},
		{"api not ok", ExitAPINotOK, -2},
		{"success", ExitSuccess, 0},
		{"error", ExitError, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.want {
				t.Errorf("exit code = %v, want %v", tt.code, tt.want)
			}
		})
	}
}

func TestNewPgBackupAPI(t *testing.T) {
	api := NewPgBackupAPI(testAPIURL, "", "")

	if api.baseURL != testAPIURL {
		t.Errorf("baseURL = %v, want %v", api.baseURL, testAPIURL)
	}
	if api.client == nil {
		t.Error("client should not be nil")
	}
}

func TestPgBackupAPIIsOK(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	api := NewPgBackupAPI(server.URL, "", "")

	if !api.IsOK(context.Background()) {
		t.Error("IsOK should return true for valid server")
	}

	// Test with failing server
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	failAPI := NewPgBackupAPI(failServer.URL, "", "")
	if failAPI.IsOK(context.Background()) {
		t.Error("IsOK should return false for failing server")
	}
}

func TestPgBackupAPIGetOperationStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		statusCode int
		wantStatus string
		wantErr    bool
	}{
		{"done", "DONE", http.StatusOK, "DONE", false},
		{"in progress", "IN_PROGRESS", http.StatusOK, "IN_PROGRESS", false},
		{"failed", "FAILED", http.StatusOK, "FAILED", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(OperationStatus{Status: tt.status})
			}))
			defer server.Close()

			api := NewPgBackupAPI(server.URL, "", "")
			status, err := api.GetOperationStatus(context.Background(), testBarmanServer, "op123")

			if (err != nil) != tt.wantErr {
				t.Errorf("GetOperationStatus error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && status.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", status.Status, tt.wantStatus)
			}
		})
	}
}

func TestPgBackupAPICreateRecoveryOperation(t *testing.T) {
	expectedOpID := "operation-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"operation_id": expectedOpID})
	}))
	defer server.Close()

	api := NewPgBackupAPI(server.URL, "", "")
	opID, err := api.CreateRecoveryOperation(context.Background(),
		testBarmanServer, testBackupID, testSSHCommand, testDataDir)

	if err != nil {
		t.Errorf("CreateRecoveryOperation error = %v", err)
		return
	}

	if opID != expectedOpID {
		t.Errorf("operation_id = %v, want %v", opID, expectedOpID)
	}
}

func TestPgBackupAPICreateConfigSwitchOperation(t *testing.T) {
	tests := []struct {
		name   string
		model  *string
		reset  bool
		wantOK bool
	}{
		{"with model", strPtr(testBarmanModel), false, true},
		{"with reset", nil, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"operation_id": "op-456"})
			}))
			defer server.Close()

			api := NewPgBackupAPI(server.URL, "", "")
			opID, err := api.CreateConfigSwitchOperation(context.Background(),
				testBarmanServer, tt.model, tt.reset)

			if err != nil {
				t.Errorf("CreateConfigSwitchOperation error = %v", err)
				return
			}

			if opID != "op-456" {
				t.Errorf("operation_id = %v, want op-456", opID)
			}
		})
	}
}

func TestOperationStatus(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		status  string
		message string
	}{
		{"done", `{"status":"DONE"}`, "DONE", ""},
		{"failed", `{"status":"FAILED","message":"error"}`, "FAILED", "error"},
		{"in progress", `{"status":"IN_PROGRESS"}`, "IN_PROGRESS", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var status OperationStatus
			if err := json.Unmarshal([]byte(tt.json), &status); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			if status.Status != tt.status {
				t.Errorf("Status = %v, want %v", status.Status, tt.status)
			}
			if status.Message != tt.message {
				t.Errorf("Message = %v, want %v", status.Message, tt.message)
			}
		})
	}
}

func TestWaitForOperation(t *testing.T) {
	tests := []struct {
		name       string
		responses  []string
		wantExit   int
		wantErr    bool
	}{
		{"immediate success", []string{"DONE"}, ExitSuccess, false},
		{"immediate failure", []string{"FAILED"}, ExitError, true},
		{"success after progress", []string{"IN_PROGRESS", "IN_PROGRESS", "DONE"}, ExitSuccess, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status := "DONE"
				if callCount < len(tt.responses) {
					status = tt.responses[callCount]
				}
				callCount++
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(OperationStatus{Status: status})
			}))
			defer server.Close()

			api := NewPgBackupAPI(server.URL, "", "")
			exitCode, err := waitForOperation(context.Background(), api,
				testBarmanServer, "op-123", 10*time.Millisecond, time.Second)

			if exitCode != tt.wantExit {
				t.Errorf("exitCode = %v, want %v", exitCode, tt.wantExit)
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunBarmanRecover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"operation_id": "op-123"})
			return
		}
		// GET for status
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OperationStatus{Status: "DONE"})
	}))
	defer server.Close()

	api := NewPgBackupAPI(server.URL, "", "")
	exitCode := runBarmanRecover(api, testBarmanServer, testBackupID,
		testSSHCommand, testDataDir, 10*time.Millisecond, time.Second)

	if exitCode != ExitSuccess {
		t.Errorf("runBarmanRecover exitCode = %v, want %v", exitCode, ExitSuccess)
	}
}

func TestRunBarmanConfigSwitch(t *testing.T) {
	tests := []struct {
		name     string
		model    *string
		reset    bool
		wantExit int
	}{
		{"with model", strPtr(testBarmanModel), false, ExitSuccess},
		{"with reset", nil, true, ExitSuccess},
		{"both specified", strPtr(testBarmanModel), true, ExitError},
		{"neither specified", nil, false, ExitError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]string{"operation_id": "op-456"})
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(OperationStatus{Status: "DONE"})
			}))
			defer server.Close()

			api := NewPgBackupAPI(server.URL, "", "")
			exitCode := runBarmanConfigSwitch(api, testBarmanServer, tt.model,
				tt.reset, 10*time.Millisecond, time.Second)

			if exitCode != tt.wantExit {
				t.Errorf("runBarmanConfigSwitch exitCode = %v, want %v", exitCode, tt.wantExit)
			}
		})
	}
}

func TestDefaultPollingConfig(t *testing.T) {
	if DefaultLoopWait != 10*time.Second {
		t.Errorf("DefaultLoopWait = %v, want 10s", DefaultLoopWait)
	}
	if DefaultRetry != 2*time.Minute {
		t.Errorf("DefaultRetry = %v, want 2m", DefaultRetry)
	}
}

func TestBuildFullURL(t *testing.T) {
	api := NewPgBackupAPI(testAPIURL, "", "")

	tests := []struct {
		name string
		path string
		want string
	}{
		{"status", "/status", testAPIURL + "/status"},
		{"operation", "/servers/test/operations/123", testAPIURL + "/servers/test/operations/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := api.baseURL + tt.path
			if got != tt.want {
				t.Errorf("URL = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTTPMethods(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"GET for status", http.MethodGet},
		{"POST for create", http.MethodPost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method {
					t.Errorf("Method = %v, want %v", r.Method, tt.method)
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{})
			}))
			defer server.Close()

			api := NewPgBackupAPI(server.URL, "", "")
			req, _ := http.NewRequest(tt.method, server.URL+"/test", nil)
			api.client.Do(req)
		})
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OperationStatus{Status: "IN_PROGRESS"})
	}))
	defer server.Close()

	api := NewPgBackupAPI(server.URL, "", "")
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := waitForOperation(ctx, api, testBarmanServer, "op-123",
		10*time.Millisecond, time.Second)

	if err == nil {
		t.Error("Expected error from cancelled context")
	}
}

func TestOperationTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OperationStatus{Status: "IN_PROGRESS"})
	}))
	defer server.Close()

	api := NewPgBackupAPI(server.URL, "", "")

	// Very short timeout
	_, err := waitForOperation(context.Background(), api, testBarmanServer, "op-123",
		10*time.Millisecond, 50*time.Millisecond)

	if err == nil {
		t.Error("Expected timeout error")
	}
}

func strPtr(s string) *string {
	return &s
}
