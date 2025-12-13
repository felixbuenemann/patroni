// Package raft provides the Raft DCS implementation and controller.
// This file contains the RaftController for running the Raft consensus daemon.
package raft

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/patroni/patroni-go/internal/dcs"
)

// Errors
var (
	ErrNoSelfAddr = errors.New("self_addr is required")
)

// ControllerConfig holds Raft controller configuration.
type ControllerConfig struct {
	Raft struct {
		SelfAddr       string   `yaml:"self_addr"`
		Partner        []string `yaml:"partner_addrs"`
		DataDir        string   `yaml:"data_dir"`
		AutoTickPeriod float64  `yaml:"auto_tick_period"`
	} `yaml:"raft"`
}

// Controller manages a Raft consensus node.
type Controller struct {
	config *ControllerConfig
	raft   *Raft
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewController creates a new Raft controller.
func NewController(configPath string) (*Controller, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config ControllerConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if config.Raft.SelfAddr == "" {
		return nil, ErrNoSelfAddr
	}

	if config.Raft.AutoTickPeriod == 0 {
		config.Raft.AutoTickPeriod = 0.1 // 100ms default
	}

	return &Controller{
		config: &config,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}, nil
}

// Start initializes and starts the Raft controller.
func (c *Controller) Start() error {
	// Build hosts list with self address first
	hosts := []string{c.config.Raft.SelfAddr}
	hosts = append(hosts, c.config.Raft.Partner...)

	cfg := &dcs.Config{
		Hosts:     hosts,
		Namespace: c.config.Raft.DataDir,
		Name:      c.config.Raft.SelfAddr, // Use address as node name
	}

	raftDCS, err := New(cfg)
	if err != nil {
		return err
	}

	// Type assertion to get the concrete *Raft type
	raft, ok := raftDCS.(*Raft)
	if !ok {
		return errors.New("unexpected DCS type returned from New")
	}
	c.raft = raft

	go c.runLoop()
	return nil
}

// runLoop runs the main tick loop.
func (c *Controller) runLoop() {
	defer close(c.doneCh)

	tickDuration := time.Duration(c.config.Raft.AutoTickPeriod * float64(time.Second))
	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			// The hashicorp/raft library handles ticking internally,
			// but we keep this loop for any periodic maintenance tasks
			// and to check leadership status
			if c.raft.IsLeader() {
				log.Debug().Msg("This node is the Raft leader")
			}
		}
	}
}

// Stop stops the Raft controller.
func (c *Controller) Stop() {
	close(c.stopCh)
	<-c.doneCh

	if c.raft != nil {
		c.raft.Close()
	}
}

// Wait waits for the controller to stop.
func (c *Controller) Wait() {
	<-c.doneCh
}

// RunRaftController is the entry point for the raft controller command.
func RunRaftController(configFile string) error {
	controller, err := NewController(configFile)
	if err != nil {
		return err
	}

	if err := controller.Start(); err != nil {
		return err
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info().Msg("Shutting down raft controller")
	controller.Stop()

	return nil
}

// NewRaftControllerCommand creates the cobra command for the raft controller.
func NewRaftControllerCommand() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:   "patroni_raft_controller",
		Short: "Run the Patroni Raft controller daemon",
		Long: `The Raft controller manages a Raft consensus node for Patroni.
It provides distributed consensus without requiring external DCS like etcd or ZooKeeper.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Setup logging
			log.Logger = zerolog.New(zerolog.ConsoleWriter{
				Out:        os.Stderr,
				TimeFormat: "2006-01-02 15:04:05",
			}).With().Timestamp().Logger()

			return RunRaftController(configFile)
		},
	}

	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to configuration file (required)")
	cmd.MarkFlagRequired("config")

	return cmd
}

// Main is the entry point for the standalone raft controller binary.
func Main() {
	ctx := context.Background()
	if err := NewRaftControllerCommand().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
