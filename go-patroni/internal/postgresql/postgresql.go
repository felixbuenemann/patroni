// Package postgresql provides PostgreSQL instance management.
package postgresql

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v3/process"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/pkg/types"
)

// Postgresql manages a PostgreSQL instance.
type Postgresql struct {
	mu sync.RWMutex

	name     string
	scope    string
	dataDir  string
	binDir   string
	database string

	state types.PostgresqlState
	role  types.PostgresqlRole

	majorVersion int
	sysID        string

	pool     *pgxpool.Pool
	connStr  string

	config *config.Config

	postmaster *process.Process

	stateEntryTime    time.Time
	pendingRestart    bool
	pendingRestartReason map[string]interface{}

	callbacks *CallbackExecutor

	// Replication slot handler
	slotsHandler *SlotsHandler

	// Cancellation support
	cancelFunc context.CancelFunc
}

// New creates a new Postgresql manager.
func New(cfg *config.Config) (*Postgresql, error) {
	pg := &Postgresql{
		name:     cfg.Name,
		scope:    cfg.Scope,
		dataDir:  cfg.PostgreSQL.DataDir,
		binDir:   cfg.PostgreSQL.BinDir,
		database: "postgres",
		config:   cfg,
		state:    types.PostgresqlStateStopped,
		role:     types.PostgresqlRoleUninitialized,
		pendingRestartReason: make(map[string]interface{}),
	}

	// Initialize callbacks
	pg.callbacks = NewCallbackExecutor(cfg.PostgreSQL.Callbacks)

	// Initialize slots handler
	pg.slotsHandler = NewSlotsHandler(pg)

	// Check data directory
	if err := pg.checkDirectories(); err != nil {
		return nil, fmt.Errorf("failed to check directories: %w", err)
	}

	// Determine major version if data directory exists
	if pg.dataDirectoryExists() {
		pg.majorVersion = pg.getMajorVersion()
	}

	// Check if PostgreSQL is already running
	if pm := pg.isRunning(); pm != nil {
		pg.postmaster = pm
		pg.setState(types.PostgresqlStateStarting)

		// Try to determine role from data directory
		pg.role = pg.getRoleFromDataDirectory()

		// Check if it's accepting connections
		if pg.isAcceptingConnections() {
			pg.setState(types.PostgresqlStateRunning)
			if pg.isPrimary() {
				pg.role = types.PostgresqlRolePrimary
			} else {
				pg.role = types.PostgresqlRoleReplica
			}
		}
	}

	return pg, nil
}

// checkDirectories ensures required directories exist.
func (pg *Postgresql) checkDirectories() error {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(pg.dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	return nil
}

// dataDirectoryExists returns true if the data directory contains a valid PG installation.
func (pg *Postgresql) dataDirectoryExists() bool {
	pgVersion := filepath.Join(pg.dataDir, "PG_VERSION")
	_, err := os.Stat(pgVersion)
	return err == nil
}

// dataDirectoryEmpty returns true if the data directory is empty or doesn't exist.
func (pg *Postgresql) dataDirectoryEmpty() bool {
	pgControl := filepath.Join(pg.dataDir, "global", "pg_control")
	if _, err := os.Stat(pgControl); err == nil {
		return false
	}

	entries, err := os.ReadDir(pg.dataDir)
	if err != nil {
		return true
	}

	// Check if directory is empty or only contains allowed files
	for _, entry := range entries {
		name := entry.Name()
		if name != "lost+found" && name != ".DS_Store" {
			return false
		}
	}

	return true
}

// getMajorVersion reads the major version from PG_VERSION file.
func (pg *Postgresql) getMajorVersion() int {
	path := filepath.Join(pg.dataDir, "PG_VERSION")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	versionStr := strings.TrimSpace(string(data))
	parts := strings.Split(versionStr, ".")

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}

	// Convert to internal format (e.g., 15 -> 150000, 9.6 -> 90600)
	if major < 10 && len(parts) > 1 {
		minor, _ := strconv.Atoi(parts[1])
		return major*10000 + minor*100
	}
	return major * 10000
}

// getRoleFromDataDirectory determines the role based on data directory state.
func (pg *Postgresql) getRoleFromDataDirectory() types.PostgresqlRole {
	if pg.dataDirectoryEmpty() {
		return types.PostgresqlRoleUninitialized
	}

	// Check for standby.signal or recovery.conf
	standbySignal := filepath.Join(pg.dataDir, "standby.signal")
	recoveryConf := filepath.Join(pg.dataDir, "recovery.conf")

	if _, err := os.Stat(standbySignal); err == nil {
		return types.PostgresqlRoleReplica
	}
	if _, err := os.Stat(recoveryConf); err == nil {
		return types.PostgresqlRoleReplica
	}

	return types.PostgresqlRolePrimary
}

