package postgresql

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/pkg/types"
)

// SyncMode represents the synchronous replication mode.
type SyncMode int

const (
	SyncModeOff SyncMode = iota
	SyncModeOn
	SyncModeStrict
	SyncModeQuorum
)

// SyncConfig holds synchronous replication configuration.
type SyncConfig struct {
	Mode             SyncMode `yaml:"mode" json:"mode"`
	StrictMode       bool     `yaml:"strict_mode" json:"strict_mode"`
	SyncNodeCount    int      `yaml:"synchronous_node_count" json:"synchronous_node_count"`
	MaxLagOnSyncNode int64    `yaml:"maximum_lag_on_syncnode" json:"maximum_lag_on_syncnode"`
}

// DefaultSyncConfig returns default synchronous replication configuration.
func DefaultSyncConfig() *SyncConfig {
	return &SyncConfig{
		Mode:             SyncModeOff,
		StrictMode:       false,
		SyncNodeCount:    1,
		MaxLagOnSyncNode: -1, // Disabled
	}
}

// SyncReplication manages synchronous replication settings.
type SyncReplication struct {
	pg           *Postgresql
	config       *SyncConfig
	currentSync  *types.SyncState
}

// NewSyncReplication creates a new SyncReplication manager.
func NewSyncReplication(pg *Postgresql, config *SyncConfig) *SyncReplication {
	if config == nil {
		config = DefaultSyncConfig()
	}

	return &SyncReplication{
		pg:     pg,
		config: config,
	}
}

// SetConfig updates the synchronous replication configuration.
func (s *SyncReplication) SetConfig(config *SyncConfig) {
	s.config = config
}

// GetCurrentSyncState returns the current sync state.
func (s *SyncReplication) GetCurrentSyncState() *types.SyncState {
	return s.currentSync
}

// SetCurrentSyncState updates the current sync state.
func (s *SyncReplication) SetCurrentSyncState(state *types.SyncState) {
	s.currentSync = state
}

// PickSynchronousStandbys picks the best standby(s) for synchronous replication.
func (s *SyncReplication) PickSynchronousStandbys(cluster *types.Cluster) []string {
	if s.config.Mode == SyncModeOff {
		return nil
	}

	// Get eligible members
	eligible := s.getEligibleStandbys(cluster)
	if len(eligible) == 0 {
		return nil
	}

	// Sort by priority and LSN
	sort.Slice(eligible, func(i, j int) bool {
		// Higher priority first
		if eligible[i].Priority != eligible[j].Priority {
			return eligible[i].Priority > eligible[j].Priority
		}
		// Then by LSN (larger is better)
		return eligible[i].LSN > eligible[j].LSN
	})

	// Pick the required number of sync nodes
	count := s.config.SyncNodeCount
	if count > len(eligible) {
		count = len(eligible)
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = eligible[i].Name
	}

	log.Info().
		Strs("sync_standbys", result).
		Msg("Selected synchronous standbys")

	return result
}

// eligibleStandby represents a standby that can be used for sync replication.
type eligibleStandby struct {
	Name     string
	Priority int
	LSN      int64
	Lag      int64
}

// getEligibleStandbys returns standbys eligible for synchronous replication.
func (s *SyncReplication) getEligibleStandbys(cluster *types.Cluster) []eligibleStandby {
	var eligible []eligibleStandby

	for _, member := range cluster.Members {
		// Skip the leader
		if cluster.Leader != nil && member.Name == cluster.Leader.MemberName {
			continue
		}

		// Skip members with nosync tag
		if member.Data.Tags.NoSync {
			continue
		}

		// Skip members with nofailover tag (they can't become primary anyway)
		if member.Data.Tags.NoFailover {
			continue
		}

		// Check lag if configured
		if s.config.MaxLagOnSyncNode >= 0 {
			lag := s.calculateLag(cluster.Leader, member)
			if lag > s.config.MaxLagOnSyncNode {
				log.Debug().
					Str("member", member.Name).
					Int64("lag", lag).
					Int64("max_lag", s.config.MaxLagOnSyncNode).
					Msg("Member excluded from sync due to lag")
				continue
			}
		}

		// Determine priority
		priority := 1
		if member.Data.Tags.FailoverPriority > 0 {
			priority = member.Data.Tags.FailoverPriority
		}

		eligible = append(eligible, eligibleStandby{
			Name:     member.Name,
			Priority: priority,
			LSN:      member.Data.XlogLocation,
		})
	}

	return eligible
}

