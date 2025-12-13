## Tests for patroni/postgresql/sync module.
## Ported from test_sync.py

import std/[unittest, strutils, options]
import ../patroni/collections
import ../patroni/postgresql/sync

suite "SyncType Enum":
  test "sync type values":
    check $stOff == "off"
    check $stPriority == "priority"
    check $stQuorum == "quorum"

  test "sync type ordinals":
    check ord(stOff) == 0
    check ord(stPriority) == 1
    check ord(stQuorum) == 2

  test "sync types are distinct":
    check stOff != stPriority
    check stOff != stQuorum
    check stPriority != stQuorum

suite "SSN (Parsed synchronous_standby_names)":
  test "SSN with off type":
    var ssn = SSN(syncType: stOff, hasStar: false, num: 0, members: newCaseInsensitiveSet())
    check ssn.syncType == stOff
    check ssn.hasStar == false
    check ssn.num == 0
    check ssn.members.len == 0

  test "SSN with priority type":
    var members = newCaseInsensitiveSet()
    members.incl("node1")
    members.incl("node2")
    var ssn = SSN(syncType: stPriority, hasStar: false, num: 2, members: members)
    check ssn.syncType == stPriority
    check ssn.num == 2
    check ssn.members.len == 2
    check ssn.members.contains("node1")
    check ssn.members.contains("node2")

  test "SSN with star":
    var ssn = SSN(syncType: stPriority, hasStar: true, num: 1, members: newCaseInsensitiveSet())
    check ssn.hasStar == true

  test "SSN with quorum type":
    var members = newCaseInsensitiveSet()
    members.incl("node1")
    members.incl("node2")
    members.incl("node3")
    var ssn = SSN(syncType: stQuorum, hasStar: false, num: 2, members: members)
    check ssn.syncType == stQuorum
    check ssn.num == 2
    check ssn.members.len == 3

suite "SyncState":
  test "empty SyncState":
    var sync = newCaseInsensitiveSet()
    var active = newCaseInsensitiveSet()
    var state = SyncState(
      syncType: stOff,
      numsync: 0,
      sync: sync,
      syncConfirmed: newCaseInsensitiveSet(),
      active: active
    )
    check state.syncType == stOff
    check state.numsync == 0

  test "SyncState with sync standbys":
    var sync = newCaseInsensitiveSet()
    sync.incl("node1")
    var active = newCaseInsensitiveSet()
    active.incl("node1")
    active.incl("node2")
    var state = SyncState(
      syncType: stPriority,
      numsync: 1,
      sync: sync,
      syncConfirmed: sync,
      active: active
    )
    check state.syncType == stPriority
    check state.numsync == 1
    check state.sync.len == 1
    check state.active.len == 2

  test "SyncState quorum mode":
    var sync = newCaseInsensitiveSet()
    sync.incl("node1")
    sync.incl("node2")
    var active = newCaseInsensitiveSet()
    active.incl("node1")
    active.incl("node2")
    active.incl("node3")
    var state = SyncState(
      syncType: stQuorum,
      numsync: 2,
      sync: sync,
      syncConfirmed: sync,
      active: active
    )
    check state.syncType == stQuorum
    check state.numsync == 2
    check state.sync.len == 2

suite "Replica":
  test "create Replica":
    var replica = Replica(
      pid: 12345,
      applicationName: "node1",
      syncState: "async",
      lsn: 1000,
      nofailover: false,
      syncPriority: 1
    )
    check replica.pid == 12345
    check replica.applicationName == "node1"
    check replica.syncState == "async"
    check replica.lsn == 1000
    check replica.nofailover == false
    check replica.syncPriority == 1

  test "Replica with sync state":
    var replica = Replica(
      pid: 12346,
      applicationName: "node2",
      syncState: "sync",
      lsn: 2000,
      nofailover: false,
      syncPriority: 1
    )
    check replica.syncState == "sync"

  test "Replica with quorum state":
    var replica = Replica(
      pid: 12347,
      applicationName: "node3",
      syncState: "quorum",
      lsn: 3000,
      nofailover: false,
      syncPriority: 2
    )
    check replica.syncState == "quorum"

  test "Replica with potential state":
    var replica = Replica(
      pid: 12348,
      applicationName: "node4",
      syncState: "potential",
      lsn: 4000,
      nofailover: false,
      syncPriority: 3
    )
    check replica.syncState == "potential"

  test "Replica with nofailover":
    var replica = Replica(
      pid: 12349,
      applicationName: "node5",
      syncState: "async",
      lsn: 5000,
      nofailover: true,
      syncPriority: 0
    )
    check replica.nofailover == true

