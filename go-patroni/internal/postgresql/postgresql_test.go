package postgresql

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/pkg/types"
)

func TestQuoteValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "'simple'"},
		{"", "''"},
		{"with'quote", "'with''quote'"},
		{"multiple''quotes", "'multiple''''quotes'"},
		{"123", "'123'"},
		{"hello world", "'hello world'"},
		{"path/to/file", "'path/to/file'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := quoteValue(tt.input); got != tt.expected {
				t.Errorf("quoteValue(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDataDirectoryExists(t *testing.T) {
	// Create a temporary directory with PG_VERSION file
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pg := &Postgresql{
		dataDir: tmpDir,
	}

	// Without PG_VERSION, directory should not be considered existing
	if pg.dataDirectoryExists() {
		t.Error("dataDirectoryExists() should return false without PG_VERSION file")
	}

	// Create PG_VERSION file
	pgVersionFile := filepath.Join(tmpDir, "PG_VERSION")
	if err := os.WriteFile(pgVersionFile, []byte("15"), 0644); err != nil {
		t.Fatalf("Failed to create PG_VERSION file: %v", err)
	}

	// Now it should return true
	if !pg.dataDirectoryExists() {
		t.Error("dataDirectoryExists() should return true with PG_VERSION file")
	}

	pg.dataDir = "/nonexistent/path"
	if pg.dataDirectoryExists() {
		t.Error("dataDirectoryExists() should return false for non-existing directory")
	}
}

func TestDataDirectoryEmpty(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pg := &Postgresql{
		dataDir: tmpDir,
	}

	// Empty directory
	if !pg.dataDirectoryEmpty() {
		t.Error("dataDirectoryEmpty() should return true for empty directory")
	}

	// Create lost+found (should be ignored)
	lostFound := filepath.Join(tmpDir, "lost+found")
	if err := os.Mkdir(lostFound, 0755); err != nil {
		t.Fatalf("Failed to create lost+found: %v", err)
	}
	if !pg.dataDirectoryEmpty() {
		t.Error("dataDirectoryEmpty() should ignore lost+found")
	}

	// Create a file in the directory
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if pg.dataDirectoryEmpty() {
		t.Error("dataDirectoryEmpty() should return false for non-empty directory")
	}

	// Test with pg_control file
	tmpDir2, _ := os.MkdirTemp("", "pgdata2")
	defer os.RemoveAll(tmpDir2)
	globalDir := filepath.Join(tmpDir2, "global")
	os.MkdirAll(globalDir, 0755)
	pgControl := filepath.Join(globalDir, "pg_control")
	os.WriteFile(pgControl, []byte("control"), 0644)

	pg2 := &Postgresql{dataDir: tmpDir2}
	if pg2.dataDirectoryEmpty() {
		t.Error("dataDirectoryEmpty() should return false when pg_control exists")
	}
}

func TestPgCommand(t *testing.T) {
	tests := []struct {
		binDir   string
		cmd      string
		expected string
	}{
		{"/usr/lib/postgresql/14/bin", "pg_ctl", "/usr/lib/postgresql/14/bin/pg_ctl"},
		{"", "pg_ctl", "pg_ctl"},
		{"/usr/bin", "postgres", "/usr/bin/postgres"},
		{"/opt/pgsql/bin", "pg_isready", "/opt/pgsql/bin/pg_isready"},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			pg := &Postgresql{binDir: tt.binDir}
			if got := pg.pgCommand(tt.cmd); got != tt.expected {
				t.Errorf("pgCommand(%q) = %q, want %q", tt.cmd, got, tt.expected)
			}
		})
	}
}

func TestPgCommandWithCustomBinName(t *testing.T) {
	pg := &Postgresql{
		binDir: "/usr/bin",
		config: &config.Config{
			PostgreSQL: config.PostgreSQLConfig{
				BinName: map[string]string{
					"pg_ctl": "pg_ctl14",
				},
			},
		},
	}

	// With custom bin name
	if got := pg.pgCommand("pg_ctl"); got != "/usr/bin/pg_ctl14" {
		t.Errorf("pgCommand(pg_ctl) = %q, want /usr/bin/pg_ctl14", got)
	}

	// Without custom bin name
	if got := pg.pgCommand("pg_isready"); got != "/usr/bin/pg_isready" {
		t.Errorf("pgCommand(pg_isready) = %q, want /usr/bin/pg_isready", got)
	}
}