// calculateLag calculates the replication lag for a member.
func (s *SyncReplication) calculateLag(leader *types.Leader, member *types.Member) int64 {
	if leader == nil || leader.Member == nil {
		return 0
	}

	leaderLSN := leader.Member.Data.XlogLocation
	memberLSN := member.Data.XlogLocation

	if leaderLSN <= memberLSN {
		return 0
	}

	return leaderLSN - memberLSN
}

// GenerateSynchronousStandbyNames generates the synchronous_standby_names parameter.
func (s *SyncReplication) GenerateSynchronousStandbyNames(standbys []string) string {
	if len(standbys) == 0 {
		return ""
	}

	// Quote standby names
	quoted := make([]string, len(standbys))
	for i, name := range standbys {
		quoted[i] = quoteIdentifier(name)
	}

	if s.config.Mode == SyncModeQuorum || s.config.SyncNodeCount > 1 {
		// Use quorum or FIRST syntax
		return fmt.Sprintf("FIRST %d (%s)", s.config.SyncNodeCount, strings.Join(quoted, ", "))
	}

	// Simple case: single sync standby
	if len(standbys) == 1 {
		return quoted[0]
	}

	// Multiple potential standbys, use FIRST 1
	return fmt.Sprintf("FIRST 1 (%s)", strings.Join(quoted, ", "))
}

// quoteIdentifier quotes a PostgreSQL identifier.
func quoteIdentifier(name string) string {
	// Double any double quotes and wrap in quotes
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}

// ParseSynchronousStandbyNames parses a synchronous_standby_names value.
func ParseSynchronousStandbyNames(value string) ([]string, int, error) {
	if value == "" {
		return nil, 0, nil
	}

	value = strings.TrimSpace(value)

	// Check for FIRST n (...) or ANY n (...) syntax
	re := regexp.MustCompile(`(?i)^(FIRST|ANY)\s+(\d+)\s*\(\s*(.+)\s*\)$`)
	matches := re.FindStringSubmatch(value)

	if matches != nil {
		count, _ := strconv.Atoi(matches[2])
		names := parseStandbyList(matches[3])
		return names, count, nil
	}

	// Check for simple list: name1, name2, ...
	if strings.Contains(value, ",") {
		names := parseStandbyList(value)
		return names, 1, nil
	}

	// Single name
	name := strings.Trim(value, `"`)
	return []string{name}, 1, nil
}

// parseStandbyList parses a comma-separated list of standby names.
func parseStandbyList(list string) []string {
	var names []string

	// Split by comma, handling quoted identifiers
	parts := splitQuotedList(list)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"`)
		if part != "" {
			names = append(names, part)
		}
	}

	return names
}

