package quorum

import (
	"testing"
)

func TestStringSet(t *testing.T) {
	t.Run("NewStringSet", func(t *testing.T) {
		s := NewStringSet("a", "b", "c")
		if s.Len() != 3 {
			t.Errorf("Len() = %d, want 3", s.Len())
		}
	})

	t.Run("Add", func(t *testing.T) {
		s := NewStringSet()
		s.Add("a")
		if !s.Contains("a") {
			t.Error("Set should contain 'a'")
		}
		if s.Len() != 1 {
			t.Errorf("Len() = %d, want 1", s.Len())
		}
	})

	t.Run("Remove", func(t *testing.T) {
		s := NewStringSet("a", "b")
		s.Remove("a")
		if s.Contains("a") {
			t.Error("Set should not contain 'a'")
		}
		if s.Len() != 1 {
			t.Errorf("Len() = %d, want 1", s.Len())
		}
	})

	t.Run("Contains case insensitive", func(t *testing.T) {
		s := NewStringSet("ABC")
		if !s.Contains("abc") {
			t.Error("Contains should be case insensitive")
		}
		if !s.Contains("ABC") {
			t.Error("Contains should match original case")
		}
		if !s.Contains("AbC") {
			t.Error("Contains should be case insensitive")
		}
	})

	t.Run("Union", func(t *testing.T) {
		s1 := NewStringSet("a", "b")
		s2 := NewStringSet("b", "c")
		union := s1.Union(s2)
		if union.Len() != 3 {
			t.Errorf("Union Len() = %d, want 3", union.Len())
		}
		if !union.Contains("a") || !union.Contains("b") || !union.Contains("c") {
			t.Error("Union should contain all elements")
		}
	})

	t.Run("Intersection", func(t *testing.T) {
		s1 := NewStringSet("a", "b", "c")
		s2 := NewStringSet("b", "c", "d")
		inter := s1.Intersection(s2)
		if inter.Len() != 2 {
			t.Errorf("Intersection Len() = %d, want 2", inter.Len())
		}
		if !inter.Contains("b") || !inter.Contains("c") {
			t.Error("Intersection should contain b and c")
		}
	})

	t.Run("Difference", func(t *testing.T) {
		s1 := NewStringSet("a", "b", "c")
		s2 := NewStringSet("b", "c")
		diff := s1.Difference(s2)
		if diff.Len() != 1 {
			t.Errorf("Difference Len() = %d, want 1", diff.Len())
		}
		if !diff.Contains("a") {
			t.Error("Difference should contain a")
		}
	})

	t.Run("IsSubset", func(t *testing.T) {
		s1 := NewStringSet("a", "b")
		s2 := NewStringSet("a", "b", "c")

		if !s1.IsSubset(s2) {
			t.Error("s1 should be subset of s2")
		}
		if s2.IsSubset(s1) {
			t.Error("s2 should not be subset of s1")
		}

		empty := NewStringSet()
		if !empty.IsSubset(s1) {
			t.Error("empty set should be subset of any set")
		}
	})

	t.Run("Equal", func(t *testing.T) {
		s1 := NewStringSet("a", "b")
		s2 := NewStringSet("a", "b")
		s3 := NewStringSet("a", "c")

		if !s1.Equal(s2) {
			t.Error("s1 and s2 should be equal")
		}
		if s1.Equal(s3) {
			t.Error("s1 and s3 should not be equal")
		}
	})

	t.Run("Slice", func(t *testing.T) {
		s := NewStringSet("c", "a", "b")
		slice := s.Slice()
		if len(slice) != 3 {
			t.Errorf("Slice length = %d, want 3", len(slice))
		}
		// Should be sorted
		if slice[0] != "a" || slice[1] != "b" || slice[2] != "c" {
			t.Errorf("Slice = %v, want [a b c]", slice)
		}
	})

	t.Run("Copy", func(t *testing.T) {
		s1 := NewStringSet("a", "b")
		s2 := s1.Copy()
		s1.Add("c")

		if s1.Len() != 3 {
			t.Errorf("s1 Len() = %d, want 3", s1.Len())
		}
		if s2.Len() != 2 {
			t.Errorf("s2 Len() = %d, want 2 (copy should be independent)", s2.Len())
		}
	})
}