// pgCommand returns the path to a PostgreSQL binary.
func (pg *Postgresql) pgCommand(cmd string) string {
	// Check for custom binary name
	if pg.config != nil && pg.config.PostgreSQL.BinName != nil {
		if customName, ok := pg.config.PostgreSQL.BinName[cmd]; ok {
			cmd = customName
		}
	}

	if pg.binDir != "" {
		return filepath.Join(pg.binDir, cmd)
	}
	return cmd
}

// setState sets the PostgreSQL state.
func (pg *Postgresql) setState(state types.PostgresqlState) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.state = state
	pg.stateEntryTime = time.Now()
}

// State returns the current PostgreSQL state.
func (pg *Postgresql) State() types.PostgresqlState {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.state
}

// setRole sets the PostgreSQL role.
func (pg *Postgresql) setRole(role types.PostgresqlRole) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.role = role
}

// Role returns the current PostgreSQL role.
func (pg *Postgresql) Role() types.PostgresqlRole {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.role
}

// Name returns the node name.
func (pg *Postgresql) Name() string {
	return pg.name
}

// Scope returns the cluster scope.
func (pg *Postgresql) Scope() string {
	return pg.scope
}

// DataDir returns the data directory path.
func (pg *Postgresql) DataDir() string {
	return pg.dataDir
}

// MajorVersion returns the PostgreSQL major version.
func (pg *Postgresql) MajorVersion() int {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.majorVersion
}

// SysID returns the database system identifier.
func (pg *Postgresql) SysID() string {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	if pg.sysID == "" {
		pg.mu.RUnlock()
		pg.mu.Lock()
		defer pg.mu.Unlock()
		// Re-check after acquiring write lock
		if pg.sysID == "" {
			data := pg.controldata()
			if sysID, ok := data["Database system identifier"]; ok {
				pg.sysID = sysID
			}
		}
		return pg.sysID
	}
	return pg.sysID
}

// isRunning checks if PostgreSQL is running and returns the process if found.
func (pg *Postgresql) isRunning() *process.Process {
	// Check cached process first
	pg.mu.RLock()
	pm := pg.postmaster
	pg.mu.RUnlock()

	if pm != nil {
		running, err := pm.IsRunning()
		if err == nil && running {
			return pm
		}
	}

	// Try to find process from pid file
	pidFile := filepath.Join(pg.dataDir, "postmaster.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return nil
	}

	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil
	}

	// Verify it's actually postgres
	name, err := proc.Name()
	if err != nil || !strings.Contains(strings.ToLower(name), "postgres") {
		return nil
	}

	pg.mu.Lock()
	pg.postmaster = proc
	pg.mu.Unlock()

	return proc
}

// IsRunning returns true if PostgreSQL is running.
func (pg *Postgresql) IsRunning() bool {
	return pg.isRunning() != nil
}

// isAcceptingConnections checks if PostgreSQL is accepting connections.
func (pg *Postgresql) isAcceptingConnections() bool {
	cmd := exec.Command(pg.pgCommand("pg_isready"),
		"-p", pg.getPort(),
		"-d", pg.database,
	)

	if host := pg.getHost(); host != "" && host != "*" {
		cmd.Args = append(cmd.Args, "-h", host)
	}

	err := cmd.Run()
	return err == nil
}

// getHost returns the host part of the listen address.
func (pg *Postgresql) getHost() string {
	listen := pg.config.PostgreSQL.Listen
	if listen == "" {
		return ""
	}
	parts := strings.Split(listen, ":")
	if len(parts) >= 1 {
		host := parts[0]
		if host == "*" || host == "0.0.0.0" {
			return "localhost"
		}
		return host
	}
	return "localhost"
}

// getPort returns the port part of the listen address.
func (pg *Postgresql) getPort() string {
	listen := pg.config.PostgreSQL.Listen
	if listen == "" {
		return "5432"
	}
	parts := strings.Split(listen, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "5432"
}

// connect establishes a connection pool to PostgreSQL.
func (pg *Postgresql) connect(ctx context.Context) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.pool != nil {
		return nil
	}

	connStr := pg.buildConnectionString()

	poolConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}

	poolConfig.MaxConns = 5
	poolConfig.MinConns = 1
	poolConfig.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}

	pg.pool = pool
	pg.connStr = connStr

	return nil
}

