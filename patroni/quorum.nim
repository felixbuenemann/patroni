## Implement state machine to manage ``synchronous_standby_names`` GUC and ``/sync`` key in DCS.

import std/[algorithm, options, sequtils, strformat, tables]
import ./collections
import ./exceptions
import ./log

let logger = getLogger("patroni.quorum")

type
  TransitionType* = enum
    ## Type of transition.
    ttSync = "sync"       ## Indicates that we needed to update synchronous_standby_names.
    ttQuorum = "quorum"   ## Indicates that we need to update /sync key in DCS.
    ttRestart = "restart" ## Caller should stop iterating over transitions and restart QuorumStateResolver.

  Transition* = object
    ## Object describing transition of /sync or synchronous_standby_names to the new state.
    ##
    ## .. note::
    ##     Object attributes represent the new state.
    ##
    ## :ivar transitionType: possible values: sync, quorum, restart.
    ## :ivar leader: the new value of the leader field in the /sync key.
    ## :ivar num: the new value of the synchronous nodes count in synchronous_standby_names or value of the quorum
    ##            field in the /sync key for transitionType values sync and quorum respectively.
    ## :ivar names: the new value of node names listed in synchronous_standby_names or value of voters
    ##              field in the /sync key for transitionType values sync and quorum respectively.
    transitionType*: TransitionType
    leader*: string
    num*: int
    names*: CaseInsensitiveSet

  QuorumError* = object of PatroniException
    ## Exception indicating that the quorum state is broken.

  QuorumStateResolver* = ref object
    ## Calculates a list of state transitions and yields them as Transition objects.
    ##
    ## Synchronous replication state is set in two places:
    ##
    ## * PostgreSQL configuration sets how many and which nodes are needed for a commit to succeed, abbreviated as
    ##   numsync and sync set here;
    ## * DCS contains information about how many and which nodes need to be interrogated to be sure to see a wal position
    ##   containing latest confirmed commit, abbreviated as quorum and voters set.
    ##
    ## .. note::
    ##     Both of above pairs have the meaning "ANY n OF set".
    ##
    ##     The number of nodes needed for commit to succeed, numsync, is also called the replication factor.
    leader*: string
    quorum*: int
    voters*: CaseInsensitiveSet
    numsync*: int
    sync*: CaseInsensitiveSet
    numsyncConfirmed*: int
    active*: CaseInsensitiveSet
    syncWanted*: int
    leaderWanted*: string

proc newTransition*(transitionType: TransitionType, leader: string, num: int, names: CaseInsensitiveSet): Transition =
  result.transitionType = transitionType
  result.leader = leader
  result.num = num
  result.names = names

proc newQuorumStateResolver*(leader: string, quorum: int, voters: openArray[string],
                             numsync: int, sync: openArray[string], numsyncConfirmed: int,
                             active: openArray[string], syncWanted: int, leaderWanted: string): QuorumStateResolver =
  ## Instantiate QuorumStateResolver based on input parameters.
  ##
  ## :param leader: name of the leader, according to the /sync key.
  ## :param quorum: quorum value from the /sync key, the minimal number of nodes we need see
  ##                 when doing the leader race.
  ## :param voters: sync_standby value from the /sync key, set of node names we will be
  ##                running the leader race against.
  ## :param numsync: the number of synchronous nodes from the synchronous_standby_names.
  ## :param sync: Set of node names listed in the synchronous_standby_names.
  ## :param numsyncConfirmed: the number of nodes that are confirmed to reach "safe" LSN after
  ##                           they were added to the synchronous_standby_names.
  ## :param active: set of node names that are replicating from the primary (according to pg_stat_replication)
  ##                and are eligible to be listed in synchronous_standby_names.
  ## :param syncWanted: desired number of synchronous nodes
  ##                     (synchronous_node_count from the global configuration).
  ## :param leaderWanted: the desired leader (could be different from the leader right after a failover).
  new(result)
  result.leader = leader
  result.quorum = quorum
  result.voters = newCaseInsensitiveSet(voters)
  result.numsync = min(numsync, sync.len)  # numsync can't be bigger than number of listed synchronous nodes.
  result.sync = newCaseInsensitiveSet(sync)
  result.numsyncConfirmed = numsyncConfirmed
  result.active = newCaseInsensitiveSet(active)
  result.syncWanted = syncWanted
  result.leaderWanted = leaderWanted