func TestTransitionType(t *testing.T) {
	if TransitionSync != "sync" {
		t.Errorf("TransitionSync = %q, want sync", TransitionSync)
	}
	if TransitionQuorum != "quorum" {
		t.Errorf("TransitionQuorum = %q, want quorum", TransitionQuorum)
	}
	if TransitionRestart != "restart" {
		t.Errorf("TransitionRestart = %q, want restart", TransitionRestart)
	}
}

func TestQuorumError(t *testing.T) {
	err := &QuorumError{message: "test error"}
	if err.Error() != "test error" {
		t.Errorf("QuorumError.Error() = %q, want %q", err.Error(), "test error")
	}
}

func TestNewQuorumStateResolver(t *testing.T) {
	resolver := NewQuorumStateResolver(
		"leader",     // leader
		1,            // quorum
		[]string{"b", "c"}, // voters
		2,            // numsync
		[]string{"b", "c"}, // sync
		2,            // numsyncConfirmed
		[]string{"b"}, // active
		2,            // syncWanted
		"leader",     // leaderWanted
	)

	if resolver.Leader != "leader" {
		t.Errorf("Leader = %q, want leader", resolver.Leader)
	}
	if resolver.Quorum != 1 {
		t.Errorf("Quorum = %d, want 1", resolver.Quorum)
	}
	if resolver.Voters.Len() != 2 {
		t.Errorf("Voters.Len() = %d, want 2", resolver.Voters.Len())
	}
	if resolver.NumSync != 2 {
		t.Errorf("NumSync = %d, want 2", resolver.NumSync)
	}
	if resolver.Sync.Len() != 2 {
		t.Errorf("Sync.Len() = %d, want 2", resolver.Sync.Len())
	}
	if resolver.NumSyncConfirmed != 2 {
		t.Errorf("NumSyncConfirmed = %d, want 2", resolver.NumSyncConfirmed)
	}
	if resolver.SyncWanted != 2 {
		t.Errorf("SyncWanted = %d, want 2", resolver.SyncWanted)
	}
	if resolver.LeaderWanted != "leader" {
		t.Errorf("LeaderWanted = %q, want leader", resolver.LeaderWanted)
	}
}

func TestNewQuorumStateResolverNumSyncCapping(t *testing.T) {
	// numsync should be capped at sync.Len()
	resolver := NewQuorumStateResolver(
		"leader",
		0,
		[]string{},
		5, // numsync > sync count
		[]string{"b"},
		0,
		[]string{},
		2,
		"leader",
	)

	if resolver.NumSync != 1 {
		t.Errorf("NumSync should be capped at sync.Len(), got %d", resolver.NumSync)
	}
}

func TestCheckInvariants(t *testing.T) {
	t.Run("valid state", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		err := resolver.CheckInvariants()
		if err != nil {
			t.Errorf("CheckInvariants() should pass, got %v", err)
		}
	})

	t.Run("empty state", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		err := resolver.CheckInvariants()
		if err != nil {
			t.Errorf("CheckInvariants() for empty state should pass, got %v", err)
		}
	})
}

func TestQuorumUpdate(t *testing.T) {
	t.Run("negative quorum", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		_, err := resolver.QuorumUpdate(-1, NewStringSet(), nil, false)
		if err == nil {
			t.Error("QuorumUpdate with negative quorum should error")
		}
	})

	t.Run("quorum >= voters", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		_, err := resolver.QuorumUpdate(2, NewStringSet("b"), nil, false)
		if err == nil {
			t.Error("QuorumUpdate with quorum >= voters.Len() should error")
		}
	})

	t.Run("valid update", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		transitions, err := resolver.QuorumUpdate(0, NewStringSet("b"), nil, false)
		if err != nil {
			t.Errorf("Valid QuorumUpdate should not error, got %v", err)
		}
		if len(transitions) == 0 {
			t.Error("QuorumUpdate should return transitions")
		}
	})

	t.Run("no change with empty voters", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		transitions, err := resolver.QuorumUpdate(0, NewStringSet(), nil, false)
		if err != nil {
			t.Errorf("QuorumUpdate should not error, got %v", err)
		}
		// With empty voters it should return restart transition
		if len(transitions) > 0 && transitions[0].Type != TransitionRestart {
			t.Errorf("Expected restart transition, got %v", transitions)
		}
	})

	t.Run("with leader change", func(t *testing.T) {
		// Test that leader pointer changes the leader field
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		newLeader := "b" // Change to a node that's in sync
		// This may fail invariants but we're testing the leader pointer behavior
		_, _ = resolver.QuorumUpdate(0, NewStringSet("b"), &newLeader, false)
		if resolver.Leader != "b" {
			t.Errorf("Leader should be updated to b, got %s", resolver.Leader)
		}
	})
}