// buildConnectionString builds a PostgreSQL connection string.
func (pg *Postgresql) buildConnectionString() string {
	auth := pg.config.PostgreSQL.Authentication.Superuser

	user := auth.Username
	if user == "" {
		user = "postgres"
	}

	host := pg.getHost()
	port := pg.getPort()

	connStr := fmt.Sprintf("host=%s port=%s user=%s dbname=%s application_name=Patroni",
		host, port, user, pg.database)

	if auth.Password != "" {
		connStr += fmt.Sprintf(" password=%s", auth.Password)
	}

	if auth.SSLMode != "" {
		connStr += fmt.Sprintf(" sslmode=%s", auth.SSLMode)
	}

	return connStr
}

// Query executes a query and returns the results.
func (pg *Postgresql) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if err := pg.connect(ctx); err != nil {
		return nil, err
	}

	pg.mu.RLock()
	pool := pg.pool
	pg.mu.RUnlock()

	return pool.Query(ctx, query, args...)
}

// QueryRow executes a query that returns a single row.
func (pg *Postgresql) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	if err := pg.connect(ctx); err != nil {
		return &errorRow{err: err}
	}

	pg.mu.RLock()
	pool := pg.pool
	pg.mu.RUnlock()

	return pool.QueryRow(ctx, query, args...)
}

// Exec executes a query without returning results.
func (pg *Postgresql) Exec(ctx context.Context, query string, args ...interface{}) error {
	if err := pg.connect(ctx); err != nil {
		return err
	}

	pg.mu.RLock()
	pool := pg.pool
	pg.mu.RUnlock()

	_, err := pool.Exec(ctx, query, args...)
	return err
}

// errorRow implements pgx.Row for error cases.
type errorRow struct {
	err error
}

func (r *errorRow) Scan(dest ...interface{}) error {
	return r.err
}

// isPrimary returns true if PostgreSQL is running as primary.
func (pg *Postgresql) isPrimary() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var inRecovery bool
	err := pg.QueryRow(ctx, "SELECT pg_catalog.pg_is_in_recovery()").Scan(&inRecovery)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to check if in recovery")
		return false
	}

	return !inRecovery
}

// IsPrimary returns true if this node is the primary.
func (pg *Postgresql) IsPrimary() bool {
	pg.mu.RLock()
	role := pg.role
	pg.mu.RUnlock()

	if role == types.PostgresqlRolePrimary || role == types.PostgresqlRolePromoted {
		return true
	}

	if pg.IsRunning() && pg.isAcceptingConnections() {
		return pg.isPrimary()
	}

	return false
}

// controldata runs pg_controldata and parses the output.
func (pg *Postgresql) controldata() map[string]string {
	if !pg.dataDirectoryExists() {
		return nil
	}

	cmd := exec.Command(pg.pgCommand("pg_controldata"), pg.dataDir)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")

	output, err := cmd.Output()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to run pg_controldata")
		return nil
	}

	result := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove "Current " prefix from some keys
			key = strings.TrimPrefix(key, "Current ")
			result[key] = value
		}
	}

	return result
}

// Close closes the PostgreSQL manager.
func (pg *Postgresql) Close() error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.pool != nil {
		pg.pool.Close()
		pg.pool = nil
	}

	if pg.cancelFunc != nil {
		pg.cancelFunc()
	}

	return nil
}

// CloseConnections closes all database connections.
func (pg *Postgresql) CloseConnections() {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if pg.pool != nil {
		pg.pool.Close()
		pg.pool = nil
	}
}

// GetMemberData returns the member data for DCS updates.
func (pg *Postgresql) GetMemberData() *types.MemberData {
	pg.mu.RLock()
	defer pg.mu.RUnlock()

	data := &types.MemberData{
		ConnURL: pg.config.GetConnectionURL(),
		APIURL:  pg.config.GetAPIURL(),
		State:   pg.state.String(),
		Role:    pg.role.String(),
		Version: types.Version,
	}

	// Add timeline and WAL position if running
	if pg.state == types.PostgresqlStateRunning {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		info, err := pg.getClusterInfo(ctx)
		if err == nil {
			data.Timeline = info.Timeline
			data.XlogLocation = info.WALPosition
		}
	}

	// Add tags
	if pg.config.Tags != nil {
		data.Tags = types.Tags{}
		// Convert tags from config
		if nf, ok := pg.config.Tags["nofailover"].(bool); ok {
			data.Tags.NoFailover = nf
		}
		if nlb, ok := pg.config.Tags["noloadbalance"].(bool); ok {
			data.Tags.NoLoadbalance = nlb
		}
		if cf, ok := pg.config.Tags["clonefrom"].(bool); ok {
			data.Tags.CloneFrom = cf
		}
	}

	return data
}

// ClusterInfo holds information about the cluster state.
type ClusterInfo struct {
	Timeline     int
	WALPosition  int64
	ReplayLSN    int64
	ReceiveLSN   int64
	ReplayPaused bool
	InRecovery   bool
}