proc checkInvariants*(self: QuorumStateResolver) =
  ## Checks invariant of synchronous_standby_names and /sync key in DCS.
  ##
  ## :raises: QuorumError in case of broken state
  var votersSet = self.voters
  votersSet.incl(self.leader)
  var syncSet = self.sync
  syncSet.incl(self.leaderWanted)

  # We need to verify that subset of nodes that can acknowledge a commit overlaps
  # with any subset of nodes that can achieve quorum to promote a new leader.
  # +1 is required because the leader is included in the set.
  if self.voters.len > 0:
    let combined = votersSet.union(syncSet)
    if not (combined.len <= self.quorum + self.numsync + 1):
      let lenNodes = combined.len
      raise newException(QuorumError,
        fmt"Quorum and sync not guaranteed to overlap: nodes {lenNodes} >= quorum {self.quorum} + sync {self.numsync} + 1")

  # unstable cases, we are changing synchronous_standby_names and /sync key
  # one after another, hence one set is allowed to be a subset of another
  if not (votersSet.isSubsetOf(syncSet) or syncSet.isSubsetOf(votersSet)):
    let votersOnly = votersSet.difference(syncSet)
    let syncOnly = syncSet.difference(votersSet)
    raise newException(QuorumError,
      fmt"Mismatched sets: voter only={votersOnly} sync only={syncOnly}")

proc quorumUpdate*(self: QuorumStateResolver, quorum: int, voters: CaseInsensitiveSet,
                   leader: string = "", adjustQuorum: bool = true): seq[Transition] =
  ## Updates quorum, voters and optionally leader fields.
  ##
  ## :param quorum: the new value for quorum, could be adjusted depending
  ##                on values of numsyncConfirmed and adjustQuorum.
  ## :param voters: the new value for voters, could be adjusted if numsyncConfirmed == 0.
  ## :param leader: the new value for leader, optional.
  ## :param adjustQuorum: if set to true the quorum requirement will be increased by the
  ##                       difference between numsync and numsyncConfirmed.
  ##
  ## :returns: the new state of the /sync key as Transition objects.
  ##
  ## :raises: QuorumError in case of invalid data or if the invariant after transition could not be satisfied.
  result = @[]
  var newQuorum = quorum
  var newVoters = voters

  if newQuorum < 0:
    raise newException(QuorumError, fmt"Quorum {newQuorum} < 0 of ({voters})")
  if newQuorum > 0 and newQuorum >= voters.len:
    raise newException(QuorumError, fmt"Quorum {newQuorum} >= N of ({voters})")

  let oldLeader = self.leader
  if leader.len > 0:  # Change of leader was requested
    self.leader = leader
  elif self.numsyncConfirmed == 0 and self.voters.len == 0:
    # If there are no nodes that known to caught up with the primary we want to reset quorum/voters in /sync key
    newQuorum = 0
    newVoters = newCaseInsensitiveSet(@[])
  elif adjustQuorum:
    # It could be that the number of nodes that are known to catch up with the primary is below desired numsync.
    # We want to increase quorum to guarantee that the sync node will be found during the leader race.
    newQuorum += max(self.numsync - self.numsyncConfirmed, 0)

  if self.leader == oldLeader and newQuorum == self.quorum and newVoters == self.voters:
    if self.voters.len > 0:
      return @[]
    # If transition produces no change of leader/quorum/voters we want to give a hint to
    # the caller to fetch the new state from the database and restart QuorumStateResolver.
    return @[newTransition(ttRestart, self.leader, self.quorum, self.voters)]

  self.quorum = newQuorum
  self.voters = newVoters
  self.checkInvariants()
  logger.debug(fmt"quorum {self.leader} {self.quorum} {self.voters}")
  result.add(newTransition(ttQuorum, self.leader, self.quorum, self.voters))

