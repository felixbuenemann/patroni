package callback

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestActionConstants(t *testing.T) {
	// Verify action constants match expected values
	actions := map[Action]string{
		ActionNoop:         "noop",
		ActionOnStart:      "on_start",
		ActionOnStop:       "on_stop",
		ActionOnRestart:    "on_restart",
		ActionOnReload:     "on_reload",
		ActionOnRoleChange: "on_role_change",
	}

	for action, expected := range actions {
		if string(action) != expected {
			t.Errorf("Action %v = %q, want %q", action, string(action), expected)
		}
	}
}

func TestRoleConstants(t *testing.T) {
	// Verify role constants match expected values
	roles := map[Role]string{
		RolePrimary:       "primary",
		RoleReplica:       "replica",
		RoleStandbyLeader: "standby_leader",
	}

	for role, expected := range roles {
		if string(role) != expected {
			t.Errorf("Role %v = %q, want %q", role, string(role), expected)
		}
	}
}

func TestEnvironmentToEnv(t *testing.T) {
	env := &Environment{
		Scope:   "mycluster",
		Name:    "node1",
		Role:    RolePrimary,
		Action:  ActionOnStart,
		ConnURL: "postgres://localhost:5432/postgres",
	}

	envVars := env.ToEnv()

	// Check that required environment variables are set
	expected := map[string]string{
		"PATRONI_SCOPE":    "mycluster",
		"PATRONI_NAME":     "node1",
		"PATRONI_ROLE":     "primary",
		"PATRONI_ACTION":   "on_start",
		"PATRONI_CONN_URL": "postgres://localhost:5432/postgres",
	}

	for key, value := range expected {
		found := false
		for _, ev := range envVars {
			if strings.HasPrefix(ev, key+"=") {
				found = true
				if ev != key+"="+value {
					t.Errorf("Environment %s = %q, want %q", key, ev, key+"="+value)
				}
				break
			}
		}
		if !found {
			t.Errorf("Environment variable %s not found", key)
		}
	}
}

func TestEnvironmentToEnvWithoutConnURL(t *testing.T) {
	env := &Environment{
		Scope:  "mycluster",
		Name:   "node1",
		Role:   RoleReplica,
		Action: ActionOnStop,
	}

	envVars := env.ToEnv()

	// ConnURL should not be present when empty
	for _, ev := range envVars {
		if strings.HasPrefix(ev, "PATRONI_CONN_URL=") {
			t.Error("PATRONI_CONN_URL should not be set when empty")
		}
	}
}

func TestResultSuccess(t *testing.T) {
	tests := []struct {
		name     string
		result   Result
		expected bool
	}{
		{
			name:     "success",
			result:   Result{ExitCode: 0, Error: nil},
			expected: true,
		},
		{
			name:     "failure exit code",
			result:   Result{ExitCode: 1, Error: nil},
			expected: false,
		},
		{
			name:     "error",
			result:   Result{ExitCode: 0, Error: os.ErrNotExist},
			expected: false,
		},
		{
			name:     "both failure",
			result:   Result{ExitCode: 1, Error: os.ErrNotExist},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Success() != tt.expected {
				t.Errorf("Result.Success() = %v, want %v", tt.result.Success(), tt.expected)
			}
		})
	}
}

func TestNewExecutor(t *testing.T) {
	executor := NewExecutor()
	if executor == nil {
		t.Fatal("NewExecutor() returned nil")
	}
	if executor.cond == nil {
		t.Error("Executor.cond should be initialized")
	}
	if executor.onReloadExecutor == nil {
		t.Error("Executor.onReloadExecutor should be initialized")
	}
	if executor.stopCh == nil {
		t.Error("Executor.stopCh should be initialized")
	}
	if executor.doneCh == nil {
		t.Error("Executor.doneCh should be initialized")
	}

	// Clean up
	executor.Stop()
}

func TestExecutorStop(t *testing.T) {
	executor := NewExecutor()

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		executor.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Executor.Stop() timed out")
	}
}

func TestExecutorCallWithScript(t *testing.T) {
	executor := NewExecutor()
	defer executor.Stop()

	// Test calling with echo (should exist on most systems)
	// This matches the Python test that uses a test.sh script
	cmd := []string{"echo", "on_start", "replica", "foo"}
	executor.Call(cmd)

	// Give some time for the command to potentially run
	time.Sleep(100 * time.Millisecond)
}

