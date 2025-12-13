// Package main implements the patroni_wale_restore script for cloning replicas using WAL-E.
//
// This script clones new replicas using WAL-E restore from S3/Swift, falling back
// to pg_basebackup if WAL-E restore fails or if the WAL-E backup is too far behind.
//
// Exit codes:
//   - 0: Success
//   - 1: External issue, retry later
//   - 2: Permanent failure, don't try again unless configuration changes
package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Exit codes matching Python implementation
const (
	ExitSuccess    = 0
	ExitRetryLater = 1
	ExitFail       = 2
)

const (
	retrySleepInterval = time.Second
)

var siPrefixes = []string{"K", "M", "G", "T", "P", "E", "Z", "Y"}

// WALEConfig holds WAL-E configuration.
type WALEConfig struct {
	EnvDir       string
	ThresholdMB  int
	ThresholdPct int
	Cmd          []string
}

// WALERestore handles replica creation using WAL-E.
type WALERestore struct {
	scope            string
	leaderConnection string
	dataDir          string
	noLeader         bool
	walE             WALEConfig
	initError        bool
	retries          int
}

// NewWALERestore creates a new WALERestore instance.
func NewWALERestore(scope, datadir, connstring, envDir string, thresholdMB, thresholdPct, useIAM int, noLeader bool, retries int) *WALERestore {
	waleCmd := []string{"envdir", envDir, "wal-e"}

	if useIAM == 1 {
		waleCmd = append(waleCmd, "--aws-instance-profile")
	}

	return &WALERestore{
		scope:            scope,
		leaderConnection: connstring,
		dataDir:          datadir,
		noLeader:         noLeader,
		walE: WALEConfig{
			EnvDir:       envDir,
			ThresholdMB:  thresholdMB,
			ThresholdPct: thresholdPct,
			Cmd:          waleCmd,
		},
		initError: !dirExists(envDir),
		retries:   retries,
	}
}

// Run creates a new replica using WAL-E.
func (w *WALERestore) Run() int {
	if w.initError {
		log.Error().Str("envdir", w.walE.EnvDir).Msg("init error: envdir did not exist at initialization time")
		return ExitFail
	}

	shouldUseS3, err := w.shouldUseS3ToCreateReplica()
	if err != nil {
		log.Error().Err(err).Msg("error checking S3 backup")
		return ExitRetryLater
	}

	if shouldUseS3 == nil {
		// Need to retry
		return ExitRetryLater
	}

	if *shouldUseS3 {
		return w.createReplicaWithS3()
	}

	return ExitFail
}

