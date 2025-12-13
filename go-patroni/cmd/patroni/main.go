// Package main implements the Patroni daemon.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/patroni/patroni-go/internal/api"
	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/dcs"
	_ "github.com/patroni/patroni-go/internal/dcs/etcd3" // Register etcd3 DCS
	"github.com/patroni/patroni-go/internal/ha"
	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/pkg/types"
)

var (
	configFile string
	logLevel   string
	version    bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "patroni",
		Short: "Patroni - PostgreSQL High Availability",
		Long:  `Patroni is a template for PostgreSQL HA using distributed consensus stores.`,
		RunE:  run,
	}

	rootCmd.Flags().StringVarP(&configFile, "config", "c", "", "Configuration file path")
	rootCmd.Flags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error)")
	rootCmd.Flags().BoolVarP(&version, "version", "v", false, "Print version and exit")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	if version {
		fmt.Printf("Patroni %s (Go)\n", types.Version)
		return nil
	}

	// Setup logging
	setupLogging(logLevel)

	log.Info().
		Str("version", types.Version).
		Msg("Starting Patroni")

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	log.Info().
		Str("scope", cfg.Scope).
		Str("name", cfg.Name).
		Msg("Configuration loaded")

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Initialize DCS
	dcsType, dcsConfig := cfg.GetDCSType()
	log.Info().Str("type", dcsType).Msg("Initializing DCS")

	dcsClient, err := dcs.New(dcsType, dcsConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize DCS")
	}
	defer dcsClient.Close()

	// Initialize PostgreSQL manager
	pg, err := postgresql.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize PostgreSQL manager")
	}
	defer pg.Close()

	// Initialize HA state machine
	haInstance := ha.New(cfg, dcsClient, pg)

	// Initialize REST API server
	apiServer := api.New(cfg, haInstance, pg)
	if err := apiServer.Start(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start API server")
	}
	defer apiServer.Stop(ctx)

	// Start HA loop in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- haInstance.Run(ctx)
	}()

	// Main loop - handle signals
	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				log.Info().Msg("Received SIGHUP, reloading configuration")
				// TODO: Implement config reload
			case syscall.SIGINT, syscall.SIGTERM:
				log.Info().Msg("Received shutdown signal")
				return shutdown(ctx, haInstance, pg, apiServer)
			}
		case err := <-errCh:
			if err != nil {
				log.Error().Err(err).Msg("HA loop error")
			}
			return err
		}
	}
}

func setupLogging(level string) {
	// Setup pretty console output
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	}

	log.Logger = zerolog.New(output).With().Timestamp().Logger()

	// Set log level
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func shutdown(ctx context.Context, haInstance *ha.HA, pg *postgresql.Postgresql, apiServer *api.Server) error {
	log.Info().Msg("Initiating graceful shutdown")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Stop HA loop
	haInstance.Stop()

	// Stop API server
	if err := apiServer.Stop(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("Error stopping API server")
	}

	// If we're the leader, release the lock
	if haInstance.IsLeader() {
		log.Info().Msg("Releasing leader lock")
		// The DCS client will handle this on close
	}

	log.Info().Msg("Shutdown complete")
	return nil
}