func TestSyncUpdate(t *testing.T) {
	t.Run("negative numsync", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		_, err := resolver.SyncUpdate(-1, NewStringSet())
		if err == nil {
			t.Error("SyncUpdate with negative numsync should error")
		}
	})

	t.Run("numsync > sync.Len()", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a",
		)
		_, err := resolver.SyncUpdate(2, NewStringSet("b"))
		if err == nil {
			t.Error("SyncUpdate with numsync > sync.Len() should error")
		}
	})

	t.Run("valid update", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		transitions, err := resolver.SyncUpdate(1, NewStringSet("b"))
		if err != nil {
			t.Errorf("Valid SyncUpdate should not error, got %v", err)
		}
		if len(transitions) == 0 {
			t.Error("SyncUpdate should return transitions")
		}
		if transitions[0].Type != TransitionSync {
			t.Errorf("Expected sync transition, got %v", transitions[0].Type)
		}
	})
}

func TestGenerateTransitions(t *testing.T) {
	t.Run("steady state no changes", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.GenerateTransitions()
		// In steady state with matching sync/voters, should have minimal transitions
		t.Logf("Transitions: %v", transitions)
	})

	t.Run("add new node", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{"b"}, 2, "a",
		)
		transitions := resolver.GenerateTransitions()
		// Should have transitions to add node b
		if len(transitions) == 0 {
			t.Error("Should generate transitions to add new node")
		}
	})

	t.Run("remove inactive node", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 1, []string{"b", "c"}, 1, []string{"b", "c"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.GenerateTransitions()
		// Should have transitions to remove inactive node c
		t.Logf("Transitions: %v", transitions)
	})

	t.Run("leader change", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "c",
		)
		transitions := resolver.GenerateTransitions()
		// Should generate transitions for leader change
		if len(transitions) == 0 {
			t.Error("Should generate transitions for leader change")
		}
	})
}

func TestMergeTransitions(t *testing.T) {
	resolver := NewQuorumStateResolver("a", 0, []string{}, 0, []string{}, 0, []string{}, 0, "a")

	t.Run("empty transitions", func(t *testing.T) {
		result := resolver.mergeTransitions(nil)
		if result != nil {
			t.Errorf("mergeTransitions(nil) should return nil, got %v", result)
		}
	})

	t.Run("merge consecutive same type", func(t *testing.T) {
		transitions := []Transition{
			{Type: TransitionSync, Leader: "a", Num: 1, Names: NewStringSet("b")},
			{Type: TransitionSync, Leader: "a", Num: 2, Names: NewStringSet("b", "c")},
			{Type: TransitionQuorum, Leader: "a", Num: 0, Names: NewStringSet("b", "c")},
		}
		result := resolver.mergeTransitions(transitions)
		// Should keep only last sync and the quorum
		if len(result) != 2 {
			t.Errorf("mergeTransitions should merge consecutive same types, got %d transitions", len(result))
		}
	})

	t.Run("stop at restart", func(t *testing.T) {
		transitions := []Transition{
			{Type: TransitionSync, Leader: "a", Num: 1, Names: NewStringSet("b")},
			{Type: TransitionRestart, Leader: "a", Num: 0, Names: NewStringSet()},
			{Type: TransitionQuorum, Leader: "a", Num: 0, Names: NewStringSet("b")},
		}
		result := resolver.mergeTransitions(transitions)
		// Should stop at restart
		if len(result) != 2 {
			t.Errorf("mergeTransitions should stop at restart, got %d transitions", len(result))
		}
		if result[len(result)-1].Type != TransitionRestart {
			t.Error("Last transition should be restart")
		}
	})
}

