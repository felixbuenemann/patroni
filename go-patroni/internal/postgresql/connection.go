// Package postgresql provides PostgreSQL management functionality.
// This file contains connection pool management for Patroni's connections to PostgreSQL.
package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// ConnectionParams holds PostgreSQL connection parameters.
type ConnectionParams struct {
	Host            string
	Port            int
	Database        string
	User            string
	Password        string
	SSLMode         string
	SSLCert         string
	SSLKey          string
	SSLRootCert     string
	ConnectTimeout  time.Duration
	ApplicationName string
}

// DSN returns the connection string for the parameters.
func (p *ConnectionParams) DSN() string {
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s",
		p.Host, p.Port, p.Database)

	if p.User != "" {
		dsn += fmt.Sprintf(" user=%s", p.User)
	}
	if p.Password != "" {
		dsn += fmt.Sprintf(" password=%s", p.Password)
	}
	if p.SSLMode != "" {
		dsn += fmt.Sprintf(" sslmode=%s", p.SSLMode)
	}
	if p.SSLCert != "" {
		dsn += fmt.Sprintf(" sslcert=%s", p.SSLCert)
	}
	if p.SSLKey != "" {
		dsn += fmt.Sprintf(" sslkey=%s", p.SSLKey)
	}
	if p.SSLRootCert != "" {
		dsn += fmt.Sprintf(" sslrootcert=%s", p.SSLRootCert)
	}
	if p.ConnectTimeout > 0 {
		dsn += fmt.Sprintf(" connect_timeout=%d", int(p.ConnectTimeout.Seconds()))
	}
	if p.ApplicationName != "" {
		dsn += fmt.Sprintf(" application_name=%s", p.ApplicationName)
	}

	return dsn
}

// NamedConnection manages a named PostgreSQL connection for Patroni.
type NamedConnection struct {
	mu            sync.Mutex
	pool          *ConnectionPool
	name          string
	paramsOverride *ConnectionParams
	db            *sql.DB
	serverVersion int
}

// newNamedConnection creates a new named connection.
func newNamedConnection(pool *ConnectionPool, name string, paramsOverride *ConnectionParams) *NamedConnection {
	return &NamedConnection{
		pool:          pool,
		name:          name,
		paramsOverride: paramsOverride,
	}
}

// connParams returns the connection parameters for this named connection.
func (nc *NamedConnection) connParams() *ConnectionParams {
	base := nc.pool.ConnParams()
	if nc.paramsOverride != nil {
		// Merge override with base
		if nc.paramsOverride.Host != "" {
			base.Host = nc.paramsOverride.Host
		}
		if nc.paramsOverride.Port != 0 {
			base.Port = nc.paramsOverride.Port
		}
		if nc.paramsOverride.Database != "" {
			base.Database = nc.paramsOverride.Database
		}
		if nc.paramsOverride.User != "" {
			base.User = nc.paramsOverride.User
		}
		if nc.paramsOverride.Password != "" {
			base.Password = nc.paramsOverride.Password
		}
	}
	base.ApplicationName = fmt.Sprintf("Patroni %s", nc.name)
	return base
}

// Get returns the database connection, establishing a new one if necessary.
func (nc *NamedConnection) Get(ctx context.Context) (*sql.DB, error) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Check if connection is still valid
	if nc.db != nil {
		if err := nc.db.PingContext(ctx); err == nil {
			return nc.db, nil
		}
		// Connection is dead, close it
		nc.db.Close()
		nc.db = nil
	}

	// Establish new connection
	log.Info().Str("name", nc.name).Msg("establishing a new patroni connection to postgres")

	params := nc.connParams()
	db, err := sql.Open("postgres", params.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	// Verify connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Get server version
	var versionStr string
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&versionStr); err == nil {
		fmt.Sscanf(versionStr, "%d", &nc.serverVersion)
	}

	nc.db = db
	return nc.db, nil
}

// Query executes a query and returns the results.
func (nc *NamedConnection) Query(ctx context.Context, query string, args ...interface{}) ([][]interface{}, error) {
	db, err := nc.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("connection problems: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		// Check if connection is still valid
		if pingErr := nc.db.PingContext(ctx); pingErr != nil {
			nc.Close(true)
		}
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Get column types
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results [][]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		results = append(results, values)
	}

	return results, rows.Err()
}

// ServerVersion returns the PostgreSQL server version.
func (nc *NamedConnection) ServerVersion() int {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	return nc.serverVersion
}

// Close closes the connection.
func (nc *NamedConnection) Close(silent bool) bool {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	if nc.db == nil {
		return false
	}

	nc.db.Close()
	nc.db = nil

	if !silent {
		log.Info().Str("name", nc.name).Msg("closed patroni connection to postgres")
	}

	return true
}

// ConnectionPool manages named connections from Patroni to PostgreSQL.
type ConnectionPool struct {
	mu          sync.RWMutex
	connections map[string]*NamedConnection
	connParams  *ConnectionParams
}

// NewConnectionPool creates a new connection pool.
func NewConnectionPool() *ConnectionPool {
	return &ConnectionPool{
		connections: make(map[string]*NamedConnection),
		connParams:  &ConnectionParams{},
	}
}

// ConnParams returns a copy of the connection parameters.
func (p *ConnectionPool) ConnParams() *ConnectionParams {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy
	params := *p.connParams
	return &params
}

// SetConnParams sets new connection parameters.
func (p *ConnectionPool) SetConnParams(params *ConnectionParams) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connParams = params
}

// Get returns a named connection from the pool, creating it if necessary.
func (p *ConnectionPool) Get(name string, paramsOverride *ConnectionParams) *NamedConnection {
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, ok := p.connections[name]; ok {
		return conn
	}

	conn := newNamedConnection(p, name, paramsOverride)
	p.connections[name] = conn
	return conn
}

// Close closes all connections in the pool.
func (p *ConnectionPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	var closed bool
	for _, conn := range p.connections {
		if conn.Close(true) {
			closed = true
		}
	}

	if closed {
		log.Info().Msg("closed patroni connections to postgres")
	}
}

// GetConnectionCursor is a helper to get a cursor for a one-off query.
func GetConnectionCursor(ctx context.Context, params *ConnectionParams) (*sql.DB, error) {
	db, err := sql.Open("postgres", params.DSN())
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
