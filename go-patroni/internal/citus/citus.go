// Package citus provides Citus distributed PostgreSQL support for Patroni.
package citus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/pkg/types"
)

// WorkerRole represents the role of a node in a Citus cluster.
type WorkerRole string

const (
	RoleCoordinator WorkerRole = "coordinator"
	RoleWorker      WorkerRole = "worker"
)

// Config holds Citus cluster configuration.
type Config struct {
	Group           int    `yaml:"group" json:"group"`
	Database        string `yaml:"database" json:"database"`
	Username        string `yaml:"username" json:"username"`
	Password        string `yaml:"password" json:"password"`
}

// DefaultConfig returns default Citus configuration.
func DefaultConfig() *Config {
	return &Config{
		Group:    0,
		Database: "citus",
	}
}

// Worker represents a Citus worker node.
type Worker struct {
	NodeID       int        `json:"node_id"`
	GroupID      int        `json:"group_id"`
	NodeName     string     `json:"node_name"`
	NodePort     int        `json:"node_port"`
	NodeCluster  string     `json:"node_cluster"`
	NodeRole     WorkerRole `json:"node_role"`
	IsActive     bool       `json:"is_active"`
}

// Coordinator represents the Citus coordinator state.
type Coordinator struct {
	ClusterName string    `json:"cluster_name"`
	Leader      string    `json:"leader"`
	Workers     []*Worker `json:"workers"`
}

// CitusHandler manages Citus cluster operations.
type CitusHandler struct {
	mu          sync.RWMutex
	config      *Config
	pool        *pgxpool.Pool
	role        WorkerRole
	coordinator *Coordinator
	workers     map[int]*Worker
}

// NewCitusHandler creates a new CitusHandler.
func NewCitusHandler(config *Config) *CitusHandler {
	if config == nil {
		config = DefaultConfig()
	}

	return &CitusHandler{
		config:  config,
		workers: make(map[int]*Worker),
	}
}

// SetPool sets the database connection pool.
func (c *CitusHandler) SetPool(pool *pgxpool.Pool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pool = pool
}

// SetRole sets the node's role in the Citus cluster.
func (c *CitusHandler) SetRole(role WorkerRole) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.role = role
}

// GetRole returns the node's role in the Citus cluster.
func (c *CitusHandler) GetRole() WorkerRole {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role
}

// IsCoordinator returns true if this node is a coordinator.
func (c *CitusHandler) IsCoordinator() bool {
	return c.GetRole() == RoleCoordinator
}

// IsWorker returns true if this node is a worker.
func (c *CitusHandler) IsWorker() bool {
	return c.GetRole() == RoleWorker
}

// Group returns the group ID for this node.
func (c *CitusHandler) Group() int {
	return c.config.Group
}

// Initialize initializes Citus on this node.
func (c *CitusHandler) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	// Check if Citus extension exists
	var exists bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_extension WHERE extname = 'citus'
		)
	`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check citus extension: %w", err)
	}

	if !exists {
		// Create the extension
		_, err := c.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS citus")
		if err != nil {
			return fmt.Errorf("failed to create citus extension: %w", err)
		}
		log.Info().Msg("Created Citus extension")
	}

	return nil
}

// AddWorker adds a worker node to the Citus cluster.
func (c *CitusHandler) AddWorker(ctx context.Context, nodeName string, nodePort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can add workers")
	}

	// Check if worker already exists
	var exists bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_dist_node
			WHERE nodename = $1 AND nodeport = $2
		)
	`, nodeName, nodePort).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check existing worker: %w", err)
	}

	if exists {
		log.Debug().
			Str("node", nodeName).
			Int("port", nodePort).
			Msg("Worker already exists")
		return nil
	}

	// Add the worker
	_, err = c.pool.Exec(ctx,
		"SELECT master_add_node($1, $2)",
		nodeName, nodePort,
	)
	if err != nil {
		return fmt.Errorf("failed to add worker: %w", err)
	}

	log.Info().
		Str("node", nodeName).
		Int("port", nodePort).
		Msg("Added Citus worker")

	return nil
}

// RemoveWorker removes a worker node from the Citus cluster.
func (c *CitusHandler) RemoveWorker(ctx context.Context, nodeName string, nodePort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can remove workers")
	}

	_, err := c.pool.Exec(ctx,
		"SELECT master_remove_node($1, $2)",
		nodeName, nodePort,
	)
	if err != nil {
		return fmt.Errorf("failed to remove worker: %w", err)
	}

	log.Info().
		Str("node", nodeName).
		Int("port", nodePort).
		Msg("Removed Citus worker")

	return nil
}

// DisableWorker disables a worker node.
func (c *CitusHandler) DisableWorker(ctx context.Context, nodeName string, nodePort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can disable workers")
	}

	_, err := c.pool.Exec(ctx,
		"SELECT citus_disable_node($1, $2)",
		nodeName, nodePort,
	)
	if err != nil {
		return fmt.Errorf("failed to disable worker: %w", err)
	}

	log.Info().
		Str("node", nodeName).
		Int("port", nodePort).
		Msg("Disabled Citus worker")

	return nil
}

// EnableWorker enables a disabled worker node.
func (c *CitusHandler) EnableWorker(ctx context.Context, nodeName string, nodePort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can enable workers")
	}

	_, err := c.pool.Exec(ctx,
		"SELECT citus_activate_node($1, $2)",
		nodeName, nodePort,
	)
	if err != nil {
		return fmt.Errorf("failed to enable worker: %w", err)
	}

	log.Info().
		Str("node", nodeName).
		Int("port", nodePort).
		Msg("Enabled Citus worker")

	return nil
}