// shouldUseS3ToCreateReplica determines whether to use S3 vs pg_basebackup.
func (w *WALERestore) shouldUseS3ToCreateReplica() (*bool, error) {
	thresholdMegabytes := w.walE.ThresholdMB
	thresholdPercent := w.walE.ThresholdPct

	// Get latest backup info from WAL-E
	cmd := append(w.walE.Cmd, "backup-list", "--detail", "LATEST")
	log.Debug().Strs("cmd", cmd).Msg("calling wal-e")

	output, err := exec.Command(cmd[0], cmd[1:]...).Output()
	if err != nil {
		log.Error().Err(err).Msg("could not query wal-e latest backup")
		return nil, nil // Return nil to trigger retry
	}

	// Parse CSV output
	reader := csv.NewReader(strings.NewReader(string(output)))
	reader.Comma = '\t'

	records, err := reader.ReadAll()
	if err != nil {
		log.Error().Err(err).Msg("failed to parse wal-e output")
		return boolPtr(false), nil
	}

	if len(records) < 2 {
		log.Warn().Msg("wal-e did not find any backups")
		return boolPtr(false), nil
	}

	if len(records) > 2 {
		log.Warn().Int("rows", len(records)-1).Msg("wal-e returned more than one row of backups")
		return boolPtr(false), nil
	}

	// Parse header and data
	header := records[0]
	data := records[1]
	backupInfo := make(map[string]string)
	for i, h := range header {
		if i < len(data) {
			backupInfo[h] = data[i]
		}
	}

	// Extract backup parameters
	backupSizeStr, ok := backupInfo["expanded_size_bytes"]
	if !ok {
		log.Error().Msg("unable to get expanded_size_bytes from WALE backup")
		return nil, nil
	}
	backupSize, err := strconv.ParseInt(backupSizeStr, 10, 64)
	if err != nil {
		log.Error().Err(err).Msg("unable to parse backup size")
		return nil, nil
	}

	backupStartSegment, ok := backupInfo["wal_segment_backup_start"]
	if !ok {
		log.Error().Msg("unable to get wal_segment_backup_start from WALE backup")
		return nil, nil
	}

	backupStartOffsetStr, ok := backupInfo["wal_segment_offset_backup_start"]
	if !ok {
		log.Error().Msg("unable to get wal_segment_offset_backup_start from WALE backup")
		return nil, nil
	}
	backupStartOffset, err := strconv.ParseInt(backupStartOffsetStr, 10, 64)
	if err != nil {
		log.Error().Err(err).Msg("unable to parse backup start offset")
		return nil, nil
	}

	// Construct LSN from segment and offset
	// WAL filename is XXXXXXXXYYYYYYYY000000ZZ
	lsnSegment := backupStartSegment[8:16]
	highOffset, _ := strconv.ParseInt(backupStartSegment[16:], 16, 64)
	lsnOffset := fmt.Sprintf("%X", (highOffset<<24)+backupStartOffset)
	backupStartLSN := fmt.Sprintf("%s/%s", lsnSegment, lsnOffset)

	diffInBytes := backupSize
	attemptsNo := 0

	for {
		if w.leaderConnection != "" {
			db, err := sql.Open("postgres", w.leaderConnection)
			if err != nil {
				log.Error().Err(err).Msg("could not connect to leader")
				if attemptsNo < w.retries {
					attemptsNo++
					time.Sleep(retrySleepInterval)
					continue
				}
				if !w.noLeader {
					return boolPtr(false), nil
				}
				log.Info().Msg("continue with base backup from S3 since leader is not available")
				diffInBytes = 0
				break
			}
			defer db.Close()

			// Check server version
			var serverVersion int
			err = db.QueryRow("SHOW server_version_num").Scan(&serverVersion)
			if err != nil {
				serverVersion = 0
			}

			var walName, lsnName string
			if serverVersion >= 100000 {
				walName = "wal"
				lsnName = "lsn"
			} else {
				walName = "xlog"
				lsnName = "location"
			}

			query := fmt.Sprintf(`SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
				THEN GREATEST(pg_catalog.pg_%s_%s_diff(COALESCE(
				pg_last_%s_receive_%s(), '0/0'), $1)::bigint,
				pg_catalog.pg_%s_%s_diff(pg_catalog.pg_last_%s_replay_%s(), $2)::bigint)
				ELSE pg_catalog.pg_%s_%s_diff(pg_catalog.pg_current_%s_%s(), $3)::bigint
				END`,
				walName, lsnName, walName, lsnName,
				walName, lsnName, walName, lsnName,
				walName, lsnName, walName, lsnName)

			err = db.QueryRow(query, backupStartLSN, backupStartLSN, backupStartLSN).Scan(&diffInBytes)
			if err != nil {
				log.Error().Err(err).Msg("could not determine difference with the leader location")
				if attemptsNo < w.retries {
					attemptsNo++
					time.Sleep(retrySleepInterval)
					continue
				}
				if !w.noLeader {
					return boolPtr(false), nil
				}
				log.Info().Msg("continue with base backup from S3 since leader is not available")
				diffInBytes = 0
			}
			break
		} else {
			// Always try to use WAL-E if leader connection string is not available
			diffInBytes = 0
			break
		}
	}

	// Check thresholds
	isSizeThreshOK := diffInBytes < int64(thresholdMegabytes)*1048576
	thresholdPctBytes := float64(backupSize) * float64(thresholdPercent) / 100.0
	isPercentageThreshOK := float64(diffInBytes) < thresholdPctBytes
	areThresholdsOK := isSizeThreshOK && isPercentageThreshOK

	log.Info().
		Str("threshold_size", reprSize(float64(thresholdMegabytes)*1048576)).
		Int("threshold_percent", thresholdPercent).
		Str("threshold_percent_size", reprSize(thresholdPctBytes)).
		Str("backup_size", reprSize(float64(backupSize))).
		Str("backup_diff", reprSize(float64(diffInBytes))).
		Bool("is_size_thresh_ok", isSizeThreshOK).
		Bool("is_percentage_thresh_ok", isPercentageThreshOK).
		Msg(func() string {
			if areThresholdsOK {
				return "Thresholds are OK, using wal-e basebackup"
			}
			return "wal-e backup size diff is over threshold, falling back to other means of restore"
		}())

	return boolPtr(areThresholdsOK), nil
}

// createReplicaWithS3 restores a replica using WAL-E backup-fetch.
func (w *WALERestore) createReplicaWithS3() int {
	cmd := append(w.walE.Cmd, "backup-fetch", w.dataDir, "LATEST")
	log.Debug().Strs("cmd", cmd).Msg("calling wal-e backup-fetch")

	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		log.Error().Err(err).Msg("Error when fetching backup with WAL-E")
		return ExitRetryLater
	}

	// Fix WAL directory if needed
	walDir := "pg_wal"
	if getMajorVersion(w.dataDir) < 10 {
		walDir = "pg_xlog"
	}

	if !w.fixSubdirectoryPathIfBroken(walDir) {
		return ExitFail
	}

	return ExitSuccess
}