proc syncUpdate*(self: QuorumStateResolver, numsync: int, sync: CaseInsensitiveSet): seq[Transition] =
  ## Updates numsync and sync fields.
  ##
  ## :param numsync: the new value for numsync.
  ## :param sync: the new value for sync:
  ##
  ## :returns: the new state of synchronous_standby_names as Transition objects.
  ##
  ## :raises: QuorumError in case of invalid data or if invariant after transition could not be satisfied
  result = @[]

  if numsync < 0:
    raise newException(QuorumError, fmt"Sync {numsync} < 0 of ({sync})")
  if numsync > sync.len:
    raise newException(QuorumError, fmt"Sync {numsync} > N of ({sync})")

  self.numsync = numsync
  self.sync = sync
  self.checkInvariants()
  logger.debug(fmt"sync {self.leader} {self.numsync} {self.sync}")
  result.add(newTransition(ttSync, self.leader, self.numsync, self.sync))

proc handleNonSteadyCases(self: QuorumStateResolver): seq[Transition] =
  ## Handle cases when set of transitions produced on previous run was interrupted.
  result = @[]

  if self.sync.isProperSubsetOf(self.voters):
    logger.debug(fmt"Case 1: synchronous_standby_names {self.sync} is a subset of DCS state {self.voters}")
    # Case 1: voters is superset of sync nodes. In the middle of changing voters (quorum).
    # Evict dead nodes from voters that are not being synced.
    let removeFromVoters = self.voters.difference(self.sync.union(self.active))
    if removeFromVoters.len > 0:
      let newVoters = self.voters.difference(removeFromVoters)
      let shouldAdjust = not (self.sync.difference(self.active).len > 0)
      result.add(self.quorumUpdate(
        self.voters.len - removeFromVoters.len - self.numsync,
        newVoters,
        adjustQuorum = shouldAdjust))
    # Start syncing to nodes that are in voters and alive
    let addToSync = self.voters.intersection(self.active).difference(self.sync)
    if addToSync.len > 0:
      result.add(self.syncUpdate(self.numsync, self.sync.union(addToSync)))

  elif self.voters.isProperSubsetOf(self.sync):
    logger.debug(fmt"Case 2: synchronous_standby_names {self.sync} is a superset of DCS state {self.voters}")
    # Case 2: sync is superset of voters nodes. In the middle of changing replication factor (sync).
    # Add to voters nodes that are already synced and active
    let removeFromSync = self.sync.difference(self.active)
    var syncSet = self.sync.difference(removeFromSync)
    # If sync will not become empty after removing dead nodes - remove them.
    # However, do it carefully, between sync and voters should remain common nodes!
    if removeFromSync.len > 0 and syncSet.len > 0 and
       (self.voters.len == 0 or syncSet.intersection(self.voters).len > 0):
      result.add(self.syncUpdate(min(self.numsync, self.sync.len - removeFromSync.len), syncSet))
    let addToVoters = self.sync.difference(self.voters).intersection(self.active)
    if addToVoters.len > 0:
      let voters = self.voters.union(addToVoters)
      result.add(self.quorumUpdate(voters.len - self.numsync, voters))
    # Remove from sync nodes that are dead
    let removeFromSyncFinal = self.sync.difference(self.voters)
    if removeFromSyncFinal.len > 0:
      result.add(self.syncUpdate(
        min(self.numsync, self.sync.len - removeFromSyncFinal.len),
        self.sync.difference(removeFromSyncFinal)))

  # After handling these two cases voters and sync must match.
  assert self.voters == self.sync

  let safetyMargin = self.quorum + min(self.numsync, self.numsyncConfirmed) - self.voters.union(self.sync).len
  if safetyMargin > 0:  # In the middle of changing replication factor.
    if self.numsync > self.syncWanted:
      let numsync = max(self.syncWanted, self.voters.len - self.quorum)
      logger.debug(fmt"Case 3: replication factor {self.numsync} is bigger than needed {numsync}")
      result.add(self.syncUpdate(numsync, self.sync))
    else:
      let quorum = self.sync.len - self.numsync
      logger.debug(fmt"Case 4: quorum {self.quorum} is bigger than needed {quorum}")
      result.add(self.quorumUpdate(quorum, self.voters))
  else:
    let safetyMargin2 = self.quorum + self.numsync - self.voters.union(self.sync).len
    if self.numsync == self.syncWanted and safetyMargin2 > 0 and self.numsync > self.numsyncConfirmed:
      result.add(self.quorumUpdate(self.sync.len - self.numsync, self.voters))

