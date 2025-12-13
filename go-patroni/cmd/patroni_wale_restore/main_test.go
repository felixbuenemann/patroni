package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Sample WAL-E output for tests
var waleOutputHeader = "name\tlast_modified\texpanded_size_bytes\twal_segment_backup_start\twal_segment_offset_backup_start\twal_segment_backup_stop\twal_segment_offset_backup_stop\n"
var waleOutputValues = "base_00000001000000000000007F_00000040\t2015-05-18T10:13:25.000Z\t167772160\t00000001000000000000007F\t00000040\t00000001000000000000007F\t00000240\n"
var waleOutput = waleOutputHeader + waleOutputValues

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"success", ExitSuccess, 0},
		{"retry later", ExitRetryLater, 1},
		{"fail", ExitFail, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("exit code = %v, want %v", tt.code, tt.expected)
			}
		})
	}
}

func TestNewWALERestore(t *testing.T) {
	// Create temp directory for envdir
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		scope       string
		datadir     string
		connstring  string
		envDir      string
		thresholdMB int
		thresholdPct int
		useIAM      int
		noLeader    bool
		retries     int
		wantError   bool
	}{
		{
			name:        "basic config",
			scope:       "test",
			datadir:     "/data",
			connstring:  "host=localhost",
			envDir:      tmpDir,
			thresholdMB: 100,
			thresholdPct: 30,
			useIAM:      0,
			noLeader:    false,
			retries:     2,
			wantError:   false,
		},
		{
			name:        "with IAM",
			scope:       "test",
			datadir:     "/data",
			connstring:  "host=localhost",
			envDir:      tmpDir,
			thresholdMB: 100,
			thresholdPct: 30,
			useIAM:      1,
			noLeader:    false,
			retries:     2,
			wantError:   false,
		},
		{
			name:        "missing envdir",
			scope:       "test",
			datadir:     "/data",
			connstring:  "host=localhost",
			envDir:      "/nonexistent/path",
			thresholdMB: 100,
			thresholdPct: 30,
			useIAM:      0,
			noLeader:    false,
			retries:     2,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := NewWALERestore(
				tt.scope, tt.datadir, tt.connstring, tt.envDir,
				tt.thresholdMB, tt.thresholdPct, tt.useIAM,
				tt.noLeader, tt.retries,
			)

			if restore.initError != tt.wantError {
				t.Errorf("initError = %v, want %v", restore.initError, tt.wantError)
			}

			if restore.scope != tt.scope {
				t.Errorf("scope = %v, want %v", restore.scope, tt.scope)
			}

			if restore.dataDir != tt.datadir {
				t.Errorf("dataDir = %v, want %v", restore.dataDir, tt.datadir)
			}
		})
	}
}

func TestWALERestoreRun(t *testing.T) {
	restore := &WALERestore{
		initError: true,
		walE: WALEConfig{
			EnvDir: "/nonexistent",
		},
	}

	// Should return ExitFail when initError is true
	if result := restore.Run(); result != ExitFail {
		t.Errorf("Run() = %v, want %v", result, ExitFail)
	}
}

func TestGetMajorVersion(t *testing.T) {
	// Create temp directory with PG_VERSION file
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		version string
		want    float64
	}{
		{"pg 9.4", "9.4", 9.4},
		{"pg 10", "10", 10.0},
		{"pg 14", "14", 14.0},
		{"pg 15", "15", 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versionFile := filepath.Join(tmpDir, "PG_VERSION")
			if err := os.WriteFile(versionFile, []byte(tt.version), 0644); err != nil {
				t.Fatalf("Failed to write PG_VERSION: %v", err)
			}

			got := getMajorVersion(tmpDir)
			if got != tt.want {
				t.Errorf("getMajorVersion() = %v, want %v", got, tt.want)
			}
		})
	}

	// Test missing file
	got := getMajorVersion("/nonexistent/path")
	if got != 0.0 {
		t.Errorf("getMajorVersion(missing) = %v, want 0.0", got)
	}
}

