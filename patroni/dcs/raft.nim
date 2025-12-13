## Raft DCS backend for Patroni.
##
## This module provides a distributed configuration store using the Raft
## consensus algorithm. Provides KVStoreTTL for key-value storage with TTL.

import std/[json, locks, options, os, sequtils, strformat, strutils, tables, times, threadpool]
import ../exceptions
import ../log
import ../utils
import ../dcs as base

export base

let logger = getLogger("patroni.dcs.raft")

type
  RaftError* = object of DCSError
    ## Raft-specific error.

  FailReason* = enum
    ## Reason for operation failure.
    frSuccess = 0
    frRequestDenied = 1
    frTimeout = 2
    frNetworkError = 3
    frNotLeader = 4

  KVEntry* = object
    ## Key-value entry with metadata.
    value*: string
    created*: float
    updated*: float
    expire*: Option[float]
    index*: int64

  KVStoreTTL* = ref object
    ## Key-value store with TTL support.
    selfAddr*: string
    partnerAddrs*: seq[string]
    data*: Table[string, KVEntry]
    retryTimeout*: int
    running*: bool
    lock*: Lock
    onSet*: proc(key: string, entry: KVEntry)
    onDelete*: proc(key: string)
    appliedLocalLog*: bool
    dataDir*: string
    password*: string
    autoTickPeriod*: float

  Raft* = ref object of AbstractDCS
    ## Raft DCS implementation.
    ttl: int
    syncObj: KVStoreTTL
    hasFailed: bool

proc checkRequirements(oldValue: Option[KVEntry], kwargs: Table[string, string]): bool =
  ## Check if operation requirements are met.
  let hasOld = oldValue.isSome

  # Check prevExist
  if "prevExist" in kwargs:
    let wantExist = kwargs["prevExist"] == "true"
    if wantExist != hasOld:
      return false

  # Check prevValue
  if "prevValue" in kwargs:
    if not hasOld or oldValue.get.value != kwargs["prevValue"]:
      return false

  # Check prevIndex
  if "prevIndex" in kwargs:
    let prevIdx = parseInt(kwargs["prevIndex"])
    if not hasOld or oldValue.get.index != prevIdx:
      return false

  return true

proc newKVStoreTTL*(config: JsonNode): KVStoreTTL =
  ## Create a new KVStoreTTL.
  new(result)
  result.selfAddr = config.getOrDefault("self_addr").getStr("")
  result.partnerAddrs = @[]

  let partners = config.getOrDefault("partner_addrs")
  if partners != nil and partners.kind == JArray:
    for p in partners:
      result.partnerAddrs.add(p.getStr())

  result.data = initTable[string, KVEntry]()
  result.retryTimeout = config.getOrDefault("retry_timeout").getInt(10)
  result.running = true
  initLock(result.lock)
  result.onSet = nil
  result.onDelete = nil
  result.appliedLocalLog = false
  result.dataDir = config.getOrDefault("data_dir").getStr("")
  result.password = config.getOrDefault("password").getStr("")
  result.autoTickPeriod = config.getOrDefault("loop_wait").getFloat(10.0) / 1000.0

  # Validate/create data directory
  if result.dataDir.len > 0 and not dirExists(result.dataDir):
    createDir(result.dataDir)

proc setRetryTimeout*(self: KVStoreTTL, timeout: int) =
  ## Set the retry timeout.
  self.retryTimeout = timeout

proc get*(self: KVStoreTTL, key: string, recursive: bool = false): Option[Table[string, KVEntry]] =
  ## Get a value or all values under a key prefix.
  withLock(self.lock):
    if not recursive:
      if key in self.data:
        var result = initTable[string, KVEntry]()
        result[key] = self.data[key]
        return some(result)
      return none(Table[string, KVEntry])

    var result = initTable[string, KVEntry]()
    for k, v in self.data:
      if k.startsWith(key):
        result[k] = v
    if result.len > 0:
      return some(result)
    return none(Table[string, KVEntry])

proc set*(self: KVStoreTTL, key: string, value: string, ttl: int = 0,
          kwargs: Table[string, string] = initTable[string, string]()): bool =
  ## Set a key-value pair.
  withLock(self.lock):
    let oldValue = if key in self.data: some(self.data[key]) else: none(KVEntry)

    if not checkRequirements(oldValue, kwargs):
      return false

    var entry: KVEntry
    entry.value = value
    entry.updated = epochTime()
    entry.created = if oldValue.isSome: oldValue.get.created else: entry.updated
    entry.index = if oldValue.isSome: oldValue.get.index + 1 else: 1
    if ttl > 0:
      entry.expire = some(entry.updated + float(ttl))
    else:
      entry.expire = none(float)

    self.data[key] = entry

    if self.onSet != nil:
      self.onSet(key, entry)

    return true

