// Package types defines core data structures used throughout Patroni.
package types

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version represents the Patroni version.
const Version = "4.0.0-go"

// PostgresqlState represents the state of a PostgreSQL instance.
type PostgresqlState int

const (
	PostgresqlStateStopped PostgresqlState = iota
	PostgresqlStateStarting
	PostgresqlStateRunning
	PostgresqlStateStopping
	PostgresqlStateCrashed
	PostgresqlStateStartFailed
	PostgresqlStateStopFailed
	PostgresqlStateRestarting
	PostgresqlStateRestartFailed
	PostgresqlStateCreatingReplica
	PostgresqlStateBootstrapStarting
)

func (s PostgresqlState) String() string {
	states := []string{
		"stopped", "starting", "running", "stopping", "crashed",
		"start failed", "stop failed", "restarting", "restart failed",
		"creating replica", "bootstrap starting",
	}
	if int(s) < len(states) {
		return states[s]
	}
	return "unknown"
}

// PostgresqlRole represents the role of a PostgreSQL instance.
type PostgresqlRole int

const (
	PostgresqlRoleUninitialized PostgresqlRole = iota
	PostgresqlRolePrimary
	PostgresqlRoleReplica
	PostgresqlRoleDemoted
	PostgresqlRolePromoted
	PostgresqlRoleStandbyLeader
)

func (r PostgresqlRole) String() string {
	roles := []string{"uninitialized", "primary", "replica", "demoted", "promoted", "standby_leader"}
	if int(r) < len(roles) {
		return roles[r]
	}
	return "unknown"
}

// Tags represents node configuration tags that affect HA behavior.
type Tags struct {
	NoFailover        bool   `json:"nofailover,omitempty" yaml:"nofailover,omitempty"`
	NoLoadbalance     bool   `json:"noloadbalance,omitempty" yaml:"noloadbalance,omitempty"`
	CloneFrom         bool   `json:"clonefrom,omitempty" yaml:"clonefrom,omitempty"`
	NoSync            bool   `json:"nosync,omitempty" yaml:"nosync,omitempty"`
	ReplicateFrom     string `json:"replicatefrom,omitempty" yaml:"replicatefrom,omitempty"`
	FailoverPriority  int    `json:"failover_priority,omitempty" yaml:"failover_priority,omitempty"`
	SyncPriority      int    `json:"sync_priority,omitempty" yaml:"sync_priority,omitempty"`
	NoStream          bool   `json:"nostream,omitempty" yaml:"nostream,omitempty"`
}

// Member represents a single member of a PostgreSQL cluster.
type Member struct {
	Version int64             `json:"version"`
	Name    string            `json:"name"`
	Session string            `json:"session,omitempty"`
	Data    MemberData        `json:"data"`
}

// MemberData contains the data stored for a cluster member.
type MemberData struct {
	ConnURL      string            `json:"conn_url,omitempty"`
	APIURL       string            `json:"api_url,omitempty"`
	State        string            `json:"state,omitempty"`
	Role         string            `json:"role,omitempty"`
	XlogLocation int64             `json:"xlog_location,omitempty"`
	Timeline     int               `json:"timeline,omitempty"`
	Tags         Tags              `json:"tags,omitempty"`
	ConnKwargs   map[string]string `json:"conn_kwargs,omitempty"`
	Version      string            `json:"version,omitempty"`
}

// ParseMemberFromJSON creates a Member from JSON data.
func ParseMemberFromJSON(version int64, name, session, value string) (*Member, error) {
	member := &Member{
		Version: version,
		Name:    name,
		Session: session,
	}

	// Handle legacy postgres:// URL format
	if strings.HasPrefix(value, "postgres") {
		connURL, apiURL := parseConnectionString(value)
		member.Data = MemberData{
			ConnURL: connURL,
			APIURL:  apiURL,
		}
		return member, nil
	}

	// Parse JSON data
	if err := json.Unmarshal([]byte(value), &member.Data); err != nil {
		// Return member with empty data on parse error
		return member, nil
	}

	return member, nil
}

// parseConnectionString splits a connection URL into conn_url and api_url.
func parseConnectionString(value string) (connURL, apiURL string) {
	u, err := url.Parse(value)
	if err != nil {
		return value, ""
	}

	// Extract api_url from application_name query parameter
	q := u.Query()
	apiURL = q.Get("application_name")
	q.Del("application_name")

	// Rebuild URL without application_name
	u.RawQuery = q.Encode()
	connURL = u.String()

	return connURL, apiURL
}

