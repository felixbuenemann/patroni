// Package ha implements the high availability state machine.
package ha

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/pkg/types"
)

// HAState represents the current state of the HA state machine.
type HAState int

const (
	HAStateStarting HAState = iota
	HAStateRunning
	HAStatePaused
	HAStateStopped
)

func (s HAState) String() string {
	states := []string{"starting", "running", "paused", "stopped"}
	if int(s) < len(states) {
		return states[s]
	}
	return "unknown"
}

// HA implements the high availability state machine.
type HA struct {
	mu sync.RWMutex

	state  HAState
	config *config.Config
	dcs    dcs.DCS
	pg     *postgresql.Postgresql

	// Cluster state
	cluster *types.Cluster

	// Control channels
	wakeupCh chan struct{}
	stopCh   chan struct{}

	// State tracking
	isLeader         bool
	leaderTimeline   int
	lastLoopTime     time.Time
	busy             bool
	scheduledRestart *ScheduledRestart

	// Failsafe state
	failsafe *Failsafe
}

// New creates a new HA instance.
func New(cfg *config.Config, d dcs.DCS, pg *postgresql.Postgresql) *HA {
	return &HA{
		state:    HAStateStarting,
		config:   cfg,
		dcs:      d,
		pg:       pg,
		wakeupCh: make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		failsafe: NewFailsafe(d),
	}
}

// Run starts the HA main loop.
func (ha *HA) Run(ctx context.Context) error {
	log.Info().Msg("Starting HA state machine")
	ha.setState(HAStateRunning)

	loopWait := time.Duration(ha.config.LoopWait) * time.Second
	if loopWait == 0 {
		loopWait = 10 * time.Second
	}

	ticker := time.NewTicker(loopWait)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ha.setState(HAStateStopped)
			return ctx.Err()
		case <-ha.stopCh:
			ha.setState(HAStateStopped)
			return nil
		case <-ha.wakeupCh:
			ha.runCycle(ctx)
		case <-ticker.C:
			ha.runCycle(ctx)
		}
	}
}

// runCycle runs a single HA loop iteration.
func (ha *HA) runCycle(ctx context.Context) {
	ha.mu.Lock()
	defer ha.mu.Unlock()

	if ha.state == HAStatePaused {
		return
	}

	startTime := time.Now()
	log.Debug().Msg("Starting HA cycle")

	// Fetch cluster state from DCS
	cluster, err := ha.dcs.GetCluster(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get cluster from DCS")
		ha.handleDCSError(ctx)
		return
	}
	ha.cluster = cluster

	// Update member status in DCS
	if err := ha.touchMember(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to update member in DCS")
	}

	// Handle cluster state
	action := ha.determineAction(ctx)
	if err := ha.executeAction(ctx, action); err != nil {
		log.Error().Err(err).Str("action", string(action)).Msg("Failed to execute action")
	}

	ha.lastLoopTime = time.Now()
	log.Debug().Dur("duration", time.Since(startTime)).Msg("HA cycle completed")
}

// Action represents an action to be taken by the HA state machine.
type Action string

const (
	ActionNone            Action = "none"
	ActionBootstrap       Action = "bootstrap"
	ActionStartAsPrimary  Action = "start_as_primary"
	ActionStartAsReplica  Action = "start_as_replica"
	ActionAcquireLock     Action = "acquire_lock"
	ActionRenewLock       Action = "renew_lock"
	ActionFollowLeader    Action = "follow_leader"
	ActionPromote         Action = "promote"
	ActionDemote          Action = "demote"
	ActionRestart         Action = "restart"
	ActionFailover        Action = "failover"
	ActionReinitialize    Action = "reinitialize"
)