func TestStopModeValues(t *testing.T) {
	tests := []struct {
		mode     StopMode
		expected string
	}{
		{StopModeSmart, "smart"},
		{StopModeFast, "fast"},
		{StopModeImmediate, "immediate"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := string(tt.mode); got != tt.expected {
				t.Errorf("StopMode = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSupportsTimelines(t *testing.T) {
	tests := []struct {
		majorVersion int
		expected     bool
	}{
		{90200, false},
		{90300, true},
		{100000, true},
		{140000, true},
		{150000, true},
		{0, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			pg := &Postgresql{majorVersion: tt.majorVersion}
			if got := pg.SupportsTimelines(); got != tt.expected {
				t.Errorf("SupportsTimelines() with version %d = %v, want %v", tt.majorVersion, got, tt.expected)
			}
		})
	}
}

func TestGetVersion(t *testing.T) {
	pg := &Postgresql{majorVersion: 140000}

	if got := pg.GetVersion(); got != 140000 {
		t.Errorf("GetVersion() = %d, want 140000", got)
	}
}

func TestMajorVersion(t *testing.T) {
	pg := &Postgresql{majorVersion: 150000}

	if got := pg.MajorVersion(); got != 150000 {
		t.Errorf("MajorVersion() = %d, want 150000", got)
	}
}

func TestName(t *testing.T) {
	pg := &Postgresql{name: "node1"}

	if got := pg.Name(); got != "node1" {
		t.Errorf("Name() = %q, want %q", got, "node1")
	}
}

func TestScope(t *testing.T) {
	pg := &Postgresql{scope: "mycluster"}

	if got := pg.Scope(); got != "mycluster" {
		t.Errorf("Scope() = %q, want %q", got, "mycluster")
	}
}

func TestDataDir(t *testing.T) {
	pg := &Postgresql{dataDir: "/var/lib/postgresql/data"}

	if got := pg.DataDir(); got != "/var/lib/postgresql/data" {
		t.Errorf("DataDir() = %q, want %q", got, "/var/lib/postgresql/data")
	}
}

func TestSysID(t *testing.T) {
	pg := &Postgresql{sysID: "7123456789012345678"}

	if got := pg.SysID(); got != "7123456789012345678" {
		t.Errorf("SysID() = %q, want %q", got, "7123456789012345678")
	}
}

func TestStateSetAndGet(t *testing.T) {
	pg := &Postgresql{}

	testCases := []types.PostgresqlState{
		types.PostgresqlStateStopped,
		types.PostgresqlStateStarting,
		types.PostgresqlStateRunning,
		types.PostgresqlStateStopping,
		types.PostgresqlStateRestarting,
	}

	for _, state := range testCases {
		pg.setState(state)
		if got := pg.State(); got != state {
			t.Errorf("State() = %v, want %v", got, state)
		}
	}
}

func TestRoleSetAndGet(t *testing.T) {
	pg := &Postgresql{}

	testCases := []types.PostgresqlRole{
		types.PostgresqlRoleUninitialized,
		types.PostgresqlRolePrimary,
		types.PostgresqlRoleReplica,
		types.PostgresqlRolePromoted,
		types.PostgresqlRoleDemoted,
	}

	for _, role := range testCases {
		pg.setRole(role)
		if got := pg.Role(); got != role {
			t.Errorf("Role() = %v, want %v", got, role)
		}
	}
}

func TestGetMajorVersion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pg := &Postgresql{dataDir: tmpDir}

	// Test various PG_VERSION formats
	tests := []struct {
		version  string
		expected int
	}{
		{"15", 150000},
		{"14", 140000},
		{"13", 130000},
		{"12", 120000},
		{"11", 110000},
		{"10", 100000},
		{"9.6", 90600},
		{"9.5", 90500},
		{"9.4", 90400},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			pgVersionFile := filepath.Join(tmpDir, "PG_VERSION")
			if err := os.WriteFile(pgVersionFile, []byte(tt.version), 0644); err != nil {
				t.Fatalf("Failed to create PG_VERSION file: %v", err)
			}

			if got := pg.getMajorVersion(); got != tt.expected {
				t.Errorf("getMajorVersion() = %d, want %d", got, tt.expected)
			}
		})
	}

	// Test with invalid PG_VERSION
	os.WriteFile(filepath.Join(tmpDir, "PG_VERSION"), []byte("invalid"), 0644)
	if got := pg.getMajorVersion(); got != 0 {
		t.Errorf("getMajorVersion() with invalid version = %d, want 0", got)
	}

	// Test without PG_VERSION file
	os.Remove(filepath.Join(tmpDir, "PG_VERSION"))
	if got := pg.getMajorVersion(); got != 0 {
		t.Errorf("getMajorVersion() without file = %d, want 0", got)
	}
}

func TestGetRoleFromDataDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pg := &Postgresql{dataDir: tmpDir}

	// Empty directory = uninitialized
	if got := pg.getRoleFromDataDirectory(); got != types.PostgresqlRoleUninitialized {
		t.Errorf("getRoleFromDataDirectory() empty = %v, want Uninitialized", got)
	}

	// Create a file to make it non-empty
	testFile := filepath.Join(tmpDir, "test.conf")
	os.WriteFile(testFile, []byte("test"), 0644)

	// Without standby.signal or recovery.conf = primary
	if got := pg.getRoleFromDataDirectory(); got != types.PostgresqlRolePrimary {
		t.Errorf("getRoleFromDataDirectory() without standby signal = %v, want Primary", got)
	}

	// With standby.signal = replica
	standbySignal := filepath.Join(tmpDir, "standby.signal")
	os.WriteFile(standbySignal, []byte{}, 0644)
	if got := pg.getRoleFromDataDirectory(); got != types.PostgresqlRoleReplica {
		t.Errorf("getRoleFromDataDirectory() with standby.signal = %v, want Replica", got)
	}
	os.Remove(standbySignal)

	// With recovery.conf = replica
	recoveryConf := filepath.Join(tmpDir, "recovery.conf")
	os.WriteFile(recoveryConf, []byte("standby_mode = 'on'"), 0644)
	if got := pg.getRoleFromDataDirectory(); got != types.PostgresqlRoleReplica {
		t.Errorf("getRoleFromDataDirectory() with recovery.conf = %v, want Replica", got)
	}
}

func TestGetHostAndPort(t *testing.T) {
	tests := []struct {
		listen       string
		expectedHost string
		expectedPort string
	}{
		{"localhost:5432", "localhost", "5432"},
		{"*:5433", "localhost", "5433"},
		{"0.0.0.0:5434", "localhost", "5434"},
		{"192.168.1.1:5435", "192.168.1.1", "5435"},
		{"", "", "5432"},
		{"127.0.0.1", "127.0.0.1", "5432"},
	}

	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			pg := &Postgresql{
				config: &config.Config{
					PostgreSQL: config.PostgreSQLConfig{
						Listen: tt.listen,
					},
				},
			}

			if got := pg.getHost(); got != tt.expectedHost {
				t.Errorf("getHost() with listen %q = %q, want %q", tt.listen, got, tt.expectedHost)
			}

			if got := pg.getPort(); got != tt.expectedPort {
				t.Errorf("getPort() with listen %q = %q, want %q", tt.listen, got, tt.expectedPort)
			}
		})
	}
}

func TestBuildConnectionString(t *testing.T) {
	pg := &Postgresql{
		database: "postgres",
		config: &config.Config{
			PostgreSQL: config.PostgreSQLConfig{
				Listen: "localhost:5432",
				Authentication: config.AuthConfig{
					Superuser: config.UserAuth{
						Username: "superuser",
						Password: "secret",
						SSLMode:  "require",
					},
				},
			},
		},
	}

	connStr := pg.buildConnectionString()

	// Check that connection string contains expected parts
	expectedParts := []string{
		"host=localhost",
		"port=5432",
		"user=superuser",
		"dbname=postgres",
		"password=secret",
		"sslmode=require",
		"application_name=Patroni",
	}

	for _, part := range expectedParts {
		if !contains(connStr, part) {
			t.Errorf("buildConnectionString() = %q, missing %q", connStr, part)
		}
	}
}

func TestBuildConnectionStringDefaults(t *testing.T) {
	pg := &Postgresql{
		database: "postgres",
		config: &config.Config{
			PostgreSQL: config.PostgreSQLConfig{
				Listen: "localhost:5432",
				Authentication: config.AuthConfig{
					Superuser: config.UserAuth{
						Username: "", // Should default to postgres
					},
				},
			},
		},
	}

	connStr := pg.buildConnectionString()

	if !contains(connStr, "user=postgres") {
		t.Errorf("buildConnectionString() should default username to postgres, got %q", connStr)
	}
}

func TestIsRunningNotRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pg := &Postgresql{dataDir: tmpDir}

	// No pid file = not running
	if pg.IsRunning() {
		t.Error("IsRunning() should return false when pid file doesn't exist")
	}

	// Invalid pid file
	pidFile := filepath.Join(tmpDir, "postmaster.pid")
	os.WriteFile(pidFile, []byte("invalid"), 0644)
	if pg.IsRunning() {
		t.Error("IsRunning() should return false with invalid pid file")
	}

	// Empty pid file
	os.WriteFile(pidFile, []byte(""), 0644)
	if pg.IsRunning() {
		t.Error("IsRunning() should return false with empty pid file")
	}

	// Non-existent PID
	os.WriteFile(pidFile, []byte("999999999\n"), 0644)
	if pg.IsRunning() {
		t.Error("IsRunning() should return false with non-existent PID")
	}
}

func TestPendingRestart(t *testing.T) {
	pg := &Postgresql{
		pendingRestartReason: make(map[string]interface{}),
	}

	// Initially not pending
	if pg.IsPendingRestart() {
		t.Error("IsPendingRestart() should return false initially")
	}

	// Set pending restart
	pg.SetPendingRestart("max_connections", "100", "200")

	if !pg.IsPendingRestart() {
		t.Error("IsPendingRestart() should return true after SetPendingRestart")
	}

	reason := pg.PendingRestartReason()
	if reason == nil {
		t.Error("PendingRestartReason() should not be nil")
	}

	if _, ok := reason["max_connections"]; !ok {
		t.Error("PendingRestartReason() should contain max_connections")
	}
}

func TestCheckDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgtest")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	newDataDir := filepath.Join(tmpDir, "data", "subdir")
	pg := &Postgresql{dataDir: newDataDir}

	if err := pg.checkDirectories(); err != nil {
		t.Errorf("checkDirectories() error = %v", err)
	}

	// Check that directory was created
	if _, err := os.Stat(newDataDir); os.IsNotExist(err) {
		t.Error("checkDirectories() should create data directory")
	}
}

func TestClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	pg := &Postgresql{
		cancelFunc: cancel,
	}

	if err := pg.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Verify context was cancelled
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("Close() should cancel context")
	}
}

func TestCloseConnections(t *testing.T) {
	pg := &Postgresql{}

	// Should not panic with nil pool
	pg.CloseConnections()
}

func TestConcurrentStateAccess(t *testing.T) {
	pg := &Postgresql{}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			pg.setState(types.PostgresqlStateRunning)
		}()

		go func() {
			defer wg.Done()
			_ = pg.State()
		}()
	}

	wg.Wait()
}

func TestConcurrentRoleAccess(t *testing.T) {
	pg := &Postgresql{}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			pg.setRole(types.PostgresqlRolePrimary)
		}()

		go func() {
			defer wg.Done()
			_ = pg.Role()
		}()
	}

	wg.Wait()
}

func TestControldata(t *testing.T) {
	// Test with non-existent data directory
	pg := &Postgresql{
		dataDir: "/nonexistent/path",
	}

	result := pg.controldata()
	if result != nil {
		t.Error("controldata() should return nil for non-existent directory")
	}

	// Test with directory but no PG_VERSION
	tmpDir, _ := os.MkdirTemp("", "pgdata")
	defer os.RemoveAll(tmpDir)

	pg2 := &Postgresql{dataDir: tmpDir}
	result2 := pg2.controldata()
	if result2 != nil {
		t.Error("controldata() should return nil when dataDirectoryExists returns false")
	}
}

func TestGetControlData(t *testing.T) {
	pg := &Postgresql{
		dataDir: "/nonexistent",
	}

	data, err := pg.GetControlData()
	if err != nil {
		t.Errorf("GetControlData() unexpected error = %v", err)
	}

	if data != nil {
		t.Error("GetControlData() should return nil for non-existent directory")
	}
}

func TestErrorRow(t *testing.T) {
	expectedErr := context.DeadlineExceeded
	row := &errorRow{err: expectedErr}

	var result int
	err := row.Scan(&result)
	if err != expectedErr {
		t.Errorf("errorRow.Scan() = %v, want %v", err, expectedErr)
	}
}

func TestGetMemberData(t *testing.T) {
	pg := &Postgresql{
		state: types.PostgresqlStateStopped,
		role:  types.PostgresqlRolePrimary,
		config: &config.Config{
			Name:  "node1",
			Scope: "cluster1",
			Tags: map[string]interface{}{
				"nofailover":    true,
				"noloadbalance": false,
				"clonefrom":     true,
			},
		},
	}

	data := pg.GetMemberData()

	if data == nil {
		t.Fatal("GetMemberData() returned nil")
	}

	if data.State != "stopped" {
		t.Errorf("GetMemberData().State = %q, want %q", data.State, "stopped")
	}

	if data.Role != "primary" {
		t.Errorf("GetMemberData().Role = %q, want %q", data.Role, "primary")
	}

	if data.Version != types.Version {
		t.Errorf("GetMemberData().Version = %q, want %q", data.Version, types.Version)
	}

	// Check tags
	if !data.Tags.NoFailover {
		t.Error("GetMemberData().Tags.NoFailover should be true")
	}

	if data.Tags.NoLoadbalance {
		t.Error("GetMemberData().Tags.NoLoadbalance should be false")
	}

	if !data.Tags.CloneFrom {
		t.Error("GetMemberData().Tags.CloneFrom should be true")
	}
}

