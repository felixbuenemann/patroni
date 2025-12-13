// Package dcs provides the Distributed Configuration Store abstraction.
package dcs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/patroni/patroni-go/pkg/types"
)

// Common errors returned by DCS implementations.
var (
	ErrNotFound       = errors.New("key not found")
	ErrAlreadyExists  = errors.New("key already exists")
	ErrPrecondition   = errors.New("precondition failed")
	ErrSessionExpired = errors.New("session expired")
	ErrNotLeader      = errors.New("not the leader")
	ErrNoLeader       = errors.New("no leader")
	ErrTimeout        = errors.New("operation timed out")
)

// Config holds DCS configuration options.
type Config struct {
	// Namespace is the key prefix for all cluster data
	Namespace string `yaml:"namespace" json:"namespace"`

	// Scope is the cluster name
	Scope string `yaml:"scope" json:"scope"`

	// Name is this node's name
	Name string `yaml:"name" json:"name"`

	// TTL is the time-to-live for session/lease in seconds
	TTL int `yaml:"ttl" json:"ttl"`

	// LoopWait is the main loop sleep interval in seconds
	LoopWait int `yaml:"loop_wait" json:"loop_wait"`

	// RetryTimeout is the timeout for DCS operations
	RetryTimeout int `yaml:"retry_timeout" json:"retry_timeout"`

	// Hosts is a list of DCS endpoints
	Hosts []string `yaml:"hosts" json:"hosts"`

	// Host is a single DCS endpoint (alternative to Hosts)
	Host string `yaml:"host" json:"host"`

	// Username for authentication
	Username string `yaml:"username" json:"username"`

	// Password for authentication
	Password string `yaml:"password" json:"password"`

	// CACert is the path to CA certificate
	CACert string `yaml:"cacert" json:"cacert"`

	// Cert is the path to client certificate
	Cert string `yaml:"cert" json:"cert"`

	// Key is the path to client key
	Key string `yaml:"key" json:"key"`
}

// GetHosts returns the list of hosts, handling both Hosts and Host fields.
func (c *Config) GetHosts() []string {
	if len(c.Hosts) > 0 {
		return c.Hosts
	}
	if c.Host != "" {
		return []string{c.Host}
	}
	return nil
}

// GetTTL returns the TTL with a sensible default.
func (c *Config) GetTTL() time.Duration {
	if c.TTL > 0 {
		return time.Duration(c.TTL) * time.Second
	}
	return 30 * time.Second
}

// GetRetryTimeout returns the retry timeout with a sensible default.
func (c *Config) GetRetryTimeout() time.Duration {
	if c.RetryTimeout > 0 {
		return time.Duration(c.RetryTimeout) * time.Second
	}
	return 10 * time.Second
}

// DCS is the interface that all distributed configuration store backends must implement.
type DCS interface {
	// GetCluster retrieves the current cluster state from DCS.
	GetCluster(ctx context.Context) (*types.Cluster, error)

	// TouchMember updates this member's entry in DCS with current state.
	TouchMember(ctx context.Context, data *types.MemberData) error

	// TakeLock attempts to acquire the leader lock.
	// Returns true if the lock was acquired, false if someone else holds it.
	TakeLock(ctx context.Context) (bool, error)

	// UpdateLeader updates the leader key with new information.
	UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error

	// DeleteLeader releases the leader lock.
	DeleteLeader(ctx context.Context) error

	// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
	AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error)

	// SetFailoverValue sets/updates the failover key.
	SetFailoverValue(ctx context.Context, failover *types.Failover) error

	// DeleteFailover deletes the failover key.
	DeleteFailover(ctx context.Context) error

	// SetConfigValue sets/updates the cluster configuration.
	SetConfigValue(ctx context.Context, config map[string]interface{}) error

	// SetSyncState sets the synchronous replication state.
	SetSyncState(ctx context.Context, state *types.SyncState) error

	// DeleteSyncState deletes the sync state.
	DeleteSyncState(ctx context.Context) error

	// Initialize initializes the cluster in DCS.
	Initialize(ctx context.Context, sysID string) (bool, error)

	// SetHistoryValue sets the timeline history.
	SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error

	// DeleteCluster removes all cluster data from DCS.
	DeleteCluster(ctx context.Context) error

	// Watch returns a channel that receives cluster updates.
	Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error)

	// Close closes the DCS connection.
	Close() error

	// Name returns the name of this DCS implementation.
	Name() string
}

// Factory is a function that creates a new DCS instance.
type Factory func(config *Config) (DCS, error)

var (
	factoriesMu sync.RWMutex
	factories   = make(map[string]Factory)
)

// Register registers a DCS factory under the given name.
func Register(name string, factory Factory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[name] = factory
}

// New creates a new DCS instance based on the given name and configuration.
func New(name string, config *Config) (DCS, error) {
	factoriesMu.RLock()
	factory, ok := factories[name]
	factoriesMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown DCS type: %s", name)
	}

	return factory(config)
}

// Available returns a list of registered DCS types.
func Available() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()

	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	return names
}

// BaseDCS provides common functionality for DCS implementations.
type BaseDCS struct {
	Config    *Config
	Namespace string
	Scope     string
	Name      string
	Session   string
	mu        sync.RWMutex
}

// NewBaseDCS creates a new BaseDCS with the given configuration.
func NewBaseDCS(config *Config) *BaseDCS {
	namespace := config.Namespace
	if namespace == "" {
		namespace = "/service/"
	}
	if namespace[len(namespace)-1] != '/' {
		namespace += "/"
	}

	return &BaseDCS{
		Config:    config,
		Namespace: namespace,
		Scope:     config.Scope,
		Name:      config.Name,
	}
}

// Key paths for DCS storage.
func (b *BaseDCS) ClusterPath() string {
	return b.Namespace + b.Scope
}

func (b *BaseDCS) MembersPath() string {
	return b.ClusterPath() + "/members/"
}

func (b *BaseDCS) MemberPath() string {
	return b.MembersPath() + b.Name
}

func (b *BaseDCS) LeaderPath() string {
	return b.ClusterPath() + "/leader"
}

func (b *BaseDCS) LeaderOpTimePath() string {
	return b.ClusterPath() + "/optime/leader"
}

func (b *BaseDCS) ConfigPath() string {
	return b.ClusterPath() + "/config"
}

func (b *BaseDCS) InitializePath() string {
	return b.ClusterPath() + "/initialize"
}

func (b *BaseDCS) SyncPath() string {
	return b.ClusterPath() + "/sync"
}

func (b *BaseDCS) FailoverPath() string {
	return b.ClusterPath() + "/failover"
}

func (b *BaseDCS) HistoryPath() string {
	return b.ClusterPath() + "/history"
}

func (b *BaseDCS) StatusPath() string {
	return b.ClusterPath() + "/status"
}

func (b *BaseDCS) FailsafePath() string {
	return b.ClusterPath() + "/failsafe"
}

// SetSession sets the current session ID.
func (b *BaseDCS) SetSession(session string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Session = session
}

// GetSession returns the current session ID.
func (b *BaseDCS) GetSession() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Session
}

// IsLeader returns true if this node holds the leader session.
func (b *BaseDCS) IsLeader(leaderSession string) bool {
	return b.GetSession() != "" && b.GetSession() == leaderSession
}