// determineAction determines what action should be taken based on current state.
func (ha *HA) determineAction(ctx context.Context) Action {
	cluster := ha.cluster
	pgState := ha.pg.State()
	pgRole := ha.pg.Role()

	log.Debug().
		Str("pg_state", pgState.String()).
		Str("pg_role", pgRole.String()).
		Bool("cluster_initialized", cluster.IsInitialized()).
		Bool("has_leader", cluster.HasLeader()).
		Msg("Determining action")

	// Case 1: Cluster is not initialized
	if !cluster.IsInitialized() {
		if ha.pg.DataDir() != "" && !ha.pg.IsRunning() {
			if pgRole == types.PostgresqlRoleUninitialized {
				return ActionBootstrap
			}
		}
		return ActionNone
	}

	// Case 2: PostgreSQL is not running
	if !ha.pg.IsRunning() {
		if pgRole == types.PostgresqlRoleUninitialized {
			// Need to create replica
			if cluster.HasLeader() {
				return ActionStartAsReplica
			}
			return ActionNone
		}

		// Start with appropriate role
		if cluster.HasLeader() {
			leader := cluster.GetLeaderMember()
			if leader != nil && leader.Name == ha.config.Name {
				return ActionStartAsPrimary
			}
			return ActionStartAsReplica
		}

		// No leader, try to become leader
		return ActionStartAsPrimary
	}

	// Case 3: PostgreSQL is running
	if cluster.HasLeader() {
		leaderMember := cluster.GetLeaderMember()

		if leaderMember != nil && leaderMember.Name == ha.config.Name {
			// We are the leader
			ha.isLeader = true
			return ActionRenewLock
		}

		// We are not the leader
		ha.isLeader = false

		if ha.pg.IsPrimary() {
			// We think we're primary but we're not the leader
			return ActionDemote
		}

		return ActionFollowLeader
	}

	// Case 4: No leader exists
	if ha.pg.IsPrimary() {
		return ActionAcquireLock
	}

	// Check if we can become leader
	if ha.canPromote(ctx) {
		return ActionPromote
	}

	return ActionNone
}

// executeAction executes the determined action.
func (ha *HA) executeAction(ctx context.Context, action Action) error {
	log.Info().Str("action", string(action)).Msg("Executing action")

	switch action {
	case ActionNone:
		return nil

	case ActionBootstrap:
		return ha.doBootstrap(ctx)

	case ActionStartAsPrimary:
		return ha.doStartAsPrimary(ctx)

	case ActionStartAsReplica:
		return ha.doStartAsReplica(ctx)

	case ActionAcquireLock:
		return ha.doAcquireLock(ctx)

	case ActionRenewLock:
		return ha.doRenewLock(ctx)

	case ActionFollowLeader:
		return ha.doFollowLeader(ctx)

	case ActionPromote:
		return ha.doPromote(ctx)

	case ActionDemote:
		return ha.doDemote(ctx)

	case ActionRestart:
		return ha.doRestart(ctx)

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// doBootstrap initializes a new cluster.
func (ha *HA) doBootstrap(ctx context.Context) error {
	log.Info().Msg("Bootstrapping new cluster")

	// Try to acquire the initialize lock
	sysID := fmt.Sprintf("%d", time.Now().UnixNano())
	acquired, err := ha.dcs.Initialize(ctx, sysID)
	if err != nil {
		return fmt.Errorf("failed to initialize DCS: %w", err)
	}

	if !acquired {
		log.Info().Msg("Another node is bootstrapping the cluster")
		return nil
	}

	// Bootstrap PostgreSQL
	if err := ha.pg.Bootstrap(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap PostgreSQL: %w", err)
	}

	// Acquire leader lock
	acquired, err = ha.dcs.TakeLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire leader lock: %w", err)
	}

	if acquired {
		ha.isLeader = true
		log.Info().Msg("Successfully bootstrapped cluster and acquired leadership")
	}

	return nil
}

// doStartAsPrimary starts PostgreSQL as primary.
func (ha *HA) doStartAsPrimary(ctx context.Context) error {
	log.Info().Msg("Starting PostgreSQL as primary")

	if err := ha.pg.Start(ctx); err != nil {
		return fmt.Errorf("failed to start PostgreSQL: %w", err)
	}

	// Try to acquire leader lock
	return ha.doAcquireLock(ctx)
}

// doStartAsReplica starts PostgreSQL as a replica.
func (ha *HA) doStartAsReplica(ctx context.Context) error {
	log.Info().Msg("Starting PostgreSQL as replica")

	// Get leader connection info
	leader := ha.cluster.GetLeaderMember()
	if leader == nil {
		return fmt.Errorf("no leader available")
	}

	primaryConnInfo := ha.buildPrimaryConnInfo(leader)

	// Check if we need to clone
	if ha.pg.Role() == types.PostgresqlRoleUninitialized {
		if err := ha.pg.Clone(ctx, primaryConnInfo); err != nil {
			return fmt.Errorf("failed to clone from primary: %w", err)
		}
	}

	// Start PostgreSQL
	if err := ha.pg.Start(ctx); err != nil {
		return fmt.Errorf("failed to start PostgreSQL: %w", err)
	}

	return nil
}

// doAcquireLock tries to acquire the leader lock.
func (ha *HA) doAcquireLock(ctx context.Context) error {
	log.Info().Msg("Attempting to acquire leader lock")

	acquired, err := ha.dcs.TakeLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	if acquired {
		ha.isLeader = true
		log.Info().Msg("Successfully acquired leader lock")

		// Update leader info in DCS
		if err := ha.dcs.UpdateLeader(ctx, ha.pg.GetMemberData()); err != nil {
			log.Warn().Err(err).Msg("Failed to update leader info")
		}
	} else {
		log.Info().Msg("Failed to acquire leader lock - another node is leader")
	}

	return nil
}

// doRenewLock renews the leader lock.
func (ha *HA) doRenewLock(ctx context.Context) error {
	log.Debug().Msg("Renewing leader lock")

	acquired, err := ha.dcs.AttemptToAcquireOrRenewLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to renew lock: %w", err)
	}

	if !acquired {
		ha.isLeader = false
		log.Warn().Msg("Lost leader lock")
		return ha.doDemote(ctx)
	}

	// Update leader info
	if err := ha.dcs.UpdateLeader(ctx, ha.pg.GetMemberData()); err != nil {
		log.Warn().Err(err).Msg("Failed to update leader info")
	}

	return nil
}