// getClusterInfo queries PostgreSQL for cluster state information.
func (pg *Postgresql) getClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	info := &ClusterInfo{}

	// Build the query based on version
	walName := "wal"
	lsnName := "lsn"
	if pg.majorVersion < 100000 {
		walName = "xlog"
		lsnName = "location"
	}

	query := fmt.Sprintf(`
		SELECT
			CASE WHEN pg_catalog.pg_is_in_recovery() THEN 0
			ELSE (pg_catalog.pg_current_%s_%s() - '0/0')::bigint END,
			CASE WHEN pg_catalog.pg_is_in_recovery() THEN
				(pg_catalog.pg_last_%s_replay_%s() - '0/0')::bigint
			ELSE 0 END,
			CASE WHEN pg_catalog.pg_is_in_recovery() THEN
				(COALESCE(pg_catalog.pg_last_%s_receive_%s(), '0/0') - '0/0')::bigint
			ELSE 0 END,
			pg_catalog.pg_is_in_recovery()
	`, walName, lsnName, walName, lsnName, walName, lsnName)

	err := pg.QueryRow(ctx, query).Scan(
		&info.WALPosition,
		&info.ReplayLSN,
		&info.ReceiveLSN,
		&info.InRecovery,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster info: %w", err)
	}

	// Get timeline for primary
	if !info.InRecovery {
		query = fmt.Sprintf(`
			SELECT ('x' || pg_catalog.substr(pg_catalog.pg_%sfile_name(
				pg_catalog.pg_current_%s_%s()), 1, 8))::bit(32)::int
		`, walName, walName, lsnName)

		err = pg.QueryRow(ctx, query).Scan(&info.Timeline)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get timeline")
		}
	}

	return info, nil
}

// LastOperation returns the last WAL position.
func (pg *Postgresql) LastOperation() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := pg.getClusterInfo(ctx)
	if err != nil {
		return 0
	}

	if info.InRecovery {
		if info.ReceiveLSN > info.ReplayLSN {
			return info.ReceiveLSN
		}
		return info.ReplayLSN
	}

	return info.WALPosition
}

// TimelineWALPosition returns the timeline and WAL position.
func (pg *Postgresql) TimelineWALPosition() (int, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := pg.getClusterInfo(ctx)
	if err != nil {
		return 0, 0
	}

	walPos := info.WALPosition
	if info.InRecovery {
		if info.ReceiveLSN > info.ReplayLSN {
			walPos = info.ReceiveLSN
		} else {
			walPos = info.ReplayLSN
		}
	}

	return info.Timeline, walPos
}

// GetVersion returns the PostgreSQL major version as an integer.
func (pg *Postgresql) GetVersion() int {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.majorVersion
}

// GetControlData returns the pg_controldata output as a map.
func (pg *Postgresql) GetControlData() (map[string]string, error) {
	return pg.controldata(), nil
}

// SupportsTimelines returns true if this PostgreSQL version supports timelines.
func (pg *Postgresql) SupportsTimelines() bool {
	// Timelines are supported in PostgreSQL 9.3+
	return pg.majorVersion >= 90300
}

// GetParameter retrieves a PostgreSQL parameter value.
func (pg *Postgresql) GetParameter(ctx context.Context, name string) (string, error) {
	pg.mu.RLock()
	pool := pg.pool
	pg.mu.RUnlock()

	if pool == nil {
		return "", fmt.Errorf("not connected")
	}

	var value string
	err := pool.QueryRow(ctx, "SHOW "+name).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("failed to get parameter %s: %w", name, err)
	}

	return value, nil
}

// SetParameter sets a PostgreSQL parameter value (requires reload).
func (pg *Postgresql) SetParameter(ctx context.Context, name, value string) error {
	pg.mu.RLock()
	pool := pg.pool
	pg.mu.RUnlock()

	if pool == nil {
		return fmt.Errorf("not connected")
	}

	_, err := pool.Exec(ctx, fmt.Sprintf("ALTER SYSTEM SET %s = %s", name, quoteValue(value)))
	if err != nil {
		return fmt.Errorf("failed to set parameter %s: %w", name, err)
	}

	// Reload configuration
	_, err = pool.Exec(ctx, "SELECT pg_reload_conf()")
	if err != nil {
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	return nil
}

// quoteValue quotes a PostgreSQL value for ALTER SYSTEM.
func quoteValue(value string) string {
	if value == "" {
		return "''"
	}
	// Escape single quotes
	escaped := strings.ReplaceAll(value, "'", "''")
	return "'" + escaped + "'"
}
