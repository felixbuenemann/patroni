// Package backup provides backup and restore integration for WAL-E, WAL-G, and Barman.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// BackupType represents the type of backup tool.
type BackupType string

const (
	BackupTypeWalE   BackupType = "wal_e"
	BackupTypeWalG   BackupType = "wal_g"
	BackupTypeBarman BackupType = "barman"
	BackupTypePgBackRest BackupType = "pgbackrest"
)

// Config holds backup configuration.
type Config struct {
	Type            BackupType        `yaml:"type" json:"type"`
	Command         string            `yaml:"command" json:"command"`
	DataDir         string            `yaml:"data_dir" json:"data_dir"`
	WalDir          string            `yaml:"wal_dir" json:"wal_dir"`
	Scope           string            `yaml:"scope" json:"scope"`
	RetainSize      int               `yaml:"retain_size" json:"retain_size"`       // Retain N backups
	RetainDays      int               `yaml:"retain_days" json:"retain_days"`       // Retain backups for N days
	NoMaster        bool              `yaml:"no_master" json:"no_master"`           // Skip backup on master
	EnvDir          string            `yaml:"env_dir" json:"env_dir"`               // Directory with env files
	UseWalG         bool              `yaml:"use_walg" json:"use_walg"`             // Use wal-g for wal-e compat
	SSHArgs         string            `yaml:"ssh_args" json:"ssh_args"`             // SSH args for barman
	BarmanServer    string            `yaml:"barman_server" json:"barman_server"`   // Barman server hostname
	BarmanSSH       string            `yaml:"barman_ssh" json:"barman_ssh"`         // Barman SSH command
	Environment     map[string]string `yaml:"environment" json:"environment"`       // Environment variables
}

// DefaultConfig returns default backup configuration.
func DefaultConfig() *Config {
	return &Config{
		Type:       BackupTypeWalE,
		RetainSize: 10,
		RetainDays: 7,
	}
}

