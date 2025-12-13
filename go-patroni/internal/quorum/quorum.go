// Package quorum implements a state machine to manage synchronous_standby_names
// and the /sync key in DCS for PostgreSQL synchronous replication.
package quorum

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

// TransitionType represents the type of state transition.
type TransitionType string

const (
	// TransitionSync indicates synchronous_standby_names needs update.
	TransitionSync TransitionType = "sync"
	// TransitionQuorum indicates /sync key in DCS needs update.
	TransitionQuorum TransitionType = "quorum"
	// TransitionRestart indicates caller should restart QuorumStateResolver.
	TransitionRestart TransitionType = "restart"
)

// Transition describes a transition of /sync or synchronous_standby_names to a new state.
type Transition struct {
	Type   TransitionType
	Leader string
	Num    int
	Names  StringSet
}

// QuorumError indicates the quorum state is broken.
type QuorumError struct {
	message string
}

func (e *QuorumError) Error() string {
	return e.message
}

// StringSet is a case-insensitive set of strings.
type StringSet map[string]struct{}

// NewStringSet creates a new StringSet from a slice.
func NewStringSet(items ...string) StringSet {
	s := make(StringSet)
	for _, item := range items {
		s.Add(item)
	}
	return s
}

// Add adds an item to the set.
func (s StringSet) Add(item string) {
	s[strings.ToLower(item)] = struct{}{}
}

// Remove removes an item from the set.
func (s StringSet) Remove(item string) {
	delete(s, strings.ToLower(item))
}

// Contains checks if the set contains an item.
func (s StringSet) Contains(item string) bool {
	_, ok := s[strings.ToLower(item)]
	return ok
}

// Len returns the size of the set.
func (s StringSet) Len() int {
	return len(s)
}

// Union returns a new set with items from both sets.
func (s StringSet) Union(other StringSet) StringSet {
	result := NewStringSet()
	for k := range s {
		result[k] = struct{}{}
	}
	for k := range other {
		result[k] = struct{}{}
	}
	return result
}

// Intersection returns a new set with items in both sets.
func (s StringSet) Intersection(other StringSet) StringSet {
	result := NewStringSet()
	for k := range s {
		if _, ok := other[k]; ok {
			result[k] = struct{}{}
		}
	}
	return result
}

// Difference returns a new set with items in s but not in other.
func (s StringSet) Difference(other StringSet) StringSet {
	result := NewStringSet()
	for k := range s {
		if _, ok := other[k]; !ok {
			result[k] = struct{}{}
		}
	}
	return result
}

// IsSubset checks if s is a subset of other.
func (s StringSet) IsSubset(other StringSet) bool {
	for k := range s {
		if _, ok := other[k]; !ok {
			return false
		}
	}
	return true
}

// Equal checks if two sets are equal.
func (s StringSet) Equal(other StringSet) bool {
	if len(s) != len(other) {
		return false
	}
	return s.IsSubset(other)
}