// UpdateWorker updates a worker's connection information.
func (c *CitusHandler) UpdateWorker(ctx context.Context, oldName string, oldPort int, newName string, newPort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can update workers")
	}

	_, err := c.pool.Exec(ctx,
		"SELECT citus_update_node($1, $2, $3, $4)",
		oldName, oldPort, newName, newPort,
	)
	if err != nil {
		return fmt.Errorf("failed to update worker: %w", err)
	}

	log.Info().
		Str("old_node", oldName).
		Str("new_node", newName).
		Int("new_port", newPort).
		Msg("Updated Citus worker")

	return nil
}

// GetWorkers returns all workers in the cluster.
func (c *CitusHandler) GetWorkers(ctx context.Context) ([]*Worker, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.pool == nil {
		return nil, fmt.Errorf("database pool not set")
	}

	rows, err := c.pool.Query(ctx, `
		SELECT
			nodeid,
			groupid,
			nodename,
			nodeport,
			noderole,
			isactive
		FROM pg_dist_node
		ORDER BY groupid, nodeid
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query workers: %w", err)
	}
	defer rows.Close()

	var workers []*Worker
	for rows.Next() {
		var w Worker
		var role string
		err := rows.Scan(
			&w.NodeID,
			&w.GroupID,
			&w.NodeName,
			&w.NodePort,
			&role,
			&w.IsActive,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan worker: %w", err)
		}

		w.NodeRole = WorkerRole(role)
		workers = append(workers, &w)
	}

	return workers, rows.Err()
}

// GetActiveWorkers returns only active workers.
func (c *CitusHandler) GetActiveWorkers(ctx context.Context) ([]*Worker, error) {
	workers, err := c.GetWorkers(ctx)
	if err != nil {
		return nil, err
	}

	var active []*Worker
	for _, w := range workers {
		if w.IsActive {
			active = append(active, w)
		}
	}

	return active, nil
}

// SyncWorkersFromCluster synchronizes workers based on Patroni cluster state.
func (c *CitusHandler) SyncWorkersFromCluster(ctx context.Context, cluster *types.Cluster) error {
	if !c.IsCoordinator() {
		return nil
	}

	// Get current workers
	currentWorkers, err := c.GetWorkers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current workers: %w", err)
	}

	currentMap := make(map[string]*Worker)
	for _, w := range currentWorkers {
		key := fmt.Sprintf("%s:%d", w.NodeName, w.NodePort)
		currentMap[key] = w
	}

	// Get expected workers from cluster members
	expectedWorkers := make(map[string]bool)
	for _, member := range cluster.Members {
		if member.Data.Role == "master" || member.Data.Role == "primary" {
			key := fmt.Sprintf("%s:%d", member.Data.ConnURL, 5432) // Simplified
			expectedWorkers[key] = true

			// Add if not present
			if _, exists := currentMap[key]; !exists {
				// Would need to parse the actual host/port from ConnURL
				log.Info().
					Str("member", member.Name).
					Msg("Would add worker from cluster member")
			}
		}
	}

	// Disable workers that are no longer in the cluster
	for key, worker := range currentMap {
		if !expectedWorkers[key] && worker.IsActive {
			if err := c.DisableWorker(ctx, worker.NodeName, worker.NodePort); err != nil {
				log.Warn().Err(err).
					Str("node", worker.NodeName).
					Msg("Failed to disable worker")
			}
		}
	}

	return nil
}

// HandleFailover handles a failover event for the worker group.
func (c *CitusHandler) HandleFailover(ctx context.Context, oldLeader, newLeader string, port int) error {
	if !c.IsCoordinator() {
		return nil
	}

	log.Info().
		Str("old_leader", oldLeader).
		Str("new_leader", newLeader).
		Int("port", port).
		Msg("Handling Citus worker failover")

	// Update the node in Citus metadata
	return c.UpdateWorker(ctx, oldLeader, port, newLeader, port)
}

// WaitForShardRebalance waits for shard rebalancing to complete.
func (c *CitusHandler) WaitForShardRebalance(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		var inProgress bool
		err := c.pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM citus_rebalance_status()
				WHERE status = 'in_progress'
			)
		`).Scan(&inProgress)
		if err != nil {
			// May not have rebalance status function in older versions
			return nil
		}

		if !inProgress {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return fmt.Errorf("timeout waiting for shard rebalance")
}

// RebalanceShards triggers shard rebalancing.
func (c *CitusHandler) RebalanceShards(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	if c.role != RoleCoordinator {
		return fmt.Errorf("only coordinator can rebalance shards")
	}

	_, err := c.pool.Exec(ctx, "SELECT rebalance_table_shards()")
	if err != nil {
		return fmt.Errorf("failed to rebalance shards: %w", err)
	}

	log.Info().Msg("Started shard rebalancing")
	return nil
}

// GetClusterState returns the current state of the Citus cluster.
func (c *CitusHandler) GetClusterState(ctx context.Context) (*Coordinator, error) {
	workers, err := c.GetWorkers(ctx)
	if err != nil {
		return nil, err
	}

	return &Coordinator{
		Workers: workers,
	}, nil
}

// SetCoordinatorMember sets the coordinator as a reference node in Citus.
func (c *CitusHandler) SetCoordinatorMember(ctx context.Context, nodeName string, nodePort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pool == nil {
		return fmt.Errorf("database pool not set")
	}

	_, err := c.pool.Exec(ctx,
		"SELECT citus_set_coordinator_host($1, $2)",
		nodeName, nodePort,
	)
	if err != nil {
		return fmt.Errorf("failed to set coordinator: %w", err)
	}

	log.Info().
		Str("node", nodeName).
		Int("port", nodePort).
		Msg("Set Citus coordinator")

	return nil
}