// fixSubdirectoryPathIfBroken fixes broken symlinks for pg_wal/pg_xlog.
func (w *WALERestore) fixSubdirectoryPathIfBroken(dirname string) bool {
	path := filepath.Join(w.dataDir, dirname)

	// Check if path exists
	_, err := os.Stat(path)
	if err == nil {
		return true
	}

	// Check if it's a broken symlink
	if _, err := os.Lstat(path); err == nil {
		// It's a broken symlink, remove it
		target, _ := os.Readlink(path)
		if err := os.Remove(path); err != nil {
			log.Error().Err(err).
				Str("dirname", dirname).
				Str("target", target).
				Msg("could not remove broken symlink")
			return false
		}
	}

	// Create the directory
	if err := os.Mkdir(path, 0700); err != nil {
		log.Error().Err(err).
			Str("dirname", dirname).
			Msg("could not create missing directory path")
		return false
	}

	return true
}

// getMajorVersion reads the PostgreSQL major version from PG_VERSION file.
func getMajorVersion(dataDir string) float64 {
	versionFile := filepath.Join(dataDir, "PG_VERSION")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		log.Error().Err(err).Str("data_dir", dataDir).Msg("Failed to read PG_VERSION")
		return 0.0
	}

	version, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0.0
	}
	return version
}

// reprSize formats a byte count as a human-readable string.
func reprSize(nBytes float64) string {
	if nBytes < 1024 {
		return fmt.Sprintf("%.0f Bytes", nBytes)
	}

	i := -1
	for nBytes > 1023 && i < len(siPrefixes)-1 {
		nBytes /= 1024.0
		i++
	}

	return fmt.Sprintf("%.1f %siB", nBytes, siPrefixes[i])
}

// sizeAsBytes converts a size with SI prefix to bytes.
func sizeAsBytes(size float64, prefix string) int64 {
	prefix = strings.ToUpper(prefix)
	exponent := -1
	for i, p := range siPrefixes {
		if p == prefix {
			exponent = i + 1
			break
		}
	}
	if exponent < 0 {
		return 0
	}

	result := size
	for i := 0; i < exponent; i++ {
		result *= 1024.0
	}
	return int64(result)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func boolPtr(b bool) *bool {
	return &b
}

var (
	scope        string
	role         string
	datadir      string
	connstring   string
	retries      int
	envdir       string
	thresholdMB  int
	thresholdPct int
	useIAM       int
	noLeader     int
)

func main() {
	// Setup logging
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006-01-02 15:04:05",
	}).With().Timestamp().Logger()

	rootCmd := &cobra.Command{
		Use:   "patroni_wale_restore",
		Short: "Script to clone replicas using WAL-E",
		Long: `Clone new replicas using WAL-E restore from S3/Swift.
Falls back to pg_basebackup if WAL-E restore fails or if
the WAL-E backup is too far behind.`,
		Run: run,
	}

	rootCmd.Flags().StringVar(&scope, "scope", "", "Cluster scope (required)")
	rootCmd.Flags().StringVar(&role, "role", "", "Cluster role")
	rootCmd.Flags().StringVar(&datadir, "datadir", "", "PostgreSQL data directory (required)")
	rootCmd.Flags().StringVar(&connstring, "connstring", "", "Leader connection string (required)")
	rootCmd.Flags().IntVar(&retries, "retries", 1, "Number of retries")
	rootCmd.Flags().StringVar(&envdir, "envdir", "", "WAL-E environment directory (required)")
	rootCmd.Flags().IntVar(&thresholdMB, "threshold_megabytes", 10240, "WAL threshold in megabytes")
	rootCmd.Flags().IntVar(&thresholdPct, "threshold_backup_size_percentage", 30, "WAL threshold as backup percentage")
	rootCmd.Flags().IntVar(&useIAM, "use_iam", 0, "Use IAM instance profile (0 or 1)")
	rootCmd.Flags().IntVar(&noLeader, "no_leader", 0, "Continue without leader (0 or 1)")

	rootCmd.MarkFlagRequired("scope")
	rootCmd.MarkFlagRequired("datadir")
	rootCmd.MarkFlagRequired("connstring")
	rootCmd.MarkFlagRequired("envdir")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(ExitFail)
	}
}

func run(cmd *cobra.Command, args []string) {
	if retries < 0 {
		log.Error().Msg("retries must be >= 0")
		os.Exit(ExitFail)
	}

	var exitCode int

	// Retry cloning in a loop
	for i := 0; i <= retries; i++ {
		restore := NewWALERestore(
			scope, datadir, connstring, envdir,
			thresholdMB, thresholdPct, useIAM,
			noLeader == 1, retries,
		)

		exitCode = restore.Run()
		if exitCode != ExitRetryLater {
			log.Debug().Int("exit_code", exitCode).Msg("not retrying")
			break
		}

		time.Sleep(retrySleepInterval)
	}

	os.Exit(exitCode)
}
