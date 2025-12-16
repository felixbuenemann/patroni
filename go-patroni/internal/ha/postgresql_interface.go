// Package ha implements the high availability state machine.
package ha

import (
	"context"

	"github.com/patroni/patroni-go/internal/postgresql"
	"github.com/patroni/patroni-go/pkg/types"
)

// StopMode is an alias for postgresql.StopMode to avoid import in tests.
type StopMode = postgresql.StopMode

// Stop mode constants.
const (
	StopModeFast = postgresql.StopModeFast
)

// PostgreSQLInterface defines the PostgreSQL methods needed by HA.
type PostgreSQLInterface interface {
	// Getters
	Name() string
	Scope() string
	DataDir() string
	State() types.PostgresqlState
	Role() types.PostgresqlRole
	IsRunning() bool
	IsPrimary() bool
	MajorVersion() int
	TimelineWALPosition() (int, int64)
	GetMemberData() *types.MemberData

	// Lifecycle
	Start(ctx context.Context) error
	Stop(ctx context.Context, mode StopMode) error
	Restart(ctx context.Context) error
	Promote(ctx context.Context) error
	Demote(ctx context.Context, primaryConnInfo string) error
	Bootstrap(ctx context.Context) error
	Clone(ctx context.Context, primaryConnInfo string) error
}

// Ensure postgresql.Postgresql implements PostgreSQLInterface.
var _ PostgreSQLInterface = (*postgresql.Postgresql)(nil)