// doFollowLeader ensures we're following the current leader.
func (ha *HA) doFollowLeader(ctx context.Context) error {
	leader := ha.cluster.GetLeaderMember()
	if leader == nil {
		return nil
	}

	log.Debug().Str("leader", leader.Name).Msg("Following leader")

	// Ensure we have the correct primary_conninfo
	// This would normally update the recovery configuration

	return nil
}

// doPromote promotes this node to primary.
func (ha *HA) doPromote(ctx context.Context) error {
	log.Info().Msg("Promoting to primary")

	// Acquire leader lock first
	acquired, err := ha.dcs.TakeLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for promotion: %w", err)
	}

	if !acquired {
		log.Info().Msg("Failed to acquire lock - another node became leader")
		return nil
	}

	// Promote PostgreSQL
	if err := ha.pg.Promote(ctx); err != nil {
		// Release the lock if promotion fails
		ha.dcs.DeleteLeader(ctx)
		return fmt.Errorf("failed to promote PostgreSQL: %w", err)
	}

	ha.isLeader = true
	log.Info().Msg("Successfully promoted to primary")

	return nil
}

// doDemote demotes this node from primary to replica.
func (ha *HA) doDemote(ctx context.Context) error {
	log.Info().Msg("Demoting to replica")

	leader := ha.cluster.GetLeaderMember()
	if leader == nil {
		return fmt.Errorf("no leader to follow")
	}

	primaryConnInfo := ha.buildPrimaryConnInfo(leader)

	if err := ha.pg.Demote(ctx, primaryConnInfo); err != nil {
		return fmt.Errorf("failed to demote: %w", err)
	}

	ha.isLeader = false
	return nil
}

// doRestart restarts PostgreSQL.
func (ha *HA) doRestart(ctx context.Context) error {
	log.Info().Msg("Restarting PostgreSQL")
	return ha.pg.Restart(ctx)
}

// touchMember updates this member's entry in DCS.
func (ha *HA) touchMember(ctx context.Context) error {
	return ha.dcs.TouchMember(ctx, ha.pg.GetMemberData())
}