// BackupInfo represents information about a backup.
type BackupInfo struct {
	Name        string    `json:"name"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	StartWAL    string    `json:"start_wal"`
	EndWAL      string    `json:"end_wal"`
	Size        int64     `json:"size"`
	Status      string    `json:"status"`
	Timeline    int       `json:"timeline"`
	Method      string    `json:"method"`
	ServerName  string    `json:"server_name,omitempty"`
}

// Handler manages backup operations.
type Handler struct {
	config  *Config
	dataDir string
}

// NewHandler creates a new backup Handler.
func NewHandler(config *Config, dataDir string) *Handler {
	if config == nil {
		config = DefaultConfig()
	}

	return &Handler{
		config:  config,
		dataDir: dataDir,
	}
}

// CreateBaseBackup creates a base backup.
func (h *Handler) CreateBaseBackup(ctx context.Context) error {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.createWalEBackup(ctx)
	case BackupTypeBarman:
		return h.createBarmanBackup(ctx)
	case BackupTypePgBackRest:
		return h.createPgBackRestBackup(ctx)
	default:
		return fmt.Errorf("unsupported backup type: %s", h.config.Type)
	}
}

// createWalEBackup creates a backup using wal-e or wal-g.
func (h *Handler) createWalEBackup(ctx context.Context) error {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"backup-push", h.dataDir}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("backup failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("type", string(h.config.Type)).
		Str("output", string(output)).
		Msg("Base backup created")

	return nil
}

// createBarmanBackup creates a backup using barman.
func (h *Handler) createBarmanBackup(ctx context.Context) error {
	args := []string{"backup", h.config.Scope}

	cmd := exec.CommandContext(ctx, "barman", args...)
	cmd.Env = h.buildEnv()

	if h.config.BarmanSSH != "" {
		cmd = exec.CommandContext(ctx, "ssh", h.config.BarmanServer, "barman", "backup", h.config.Scope)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("barman backup failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("output", string(output)).
		Msg("Barman backup created")

	return nil
}

// createPgBackRestBackup creates a backup using pgBackRest.
func (h *Handler) createPgBackRestBackup(ctx context.Context) error {
	args := []string{"--stanza=" + h.config.Scope, "backup"}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest backup failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("output", string(output)).
		Msg("pgBackRest backup created")

	return nil
}

// ListBackups lists available backups.
func (h *Handler) ListBackups(ctx context.Context) ([]*BackupInfo, error) {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.listWalEBackups(ctx)
	case BackupTypeBarman:
		return h.listBarmanBackups(ctx)
	case BackupTypePgBackRest:
		return h.listPgBackRestBackups(ctx)
	default:
		return nil, fmt.Errorf("unsupported backup type: %s", h.config.Type)
	}
}

// listWalEBackups lists backups using wal-e or wal-g.
func (h *Handler) listWalEBackups(ctx context.Context) ([]*BackupInfo, error) {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"backup-list", "--json"}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list backups: %w", err)
	}

	var backups []*BackupInfo
	if err := json.Unmarshal(output, &backups); err != nil {
		// Try parsing line-by-line for older wal-e format
		return h.parseWalEBackupList(string(output))
	}

	return backups, nil
}

// parseWalEBackupList parses the text output of wal-e backup-list.
func (h *Handler) parseWalEBackupList(output string) ([]*BackupInfo, error) {
	var backups []*BackupInfo

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "name") {
			continue
		}

		// Parse format: name last_modified wal_segment_backup_start
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		backup := &BackupInfo{
			Name: fields[0],
		}

		// Parse timestamp from backup name
		// Format: base_00000001000000000000000A_00000001
		re := regexp.MustCompile(`base_([0-9A-F]+)`)
		if matches := re.FindStringSubmatch(fields[0]); len(matches) > 1 {
			backup.StartWAL = matches[1]
		}

		backups = append(backups, backup)
	}

	return backups, nil
}

// listBarmanBackups lists backups using barman.
func (h *Handler) listBarmanBackups(ctx context.Context) ([]*BackupInfo, error) {
	args := []string{"list-backup", h.config.Scope, "--minimal"}

	cmd := exec.CommandContext(ctx, "barman", args...)

	if h.config.BarmanSSH != "" {
		cmd = exec.CommandContext(ctx, "ssh", h.config.BarmanServer, "barman", "list-backup", h.config.Scope, "--minimal")
	}

	cmd.Env = h.buildEnv()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list barman backups: %w", err)
	}

	var backups []*BackupInfo
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		backups = append(backups, &BackupInfo{
			Name:       line,
			ServerName: h.config.Scope,
		})
	}

	return backups, nil
}

// listPgBackRestBackups lists backups using pgBackRest.
func (h *Handler) listPgBackRestBackups(ctx context.Context) ([]*BackupInfo, error) {
	args := []string{"--stanza=" + h.config.Scope, "info", "--output=json"}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list pgbackrest backups: %w", err)
	}

	// Parse pgBackRest JSON output
	var result []struct {
		Backup []struct {
			Label     string `json:"label"`
			Type      string `json:"type"`
			Timestamp struct {
				Start int64 `json:"start"`
				Stop  int64 `json:"stop"`
			} `json:"timestamp"`
		} `json:"backup"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse pgbackrest output: %w", err)
	}

	var backups []*BackupInfo
	if len(result) > 0 {
		for _, b := range result[0].Backup {
			backups = append(backups, &BackupInfo{
				Name:      b.Label,
				StartTime: time.Unix(b.Timestamp.Start, 0),
				EndTime:   time.Unix(b.Timestamp.Stop, 0),
				Method:    b.Type,
			})
		}
	}

	return backups, nil
}

// Restore restores from a backup.
func (h *Handler) Restore(ctx context.Context, backupName string, targetDir string) error {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.restoreWalE(ctx, backupName, targetDir)
	case BackupTypeBarman:
		return h.restoreBarman(ctx, backupName, targetDir)
	case BackupTypePgBackRest:
		return h.restorePgBackRest(ctx, backupName, targetDir)
	default:
		return fmt.Errorf("unsupported backup type: %s", h.config.Type)
	}
}

// restoreWalE restores using wal-e or wal-g.
func (h *Handler) restoreWalE(ctx context.Context, backupName string, targetDir string) error {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"backup-fetch", targetDir}
	if backupName != "" && backupName != "LATEST" {
		args = append(args, backupName)
	} else {
		args = append(args, "LATEST")
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restore failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("backup", backupName).
		Str("target", targetDir).
		Msg("Restore completed")

	return nil
}