// Slice returns the set as a sorted slice.
func (s StringSet) Slice() []string {
	result := make([]string, 0, len(s))
	for k := range s {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// Copy returns a copy of the set.
func (s StringSet) Copy() StringSet {
	result := make(StringSet, len(s))
	for k := range s {
		result[k] = struct{}{}
	}
	return result
}

// QuorumStateResolver calculates state transitions for synchronous replication.
type QuorumStateResolver struct {
	Leader          string
	Quorum          int
	Voters          StringSet
	NumSync         int
	Sync            StringSet
	NumSyncConfirmed int
	Active          StringSet
	SyncWanted      int
	LeaderWanted    string
}

// NewQuorumStateResolver creates a new resolver with the given state.
func NewQuorumStateResolver(
	leader string, quorum int, voters []string,
	numsync int, sync []string, numsyncConfirmed int,
	active []string, syncWanted int, leaderWanted string,
) *QuorumStateResolver {
	syncSet := NewStringSet(sync...)
	return &QuorumStateResolver{
		Leader:          leader,
		Quorum:          quorum,
		Voters:          NewStringSet(voters...),
		NumSync:         min(numsync, syncSet.Len()),
		Sync:            syncSet,
		NumSyncConfirmed: numsyncConfirmed,
		Active:          NewStringSet(active...),
		SyncWanted:      syncWanted,
		LeaderWanted:    leaderWanted,
	}
}

// CheckInvariants verifies the invariant of synchronous_standby_names and /sync key.
func (r *QuorumStateResolver) CheckInvariants() error {
	voters := r.Voters.Union(NewStringSet(r.Leader))
	sync := r.Sync.Union(NewStringSet(r.LeaderWanted))

	// Verify subset of nodes that can acknowledge a commit overlaps with
	// any subset of nodes that can achieve quorum to promote a new leader
	if r.Voters.Len() > 0 && voters.Union(sync).Len() > r.Quorum+r.NumSync+1 {
		return &QuorumError{
			message: fmt.Sprintf("Quorum and sync not guaranteed to overlap: nodes %d >= quorum %d + sync %d + 1",
				voters.Union(sync).Len(), r.Quorum, r.NumSync),
		}
	}

	// Check for mismatched sets
	if !voters.IsSubset(sync) && !sync.IsSubset(voters) {
		votersOnly := voters.Difference(sync)
		syncOnly := sync.Difference(voters)
		return &QuorumError{
			message: fmt.Sprintf("Mismatched sets: voter only=%v sync only=%v",
				votersOnly.Slice(), syncOnly.Slice()),
		}
	}

	return nil
}

// QuorumUpdate updates quorum, voters and optionally leader fields.
func (r *QuorumStateResolver) QuorumUpdate(quorum int, voters StringSet, leader *string, adjustQuorum bool) ([]Transition, error) {
	if quorum < 0 {
		return nil, &QuorumError{message: fmt.Sprintf("Quorum %d < 0 of (%v)", quorum, voters.Slice())}
	}
	if quorum > 0 && quorum >= voters.Len() {
		return nil, &QuorumError{message: fmt.Sprintf("Quorum %d >= N of (%v)", quorum, voters.Slice())}
	}

	oldLeader := r.Leader
	if leader != nil {
		r.Leader = *leader
	} else if r.NumSyncConfirmed == 0 && r.Voters.Len() == 0 {
		quorum = 0
		voters = NewStringSet()
	} else if adjustQuorum {
		quorum += max(r.NumSync-r.NumSyncConfirmed, 0)
	}

	if r.Leader == oldLeader && quorum == r.Quorum && voters.Equal(r.Voters) {
		if r.Voters.Len() > 0 {
			return nil, nil
		}
		return []Transition{{Type: TransitionRestart, Leader: r.Leader, Num: r.Quorum, Names: r.Voters}}, nil
	}

	r.Quorum = quorum
	r.Voters = voters

	if err := r.CheckInvariants(); err != nil {
		return nil, err
	}

	log.Debug().Str("leader", r.Leader).Int("quorum", r.Quorum).Strs("voters", r.Voters.Slice()).Msg("quorum update")
	return []Transition{{Type: TransitionQuorum, Leader: r.Leader, Num: r.Quorum, Names: r.Voters}}, nil
}

// SyncUpdate updates numsync and sync fields.
func (r *QuorumStateResolver) SyncUpdate(numsync int, sync StringSet) ([]Transition, error) {
	if numsync < 0 {
		return nil, &QuorumError{message: fmt.Sprintf("Sync %d < 0 of (%v)", numsync, sync.Slice())}
	}
	if numsync > sync.Len() {
		return nil, &QuorumError{message: fmt.Sprintf("Sync %d > N of (%v)", numsync, sync.Slice())}
	}

	r.NumSync = numsync
	r.Sync = sync

	if err := r.CheckInvariants(); err != nil {
		return nil, err
	}

	log.Debug().Str("leader", r.Leader).Int("numsync", r.NumSync).Strs("sync", r.Sync.Slice()).Msg("sync update")
	return []Transition{{Type: TransitionSync, Leader: r.Leader, Num: r.NumSync, Names: r.Sync}}, nil
}

// GenerateTransitions produces transitions to move from current state to desired state.
func (r *QuorumStateResolver) GenerateTransitions() []Transition {
	var transitions []Transition

	log.Debug().
		Str("leader", r.Leader).
		Int("quorum", r.Quorum).
		Strs("voters", r.Voters.Slice()).
		Int("numsync", r.NumSync).
		Strs("sync", r.Sync.Slice()).
		Int("numsync_confirmed", r.NumSyncConfirmed).
		Strs("active", r.Active.Slice()).
		Int("sync_wanted", r.SyncWanted).
		Str("leader_wanted", r.LeaderWanted).
		Msg("Quorum state")

	// Handle leader change (failover)
	if r.LeaderWanted != r.Leader {
		voters := r.Voters.Difference(NewStringSet(r.LeaderWanted)).Union(NewStringSet(r.Leader))
		if r.Sync.Len() == 0 {
			numsync := voters.Len() - r.Quorum
			if t, err := r.SyncUpdate(numsync, voters.Copy()); err == nil {
				transitions = append(transitions, t...)
			}
		}
		if t, err := r.QuorumUpdate(r.Quorum, voters.Copy(), &r.LeaderWanted, true); err == nil {
			transitions = append(transitions, t...)
		}
		if r.Sync.Intersection(r.Active).Len() == 0 {
			return r.mergeTransitions(transitions)
		}
	} else {
		if err := r.CheckInvariants(); err != nil {
			var qe *QuorumError
			if errors.As(err, &qe) {
				log.Warn().Msg(qe.Error())
				if t, err := r.QuorumUpdate(r.Sync.Len()-r.NumSync, r.Sync.Copy(), nil, true); err == nil {
					transitions = append(transitions, t...)
				}
			}
		}
	}

	// Adjust numsync_confirmed if 0 after restart/failover
	if r.NumSyncConfirmed == 0 && r.Sync.Intersection(r.Active).Len() > 0 {
		r.NumSyncConfirmed = min(r.Sync.Intersection(r.Active).Len(), r.Voters.Len()-r.Quorum)
		log.Debug().Int("numsync_confirmed", r.NumSyncConfirmed).Msg("adjusted numsync_confirmed")
	}

	// Handle non-steady cases
	transitions = append(transitions, r.handleNonSteadyCases()...)

	// Remove gone nodes
	transitions = append(transitions, r.removeGoneNodes()...)

	// Add new nodes
	transitions = append(transitions, r.addNewNodes()...)

	// Handle replication factor change
	transitions = append(transitions, r.handleReplicationFactorChange()...)

	return r.mergeTransitions(transitions)
}

// handleNonSteadyCases handles cases when previous transitions were interrupted.
func (r *QuorumStateResolver) handleNonSteadyCases() []Transition {
	var transitions []Transition

	if r.Sync.Len() < r.Voters.Len() && r.Sync.IsSubset(r.Voters) {
		// Case 1: voters is superset of sync nodes
		log.Debug().Strs("sync", r.Sync.Slice()).Strs("voters", r.Voters.Slice()).Msg("Case 1: sync is subset of voters")
		removeFromVoters := r.Voters.Difference(r.Sync.Union(r.Active))
		if removeFromVoters.Len() > 0 {
			newVoters := r.Voters.Difference(removeFromVoters)
			adjustQuorum := r.Sync.Difference(r.Active).Len() == 0
			if t, err := r.QuorumUpdate(newVoters.Len()-r.NumSync, newVoters, nil, adjustQuorum); err == nil {
				transitions = append(transitions, t...)
			}
		}
		addToSync := r.Voters.Intersection(r.Active).Difference(r.Sync)
		if addToSync.Len() > 0 {
			if t, err := r.SyncUpdate(r.NumSync, r.Sync.Union(addToSync)); err == nil {
				transitions = append(transitions, t...)
			}
		}
	} else if r.Voters.Len() < r.Sync.Len() && r.Voters.IsSubset(r.Sync) {
		// Case 2: sync is superset of voters nodes
		log.Debug().Strs("sync", r.Sync.Slice()).Strs("voters", r.Voters.Slice()).Msg("Case 2: sync is superset of voters")
		removeFromSync := r.Sync.Difference(r.Active)
		sync := r.Sync.Difference(removeFromSync)
		if removeFromSync.Len() > 0 && sync.Len() > 0 && (r.Voters.Len() == 0 || sync.Intersection(r.Voters).Len() > 0) {
			if t, err := r.SyncUpdate(min(r.NumSync, r.Sync.Len()-removeFromSync.Len()), sync); err == nil {
				transitions = append(transitions, t...)
			}
		}
		addToVoters := r.Sync.Difference(r.Voters).Intersection(r.Active)
		if addToVoters.Len() > 0 {
			voters := r.Voters.Union(addToVoters)
			if t, err := r.QuorumUpdate(voters.Len()-r.NumSync, voters, nil, true); err == nil {
				transitions = append(transitions, t...)
			}
		}
		removeFromSync = r.Sync.Difference(r.Voters)
		if removeFromSync.Len() > 0 {
			if t, err := r.SyncUpdate(min(r.NumSync, r.Sync.Len()-removeFromSync.Len()), r.Sync.Difference(removeFromSync)); err == nil {
				transitions = append(transitions, t...)
			}
		}
	}

	return transitions
}

// removeGoneNodes removes inactive nodes from sync and voters.
func (r *QuorumStateResolver) removeGoneNodes() []Transition {
	var transitions []Transition
	toRemove := r.Sync.Difference(r.Active)

	if toRemove.Len() == 0 {
		return nil
	}

	log.Debug().Strs("nodes", toRemove.Slice()).Msg("Removing nodes")

	if toRemove.Equal(r.Sync) {
		// All sync nodes are gone
		if t, err := r.QuorumUpdate(0, NewStringSet(), nil, false); err == nil {
			transitions = append(transitions, t...)
		}
		if t, err := r.SyncUpdate(0, NewStringSet()); err == nil {
			transitions = append(transitions, t...)
		}
		return transitions
	}

	// Remove nodes while maintaining invariants
	canReduceQuorumBy := r.Quorum
	if canReduceQuorumBy > 0 {
		removeList := toRemove.Slice()
		sort.Sort(sort.Reverse(sort.StringSlice(removeList)))
		if len(removeList) > canReduceQuorumBy {
			removeList = removeList[:canReduceQuorumBy]
		}
		remove := NewStringSet(removeList...)

		sync := r.Sync.Difference(remove)
		numsync := r.NumSync
		if r.SyncWanted > r.NumSync {
			numsync = r.SyncWanted
		}
		numsync = min(numsync, sync.Len())

		if t, err := r.SyncUpdate(numsync, sync); err == nil {
			transitions = append(transitions, t...)
		}

		voters := r.Voters.Difference(remove)
		toRemove = toRemove.Intersection(r.Sync)
		adjustQuorum := toRemove.Len() == 0
		if t, err := r.QuorumUpdate(voters.Len()-r.NumSync, voters, nil, adjustQuorum); err == nil {
			transitions = append(transitions, t...)
		}
	}

	return transitions
}

// addNewNodes adds new active nodes to sync and voters.
func (r *QuorumStateResolver) addNewNodes() []Transition {
	var transitions []Transition
	toAdd := r.Active.Difference(r.Sync)

	if toAdd.Len() == 0 {
		return nil
	}

	log.Debug().Strs("nodes", toAdd.Slice()).Msg("Adding nodes")

	syncWanted := min(r.SyncWanted, r.Sync.Union(toAdd).Len())
	increaseNumsyncBy := syncWanted - r.NumSync

	if increaseNumsyncBy > 0 {
		var add StringSet
		if r.Sync.Len() > 0 {
			addList := toAdd.Slice()
			sort.Strings(addList)
			if len(addList) > increaseNumsyncBy {
				addList = addList[:increaseNumsyncBy]
			}
			add = NewStringSet(addList...)
			increaseNumsyncBy = add.Len()
		} else {
			add = toAdd.Copy()
		}

		if t, err := r.SyncUpdate(r.NumSync+increaseNumsyncBy, r.Sync.Union(add)); err == nil {
			transitions = append(transitions, t...)
		}

		voters := r.Voters.Union(add)
		if t, err := r.QuorumUpdate(voters.Len()-syncWanted, voters, nil, true); err == nil {
			transitions = append(transitions, t...)
		}

		toAdd = toAdd.Difference(r.Sync)
	}

	if toAdd.Len() > 0 {
		voters := r.Voters.Union(toAdd)
		adjustQuorum := syncWanted > r.NumSyncConfirmed
		if t, err := r.QuorumUpdate(voters.Len()-syncWanted, voters, nil, adjustQuorum); err == nil {
			transitions = append(transitions, t...)
		}
		if t, err := r.SyncUpdate(syncWanted, r.Sync.Union(toAdd)); err == nil {
			transitions = append(transitions, t...)
		}
	}

	return transitions
}

// handleReplicationFactorChange handles changes to sync_wanted.
func (r *QuorumStateResolver) handleReplicationFactorChange() []Transition {
	var transitions []Transition

	syncIncrease := min(r.SyncWanted, r.Sync.Len()) - r.NumSync

	if syncIncrease > 0 {
		log.Debug().Int("target", r.NumSync+syncIncrease).Msg("Increasing replication factor")
		if t, err := r.SyncUpdate(r.NumSync+syncIncrease, r.Sync); err == nil {
			transitions = append(transitions, t...)
		}
		if t, err := r.QuorumUpdate(r.Voters.Len()-r.NumSync, r.Voters, nil, true); err == nil {
			transitions = append(transitions, t...)
		}
	} else if syncIncrease < 0 {
		log.Debug().Int("target", r.NumSync+syncIncrease).Msg("Reducing replication factor")
		if r.Quorum-syncIncrease < r.Voters.Len() {
			adjustQuorum := r.SyncWanted > r.NumSyncConfirmed
			if t, err := r.QuorumUpdate(r.Voters.Len()-r.NumSync-syncIncrease, r.Voters, nil, adjustQuorum); err == nil {
				transitions = append(transitions, t...)
			}
		}
		if t, err := r.SyncUpdate(r.NumSync+syncIncrease, r.Sync); err == nil {
			transitions = append(transitions, t...)
		}
	}

	return transitions
}

// mergeTransitions merges consecutive transitions of the same type.
func (r *QuorumStateResolver) mergeTransitions(transitions []Transition) []Transition {
	if len(transitions) == 0 {
		return nil
	}

	var result []Transition
	for i, t := range transitions {
		if i+1 < len(transitions) && t.Type == transitions[i+1].Type {
			continue
		}
		result = append(result, t)
		if t.Type == TransitionRestart {
			break
		}
	}
	return result
}
