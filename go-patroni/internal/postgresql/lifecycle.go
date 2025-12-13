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
	"github.com/shirou/gopsutil/v3/process"

	"github.com/patroni/patroni-go/pkg/types"
)

// StopMode represents PostgreSQL shutdown modes.
type StopMode string

const (
	StopModeSmart     StopMode = "smart"
	StopModeFast      StopMode = "fast"
	StopModeImmediate StopMode = "immediate"
)

// Start starts PostgreSQL.
func (pg *Postgresql) Start(ctx context.Context) error {
	if pg.IsRunning() {
		log.Info().Msg("PostgreSQL is already running")
		return nil
	}

	pg.setState(types.PostgresqlStateStarting)
	log.Info().Str("data_dir", pg.dataDir).Msg("Starting PostgreSQL")

	// Build pg_ctl command
	args := []string{
		"-D", pg.dataDir,
		"-l", filepath.Join(pg.dataDir, "pg_log", "postgresql.log"),
		"start",
	}

	// Add options if needed
	if pg.config.PostgreSQL.Listen != "" {
		host := pg.getHost()
		port := pg.getPort()
		args = append(args, "-o", fmt.Sprintf("-p %s", port))
		if host != "" && host != "*" {
			args = append(args[0:len(args)-1], fmt.Sprintf("-p %s -h %s", port, host))
		}
	}

	cmd := exec.CommandContext(ctx, pg.pgCommand("pg_ctl"), args...)
	cmd.Env = append(os.Environ(), "PGDATA="+pg.dataDir)

	// Ensure log directory exists
	logDir := filepath.Join(pg.dataDir, "pg_log")
	if err := os.MkdirAll(logDir, 0750); err != nil {
		log.Warn().Err(err).Msg("Failed to create log directory")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		pg.setState(types.PostgresqlStateStartFailed)
		return fmt.Errorf("failed to start PostgreSQL: %w, output: %s", err, string(output))
	}

	log.Info().Msg("PostgreSQL started successfully")

	// Wait for PostgreSQL to be accepting connections
	if err := pg.waitForStartup(ctx); err != nil {
		pg.setState(types.PostgresqlStateStartFailed)
		return err
	}

	pg.setState(types.PostgresqlStateRunning)

	// Determine role
	if pg.isPrimary() {
		pg.setRole(types.PostgresqlRolePrimary)
	} else {
		pg.setRole(types.PostgresqlRoleReplica)
	}

	// Execute on_start callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnStart, pg.role.String(), pg.scope)
	}

	return nil
}

// waitForStartup waits for PostgreSQL to accept connections.
func (pg *Postgresql) waitForStartup(ctx context.Context) error {
	timeout := time.Duration(pg.config.PostgreSQL.PgCtlTimeout) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if pg.isAcceptingConnections() {
			return nil
		}

		// Check if postmaster is still running
		if pm := pg.isRunning(); pm == nil {
			return fmt.Errorf("postmaster process died during startup")
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for PostgreSQL to start")
}

// Stop stops PostgreSQL.
func (pg *Postgresql) Stop(ctx context.Context, mode StopMode) error {
	if !pg.IsRunning() {
		log.Info().Msg("PostgreSQL is not running")
		pg.setState(types.PostgresqlStateStopped)
		return nil
	}

	pg.setState(types.PostgresqlStateStopping)
	log.Info().Str("mode", string(mode)).Msg("Stopping PostgreSQL")

	// Close connections first
	pg.CloseConnections()

	// Execute before_stop callback
	if pg.config.PostgreSQL.BeforeStop != "" {
		if err := pg.executeCallback(ctx, pg.config.PostgreSQL.BeforeStop, "before_stop"); err != nil {
			log.Warn().Err(err).Msg("before_stop callback failed")
		}
	}

	// Build pg_ctl command
	args := []string{
		"-D", pg.dataDir,
		"-m", string(mode),
		"stop",
		"-w", // Wait for shutdown to complete
	}

	timeout := pg.config.PostgreSQL.PgCtlTimeout
	if timeout > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", timeout))
	}

	cmd := exec.CommandContext(ctx, pg.pgCommand("pg_ctl"), args...)
	cmd.Env = append(os.Environ(), "PGDATA="+pg.dataDir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it's actually stopped
		if !pg.IsRunning() {
			pg.setState(types.PostgresqlStateStopped)
			return nil
		}
		pg.setState(types.PostgresqlStateStopFailed)
		return fmt.Errorf("failed to stop PostgreSQL: %w, output: %s", err, string(output))
	}

	pg.setState(types.PostgresqlStateStopped)
	pg.mu.Lock()
	pg.postmaster = nil
	pg.mu.Unlock()

	log.Info().Msg("PostgreSQL stopped successfully")

	// Execute on_stop callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnStop, pg.role.String(), pg.scope)
	}

	return nil
}

// Restart restarts PostgreSQL.
func (pg *Postgresql) Restart(ctx context.Context) error {
	pg.setState(types.PostgresqlStateRestarting)
	log.Info().Msg("Restarting PostgreSQL")

	// Stop first
	if err := pg.Stop(ctx, StopModeFast); err != nil {
		pg.setState(types.PostgresqlStateRestartFailed)
		return fmt.Errorf("failed to stop for restart: %w", err)
	}

	// Clear pending restart flag
	pg.mu.Lock()
	pg.pendingRestart = false
	pg.pendingRestartReason = make(map[string]interface{})
	pg.mu.Unlock()

	// Start again
	if err := pg.Start(ctx); err != nil {
		pg.setState(types.PostgresqlStateRestartFailed)
		return fmt.Errorf("failed to start after restart: %w", err)
	}

	// Execute on_restart callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnRestart, pg.role.String(), pg.scope)
	}

	return nil
}