// restoreBarman restores using barman.
func (h *Handler) restoreBarman(ctx context.Context, backupName string, targetDir string) error {
	args := []string{
		"recover",
		h.config.Scope,
		backupName,
		targetDir,
		"--remote-ssh-command", fmt.Sprintf("ssh %s", h.config.SSHArgs),
	}

	cmd := exec.CommandContext(ctx, "barman", args...)
	if h.config.BarmanSSH != "" {
		cmd = exec.CommandContext(ctx, "ssh", h.config.BarmanServer,
			"barman", "recover", h.config.Scope, backupName, targetDir)
	}

	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("barman restore failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("backup", backupName).
		Str("target", targetDir).
		Msg("Barman restore completed")

	return nil
}

// restorePgBackRest restores using pgBackRest.
func (h *Handler) restorePgBackRest(ctx context.Context, backupName string, targetDir string) error {
	args := []string{
		"--stanza=" + h.config.Scope,
		"--pg1-path=" + targetDir,
		"restore",
	}

	if backupName != "" && backupName != "LATEST" {
		args = append(args, "--set="+backupName)
	}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest restore failed: %w, output: %s", err, string(output))
	}

	log.Info().
		Str("backup", backupName).
		Str("target", targetDir).
		Msg("pgBackRest restore completed")

	return nil
}

// DeleteBackup deletes a specific backup.
func (h *Handler) DeleteBackup(ctx context.Context, backupName string) error {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.deleteWalEBackup(ctx, backupName)
	case BackupTypeBarman:
		return h.deleteBarmanBackup(ctx, backupName)
	case BackupTypePgBackRest:
		return h.deletePgBackRestBackup(ctx, backupName)
	default:
		return fmt.Errorf("unsupported backup type: %s", h.config.Type)
	}
}

// deleteWalEBackup deletes a backup using wal-e or wal-g.
func (h *Handler) deleteWalEBackup(ctx context.Context, backupName string) error {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"delete", "--confirm", "before", backupName}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete failed: %w, output: %s", err, string(output))
	}

	log.Info().Str("backup", backupName).Msg("Backup deleted")
	return nil
}

// deleteBarmanBackup deletes a backup using barman.
func (h *Handler) deleteBarmanBackup(ctx context.Context, backupName string) error {
	args := []string{"delete", h.config.Scope, backupName}

	cmd := exec.CommandContext(ctx, "barman", args...)
	if h.config.BarmanSSH != "" {
		cmd = exec.CommandContext(ctx, "ssh", h.config.BarmanServer,
			"barman", "delete", h.config.Scope, backupName)
	}

	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("barman delete failed: %w, output: %s", err, string(output))
	}

	log.Info().Str("backup", backupName).Msg("Barman backup deleted")
	return nil
}

// deletePgBackRestBackup deletes a backup using pgBackRest.
func (h *Handler) deletePgBackRestBackup(ctx context.Context, backupName string) error {
	args := []string{
		"--stanza=" + h.config.Scope,
		"expire",
		"--set=" + backupName,
	}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest expire failed: %w, output: %s", err, string(output))
	}

	log.Info().Str("backup", backupName).Msg("pgBackRest backup expired")
	return nil
}

// PurgeOldBackups removes old backups based on retention policy.
func (h *Handler) PurgeOldBackups(ctx context.Context) error {
	backups, err := h.ListBackups(ctx)
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}

	if len(backups) <= h.config.RetainSize {
		return nil
	}

	// Sort by start time
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].StartTime.Before(backups[j].StartTime)
	})

	// Keep only RetainSize backups
	toDelete := backups[:len(backups)-h.config.RetainSize]

	for _, backup := range toDelete {
		// Check retention days
		if h.config.RetainDays > 0 {
			cutoff := time.Now().AddDate(0, 0, -h.config.RetainDays)
			if backup.StartTime.After(cutoff) {
				continue
			}
		}

		log.Info().
			Str("backup", backup.Name).
			Time("start_time", backup.StartTime).
			Msg("Purging old backup")

		if err := h.DeleteBackup(ctx, backup.Name); err != nil {
			log.Warn().Err(err).Str("backup", backup.Name).Msg("Failed to delete backup")
		}
	}

	return nil
}

