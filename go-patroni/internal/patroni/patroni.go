// Package patroni implements the main Patroni daemon orchestration.
package patroni

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/api"
	"github.com/patroni/patroni-go/internal/callback"
	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/internal/ha"
	patroniLog "github.com/patroni/patroni-go/internal/log"
	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/internal/watchdog"
	"github.com/patroni/patroni-go/pkg/types"
)

// Version is the Patroni version.
const Version = "4.0.0-go"

// Patroni is the main daemon orchestrator.
type Patroni struct {
	mu sync.RWMutex

	// Configuration
	config *config.Config

	// Components
	dcs        dcs.DCS
	postgresql *postgresql.Postgresql
	ha         *ha.HA
	api        *api.Server
	watchdog   *watchdog.Watchdog
	logger     *patroniLog.Logger
	callbacks  *callback.Executor

	// State
	tags             map[string]interface{}
	nextRun          time.Time
	scheduledRestart *ScheduledRestart
	running          bool

	// Control channels
	stopCh   chan struct{}
	doneCh   chan struct{}
	reloadCh chan struct{}
}

// ScheduledRestart represents a scheduled restart operation.
type ScheduledRestart struct {
	Schedule            time.Time
	PostmasterStartTime time.Time
	Pending             bool
}

// Options holds initialization options for Patroni.
type Options struct {
	ConfigFile string
	Validate   bool
}

// New creates a new Patroni instance.
func New(cfg *config.Config) (*Patroni, error) {
	p := &Patroni{
		config:   cfg,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		reloadCh: make(chan struct{}, 1),
		tags:     make(map[string]interface{}),
	}

	// Initialize logger
	logCfg := patroniLog.DefaultConfig()
	if cfg.Log.Level != "" {
		logCfg.Level = patroniLog.ParseLevel(cfg.Log.Level)
	}
	if cfg.Log.Dir != "" {
		logCfg.Dir = cfg.Log.Dir
	}
	p.logger = patroniLog.New(logCfg)
	p.logger.Start()

	// Initialize callback executor
	p.callbacks = callback.NewExecutor()

	// Get DCS type and config
	dcsType, dcsCfg := cfg.GetDCSType()
	if dcsCfg == nil {
		return nil, fmt.Errorf("no DCS configured")
	}

	// Merge in scope and name
	dcsCfg.Scope = cfg.Scope
	dcsCfg.Namespace = cfg.Namespace
	dcsCfg.Name = cfg.Name
	dcsCfg.TTL = cfg.TTL

	var err error
	p.dcs, err = dcs.New(dcsType, dcsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DCS: %w", err)
	}

	// Ensure DCS access and get initial cluster state
	cluster, err := p.ensureDCSAccess(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to get initial cluster state: %w", err)
	}

	// Check for unique name
	if err := p.ensureUniqueName(cluster); err != nil {
		return nil, err
	}

	// Initialize watchdog
	wdCfg := &watchdog.Config{
		Mode:         cfg.Watchdog.Mode,
		Device:       cfg.Watchdog.Device,
		SafetyMargin: time.Duration(cfg.Watchdog.Safety) * time.Second,
	}
	p.watchdog, err = watchdog.New(wdCfg)
	if err != nil {
		log.Warn().Err(err).Msg("Watchdog initialization failed, continuing without watchdog")
	}

	// Initialize PostgreSQL
	p.postgresql, err = postgresql.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PostgreSQL: %w", err)
	}

	// Initialize HA
	p.ha = ha.New(cfg, p.dcs, p.postgresql)

	// Initialize API server
	p.api = api.NewServer(p.ha, p.postgresql, &cfg.RestAPI)

	// Load tags
	p.tags = p.filterTags(cfg.Tags)

	return p, nil
}

// ensureDCSAccess continuously attempts to retrieve cluster from DCS.
func (p *Patroni) ensureDCSAccess(sleepTime time.Duration) (*types.Cluster, error) {
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cluster, err := p.dcs.GetCluster(ctx)
		cancel()

		if err == nil {
			return cluster, nil
		}

		log.Warn().Err(err).Msg("Can not get cluster from DCS")
		time.Sleep(sleepTime)
	}

	return nil, fmt.Errorf("failed to connect to DCS after multiple retries")
}