// Reload reloads PostgreSQL configuration.
func (pg *Postgresql) Reload(ctx context.Context) error {
	if !pg.IsRunning() {
		return fmt.Errorf("PostgreSQL is not running")
	}

	log.Info().Msg("Reloading PostgreSQL configuration")

	cmd := exec.CommandContext(ctx, pg.pgCommand("pg_ctl"),
		"-D", pg.dataDir,
		"reload",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reload PostgreSQL: %w, output: %s", err, string(output))
	}

	// Execute on_reload callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnReload, pg.role.String(), pg.scope)
	}

	return nil
}

// Promote promotes a replica to primary.
func (pg *Postgresql) Promote(ctx context.Context) error {
	if pg.IsPrimary() {
		log.Info().Msg("Already primary")
		return nil
	}

	if !pg.IsRunning() {
		return fmt.Errorf("PostgreSQL is not running")
	}

	log.Info().Msg("Promoting replica to primary")

	// Execute pre_promote callback
	if pg.config.PostgreSQL.PrePromote != "" {
		if err := pg.executeCallback(ctx, pg.config.PostgreSQL.PrePromote, "pre_promote"); err != nil {
			return fmt.Errorf("pre_promote callback failed: %w", err)
		}
	}

	// Use pg_ctl promote for PG 12+, otherwise create trigger file or use SQL
	if pg.majorVersion >= 120000 {
		cmd := exec.CommandContext(ctx, pg.pgCommand("pg_ctl"),
			"-D", pg.dataDir,
			"promote",
			"-w", // Wait for promotion to complete
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to promote: %w, output: %s", err, string(output))
		}
	} else {
		// For older versions, use SQL function
		if err := pg.Exec(ctx, "SELECT pg_catalog.pg_promote()"); err != nil {
			return fmt.Errorf("failed to promote via SQL: %w", err)
		}
	}

	// Wait for promotion to complete
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if pg.isPrimary() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !pg.isPrimary() {
		return fmt.Errorf("promotion did not complete in time")
	}

	pg.setRole(types.PostgresqlRolePromoted)
	log.Info().Msg("Promotion completed successfully")

	// Remove standby.signal if present
	standbySignal := filepath.Join(pg.dataDir, "standby.signal")
	os.Remove(standbySignal)

	// Execute on_role_change callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnRoleChange, "master", pg.scope)
	}

	return nil
}

// Demote configures PostgreSQL as a replica.
func (pg *Postgresql) Demote(ctx context.Context, primaryConnInfo string) error {
	if !pg.IsPrimary() {
		log.Info().Msg("Already a replica")
		return nil
	}

	log.Info().Msg("Demoting primary to replica")

	pg.setRole(types.PostgresqlRoleDemoted)

	// Stop PostgreSQL
	if err := pg.Stop(ctx, StopModeFast); err != nil {
		return fmt.Errorf("failed to stop for demotion: %w", err)
	}

	// Configure as replica
	if err := pg.configureReplica(primaryConnInfo); err != nil {
		return fmt.Errorf("failed to configure as replica: %w", err)
	}

	// Start as replica
	if err := pg.Start(ctx); err != nil {
		return fmt.Errorf("failed to start as replica: %w", err)
	}

	pg.setRole(types.PostgresqlRoleReplica)

	// Execute on_role_change callback
	if pg.callbacks != nil {
		go pg.callbacks.Execute(CallbackOnRoleChange, "replica", pg.scope)
	}

	return nil
}

// configureReplica configures PostgreSQL to run as a replica.
func (pg *Postgresql) configureReplica(primaryConnInfo string) error {
	// Create standby.signal for PG 12+
	if pg.majorVersion >= 120000 {
		standbySignal := filepath.Join(pg.dataDir, "standby.signal")
		if err := os.WriteFile(standbySignal, []byte{}, 0600); err != nil {
			return fmt.Errorf("failed to create standby.signal: %w", err)
		}
	}

	// Update postgresql.auto.conf with primary connection info
	autoConf := filepath.Join(pg.dataDir, "postgresql.auto.conf")
	content := fmt.Sprintf("# Managed by Patroni\nprimary_conninfo = '%s'\n", primaryConnInfo)

	// Append to existing file or create new
	f, err := os.OpenFile(autoConf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open postgresql.auto.conf: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write postgresql.auto.conf: %w", err)
	}

	return nil
}

// executeCallback executes a callback script.
func (pg *Postgresql) executeCallback(ctx context.Context, script, name string) error {
	log.Info().Str("callback", name).Str("script", script).Msg("Executing callback")

	parts := strings.Fields(script)
	if len(parts) == 0 {
		return fmt.Errorf("empty callback script")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = append(os.Environ(),
		"PATRONI_SCOPE="+pg.scope,
		"PATRONI_NAME="+pg.name,
		"PATRONI_ROLE="+pg.role.String(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("callback %s failed: %w, output: %s", name, err, string(output))
	}

	return nil
}

// Kill sends a signal to the PostgreSQL process.
func (pg *Postgresql) Kill(signal process.Signal) error {
	pm := pg.isRunning()
	if pm == nil {
		return nil
	}

	return pm.SendSignal(signal)
}

// IsPendingRestart returns true if a restart is pending.
func (pg *Postgresql) IsPendingRestart() bool {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.pendingRestart
}

// SetPendingRestart marks PostgreSQL as needing a restart.
func (pg *Postgresql) SetPendingRestart(reason string, oldValue, newValue interface{}) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.pendingRestart = true
	pg.pendingRestartReason[reason] = map[string]interface{}{
		"old_value": oldValue,
		"new_value": newValue,
	}
}

// PendingRestartReason returns the reason for pending restart.
func (pg *Postgresql) PendingRestartReason() map[string]interface{} {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.pendingRestartReason
}