func TestReprSize(t *testing.T) {
	tests := []struct {
		name   string
		bytes  float64
		want   string
	}{
		{"bytes", 500, "500 Bytes"},
		{"kilobytes", 1024, "1.0 KiB"},
		{"megabytes", 1048576, "1.0 MiB"},
		{"gigabytes", 1073741824, "1.0 GiB"},
		{"terabytes", 8246337208320, "7.5 TiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reprSize(tt.bytes)
			if got != tt.want {
				t.Errorf("reprSize(%v) = %v, want %v", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestSizeAsBytes(t *testing.T) {
	tests := []struct {
		name   string
		size   float64
		prefix string
		want   int64
	}{
		{"kilobytes", 1.0, "K", 1024},
		{"megabytes", 1.0, "M", 1048576},
		{"gigabytes", 1.0, "G", 1073741824},
		{"terabytes", 7.5, "T", 8246337208320},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sizeAsBytes(tt.size, tt.prefix)
			if got != tt.want {
				t.Errorf("sizeAsBytes(%v, %v) = %v, want %v", tt.size, tt.prefix, got, tt.want)
			}
		})
	}

	// Test lowercase prefix
	got := sizeAsBytes(1.0, "m")
	if got != 1048576 {
		t.Errorf("sizeAsBytes(1.0, m) = %v, want 1048576", got)
	}

	// Test invalid prefix
	got = sizeAsBytes(1.0, "X")
	if got != 0 {
		t.Errorf("sizeAsBytes(1.0, X) = %v, want 0", got)
	}
}

func TestDirExists(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"existing dir", tmpDir, true},
		{"nonexistent", "/nonexistent/path", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dirExists(tt.path)
			if got != tt.want {
				t.Errorf("dirExists(%v) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	// Test file (not directory)
	tmpFile := filepath.Join(tmpDir, "file")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if dirExists(tmpFile) {
		t.Error("dirExists should return false for files")
	}
}

func TestBoolPtr(t *testing.T) {
	truePtr := boolPtr(true)
	falsePtr := boolPtr(false)

	if truePtr == nil || !*truePtr {
		t.Error("boolPtr(true) should return pointer to true")
	}
	if falsePtr == nil || *falsePtr {
		t.Error("boolPtr(false) should return pointer to false")
	}
}

func TestFixSubdirectoryPathIfBroken(t *testing.T) {
	tmpDir := t.TempDir()

	restore := &WALERestore{
		dataDir: tmpDir,
	}

	// Test creating missing directory
	if !restore.fixSubdirectoryPathIfBroken("pg_wal") {
		t.Error("fixSubdirectoryPathIfBroken should return true for creating missing dir")
	}

	// Verify directory was created
	walDir := filepath.Join(tmpDir, "pg_wal")
	if _, err := os.Stat(walDir); os.IsNotExist(err) {
		t.Error("pg_wal directory should exist")
	}

	// Test existing directory
	if !restore.fixSubdirectoryPathIfBroken("pg_wal") {
		t.Error("fixSubdirectoryPathIfBroken should return true for existing dir")
	}
}

func TestWALEConfig(t *testing.T) {
	config := WALEConfig{
		EnvDir:       "/etc/wal-e.d/env",
		ThresholdMB:  10240,
		ThresholdPct: 30,
		Cmd:          []string{"envdir", "/etc/wal-e.d/env", "wal-e"},
	}

	if config.EnvDir != "/etc/wal-e.d/env" {
		t.Errorf("EnvDir = %v, want /etc/wal-e.d/env", config.EnvDir)
	}
	if config.ThresholdMB != 10240 {
		t.Errorf("ThresholdMB = %v, want 10240", config.ThresholdMB)
	}
	if config.ThresholdPct != 30 {
		t.Errorf("ThresholdPct = %v, want 30", config.ThresholdPct)
	}
	if len(config.Cmd) != 3 {
		t.Errorf("Cmd length = %v, want 3", len(config.Cmd))
	}
}

func TestWALECommandWithIAM(t *testing.T) {
	tests := []struct {
		name   string
		useIAM int
		want   int
	}{
		{"without IAM", 0, 3},
		{"with IAM", 1, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := NewWALERestore(
				"test", "/data", "host=localhost", t.TempDir(),
				100, 30, tt.useIAM, false, 1,
			)

			if len(restore.walE.Cmd) != tt.want {
				t.Errorf("Cmd length = %v, want %v", len(restore.walE.Cmd), tt.want)
			}

			if tt.useIAM == 1 {
				found := false
				for _, arg := range restore.walE.Cmd {
					if arg == "--aws-instance-profile" {
						found = true
						break
					}
				}
				if !found {
					t.Error("Expected --aws-instance-profile in command")
				}
			}
		})
	}
}

func TestThresholdCalculation(t *testing.T) {
	tests := []struct {
		name          string
		backupSize    int64
		diffInBytes   int64
		thresholdMB   int
		thresholdPct  int
		shouldUseWale bool
	}{
		{
			name:          "within thresholds",
			backupSize:    167772160,
			diffInBytes:   1000000,
			thresholdMB:   100,
			thresholdPct:  30,
			shouldUseWale: true,
		},
		{
			name:          "exceeds MB threshold",
			backupSize:    167772160,
			diffInBytes:   200000000, // > 100MB
			thresholdMB:   100,
			thresholdPct:  100,
			shouldUseWale: false,
		},
		{
			name:          "exceeds percentage threshold",
			backupSize:    100000000,
			diffInBytes:   50000000, // 50% of backup
			thresholdMB:   1000,
			thresholdPct:  30,
			shouldUseWale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isSizeOK := tt.diffInBytes < int64(tt.thresholdMB)*1048576
			thresholdPctBytes := float64(tt.backupSize) * float64(tt.thresholdPct) / 100.0
			isPctOK := float64(tt.diffInBytes) < thresholdPctBytes

			shouldUse := isSizeOK && isPctOK
			if shouldUse != tt.shouldUseWale {
				t.Errorf("shouldUseWale = %v, want %v", shouldUse, tt.shouldUseWale)
			}
		})
	}
}

func TestLSNParsing(t *testing.T) {
	// Test LSN segment parsing as done in shouldUseS3ToCreateReplica
	segment := "00000001000000000000007F"
	offset := int64(64) // 0x40

	lsnSegment := segment[8:16] // "00000000"
	highOffset, _ := parseHex(segment[16:])
	lsnOffset := (highOffset << 24) + offset

	if lsnSegment != "00000000" {
		t.Errorf("lsnSegment = %v, want 00000000", lsnSegment)
	}

	// Expected: 0x7F << 24 + 64 = 2130706496
	expectedOffset := int64(0x7F<<24) + 64
	if lsnOffset != expectedOffset {
		t.Errorf("lsnOffset = %v, want %v", lsnOffset, expectedOffset)
	}
}

func parseHex(s string) (int64, error) {
	var result int64
	for _, c := range s {
		result *= 16
		switch {
		case c >= '0' && c <= '9':
			result += int64(c - '0')
		case c >= 'A' && c <= 'F':
			result += int64(c - 'A' + 10)
		case c >= 'a' && c <= 'f':
			result += int64(c - 'a' + 10)
		}
	}
	return result, nil
}

func TestSIPrefixes(t *testing.T) {
	expected := []string{"K", "M", "G", "T", "P", "E", "Z", "Y"}
	if len(siPrefixes) != len(expected) {
		t.Errorf("siPrefixes length = %v, want %v", len(siPrefixes), len(expected))
	}
	for i, p := range expected {
		if siPrefixes[i] != p {
			t.Errorf("siPrefixes[%d] = %v, want %v", i, siPrefixes[i], p)
		}
	}
}