// handleDCSError handles errors when communicating with DCS.
func (ha *HA) handleDCSError(ctx context.Context) {
	// If we're the leader and can't reach DCS, we need to decide what to do
	if ha.isLeader {
		// Check failsafe mode
		if ha.failsafe.IsActive() {
			log.Warn().Msg("DCS unreachable but failsafe mode active - continuing as leader")
			return
		}

		log.Warn().Msg("DCS unreachable - may need to demote if this persists")
	}
}

// canPromote checks if this node can be promoted to leader.
func (ha *HA) canPromote(ctx context.Context) bool {
	// Check if we have the most recent data
	timeline, walPos := ha.pg.TimelineWALPosition()
	if timeline == 0 || walPos == 0 {
		return false
	}

	// Check tags
	if tags := ha.getEffectiveTags(); tags.NoFailover {
		return false
	}

	// Compare with other members
	// In a full implementation, we would query other members via REST API
	// to compare WAL positions

	return true
}

// buildPrimaryConnInfo builds a connection string for connecting to the primary.
func (ha *HA) buildPrimaryConnInfo(leader *types.Member) string {
	kwargs := leader.GetConnKwargs()
	if kwargs == nil {
		return ""
	}

	host := kwargs["host"]
	port := kwargs["port"]
	if port == "" {
		port = "5432"
	}

	repl := ha.config.PostgreSQL.Authentication.Replication
	user := repl.Username
	if user == "" {
		user = "postgres"
	}

	connInfo := fmt.Sprintf("host=%s port=%s user=%s application_name=%s",
		host, port, user, ha.config.Name)

	if repl.Password != "" {
		connInfo += fmt.Sprintf(" password=%s", repl.Password)
	}

	if repl.SSLMode != "" {
		connInfo += fmt.Sprintf(" sslmode=%s", repl.SSLMode)
	}

	return connInfo
}

// getEffectiveTags returns the effective tags for this node.
func (ha *HA) getEffectiveTags() types.Tags {
	tags := types.Tags{}
	if ha.config.Tags != nil {
		if v, ok := ha.config.Tags["nofailover"].(bool); ok {
			tags.NoFailover = v
		}
		if v, ok := ha.config.Tags["noloadbalance"].(bool); ok {
			tags.NoLoadbalance = v
		}
		if v, ok := ha.config.Tags["clonefrom"].(bool); ok {
			tags.CloneFrom = v
		}
		if v, ok := ha.config.Tags["nosync"].(bool); ok {
			tags.NoSync = v
		}
	}
	return tags
}

// GetEffectiveTags returns the effective tags (exported version).
func (ha *HA) GetEffectiveTags() types.Tags {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.getEffectiveTags()
}

// setState sets the HA state.
func (ha *HA) setState(state HAState) {
	ha.state = state
}

// State returns the current HA state.
func (ha *HA) State() HAState {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.state
}

// IsLeader returns true if this node is the leader.
func (ha *HA) IsLeader() bool {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.isLeader
}

// GetCluster returns the current cluster state.
func (ha *HA) GetCluster() *types.Cluster {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.cluster
}

// Wakeup wakes up the HA loop to run immediately.
func (ha *HA) Wakeup() {
	select {
	case ha.wakeupCh <- struct{}{}:
	default:
	}
}

// Stop stops the HA loop.
func (ha *HA) Stop() {
	close(ha.stopCh)
}

// Pause pauses the HA loop.
func (ha *HA) Pause() {
	ha.mu.Lock()
	defer ha.mu.Unlock()
	ha.state = HAStatePaused
	log.Info().Msg("HA loop paused")
}

// Resume resumes the HA loop.
func (ha *HA) Resume() {
	ha.mu.Lock()
	defer ha.mu.Unlock()
	ha.state = HAStateRunning
	log.Info().Msg("HA loop resumed")
	ha.Wakeup()
}

// IsPaused returns true if the HA loop is paused.
func (ha *HA) IsPaused() bool {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.state == HAStatePaused
}

// ScheduledRestart represents a scheduled restart.
type ScheduledRestart struct {
	Schedule            time.Time `json:"schedule"`
	PostmasterStartTime time.Time `json:"postmaster_start_time,omitempty"`
	Pending             bool      `json:"pending"`
}

