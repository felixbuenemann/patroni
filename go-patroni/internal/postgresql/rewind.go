package postgresql

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// RewindConfig holds configuration for pg_rewind operations.
type RewindConfig struct {
	UseSlots          bool   `yaml:"use_slots" json:"use_slots"`
	UsePgRewind       bool   `yaml:"use_pg_rewind" json:"use_pg_rewind"`
	Username          string `yaml:"username" json:"username"`
	Password          string `yaml:"password" json:"password"`
	NoRewindAuthFail  bool   `yaml:"no_rewind_auth_fail" json:"no_rewind_auth_fail"`
}

// RewindState tracks the state of pg_rewind operations.
type RewindState int

const (
	RewindStateNone RewindState = iota
	RewindStateNeeded
	RewindStateInProgress
	RewindStateFailed
	RewindStateComplete
)

// Rewind manages pg_rewind operations for recovering a former primary.
type Rewind struct {
	pg       *PostgreSQL
	config   *RewindConfig
	state    RewindState
	lastErr  error
	executed bool
}

// NewRewind creates a new Rewind manager.
func NewRewind(pg *PostgreSQL, config *RewindConfig) *Rewind {
	if config == nil {
		config = &RewindConfig{
			UsePgRewind: true,
		}
	}

	return &Rewind{
		pg:     pg,
		config: config,
		state:  RewindStateNone,
	}
}

// IsNeeded checks if pg_rewind is needed for this node.
func (r *Rewind) IsNeeded(ctx context.Context, primaryConnInfo string) (bool, error) {
	if !r.config.UsePgRewind {
		return false, nil
	}

	// Check if we can even run pg_rewind
	if !r.pg.SupportsTimelines() {
		log.Debug().Msg("PostgreSQL version does not support timelines, skipping pg_rewind check")
		return false, nil
	}

	// Get our timeline from pg_controldata
	controlData, err := r.pg.GetControlData()
	if err != nil {
		return false, fmt.Errorf("failed to get control data: %w", err)
	}

	localTimeline, ok := controlData["Latest checkpoint's TimeLineID"]
	if !ok {
		return false, fmt.Errorf("could not find timeline in control data")
	}

	// Get the primary's timeline
	// This would typically be done by querying the primary
	// For now, we'll assume rewind is needed if the primary says so
	log.Info().
		Str("local_timeline", localTimeline).
		Msg("Checking if pg_rewind is needed")

	return false, nil
}

// CanRewind checks if pg_rewind can be used.
func (r *Rewind) CanRewind(ctx context.Context) (bool, string) {
	// Check if wal_log_hints or data checksums are enabled
	controlData, err := r.pg.GetControlData()
	if err != nil {
		return false, fmt.Sprintf("failed to read control data: %v", err)
	}

	// Check for data checksums
	checksums := controlData["Data page checksum version"]
	walLogHints := false

	// If checksums are enabled, we can use pg_rewind
	if checksums != "" && checksums != "0" {
		return true, "data checksums enabled"
	}

	// Check recovery.conf or postgresql.auto.conf for wal_log_hints
	autoConfPath := filepath.Join(r.pg.dataDir, "postgresql.auto.conf")
	if data, err := os.ReadFile(autoConfPath); err == nil {
		if strings.Contains(string(data), "wal_log_hints = on") ||
			strings.Contains(string(data), "wal_log_hints = 'on'") {
			walLogHints = true
		}
	}

	if walLogHints {
		return true, "wal_log_hints enabled"
	}

	return false, "neither data checksums nor wal_log_hints are enabled"
}

// Execute runs pg_rewind to synchronize with the primary.
func (r *Rewind) Execute(ctx context.Context, primaryConnInfo string) error {
	r.state = RewindStateInProgress
	r.executed = true

	log.Info().
		Str("primary", primaryConnInfo).
		Msg("Starting pg_rewind")

	// Build connection string for pg_rewind
	connStr := r.buildConnectionString(primaryConnInfo)

	// Build pg_rewind command
	args := []string{
		"--target-pgdata=" + r.pg.dataDir,
		"--source-server=" + connStr,
		"--progress",
	}

	// Add write-recovery-conf for PG12+
	pgVersion := r.pg.GetVersion()
	if pgVersion >= 120000 {
		args = append(args, "--write-recovery-conf")
	}

	pgRewindPath := filepath.Join(r.pg.binDir, "pg_rewind")
	cmd := exec.CommandContext(ctx, pgRewindPath, args...)

	// Set environment
	cmd.Env = append(os.Environ(),
		"PGPASSWORD="+r.config.Password,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		r.state = RewindStateFailed
		r.lastErr = fmt.Errorf("pg_rewind failed: %w, output: %s", err, string(output))
		log.Error().
			Err(r.lastErr).
			Str("output", string(output)).
			Msg("pg_rewind failed")
		return r.lastErr
	}

	log.Info().
		Str("output", string(output)).
		Msg("pg_rewind completed successfully")

	r.state = RewindStateComplete

	// Post-rewind cleanup
	if err := r.postRewindCleanup(); err != nil {
		log.Warn().Err(err).Msg("Post-rewind cleanup had issues")
	}

	return nil
}