// ArchiveWAL archives a WAL segment.
func (h *Handler) ArchiveWAL(ctx context.Context, walFile string) error {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.archiveWalE(ctx, walFile)
	case BackupTypePgBackRest:
		return h.archivePgBackRest(ctx, walFile)
	default:
		// Barman uses streaming, so no archive needed
		return nil
	}
}

// archiveWalE archives a WAL segment using wal-e or wal-g.
func (h *Handler) archiveWalE(ctx context.Context, walFile string) error {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"wal-push", walFile}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wal-push failed: %w, output: %s", err, string(output))
	}

	return nil
}

// archivePgBackRest archives a WAL segment using pgBackRest.
func (h *Handler) archivePgBackRest(ctx context.Context, walFile string) error {
	args := []string{
		"--stanza=" + h.config.Scope,
		"archive-push",
		walFile,
	}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest archive-push failed: %w, output: %s", err, string(output))
	}

	return nil
}

// RestoreWAL restores a WAL segment.
func (h *Handler) RestoreWAL(ctx context.Context, walName, destPath string) error {
	switch h.config.Type {
	case BackupTypeWalE, BackupTypeWalG:
		return h.restoreWalE_WAL(ctx, walName, destPath)
	case BackupTypePgBackRest:
		return h.restorePgBackRest_WAL(ctx, walName, destPath)
	default:
		return fmt.Errorf("WAL restore not supported for %s", h.config.Type)
	}
}

// restoreWalE_WAL restores a WAL segment using wal-e or wal-g.
func (h *Handler) restoreWalE_WAL(ctx context.Context, walName, destPath string) error {
	command := "wal-e"
	if h.config.UseWalG || h.config.Type == BackupTypeWalG {
		command = "wal-g"
	}

	args := []string{"wal-fetch", walName, destPath}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wal-fetch failed: %w, output: %s", err, string(output))
	}

	return nil
}

// restorePgBackRest_WAL restores a WAL segment using pgBackRest.
func (h *Handler) restorePgBackRest_WAL(ctx context.Context, walName, destPath string) error {
	args := []string{
		"--stanza=" + h.config.Scope,
		"archive-get",
		walName,
		destPath,
	}

	cmd := exec.CommandContext(ctx, "pgbackrest", args...)
	cmd.Env = h.buildEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest archive-get failed: %w, output: %s", err, string(output))
	}

	return nil
}

// buildEnv builds the environment for backup commands.
func (h *Handler) buildEnv() []string {
	env := os.Environ()

	// Add configured environment variables
	for k, v := range h.config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Load environment from envdir if specified
	if h.config.EnvDir != "" {
		entries, err := os.ReadDir(h.config.EnvDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				content, err := os.ReadFile(filepath.Join(h.config.EnvDir, entry.Name()))
				if err == nil {
					value := strings.TrimSpace(string(content))
					env = append(env, fmt.Sprintf("%s=%s", entry.Name(), value))
				}
			}
		}
	}

	return env
}

// GenerateRestoreCommand generates a restore_command for recovery.conf.
func (h *Handler) GenerateRestoreCommand() string {
	switch h.config.Type {
	case BackupTypeWalE:
		return "wal-e wal-fetch %f %p"
	case BackupTypeWalG:
		return "wal-g wal-fetch %f %p"
	case BackupTypePgBackRest:
		return fmt.Sprintf("pgbackrest --stanza=%s archive-get %%f %%p", h.config.Scope)
	case BackupTypeBarman:
		if h.config.BarmanSSH != "" {
			return fmt.Sprintf("barman-wal-restore -U barman %s %s %%f %%p",
				h.config.BarmanServer, h.config.Scope)
		}
		return fmt.Sprintf("barman-wal-restore %s %s %%f %%p",
			h.config.BarmanServer, h.config.Scope)
	default:
		return ""
	}
}

// GenerateArchiveCommand generates an archive_command for postgresql.conf.
func (h *Handler) GenerateArchiveCommand() string {
	switch h.config.Type {
	case BackupTypeWalE:
		return "wal-e wal-push %p"
	case BackupTypeWalG:
		return "wal-g wal-push %p"
	case BackupTypePgBackRest:
		return fmt.Sprintf("pgbackrest --stanza=%s archive-push %%p", h.config.Scope)
	default:
		return ""
	}
}