proc delete*(self: KVStoreTTL, key: string, recursive: bool = false,
             kwargs: Table[string, string] = initTable[string, string]()): bool =
  ## Delete a key or all keys under a prefix.
  withLock(self.lock):
    if recursive:
      var keysToDelete: seq[string] = @[]
      for k in self.data.keys:
        if k.startsWith(key):
          keysToDelete.add(k)
      for k in keysToDelete:
        self.data.del(k)
        if self.onDelete != nil:
          self.onDelete(k)
      return true

    let oldValue = if key in self.data: some(self.data[key]) else: none(KVEntry)
    if not checkRequirements(oldValue, kwargs):
      return false

    if key in self.data:
      self.data.del(key)
      if self.onDelete != nil:
        self.onDelete(key)
    return true

proc expireKeys(self: KVStoreTTL) =
  ## Expire keys that have passed their TTL.
  let now = epochTime()
  withLock(self.lock):
    var keysToDelete: seq[string] = @[]
    for key, entry in self.data:
      if entry.expire.isSome and entry.expire.get <= now:
        keysToDelete.add(key)

    for key in keysToDelete:
      self.data.del(key)
      if self.onDelete != nil:
        self.onDelete(key)

proc doTick*(self: KVStoreTTL) =
  ## Perform one tick of the Raft loop.
  # Expire keys
  self.expireKeys()

  # Sleep for tick period
  sleep(int(self.autoTickPeriod * 1000))

proc destroy*(self: KVStoreTTL) =
  ## Destroy the KV store.
  self.running = false

proc isLeader*(self: KVStoreTTL): bool =
  ## Check if this node is the leader.
  # Simplified - in real Raft this would be based on consensus
  result = self.selfAddr.len > 0

# Raft DCS implementation

proc memberFromNode(key: string, entry: KVEntry): Member =
  ## Create a Member from a KV entry.
  let name = key.split('/')[^1]
  result = fromNode(entry.index, name, "", entry.value)

proc clusterFromNodes(self: Raft, nodes: Table[string, KVEntry]): base.Cluster =
  ## Build a Cluster from KV nodes.
  result = newCluster()

  # Get initialize flag
  if "_INITIALIZE" in nodes:
    result.initialize = nodes["_INITIALIZE"].value

  # Get global dynamic configuration
  if "_CONFIG" in nodes:
    let entry = nodes["_CONFIG"]
    result.config = clusterConfigFromNode(entry.index, entry.value)

  # Get timeline history
  if "_HISTORY" in nodes:
    let entry = nodes["_HISTORY"]
    result.history = timelineHistoryFromNode(entry.index, entry.value)

  # Get status
  if "_STATUS" in nodes:
    result.status = statusFromNode(nodes["_STATUS"].value)
  elif "_LEADER_OPTIME" in nodes:
    result.status = statusFromNode(nodes["_LEADER_OPTIME"].value)

  # Get list of members
  for key, entry in nodes:
    if key.startsWith("_MEMBERS/"):
      result.members.add(memberFromNode(key, entry))

  # Get leader
  if "_LEADER" in nodes:
    let leaderEntry = nodes["_LEADER"]
    let leaderName = leaderEntry.value
    var leaderMember = newRemoteMember(leaderName, newMemberData())
    for m in result.members:
      if m.name == leaderName:
        leaderMember = newRemoteMember(m.name, m.data)
        break
    result.leader = newLeader(leaderEntry.index, "", leaderMember)

  # Get failover
  if "_FAILOVER" in nodes:
    let entry = nodes["_FAILOVER"]
    result.failover = failoverFromNode(entry.index, entry.value)

  # Get sync state
  if "_SYNC" in nodes:
    let entry = nodes["_SYNC"]
    result.sync = syncStateFromNode(entry.index, entry.value)

proc newRaft*(config: JsonNode): Raft =
  ## Create a new Raft DCS instance.
  new(result)
  initAbstractDCS(result, config)

  result.ttl = config.getOrDefault("ttl").getInt(30)
  result.hasFailed = false

  let raftConfig = if config.hasKey("raft"): config["raft"] else: config

  result.syncObj = newKVStoreTTL(raftConfig)

  # Set callbacks
  result.syncObj.onSet = proc(key: string, entry: KVEntry) =
    # Wake up on relevant changes
    discard
  result.syncObj.onDelete = proc(key: string) =
    discard

method setTtl*(self: Raft, ttl: int): bool =
  ## Set the TTL.
  self.ttl = ttl
  result = true

method getTtl*(self: Raft): int =
  ## Get the current TTL.
  result = self.ttl

method setRetryTimeout*(self: Raft, retryTimeout: int) =
  ## Set retry timeout.
  self.syncObj.setRetryTimeout(retryTimeout)