// GetConnKwargs returns connection parameters for connecting to this member.
func (m *Member) GetConnKwargs() map[string]string {
	if m.Data.ConnKwargs != nil && len(m.Data.ConnKwargs) > 0 {
		return m.Data.ConnKwargs
	}

	if m.Data.ConnURL == "" {
		return nil
	}

	u, err := url.Parse(m.Data.ConnURL)
	if err != nil {
		return nil
	}

	kwargs := map[string]string{
		"host":   u.Hostname(),
		"dbname": strings.TrimPrefix(u.Path, "/"),
	}

	if port := u.Port(); port != "" {
		kwargs["port"] = port
	} else {
		kwargs["port"] = "5432"
	}

	return kwargs
}

// Leader represents the cluster leader.
type Leader struct {
	Version    int64   `json:"version"`
	Session    string  `json:"session,omitempty"`
	Member     *Member `json:"member,omitempty"`
	MemberName string  `json:"member_name,omitempty"`
}

// SyncState represents synchronous replication state.
type SyncState struct {
	Version int64  `json:"version"`
	Leader  string `json:"leader,omitempty"`
	Sync    string `json:"sync,omitempty"`
	Quorum  int    `json:"quorum,omitempty"`
}

// Failover represents a scheduled failover operation.
type Failover struct {
	Version     int64     `json:"version"`
	Leader      string    `json:"leader,omitempty"`
	Candidate   string    `json:"candidate,omitempty"`
	ScheduledAt time.Time `json:"scheduled_at,omitempty"`
}

// TimelineHistory represents PostgreSQL timeline history.
type TimelineHistory struct {
	Version int64           `json:"version"`
	Value   []TimelineEntry `json:"value,omitempty"`
}

// TimelineEntry represents a single timeline history entry.
type TimelineEntry struct {
	Timeline  int    `json:"timeline"`
	LSN       int64  `json:"lsn"`
	Reason    string `json:"reason,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Name      string `json:"name,omitempty"`
}

// ClusterConfig represents the dynamic cluster configuration.
type ClusterConfig struct {
	Version       int64                  `json:"version"`
	Data          map[string]interface{} `json:"data,omitempty"`
	ModifyVersion int64                  `json:"modify_version,omitempty"`
}

// Status represents the cluster status stored in DCS.
type Status struct {
	Version         int64             `json:"version"`
	Slots           map[string]int64  `json:"slots,omitempty"`
	OpTime          int64             `json:"optime,omitempty"`
	FailsafeLeader  string            `json:"failsafe_leader,omitempty"`
}

// Cluster represents the entire cluster state as read from DCS.
type Cluster struct {
	// Initialization key version
	InitializeVersion int64 `json:"initialize_version"`

	// Cluster configuration
	Config *ClusterConfig `json:"config,omitempty"`

	// Current leader
	Leader *Leader `json:"leader,omitempty"`

	// Cluster status
	Status *Status `json:"status,omitempty"`

	// Failover configuration
	Failover *Failover `json:"failover,omitempty"`

	// Sync state
	SyncState *SyncState `json:"sync_state,omitempty"`

	// Timeline history
	History *TimelineHistory `json:"history,omitempty"`

	// All cluster members
	Members []*Member `json:"members,omitempty"`

	// Failsafe info
	Failsafe map[string]string `json:"failsafe,omitempty"`
}

// GetMember returns a member by name.
func (c *Cluster) GetMember(name string) *Member {
	for _, m := range c.Members {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// GetLeaderMember returns the leader member.
func (c *Cluster) GetLeaderMember() *Member {
	if c.Leader == nil {
		return nil
	}
	if c.Leader.Member != nil {
		return c.Leader.Member
	}
	return c.GetMember(c.Leader.MemberName)
}

// IsInitialized returns true if the cluster has been initialized.
func (c *Cluster) IsInitialized() bool {
	return c.InitializeVersion > 0
}

// HasLeader returns true if there is a current leader.
func (c *Cluster) HasLeader() bool {
	return c.Leader != nil && c.Leader.MemberName != ""
}

// LSN represents a PostgreSQL Log Sequence Number.
type LSN int64

// ParseLSN parses an LSN from string format (e.g., "0/16B5D10").
func ParseLSN(s string) (LSN, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid LSN format: %s", s)
	}

	high, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid LSN high part: %s", parts[0])
	}

	low, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid LSN low part: %s", parts[1])
	}

	return LSN((high << 32) | low), nil
}

// String returns the LSN in PostgreSQL format.
func (l LSN) String() string {
	return fmt.Sprintf("%X/%X", int64(l)>>32, int64(l)&0xFFFFFFFF)
}

// SlotNameFromMemberName converts a member name to a valid replication slot name.
func SlotNameFromMemberName(memberName string) string {
	var result strings.Builder
	for _, c := range strings.ToLower(memberName) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			result.WriteRune(c)
		case c == '-', c == '.':
			result.WriteRune('_')
		default:
			result.WriteString(fmt.Sprintf("u%04d", c))
		}
	}
	s := result.String()
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
