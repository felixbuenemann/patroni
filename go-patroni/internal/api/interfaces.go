// Package api implements the Patroni REST API server.
package api

import (
	"context"
	"time"

	"github.com/patroni/patroni-go/internal/ha"
	"github.com/patroni/patroni-go/pkg/types"
)

// HAInterface defines the HA methods needed by the API server.
type HAInterface interface {
	IsLeader() bool
	IsPaused() bool
	IsBusy() bool
	GetCluster() *types.Cluster
	GetConfig() map[string]interface{}
	GetEffectiveTags() types.Tags
	GetScheduledRestart() *ha.ScheduledRestart
	ScheduleRestart(schedule time.Time, postmasterStartTime time.Time) error
	CancelScheduledRestart() error
	Reinitialize(ctx context.Context, force bool) error
	ManualFailover(ctx context.Context, leader, candidate string, scheduledAt *time.Time) error
	CancelFailover(ctx context.Context) error
	SetConfig(ctx context.Context, cfg map[string]interface{}) error
}

// ScheduledRestart is an alias for ha.ScheduledRestart for backward compatibility.
type ScheduledRestart = ha.ScheduledRestart

// PostgreSQLInterface defines the PostgreSQL methods needed by the API server.
type PostgreSQLInterface interface {
	State() types.PostgresqlState
	Role() types.PostgresqlRole
	IsRunning() bool
	IsPrimary() bool
	MajorVersion() int
	SysID() string
	IsPendingRestart() bool
	PendingRestartReason() map[string]interface{}
	TimelineWALPosition() (int, int64)
	Restart(ctx context.Context) error
	Reload(ctx context.Context) error
}