// splitQuotedList splits a comma-separated list while respecting quoted strings.
func splitQuotedList(s string) []string {
	var result []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if c == '"' {
			if inQuotes && i+1 < len(s) && s[i+1] == '"' {
				// Escaped quote
				current.WriteByte('"')
				i++
			} else {
				inQuotes = !inQuotes
				current.WriteByte(c)
			}
		} else if c == ',' && !inQuotes {
			result = append(result, current.String())
			current.Reset()
		} else {
			current.WriteByte(c)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// UpdateSyncState updates the synchronous replication state in PostgreSQL.
func (s *SyncReplication) UpdateSyncState(ctx context.Context, standbys []string) error {
	syncNames := s.GenerateSynchronousStandbyNames(standbys)

	// Get current value
	currentValue, err := s.pg.GetParameter(ctx, "synchronous_standby_names")
	if err != nil {
		return fmt.Errorf("failed to get current synchronous_standby_names: %w", err)
	}

	if currentValue == syncNames {
		log.Debug().Msg("synchronous_standby_names unchanged")
		return nil
	}

	log.Info().
		Str("old", currentValue).
		Str("new", syncNames).
		Msg("Updating synchronous_standby_names")

	// Update the parameter
	if err := s.pg.SetParameter(ctx, "synchronous_standby_names", syncNames); err != nil {
		return fmt.Errorf("failed to set synchronous_standby_names: %w", err)
	}

	return nil
}

// GetSyncStandbys returns the currently configured synchronous standbys.
func (s *SyncReplication) GetSyncStandbys(ctx context.Context) ([]string, error) {
	value, err := s.pg.GetParameter(ctx, "synchronous_standby_names")
	if err != nil {
		return nil, err
	}

	names, _, err := ParseSynchronousStandbyNames(value)
	return names, err
}

// IsSyncStandby checks if a member is currently a synchronous standby.
func (s *SyncReplication) IsSyncStandby(ctx context.Context, memberName string) (bool, error) {
	standbys, err := s.GetSyncStandbys(ctx)
	if err != nil {
		return false, err
	}

	for _, name := range standbys {
		if name == memberName {
			return true, nil
		}
	}

	return false, nil
}

// WaitForSyncStandby waits for a synchronous standby to be connected.
func (s *SyncReplication) WaitForSyncStandby(ctx context.Context) (bool, error) {
	query := `
		SELECT count(*)
		FROM pg_stat_replication
		WHERE sync_state IN ('sync', 'quorum')
	`

	var count int
	err := s.pg.pool.QueryRow(ctx, query).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check sync standbys: %w", err)
	}

	return count > 0, nil
}

// GetReplicationState returns the replication state for all standbys.
func (s *SyncReplication) GetReplicationState(ctx context.Context) ([]ReplicationInfo, error) {
	query := `
		SELECT
			application_name,
			state,
			sync_state,
			sent_lsn,
			write_lsn,
			flush_lsn,
			replay_lsn,
			sync_priority
		FROM pg_stat_replication
	`

	rows, err := s.pg.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query replication state: %w", err)
	}
	defer rows.Close()

	var result []ReplicationInfo
	for rows.Next() {
		var info ReplicationInfo
		var sentLSN, writeLSN, flushLSN, replayLSN *string

		err := rows.Scan(
			&info.ApplicationName,
			&info.State,
			&info.SyncState,
			&sentLSN,
			&writeLSN,
			&flushLSN,
			&replayLSN,
			&info.SyncPriority,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan replication info: %w", err)
		}

		if sentLSN != nil {
			info.SentLSN = *sentLSN
		}
		if writeLSN != nil {
			info.WriteLSN = *writeLSN
		}
		if flushLSN != nil {
			info.FlushLSN = *flushLSN
		}
		if replayLSN != nil {
			info.ReplayLSN = *replayLSN
		}

		result = append(result, info)
	}

	return result, rows.Err()
}

// ReplicationInfo holds replication state for a single standby.
type ReplicationInfo struct {
	ApplicationName string `json:"application_name"`
	State           string `json:"state"`
	SyncState       string `json:"sync_state"`
	SentLSN         string `json:"sent_lsn"`
	WriteLSN        string `json:"write_lsn"`
	FlushLSN        string `json:"flush_lsn"`
	ReplayLSN       string `json:"replay_lsn"`
	SyncPriority    int    `json:"sync_priority"`
}

// ShouldPromoteSync checks if we should promote a sync standby to leader.
func (s *SyncReplication) ShouldPromoteSync(cluster *types.Cluster, candidate string) bool {
	if s.config.Mode == SyncModeOff {
		return true
	}

	// In strict mode, only sync standbys can be promoted
	if s.config.StrictMode {
		if s.currentSync == nil {
			return false
		}

		// Check if candidate is in the sync_standby list
		for _, sync := range s.currentSync.SyncStandby {
			if sync == candidate {
				return true
			}
		}

		return false
	}

	return true
}

// DisableSynchronousMode disables synchronous replication.
func (s *SyncReplication) DisableSynchronousMode(ctx context.Context) error {
	log.Info().Msg("Disabling synchronous replication")
	return s.pg.SetParameter(ctx, "synchronous_standby_names", "")
}

// EnableSynchronousMode enables synchronous replication with the given standbys.
func (s *SyncReplication) EnableSynchronousMode(ctx context.Context, standbys []string) error {
	if len(standbys) == 0 {
		return s.DisableSynchronousMode(ctx)
	}

	syncNames := s.GenerateSynchronousStandbyNames(standbys)
	log.Info().
		Str("synchronous_standby_names", syncNames).
		Msg("Enabling synchronous replication")

	return s.pg.SetParameter(ctx, "synchronous_standby_names", syncNames)
}

// UpdateFromDCS updates the sync state from DCS.
func (s *SyncReplication) UpdateFromDCS(state *types.SyncState) {
	s.currentSync = state
}

// BuildSyncState builds a new sync state for storing in DCS.
func (s *SyncReplication) BuildSyncState(leader string, standbys []string) *types.SyncState {
	return &types.SyncState{
		Leader:      leader,
		SyncStandby: standbys,
	}
}