method loadCluster*(self: Raft, path: string): base.Cluster =
  ## Load cluster from Raft.
  let response = self.syncObj.get(path, recursive = true)
  if response.isNone:
    return emptyCluster()

  var nodes = initTable[string, KVEntry]()
  for key, value in response.get:
    let relKey = key[path.len..^1]
    nodes[relKey] = value

  result = self.clusterFromNodes(nodes)

method touchMember*(self: Raft, data: JsonNode): bool =
  ## Update member data.
  let value = $data
  result = self.syncObj.set(self.clientPath("_MEMBERS/" & self.name), value, self.ttl)

method takeLeader*(self: Raft): bool =
  ## Take the leader lock.
  result = self.syncObj.set(self.clientPath("_LEADER"), self.name, self.ttl)

method attemptToAcquireLeader*(self: Raft): bool =
  ## Attempt to acquire leader lock.
  var kwargs = initTable[string, string]()
  kwargs["prevExist"] = "false"
  result = self.syncObj.set(self.clientPath("_LEADER"), self.name, self.ttl, kwargs)

method setFailoverValue*(self: Raft, value: string, version: int64 = 0): bool =
  ## Set failover value.
  var kwargs = initTable[string, string]()
  if version > 0:
    kwargs["prevIndex"] = $version
  result = self.syncObj.set(self.clientPath("_FAILOVER"), value, 0, kwargs)

method setConfigValue*(self: Raft, value: string, version: int64 = 0): bool =
  ## Set config value.
  var kwargs = initTable[string, string]()
  if version > 0:
    kwargs["prevIndex"] = $version
  result = self.syncObj.set(self.clientPath("_CONFIG"), value, 0, kwargs)

method writeLeaderOptime*(self: Raft, lastLsn: string): bool =
  ## Write leader optime.
  result = self.syncObj.set(self.clientPath("_LEADER_OPTIME"), lastLsn)

method writeStatus*(self: Raft, value: string): bool =
  ## Write status.
  result = self.syncObj.set(self.clientPath("_STATUS"), value)

method writeFailsafe*(self: Raft, value: string): bool =
  ## Write failsafe.
  result = self.syncObj.set(self.clientPath("_FAILSAFE"), value)

method updateLeader*(self: Raft, leader: Leader): bool =
  ## Update leader lock.
  var kwargs = initTable[string, string]()
  kwargs["prevValue"] = self.name
  result = self.syncObj.set(self.clientPath("_LEADER"), self.name, self.ttl, kwargs)
  if not result:
    # Check if leader lock is gone
    let leaderData = self.syncObj.get(self.clientPath("_LEADER"))
    if leaderData.isNone:
      result = self.attemptToAcquireLeader()

method initialize*(self: Raft, createNew: bool = true, sysid: string = ""): bool =
  ## Initialize the cluster.
  var kwargs = initTable[string, string]()
  kwargs["prevExist"] = $(not createNew)
  result = self.syncObj.set(self.clientPath("_INITIALIZE"), sysid, 0, kwargs)

method deleteLeader*(self: Raft, leader: Leader): bool =
  ## Delete leader lock.
  var kwargs = initTable[string, string]()
  kwargs["prevValue"] = self.name
  result = self.syncObj.delete(self.clientPath("_LEADER"), false, kwargs)

method cancelInitialization*(self: Raft): bool =
  ## Cancel initialization.
  result = self.syncObj.delete(self.clientPath("_INITIALIZE"))

method deleteCluster*(self: Raft): bool =
  ## Delete the entire cluster data.
  result = self.syncObj.delete(self.clientPath(""), recursive = true)

method setHistoryValue*(self: Raft, value: string): bool =
  ## Set history value.
  result = self.syncObj.set(self.clientPath("_HISTORY"), value)

method setSyncStateValue*(self: Raft, value: string, version: int64 = 0): int64 =
  ## Set sync state value.
  var kwargs = initTable[string, string]()
  if version > 0:
    kwargs["prevIndex"] = $version
  if self.syncObj.set(self.clientPath("_SYNC"), value, 0, kwargs):
    let data = self.syncObj.get(self.clientPath("_SYNC"))
    if data.isSome:
      for k, v in data.get:
        return v.index
  return -1

method deleteSyncState*(self: Raft, version: int64 = 0): bool =
  ## Delete sync state.
  var kwargs = initTable[string, string]()
  if version > 0:
    kwargs["prevIndex"] = $version
  result = self.syncObj.delete(self.clientPath("_SYNC"), false, kwargs)

method watch*(self: Raft, leaderVersion: int64, timeout: float): bool =
  ## Watch for changes.
  sleep(int(timeout * 1000))
  result = false