func TestHandleNonSteadyCases(t *testing.T) {
	t.Run("sync subset of voters", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 1, []string{"b", "c", "d"}, 2, []string{"b", "c"}, 2, []string{"b", "c", "d"}, 2, "a",
		)
		transitions := resolver.handleNonSteadyCases()
		t.Logf("Case 1 transitions: %v", transitions)
	})

	t.Run("voters subset of sync", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 2, []string{"b", "c"}, 1, []string{"b", "c"}, 2, "a",
		)
		transitions := resolver.handleNonSteadyCases()
		t.Logf("Case 2 transitions: %v", transitions)
	})
}

func TestRemoveGoneNodes(t *testing.T) {
	t.Run("all nodes gone", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{}, 1, "a",
		)
		transitions := resolver.removeGoneNodes()
		if len(transitions) == 0 {
			t.Error("Should generate transitions when all nodes gone")
		}
	})

	t.Run("partial nodes gone", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 1, []string{"b", "c"}, 1, []string{"b", "c"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.removeGoneNodes()
		t.Logf("Remove gone nodes transitions: %v", transitions)
	})

	t.Run("no nodes gone", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.removeGoneNodes()
		if len(transitions) != 0 {
			t.Error("Should not generate transitions when no nodes gone")
		}
	})
}

func TestAddNewNodes(t *testing.T) {
	t.Run("no new nodes", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.addNewNodes()
		if len(transitions) != 0 {
			t.Error("Should not generate transitions when no new nodes")
		}
	})

	t.Run("add new node", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b", "c"}, 2, "a",
		)
		transitions := resolver.addNewNodes()
		if len(transitions) == 0 {
			t.Error("Should generate transitions to add new node")
		}
	})

	t.Run("add first node", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{}, 0, []string{}, 0, []string{"b"}, 2, "a",
		)
		transitions := resolver.addNewNodes()
		if len(transitions) == 0 {
			t.Error("Should generate transitions to add first node")
		}
	})
}

func TestHandleReplicationFactorChange(t *testing.T) {
	t.Run("increase replication factor", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 1, []string{"b", "c"}, 1, []string{"b", "c"}, 1, []string{"b", "c"}, 2, "a",
		)
		transitions := resolver.handleReplicationFactorChange()
		if len(transitions) == 0 {
			t.Error("Should generate transitions to increase replication factor")
		}
	})

	t.Run("decrease replication factor", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b", "c"}, 2, []string{"b", "c"}, 2, []string{"b", "c"}, 1, "a",
		)
		transitions := resolver.handleReplicationFactorChange()
		if len(transitions) == 0 {
			t.Error("Should generate transitions to decrease replication factor")
		}
	})

	t.Run("no change", func(t *testing.T) {
		resolver := NewQuorumStateResolver(
			"a", 0, []string{"b"}, 1, []string{"b"}, 1, []string{"b"}, 1, "a",
		)
		transitions := resolver.handleReplicationFactorChange()
		if len(transitions) != 0 {
			t.Error("Should not generate transitions when no replication factor change")
		}
	})
}

func TestTransitionStruct(t *testing.T) {
	transition := Transition{
		Type:   TransitionSync,
		Leader: "a",
		Num:    2,
		Names:  NewStringSet("b", "c"),
	}

	if transition.Type != TransitionSync {
		t.Errorf("Type = %q, want sync", transition.Type)
	}
	if transition.Leader != "a" {
		t.Errorf("Leader = %q, want a", transition.Leader)
	}
	if transition.Num != 2 {
		t.Errorf("Num = %d, want 2", transition.Num)
	}
	if transition.Names.Len() != 2 {
		t.Errorf("Names.Len() = %d, want 2", transition.Names.Len())
	}
}
