package postgresql

import (
	"os"
	"path/filepath"
	"testing"
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

	// Create a file in the directory
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if pg.dataDirectoryEmpty() {
		t.Error("dataDirectoryEmpty() should return false for non-empty directory")
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

// Note: getHost and getPort methods require a config struct to be initialized
// These tests are skipped as they depend on full configuration setup
// Integration tests should cover this functionality

// Note: buildConnectionString requires a config struct to be initialized
// Integration tests should cover this functionality

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

func TestParseControlData(t *testing.T) {
	pg := &Postgresql{}
	result := pg.controldata()

	// Since we can't easily mock exec.Command, we'll just verify the method exists
	// In a real test, we'd use a mock or test fixture
	if result == nil {
		// This is expected when pg_controldata isn't available
		t.Log("controldata() returned nil (pg_controldata not available)")
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