func TestConfigureReplica(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test for PG 12+
	pg := &Postgresql{
		dataDir:      tmpDir,
		majorVersion: 120000,
	}

	primaryConnInfo := "host=primary port=5432 user=replicator"
	if err := pg.configureReplica(primaryConnInfo); err != nil {
		t.Errorf("configureReplica() error = %v", err)
	}

	// Check standby.signal was created
	standbySignal := filepath.Join(tmpDir, "standby.signal")
	if _, err := os.Stat(standbySignal); os.IsNotExist(err) {
		t.Error("configureReplica() should create standby.signal for PG 12+")
	}

	// Check postgresql.auto.conf was updated
	autoConf := filepath.Join(tmpDir, "postgresql.auto.conf")
	content, err := os.ReadFile(autoConf)
	if err != nil {
		t.Errorf("Failed to read postgresql.auto.conf: %v", err)
	}

	if !contains(string(content), primaryConnInfo) {
		t.Errorf("postgresql.auto.conf should contain primary_conninfo")
	}
}

func TestConfigureReplicaOldVersion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pgdata")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test for PG < 12
	pg := &Postgresql{
		dataDir:      tmpDir,
		majorVersion: 110000,
	}

	primaryConnInfo := "host=primary port=5432 user=replicator"
	if err := pg.configureReplica(primaryConnInfo); err != nil {
		t.Errorf("configureReplica() error = %v", err)
	}

	// standby.signal should NOT be created for old versions
	standbySignal := filepath.Join(tmpDir, "standby.signal")
	if _, err := os.Stat(standbySignal); !os.IsNotExist(err) {
		t.Error("configureReplica() should NOT create standby.signal for PG < 12")
	}
}

func TestStateEntryTime(t *testing.T) {
	pg := &Postgresql{}

	before := time.Now()
	pg.setState(types.PostgresqlStateRunning)
	after := time.Now()

	pg.mu.RLock()
	entryTime := pg.stateEntryTime
	pg.mu.RUnlock()

	if entryTime.Before(before) || entryTime.After(after) {
		t.Errorf("stateEntryTime should be between %v and %v, got %v", before, after, entryTime)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestIsPrimaryWithRole tests the IsPrimary method
func TestIsPrimaryWithRole(t *testing.T) {
	tests := []struct {
		name     string
		role     types.PostgresqlRole
		expected bool
	}{
		{"Primary role", types.PostgresqlRolePrimary, true},
		{"Promoted role", types.PostgresqlRolePromoted, true},
		{"Replica role", types.PostgresqlRoleReplica, false},
		{"Demoted role", types.PostgresqlRoleDemoted, false},
		{"Uninitialized role", types.PostgresqlRoleUninitialized, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &Postgresql{
				role:    tt.role,
				dataDir: "/nonexistent", // Ensure IsRunning returns false
			}

			// When not running, IsPrimary checks the role
			got := pg.IsPrimary()

			// For Primary and Promoted roles, IsPrimary returns true immediately
			// For other roles, it tries to connect which fails, so returns false
			if tt.role == types.PostgresqlRolePrimary || tt.role == types.PostgresqlRolePromoted {
				if !got {
					t.Errorf("IsPrimary() = %v, want %v for role %v", got, tt.expected, tt.role)
				}
			}
		})
	}
}

// TestLifecycleMethodsWithNilConfig tests that lifecycle methods handle nil config gracefully
func TestLifecycleMethodsWithNilConfig(t *testing.T) {
	pg := &Postgresql{
		dataDir: "/nonexistent",
	}

	// Reload should fail when not running
	ctx := context.Background()
	err := pg.Reload(ctx)
	if err == nil {
		t.Error("Reload() should return error when not running")
	}
}

func TestKillWithNilPostmaster(t *testing.T) {
	pg := &Postgresql{
		dataDir: "/nonexistent",
	}

	// Kill should return nil when postmaster is not running
	err := pg.Kill(0)
	if err != nil {
		t.Errorf("Kill() = %v, want nil when not running", err)
	}
}
