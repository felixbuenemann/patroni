// Package main implements the patroni_barman CLI for interacting with Barman via pg-backup-api.
//
// This tool provides commands to:
//   - recover: Perform remote barman backup recovery
//   - config-switch: Switch Barman server configuration
//
// Exit codes:
//   - -1: No command specified
//   - -2: pg-backup-api is not OK
//   - Other: Command-specific exit codes
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Exit codes
const (
	ExitNoCommand = -1
	ExitAPINotOK  = -2
	ExitSuccess   = 0
	ExitError     = 1
)

// Default polling configuration
const (
	DefaultLoopWait = 10 * time.Second
	DefaultRetry    = 2 * time.Minute
)

// PgBackupAPI provides methods to interact with pg-backup-api.
type PgBackupAPI struct {
	baseURL string
	client  *http.Client
	certFile string
	keyFile  string
}

// OperationStatus represents the status of a Barman operation.
type OperationStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// NewPgBackupAPI creates a new pg-backup-api client.
func NewPgBackupAPI(baseURL, certFile, keyFile string) *PgBackupAPI {
	return &PgBackupAPI{
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
		certFile: certFile,
		keyFile:  keyFile,
	}
}

// IsOK checks if the pg-backup-api is available and responding.
func (api *PgBackupAPI) IsOK(ctx context.Context) bool {
	resp, err := api.doRequest(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		log.Error().Err(err).Msg("failed to check pg-backup-api status")
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// GetOperationStatus retrieves the status of a Barman operation.
func (api *PgBackupAPI) GetOperationStatus(ctx context.Context, barmanServer, operationID string) (*OperationStatus, error) {
	path := fmt.Sprintf("/servers/%s/operations/%s", barmanServer, operationID)
	resp, err := api.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status OperationStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode operation status: %w", err)
	}

	return &status, nil
}

// CreateRecoveryOperation creates a new recovery operation.
func (api *PgBackupAPI) CreateRecoveryOperation(ctx context.Context, barmanServer, backupID, sshCommand, dataDir string) (string, error) {
	body := map[string]interface{}{
		"backup_id":              backupID,
		"remote_ssh_command":     sshCommand,
		"destination_directory": dataDir,
	}

	return api.createOperation(ctx, barmanServer, body)
}

// CreateConfigSwitchOperation creates a config-switch operation.
func (api *PgBackupAPI) CreateConfigSwitchOperation(ctx context.Context, barmanServer string, barmanModel *string, reset bool) (string, error) {
	body := map[string]interface{}{}
	if barmanModel != nil {
		body["model_name"] = *barmanModel
	}
	if reset {
		body["reset"] = true
	}

	return api.createOperation(ctx, barmanServer, body)
}

// createOperation creates a new operation on the Barman server.
func (api *PgBackupAPI) createOperation(ctx context.Context, barmanServer string, body map[string]interface{}) (string, error) {
	path := fmt.Sprintf("/servers/%s/operations", barmanServer)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	resp, err := api.doRequest(ctx, http.MethodPost, path, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create operation: %s - %s", resp.Status, string(bodyBytes))
	}

	var result struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.OperationID, nil
}

// doRequest performs an HTTP request to the pg-backup-api.
func (api *PgBackupAPI) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := api.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	return api.client.Do(req)
}

// waitForOperation polls the operation status until completion.
func waitForOperation(ctx context.Context, api *PgBackupAPI, barmanServer, operationID string, loopWait, maxRetry time.Duration) (int, error) {
	deadline := time.Now().Add(maxRetry)

	for {
		status, err := api.GetOperationStatus(ctx, barmanServer, operationID)
		if err != nil {
			log.Error().Err(err).Msg("failed to get operation status")
			return ExitError, err
		}

		switch status.Status {
		case "DONE":
			log.Info().Msg("Operation completed successfully")
			return ExitSuccess, nil
		case "FAILED":
			log.Error().Str("message", status.Message).Msg("Operation failed")
			return ExitError, fmt.Errorf("operation failed: %s", status.Message)
		case "IN_PROGRESS":
			log.Debug().Str("operation_id", operationID).Msg("Operation still in progress")
		default:
			log.Warn().Str("status", status.Status).Msg("Unknown operation status")
		}

		if time.Now().After(deadline) {
			return ExitError, fmt.Errorf("operation timed out after %v", maxRetry)
		}

		select {
		case <-ctx.Done():
			return ExitError, ctx.Err()
		case <-time.After(loopWait):
		}
	}
}

// runBarmanRecover executes the barman recover command.
func runBarmanRecover(api *PgBackupAPI, barmanServer, backupID, sshCommand, dataDir string, loopWait, maxRetry time.Duration) int {
	ctx := context.Background()

	operationID, err := api.CreateRecoveryOperation(ctx, barmanServer, backupID, sshCommand, dataDir)
	if err != nil {
		log.Error().Err(err).Msg("failed to create recovery operation")
		return ExitError
	}

	log.Info().Str("operation_id", operationID).Msg("Recovery operation created")

	exitCode, err := waitForOperation(ctx, api, barmanServer, operationID, loopWait, maxRetry)
	if err != nil {
		log.Error().Err(err).Msg("recovery operation failed")
	}

	return exitCode
}