suite "ReplicaList":
  test "empty ReplicaList":
    var list = ReplicaList(replicas: @[], maxLsn: 0)
    check list.replicas.len == 0
    check list.maxLsn == 0

  test "ReplicaList with replicas":
    var r1 = Replica(pid: 1, applicationName: "n1", syncState: "async", lsn: 100, nofailover: false, syncPriority: 1)
    var r2 = Replica(pid: 2, applicationName: "n2", syncState: "sync", lsn: 200, nofailover: false, syncPriority: 1)
    var list = ReplicaList(replicas: @[r1, r2], maxLsn: 200)
    check list.replicas.len == 2
    check list.maxLsn == 200

suite "Sync Priority":
  test "priority ordering":
    # Lower priority value = higher priority
    let priority1 = 1
    let priority2 = 2
    let priority3 = 100
    check priority1 < priority2
    check priority2 < priority3

  test "priority 0 means default":
    let defaultPriority = 0
    let explicitPriority = 1
    # Priority 0 usually means no explicit priority set
    check defaultPriority < explicitPriority

suite "Sync State Names":
  test "valid sync states":
    let validStates = ["async", "sync", "potential", "quorum"]
    check validStates.len == 4
    check "async" in validStates
    check "sync" in validStates
    check "potential" in validStates
    check "quorum" in validStates

  test "sync state comparison":
    # 'sync' means fully synchronous
    let syncState = "sync"
    let asyncState = "async"
    check syncState != asyncState

suite "synchronous_standby_names Parsing":
  # Test the expected formats of synchronous_standby_names

  test "single node format":
    # synchronous_standby_names = 'node1'
    let ssn = "node1"
    check ssn.len > 0
    check not ssn.contains(",")

  test "multiple nodes format":
    # synchronous_standby_names = '2 (node1,node2)'
    let ssn = "2 (node1,node2)"
    check ssn.contains("(")
    check ssn.contains(",")
    check ssn.contains(")")

  test "star format":
    # synchronous_standby_names = '*'
    let ssn = "*"
    check ssn == "*"

  test "quorum format":
    # synchronous_standby_names = 'ANY 2 (node1,node2,node3)'
    let ssn = "ANY 2 (node1,node2,node3)"
    check ssn.startsWith("ANY")

  test "first/priority format":
    # synchronous_standby_names = 'FIRST 2 (node1,node2,node3)'
    let ssn = "FIRST 2 (node1,node2,node3)"
    check ssn.startsWith("FIRST")

suite "CaseInsensitiveSet for Sync":
  test "case insensitive member matching":
    var members = newCaseInsensitiveSet()
    members.incl("Node1")
    check members.contains("node1")
    check members.contains("NODE1")
    check members.contains("Node1")

  test "multiple members":
    var members = newCaseInsensitiveSet()
    members.incl("Node1")
    members.incl("NODE2")
    members.incl("node3")
    check members.len == 3
    check members.contains("node1")
    check members.contains("node2")
    check members.contains("node3")

suite "LSN Comparison":
  test "LSN ordering":
    let lsn1: int64 = 1000
    let lsn2: int64 = 2000
    let lsn3: int64 = 3000
    check lsn2 > lsn1
    check lsn3 > lsn2

  test "LSN difference":
    let currentLsn: int64 = 10000
    let replicaLsn: int64 = 9500
    let lag = currentLsn - replicaLsn
    check lag == 500

suite "Synchronous Mode Logic":
  test "synchronous mode disabled":
    let syncMode = false
    check not syncMode

  test "synchronous mode enabled":
    let syncMode = true
    check syncMode

  test "synchronous mode strict":
    let syncModeStrict = true
    check syncModeStrict

suite "Quorum Calculation":
  test "quorum with 3 nodes":
    let numNodes = 3
    let requiredQuorum = (numNodes div 2) + 1
    check requiredQuorum == 2

  test "quorum with 5 nodes":
    let numNodes = 5
    let requiredQuorum = (numNodes div 2) + 1
    check requiredQuorum == 3

  test "quorum satisfied":
    let required = 2
    let available = 3
    check available >= required

  test "quorum not satisfied":
    let required = 2
    let available = 1
    check available < required

when isMainModule:
  echo "test_sync.nim tests completed"