// ExecuteWithSlot runs pg_rewind using a replication slot for WAL retention.
func (r *Rewind) ExecuteWithSlot(ctx context.Context, primaryConnInfo, slotName string) error {
	// First, ensure the slot exists on the primary
	log.Info().
		Str("primary", primaryConnInfo).
		Str("slot", slotName).
		Msg("Starting pg_rewind with slot")

	// Execute the rewind
	if err := r.Execute(ctx, primaryConnInfo); err != nil {
		return err
	}

	// Configure the slot in recovery settings
	if err := r.configureRecoverySlot(slotName); err != nil {
		log.Warn().Err(err).Msg("Failed to configure recovery slot")
	}

	return nil
}

// buildConnectionString builds a connection string for pg_rewind.
func (r *Rewind) buildConnectionString(primaryConnInfo string) string {
	// Parse the primary connection info
	// primaryConnInfo is typically "host=x port=y"
	parts := []string{primaryConnInfo}

	if r.config.Username != "" {
		parts = append(parts, fmt.Sprintf("user=%s", r.config.Username))
	}

	if r.config.Password != "" {
		// Password is set via PGPASSWORD env var, but we can also use dbname
		parts = append(parts, "dbname=postgres")
	}

	return strings.Join(parts, " ")
}

// postRewindCleanup performs cleanup tasks after pg_rewind.
func (r *Rewind) postRewindCleanup() error {
	// Remove any stale pid file
	pidFile := filepath.Join(r.pg.dataDir, "postmaster.pid")
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale pid file: %w", err)
	}

	// Remove backup_label if it exists (pg_rewind might leave one)
	backupLabel := filepath.Join(r.pg.dataDir, "backup_label")
	if err := os.Remove(backupLabel); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Msg("Failed to remove backup_label")
	}

	// Clean up tablespace symlinks that might be stale
	pgTblspc := filepath.Join(r.pg.dataDir, "pg_tblspc")
	if entries, err := os.ReadDir(pgTblspc); err == nil {
		for _, entry := range entries {
			symlink := filepath.Join(pgTblspc, entry.Name())
			if target, err := os.Readlink(symlink); err == nil {
				if _, err := os.Stat(target); os.IsNotExist(err) {
					log.Warn().
						Str("symlink", symlink).
						Str("target", target).
						Msg("Removing broken tablespace symlink")
					os.Remove(symlink)
				}
			}
		}
	}

	return nil
}

// configureRecoverySlot configures the replication slot in recovery settings.
func (r *Rewind) configureRecoverySlot(slotName string) error {
	pgVersion := r.pg.GetVersion()

	var confFile string
	var content string

	if pgVersion >= 120000 {
		// PG12+ uses postgresql.auto.conf
		confFile = filepath.Join(r.pg.dataDir, "postgresql.auto.conf")
		content = fmt.Sprintf("\nprimary_slot_name = '%s'\n", slotName)
	} else {
		// Older versions use recovery.conf
		confFile = filepath.Join(r.pg.dataDir, "recovery.conf")
		content = fmt.Sprintf("primary_slot_name = '%s'\n", slotName)
	}

	f, err := os.OpenFile(confFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write slot config: %w", err)
	}

	return nil
}

// State returns the current rewind state.
func (r *Rewind) State() RewindState {
	return r.state
}

// LastError returns the last error from a rewind operation.
func (r *Rewind) LastError() error {
	return r.lastErr
}

// WasExecuted returns true if pg_rewind was executed.
func (r *Rewind) WasExecuted() bool {
	return r.executed
}

// Reset resets the rewind state.
func (r *Rewind) Reset() {
	r.state = RewindStateNone
	r.lastErr = nil
	r.executed = false
}