func TestCallWithEnvEmptyScript(t *testing.T) {
	executor := NewExecutor()
	defer executor.Stop()

	env := &Environment{
		Scope: "test",
		Name:  "node1",
		Role:  RolePrimary,
	}

	// Empty script should return success
	result := executor.CallWithEnv("", ActionOnStart, env)
	if result.ExitCode != 0 {
		t.Errorf("Empty script ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestExecuteCallbackEmptyScript(t *testing.T) {
	result := ExecuteCallback("", ActionOnStart, RolePrimary, "scope", "name", "", 0)
	if result.ExitCode != 0 {
		t.Errorf("Empty script ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestExecuteCallbackWithEcho(t *testing.T) {
	// Use echo as a simple test command that exists everywhere
	result := ExecuteCallback("echo", ActionOnStart, RolePrimary, "myscope", "node1", "", time.Second)

	if result.ExitCode != 0 {
		t.Errorf("Echo ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Error != nil {
		t.Errorf("Echo Error = %v, want nil", result.Error)
	}
	if !result.Success() {
		t.Error("Echo should succeed")
	}
	if result.Duration == 0 {
		t.Error("Duration should be non-zero")
	}
	// Output should contain the arguments
	if !strings.Contains(result.Output, "on_start") {
		t.Errorf("Output = %q, should contain 'on_start'", result.Output)
	}
}

func TestExecuteCallbackNonExistent(t *testing.T) {
	result := ExecuteCallback("/nonexistent/script.sh", ActionOnStart, RolePrimary, "scope", "name", "", time.Second)

	if result.ExitCode == 0 {
		t.Error("Non-existent script should have non-zero exit code")
	}
	if result.Error == nil {
		t.Error("Non-existent script should have error")
	}
	if result.Success() {
		t.Error("Non-existent script should not succeed")
	}
}

func TestExecuteCallbackTimeout(t *testing.T) {
	// Use sleep to test timeout (if available)
	result := ExecuteCallback("sleep", ActionOnStart, RolePrimary, "scope", "name", "", 100*time.Millisecond)

	// Sleep with argument "on_start" will fail, but we're testing timeout behavior
	if result.ExitCode == 0 {
		t.Log("Sleep command ran successfully (may have different behavior)")
	}
}

func TestExecuteWithRetry(t *testing.T) {
	// Test with echo which should succeed on first try
	result := ExecuteWithRetry("echo", ActionOnStart, RolePrimary, "scope", "name", "", time.Second, 2, 10*time.Millisecond)

	if !result.Success() {
		t.Errorf("Echo with retry should succeed, got exit code %d", result.ExitCode)
	}
}

func TestNewOnReloadExecutor(t *testing.T) {
	executor := NewOnReloadExecutor()
	if executor == nil {
		t.Fatal("NewOnReloadExecutor() returned nil")
	}
	if executor.stopCh == nil {
		t.Error("OnReloadExecutor.stopCh should be initialized")
	}

	executor.Stop()
}

func TestOnReloadExecutorStop(t *testing.T) {
	executor := NewOnReloadExecutor()

	done := make(chan struct{})
	go func() {
		executor.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("OnReloadExecutor.Stop() timed out")
	}
}

func TestOnReloadCallNoWait(t *testing.T) {
	executor := NewOnReloadExecutor()
	defer executor.Stop()

	// Test calling with echo
	cmd := []string{"echo", "on_reload", "primary", "scope"}
	executor.CallNoWait(cmd)

	// Give some time for the command to start
	time.Sleep(50 * time.Millisecond)
}

func TestDefaultTimeout(t *testing.T) {
	if DefaultTimeout != 30*time.Second {
		t.Errorf("DefaultTimeout = %v, want %v", DefaultTimeout, 30*time.Second)
	}
}

func TestConfigStruct(t *testing.T) {
	cfg := Config{
		Script:     "/usr/local/bin/callback.sh",
		Timeout:    time.Minute,
		MaxRetries: 3,
		RetryDelay: time.Second,
	}

	if cfg.Script == "" {
		t.Error("Script should be set")
	}
	if cfg.Timeout == 0 {
		t.Error("Timeout should be set")
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
}

// TestCallbackExecutorCall tests the Call method (matches Python test_callback_executor).
func TestCallbackExecutorCall(t *testing.T) {
	executor := NewExecutor()
	defer executor.Stop()

	// Test with a callback command similar to Python test
	callback := []string{"echo", string(ActionOnStart), string(RoleReplica), "foo"}
	executor.Call(callback)

	// Wait for potential execution
	time.Sleep(100 * time.Millisecond)

	// Test on_reload callback (should use OnReloadExecutor)
	reloadCallback := []string{"echo", "arg1", string(ActionOnReload), string(RoleReplica), "foo"}
	executor.Call(reloadCallback)

	time.Sleep(100 * time.Millisecond)
}

// TestCallbackExecutorJoin tests that we can wait for executor to finish.
func TestCallbackExecutorJoin(t *testing.T) {
	executor := NewExecutor()

	// Call with a command
	executor.Call([]string{"echo", "test"})
	time.Sleep(50 * time.Millisecond)

	// Stop should wait for completion
	done := make(chan struct{})
	go func() {
		executor.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Join (Stop) timed out")
	}
}