// runBarmanConfigSwitch executes the barman config-switch command.
func runBarmanConfigSwitch(api *PgBackupAPI, barmanServer string, barmanModel *string, reset bool, loopWait, maxRetry time.Duration) int {
	ctx := context.Background()

	// Validate that exactly one of barmanModel or reset is provided
	hasModel := barmanModel != nil && *barmanModel != ""
	if hasModel == reset {
		log.Error().
			Str("barman_model", func() string {
				if barmanModel != nil {
					return *barmanModel
				}
				return ""
			}()).
			Bool("reset", reset).
			Msg("One, and only one among 'barman_model' and 'reset' should be given")
		return ExitError
	}

	operationID, err := api.CreateConfigSwitchOperation(ctx, barmanServer, barmanModel, reset)
	if err != nil {
		log.Error().Err(err).Msg("failed to create config-switch operation")
		return ExitError
	}

	log.Info().Str("operation_id", operationID).Msg("Config-switch operation created")

	exitCode, err := waitForOperation(ctx, api, barmanServer, operationID, loopWait, maxRetry)
	if err != nil {
		log.Error().Err(err).Msg("config-switch operation failed")
	}

	return exitCode
}

var (
	// Global flags
	apiURL   string
	certFile string
	keyFile  string

	// Recover flags
	recoverBarmanServer string
	recoverBackupID     string
	recoverSSHCommand   string
	recoverDataDir      string
	recoverLoopWait     time.Duration
	recoverMaxRetry     time.Duration

	// Config-switch flags
	configBarmanServer string
	configBarmanModel  string
	configReset        bool
	configLoopWait     time.Duration
	configMaxRetry     time.Duration
)

func main() {
	// Setup logging
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006-01-02 15:04:05",
	}).With().Timestamp().Logger()

	rootCmd := &cobra.Command{
		Use:   "patroni_barman",
		Short: "Perform operations on Barman through pg-backup-api",
		Long: `patroni_barman is a CLI tool that interacts with Barman through
the pg-backup-api to perform backup recovery and configuration operations.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Validate API URL
			if apiURL == "" {
				log.Error().Msg("--api-url is required")
				os.Exit(ExitError)
			}
		},
	}

	// Global flags
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "pg-backup-api base URL (required)")
	rootCmd.PersistentFlags().StringVar(&certFile, "cert-file", "", "Client certificate file for TLS")
	rootCmd.PersistentFlags().StringVar(&keyFile, "key-file", "", "Client key file for TLS")

	// Recover command
	recoverCmd := &cobra.Command{
		Use:   "recover",
		Short: "Remote 'barman recover'",
		Long:  "Perform a remote barman backup recovery through pg-backup-api.",
		Run: func(cmd *cobra.Command, args []string) {
			api := NewPgBackupAPI(apiURL, certFile, keyFile)

			if !api.IsOK(context.Background()) {
				log.Error().Msg("pg-backup-api is not available")
				os.Exit(ExitAPINotOK)
			}

			exitCode := runBarmanRecover(api, recoverBarmanServer, recoverBackupID,
				recoverSSHCommand, recoverDataDir, recoverLoopWait, recoverMaxRetry)
			os.Exit(exitCode)
		},
	}

	recoverCmd.Flags().StringVar(&recoverBarmanServer, "barman-server", "", "Barman server name (required)")
	recoverCmd.Flags().StringVar(&recoverBackupID, "backup-id", "", "Backup ID to recover (required)")
	recoverCmd.Flags().StringVar(&recoverSSHCommand, "ssh-command", "", "Remote SSH command for barman recover")
	recoverCmd.Flags().StringVar(&recoverDataDir, "data-dir", "", "Destination directory for recovery (required)")
	recoverCmd.Flags().DurationVar(&recoverLoopWait, "loop-wait", DefaultLoopWait, "Polling interval")
	recoverCmd.Flags().DurationVar(&recoverMaxRetry, "max-retry", DefaultRetry, "Maximum time to wait for operation")

	recoverCmd.MarkFlagRequired("barman-server")
	recoverCmd.MarkFlagRequired("backup-id")
	recoverCmd.MarkFlagRequired("data-dir")

	rootCmd.AddCommand(recoverCmd)

	// Config-switch command
	configSwitchCmd := &cobra.Command{
		Use:   "config-switch",
		Short: "Remote 'barman config-switch'",
		Long:  "Switch Barman server configuration through pg-backup-api.",
		Run: func(cmd *cobra.Command, args []string) {
			api := NewPgBackupAPI(apiURL, certFile, keyFile)

			if !api.IsOK(context.Background()) {
				log.Error().Msg("pg-backup-api is not available")
				os.Exit(ExitAPINotOK)
			}

			var modelPtr *string
			if configBarmanModel != "" {
				modelPtr = &configBarmanModel
			}

			exitCode := runBarmanConfigSwitch(api, configBarmanServer, modelPtr,
				configReset, configLoopWait, configMaxRetry)
			os.Exit(exitCode)
		},
	}

	configSwitchCmd.Flags().StringVar(&configBarmanServer, "barman-server", "", "Barman server name (required)")
	configSwitchCmd.Flags().StringVar(&configBarmanModel, "barman-model", "", "Barman model name to apply")
	configSwitchCmd.Flags().BoolVar(&configReset, "reset", false, "Reset to default configuration")
	configSwitchCmd.Flags().DurationVar(&configLoopWait, "loop-wait", DefaultLoopWait, "Polling interval")
	configSwitchCmd.Flags().DurationVar(&configMaxRetry, "max-retry", DefaultRetry, "Maximum time to wait for operation")

	configSwitchCmd.MarkFlagRequired("barman-server")

	rootCmd.AddCommand(configSwitchCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(ExitError)
	}

	// If no command was specified
	if len(os.Args) == 1 {
		os.Exit(ExitNoCommand)
	}
}