// ensureUniqueName prevents splitbrain from operator naming error.
func (p *Patroni) ensureUniqueName(cluster *types.Cluster) error {
	if cluster == nil {
		return nil
	}

	member := cluster.GetMember(p.config.Name)
	if member == nil {
		return nil
	}

	// Try to connect to the existing member
	// If successful, it means there's already a node with the same name
	// This is a simplified check - in production you'd make an HTTP request
	if member.Data.APIURL != "" {
		log.Fatal().Str("name", p.config.Name).
			Msg("Can't start; there is already a node with this name running")
		os.Exit(1)
	}

	return nil
}

// filterTags filters configuration tags.
func (p *Patroni) filterTags(tags map[string]interface{}) map[string]interface{} {
	if tags == nil {
		return make(map[string]interface{})
	}

	result := make(map[string]interface{})
	for k, v := range tags {
		// Remove false noloadbalance tag
		if k == "noloadbalance" {
			if b, ok := v.(bool); ok && !b {
				continue
			}
		}
		result[k] = v
	}
	return result
}

// Run starts the Patroni daemon main loop.
func (p *Patroni) Run() error {
	p.mu.Lock()
	p.running = true
	p.mu.Unlock()

	// Start API server
	go p.api.Start()

	// Set up signal handlers
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	p.nextRun = time.Now()

	log.Info().Str("version", Version).Msg("Patroni starting")

	defer close(p.doneCh)

	loopWait := time.Duration(p.config.LoopWait) * time.Second
	if loopWait == 0 {
		loopWait = 10 * time.Second
	}

	for {
		select {
		case <-p.stopCh:
			log.Info().Msg("Patroni stopping")
			p.shutdown()
			return nil

		case sig := <-sigCh:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
				p.shutdown()
				return nil
			case syscall.SIGHUP:
				log.Info().Msg("Received SIGHUP, reloading configuration")
				p.reloadConfig(true)
			}

		case <-p.reloadCh:
			p.reloadConfig(false)

		default:
			// Run HA cycle
			p.runCycle()
			p.scheduleNextRun(loopWait)
		}
	}
}

// runCycle runs a single HA cycle.
func (p *Patroni) runCycle() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run HA cycle
	p.ha.Wakeup()

	// Check for dynamic configuration changes
	if p.dcs != nil {
		cluster, err := p.dcs.GetCluster(ctx)
		if err == nil && cluster != nil && cluster.Config != nil {
			// Config changes are handled in HA loop
			log.Debug().Msg("Cluster config available")
		}
	}
}

// scheduleNextRun schedules the next HA cycle.
func (p *Patroni) scheduleNextRun(loopWait time.Duration) {
	p.nextRun = p.nextRun.Add(loopWait)

	currentTime := time.Now()
	napTime := p.nextRun.Sub(currentTime)

	if napTime <= 0 {
		p.nextRun = currentTime
		time.Sleep(time.Millisecond) // Yield
		log.Warn().Msg("Loop time exceeded, rescheduling immediately")
	} else {
		time.Sleep(napTime)
	}
}

// reloadConfig reloads configuration.
func (p *Patroni) reloadConfig(sighup bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	log.Debug().Bool("sighup", sighup).Msg("Reloading configuration")
	p.tags = p.filterTags(p.config.Tags)
}

// shutdown performs graceful shutdown.
func (p *Patroni) shutdown() {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	log.Info().Msg("Shutting down Patroni")

	// Stop API server
	if p.api != nil {
		if err := p.api.Stop(); err != nil {
			log.Error().Err(err).Msg("Error stopping API server")
		}
	}

	// Stop HA
	if p.ha != nil {
		p.ha.Stop()
	}

	// Stop callbacks
	if p.callbacks != nil {
		p.callbacks.Stop()
	}

	// Stop logger
	if p.logger != nil {
		p.logger.Shutdown()
	}

	// Close DCS
	if p.dcs != nil {
		p.dcs.Close()
	}
}

// Stop stops the Patroni daemon.
func (p *Patroni) Stop() {
	close(p.stopCh)
	<-p.doneCh
}

// wakeupHA wakes up the HA loop.
func (p *Patroni) wakeupHA() {
	select {
	case p.reloadCh <- struct{}{}:
	default:
	}
}

// Tags returns the configured tags.
func (p *Patroni) Tags() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tags
}

// IsRunning returns true if Patroni is running.
func (p *Patroni) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// Main is the main entry point for the patroni command.
func Main(configFile string) error {
	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create and run Patroni
	p, err := New(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize Patroni: %w", err)
	}

	return p.Run()
}