// ScheduleRestart schedules a restart at the specified time.
func (ha *HA) ScheduleRestart(schedule time.Time, postmasterStartTime time.Time) error {
	ha.mu.Lock()
	defer ha.mu.Unlock()

	ha.scheduledRestart = &ScheduledRestart{
		Schedule:            schedule,
		PostmasterStartTime: postmasterStartTime,
		Pending:             true,
	}

	log.Info().Time("schedule", schedule).Msg("Restart scheduled")
	return nil
}

// CancelScheduledRestart cancels any scheduled restart.
func (ha *HA) CancelScheduledRestart() error {
	ha.mu.Lock()
	defer ha.mu.Unlock()

	if ha.scheduledRestart != nil {
		ha.scheduledRestart = nil
		log.Info().Msg("Scheduled restart cancelled")
	}
	return nil
}

// GetScheduledRestart returns the scheduled restart info.
func (ha *HA) GetScheduledRestart() *ScheduledRestart {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.scheduledRestart
}

// IsBusy returns true if the HA is busy with an operation.
func (ha *HA) IsBusy() bool {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return ha.busy
}

// Reinitialize triggers a reinitialize of this member.
func (ha *HA) Reinitialize(ctx context.Context, force bool) error {
	ha.mu.Lock()
	ha.busy = true
	ha.mu.Unlock()

	defer func() {
		ha.mu.Lock()
		ha.busy = false
		ha.mu.Unlock()
	}()

	log.Info().Bool("force", force).Msg("Reinitializing member")

	// Stop PostgreSQL
	if err := ha.pg.Stop(ctx, postgresql.StopModeFast); err != nil {
		log.Warn().Err(err).Msg("Error stopping PostgreSQL during reinitialize")
	}

	// Get leader for cloning
	cluster, err := ha.dcs.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	leader := cluster.GetLeaderMember()
	if leader == nil {
		return fmt.Errorf("no leader available for reinitialize")
	}

	// Clone from leader
	primaryConnInfo := ha.buildPrimaryConnInfo(leader)
	if err := ha.pg.Clone(ctx, primaryConnInfo); err != nil {
		return fmt.Errorf("failed to clone: %w", err)
	}

	// Start PostgreSQL
	if err := ha.pg.Start(ctx); err != nil {
		return fmt.Errorf("failed to start PostgreSQL: %w", err)
	}

	log.Info().Msg("Reinitialize completed")
	return nil
}

// ManualFailover triggers a manual failover.
func (ha *HA) ManualFailover(ctx context.Context, leader, candidate string, scheduledAt *time.Time) error {
	log.Info().
		Str("leader", leader).
		Str("candidate", candidate).
		Msg("Manual failover requested")

	failover := &types.Failover{
		Leader:    leader,
		Candidate: candidate,
	}

	if scheduledAt != nil {
		failover.ScheduledAt = *scheduledAt
	}

	if err := ha.dcs.SetFailoverValue(ctx, failover); err != nil {
		return fmt.Errorf("failed to set failover: %w", err)
	}

	// Wake up HA loop to process the failover
	ha.Wakeup()
	return nil
}

// CancelFailover cancels a pending failover.
func (ha *HA) CancelFailover(ctx context.Context) error {
	log.Info().Msg("Cancelling failover")
	return ha.dcs.DeleteFailover(ctx)
}

// SetConfig sets the cluster configuration.
func (ha *HA) SetConfig(ctx context.Context, cfg map[string]interface{}) error {
	log.Info().Msg("Updating cluster configuration")

	if err := ha.dcs.SetConfigValue(ctx, cfg); err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}

	// Wake up HA loop to reload configuration
	ha.Wakeup()
	return nil
}

// GetConfig returns the current cluster configuration.
func (ha *HA) GetConfig() map[string]interface{} {
	ha.mu.RLock()
	defer ha.mu.RUnlock()

	if ha.cluster != nil && ha.cluster.Config != nil {
		return ha.cluster.Config.Data
	}
	return nil
}