proc removeGoneNodes(self: QuorumStateResolver): seq[Transition] =
  ## Remove inactive nodes from synchronous_standby_names and from /sync key.
  result = @[]

  let toRemove = self.sync.difference(self.active)
  if toRemove.len > 0 and self.sync == toRemove:
    logger.debug(fmt"Removing nodes: {toRemove}")
    result.add(self.quorumUpdate(0, newCaseInsensitiveSet(@[]), adjustQuorum = false))
    result.add(self.syncUpdate(0, newCaseInsensitiveSet(@[])))
  elif toRemove.len > 0:
    logger.debug(fmt"Removing nodes: {toRemove}")
    let canReduceQuorumBy = self.quorum
    # If we can reduce quorum size try to do so first
    if canReduceQuorumBy > 0:
      # Pick nodes to remove by sorted order to provide deterministic behavior for tests
      var sortedToRemove = toRemove.toSeq().sorted(system.cmp, Descending)
      let removeSeq = sortedToRemove[0 ..< min(canReduceQuorumBy, sortedToRemove.len)]
      let remove = newCaseInsensitiveSet(removeSeq)
      let syncSet = self.sync.difference(remove)
      # when removing nodes from sync we can safely increase numsync if requested
      let numsync = min(if self.syncWanted > self.numsync: self.syncWanted else: self.numsync, syncSet.len)
      result.add(self.syncUpdate(numsync, syncSet))
      let voters = self.voters.difference(remove)
      var toRemoveMut = toRemove.intersection(self.sync)
      result.add(self.quorumUpdate(voters.len - self.numsync, voters,
                                   adjustQuorum = toRemoveMut.len == 0))
    var toRemoveMut = toRemove
    if toRemoveMut.len > 0:
      assert self.quorum == 0
      let numsync = self.numsync - toRemoveMut.len
      let syncSet = self.sync.difference(toRemoveMut)
      let voters = self.voters.difference(toRemoveMut)
      let syncDecrease = numsync - min(self.syncWanted, syncSet.len)
      let quorum = if syncDecrease > 0: min(syncDecrease, voters.len - 1) else: 0
      result.add(self.quorumUpdate(quorum, voters, adjustQuorum = false))
      result.add(self.syncUpdate(numsync, syncSet))

proc addNewNodes(self: QuorumStateResolver): seq[Transition] =
  ## Add new active nodes to synchronous_standby_names and to /sync key.
  result = @[]

  var toAdd = self.active.difference(self.sync)
  if toAdd.len > 0:
    # First get to requested replication factor
    logger.debug(fmt"Adding nodes: {toAdd}")
    let syncWanted = min(self.syncWanted, self.sync.union(toAdd).len)
    var increaseNumsyncBy = syncWanted - self.numsync
    if increaseNumsyncBy > 0:
      var add: CaseInsensitiveSet
      if self.sync.len > 0:
        let sortedToAdd = toAdd.toSeq().sorted()
        let addSeq = sortedToAdd[0 ..< min(increaseNumsyncBy, sortedToAdd.len)]
        add = newCaseInsensitiveSet(addSeq)
        increaseNumsyncBy = add.len
      else:  # there is only the leader
        add = toAdd  # and it is safe to add all nodes at once if sync is empty
      result.add(self.syncUpdate(self.numsync + increaseNumsyncBy, self.sync.union(add)))
      let voters = self.voters.union(add)
      result.add(self.quorumUpdate(voters.len - syncWanted, voters))
      toAdd = toAdd.difference(self.sync)
    if toAdd.len > 0:
      let voters = self.voters.union(toAdd)
      result.add(self.quorumUpdate(voters.len - syncWanted, voters,
                                   adjustQuorum = syncWanted > self.numsyncConfirmed))
      result.add(self.syncUpdate(syncWanted, self.sync.union(toAdd)))