// CheckTimelines compares local and primary timelines.
func (r *Rewind) CheckTimelines(ctx context.Context, primaryHost string, primaryPort int) (*TimelineComparison, error) {
	// Get local timeline
	localControlData, err := r.pg.GetControlData()
	if err != nil {
		return nil, fmt.Errorf("failed to get local control data: %w", err)
	}

	localTimeline := localControlData["Latest checkpoint's TimeLineID"]
	localLSN := localControlData["Latest checkpoint's REDO location"]

	// Query primary for its timeline
	connStr := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres",
		primaryHost, primaryPort, r.config.Username)

	// This would typically use pgx to connect and query
	// For now, return a placeholder

	return &TimelineComparison{
		LocalTimeline:   localTimeline,
		LocalLSN:        localLSN,
		PrimaryTimeline: "",
		PrimaryLSN:      "",
		NeedsRewind:     false,
	}, nil
}

// TimelineComparison holds the result of timeline comparison.
type TimelineComparison struct {
	LocalTimeline   string
	LocalLSN        string
	PrimaryTimeline string
	PrimaryLSN      string
	NeedsRewind     bool
	Reason          string
}

// RewindWithRetry attempts pg_rewind with retries.
func (r *Rewind) RewindWithRetry(ctx context.Context, primaryConnInfo string, maxRetries int) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			log.Info().
				Int("attempt", i+1).
				Int("max_retries", maxRetries).
				Msg("Retrying pg_rewind")
			time.Sleep(time.Duration(i*2) * time.Second)
		}

		err := r.Execute(ctx, primaryConnInfo)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !r.isRetryableError(err) {
			return err
		}
	}

	return fmt.Errorf("pg_rewind failed after %d attempts: %w", maxRetries, lastErr)
}

// isRetryableError checks if the error is retryable.
func (r *Rewind) isRetryableError(err error) bool {
	errStr := err.Error()

	// Transient connection errors are retryable
	retryableErrors := []string{
		"connection refused",
		"timeout",
		"connection reset",
		"no route to host",
	}

	for _, retryable := range retryableErrors {
		if strings.Contains(strings.ToLower(errStr), retryable) {
			return true
		}
	}

	return false
}

// CreateReplicationSlot creates a replication slot on the primary for WAL retention.
func (r *Rewind) CreateReplicationSlot(ctx context.Context, primaryConnInfo, slotName string) error {
	// This would typically use pgx to connect and create the slot
	log.Info().
		Str("slot", slotName).
		Msg("Would create replication slot for pg_rewind WAL retention")
	return nil
}

// DropReplicationSlot drops a replication slot after successful rewind.
func (r *Rewind) DropReplicationSlot(ctx context.Context, primaryConnInfo, slotName string) error {
	// This would typically use pgx to connect and drop the slot
	log.Info().
		Str("slot", slotName).
		Msg("Would drop replication slot after pg_rewind")
	return nil
}

// GenerateRecoveryConf generates recovery configuration after pg_rewind.
func (r *Rewind) GenerateRecoveryConf(primaryConnInfo, applicationName string) error {
	pgVersion := r.pg.GetVersion()

	if pgVersion >= 120000 {
		// PG12+ uses standby.signal and postgresql.auto.conf
		signalFile := filepath.Join(r.pg.dataDir, "standby.signal")
		if err := os.WriteFile(signalFile, []byte{}, 0600); err != nil {
			return fmt.Errorf("failed to create standby.signal: %w", err)
		}

		autoConf := filepath.Join(r.pg.dataDir, "postgresql.auto.conf")
		content := fmt.Sprintf(`
primary_conninfo = '%s application_name=%s'
recovery_target_timeline = 'latest'
`, primaryConnInfo, applicationName)

		f, err := os.OpenFile(autoConf, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return fmt.Errorf("failed to open postgresql.auto.conf: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(content); err != nil {
			return fmt.Errorf("failed to write recovery config: %w", err)
		}
	} else {
		// Older versions use recovery.conf
		recoveryConf := filepath.Join(r.pg.dataDir, "recovery.conf")
		content := fmt.Sprintf(`
standby_mode = 'on'
primary_conninfo = '%s application_name=%s'
recovery_target_timeline = 'latest'
`, primaryConnInfo, applicationName)

		if err := os.WriteFile(recoveryConf, []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write recovery.conf: %w", err)
		}
	}

	return nil
}