proc handleReplicationFactorChange(self: QuorumStateResolver): seq[Transition] =
  ## Handle change of the replication factor (syncWanted, aka synchronous_node_count).
  result = @[]

  # Apply requested replication factor change
  let syncIncrease = min(self.syncWanted, self.sync.len) - self.numsync
  if syncIncrease > 0:
    # Increase replication factor
    logger.debug(fmt"Increasing replication factor to {self.numsync + syncIncrease}")
    result.add(self.syncUpdate(self.numsync + syncIncrease, self.sync))
    result.add(self.quorumUpdate(self.voters.len - self.numsync, self.voters))
  elif syncIncrease < 0:
    # Reduce replication factor
    logger.debug(fmt"Reducing replication factor to {self.numsync + syncIncrease}")
    if self.quorum - syncIncrease < self.voters.len:
      result.add(self.quorumUpdate(self.voters.len - self.numsync - syncIncrease, self.voters,
                                   adjustQuorum = self.syncWanted > self.numsyncConfirmed))
    result.add(self.syncUpdate(self.numsync + syncIncrease, self.sync))

proc generateTransitions(self: QuorumStateResolver): seq[Transition] =
  ## Produce a set of changes to safely transition from the current state to the desired.
  result = @[]

  logger.debug(fmt"Quorum state: leader {self.leader} quorum {self.quorum}, voters {self.voters}, " &
               fmt"numsync {self.numsync}, sync {self.sync}, numsyncConfirmed {self.numsyncConfirmed}, " &
               fmt"active {self.active}, syncWanted {self.syncWanted} leaderWanted {self.leaderWanted}")
  try:
    if self.leaderWanted != self.leader:  # failover
      var votersSet = self.voters
      votersSet.excl(self.leaderWanted)
      votersSet.incl(self.leader)
      if self.sync.len == 0:
        # If sync is empty we need to update synchronous_standby_names first
        let numsync = votersSet.len - self.quorum
        result.add(self.syncUpdate(numsync, votersSet))
      # If leader changed we need to add the old leader to quorum (voters)
      result.add(self.quorumUpdate(self.quorum, votersSet, self.leaderWanted))
      # right after promote there could be no replication connections yet
      if self.sync.intersection(self.active).len == 0:
        return  # give another loop_wait seconds for replicas to reconnect before removing them from quorum
    else:
      self.checkInvariants()
  except QuorumError as e:
    logger.warning(fmt"{e.msg}")
    result.add(self.quorumUpdate(self.sync.len - self.numsync, self.sync))

  assert self.leader == self.leaderWanted

  # numsyncConfirmed could be 0 after restart/failover, we will calculate it from quorum
  if self.numsyncConfirmed == 0 and self.sync.intersection(self.active).len > 0:
    self.numsyncConfirmed = min(self.sync.intersection(self.active).len, self.voters.len - self.quorum)
    logger.debug(fmt"numsyncConfirmed=0, adjusting it to {self.numsyncConfirmed}")

  result.add(self.handleNonSteadyCases())
  result.add(self.removeGoneNodes())
  result.add(self.addNewNodes())
  result.add(self.handleReplicationFactorChange())

iterator transitions*(self: QuorumStateResolver): Transition =
  ## Iterate over the transitions produced by generateTransitions.
  ##
  ## .. note::
  ##     Merge two transitions of the same type to a single one.
  ##
  ##     This is always safe because skipping the first transition is equivalent
  ##     to no one observing the intermediate state.
  let allTransitions = self.generateTransitions()
  var i = 0
  while i < allTransitions.len:
    let curTransition = allTransitions[i]
    let hasNext = i + 1 < allTransitions.len
    if hasNext and curTransition.transitionType == allTransitions[i + 1].transitionType:
      i.inc
      continue
    yield curTransition
    if curTransition.transitionType == ttRestart:
      break
    i.inc
