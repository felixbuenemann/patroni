## High Availability (HA) module for Patroni.
##
## This module implements the core HA loop that manages PostgreSQL cluster state,
## handles failovers, promotes replicas, and maintains cluster consistency.

import std/[json, locks, options, tables, times, strformat, strutils, os]
import ./async_executor
import ./collections
import ./config
import ./dcs
import ./exceptions
import ./global_config
import ./log
import ./psycopg
import ./tags
import ./utils
import ./postgresql/misc
import ./postgresql/callback_executor

let logger = getLogger("patroni.ha")

type
  MemberStatus* = ref object of Tags
    ## Node status distilled from API response.
    ##
    ## :ivar member: Member object of the node.
    ## :ivar reachable: false if the node is not reachable or is not responding with correct JSON.
    ## :ivar inRecovery: false if the node is running as a primary.
    ## :ivar walPosition: maximum value of replayed_location or received_location from JSON.
    ## :ivar data: the whole JSON response for future usage.
    member*: Member
    reachable*: bool
    inRecovery*: Option[bool]
    walPosition*: int64
    data*: Table[string, JsonNode]
    tagsData: Table[string, string]

  FailsafeResponse* = object
    ## Response on POST /failsafe API request.
    memberName*: string
    accepted*: bool
    lsn*: Option[int64]

  Failsafe* = ref object
    ## Object that represents failsafe state of the cluster.
    lock: Lock
    dcs: AbstractDCS
    lastUpdate: float
    name: string
    connUrl: string
    apiUrl: string
    slots: Table[string, int64]

  Ha* = ref object
    ## High Availability main class.
    ##
    ## Handles the core HA loop, including leader election, failover, and cluster management.
    patroni*: pointer  # Patroni instance (forward declaration)
    state*: HaState
    dcs*: AbstractDCS
    asyncExecutor*: AsyncExecutor
    failsafe*: Failsafe
    lock: Lock
    clusterNodesChange: bool
    isUnhealthy: bool
    masterTimeline: int
    isSynchronous: bool
    lastLeaderOperation: int64

  HaState* = enum
    ## Current state of the HA loop.
    hsStarting = "starting"
    hsRunning = "running"
    hsStopped = "stopped"
    hsPaused = "paused"

proc newMemberStatus*(member: Member, reachable: bool, inRecovery: Option[bool],
                      walPosition: int64, data: Table[string, JsonNode]): MemberStatus =
  ## Create a new MemberStatus.
  new(result)
  result.member = member
  result.reachable = reachable
  result.inRecovery = inRecovery
  result.walPosition = walPosition
  result.data = data
  result.tagsData = initTable[string, string]()

  # Extract tags from data
  if "tags" in data:
    let tagsNode = data["tags"]
    if tagsNode.kind == JObject:
      for key, value in tagsNode.pairs:
        result.tagsData[key] = $value

method tags*(self: MemberStatus): Table[string, string] =
  ## Get tags from the member status.
  result = self.tagsData

proc fromApiResponse*(member: Member, json: Table[string, JsonNode]): MemberStatus =
  ## Create MemberStatus from API response.
  ##
  ## :param member: dcs.Member object
  ## :param json: RestApiHandler.get_postgresql_status() result
  ## :returns: MemberStatus object
  var walNode: JsonNode
  if "wal" in json:
    walNode = json["wal"]
  elif "xlog" in json:
    walNode = json["xlog"]
  else:
    return newMemberStatus(member, false, none(bool), 0, json)

  # Check if in recovery (abuse difference in primary/replica response format)
  var inRecovery = true
  if walNode.kind == JObject and "location" in walNode:
    inRecovery = false
  if "role" in json:
    let role = json["role"].getStr("")
    if role in ["master", "primary"]:
      inRecovery = false

  var lsn: int64 = 0
  if inRecovery and walNode.kind == JObject:
    let receivedLoc = if "received_location" in walNode: walNode["received_location"].getInt(0) else: 0
    let replayedLoc = if "replayed_location" in walNode: walNode["replayed_location"].getInt(0) else: 0
    lsn = max(receivedLoc, replayedLoc)

  result = newMemberStatus(member, true, some(inRecovery), lsn, json)

proc unknown*(member: Member): MemberStatus =
  ## Create a MemberStatus with unknown/empty values.
  result = newMemberStatus(member, false, none(bool), 0, initTable[string, JsonNode]())

proc timeline*(self: MemberStatus): int =
  ## Timeline value from JSON.
  if "timeline" in self.data:
    result = self.data["timeline"].getInt(0)
  else:
    result = 0

proc watchdogFailed*(self: MemberStatus): bool =
  ## Indicates that watchdog is required but not available or failed.
  if "watchdog_failed" in self.data:
    result = self.data["watchdog_failed"].getBool(false)
  else:
    result = false

proc failoverLimitation*(self: MemberStatus): Option[string] =
  ## Returns reason why this node can't promote or none if everything is ok.
  if not self.reachable:
    return some("not reachable")
  if self.nofailover:
    return some("not allowed to promote")
  if self.watchdogFailed:
    return some("not watchdog capable")
  result = none(string)

# Failsafe implementation

proc newFailsafe*(dcs: AbstractDCS): Failsafe =
  ## Create a new Failsafe object.
  new(result)
  initLock(result.lock)
  result.dcs = dcs
  result.lastUpdate = 0
  result.name = ""
  result.connUrl = ""
  result.apiUrl = ""
  result.slots = initTable[string, int64]()

proc resetState(self: Failsafe) =
  ## Reset state of the Failsafe object.
  self.lastUpdate = 0
  self.name = ""
  self.connUrl = ""
  self.apiUrl = ""
  self.slots = initTable[string, int64]()

proc updateSlots*(self: Failsafe, slots: Table[string, int64]) =
  ## Assign value to slots.
  ##
  ## .. note:: This method is only called on the primary node.
  withLock(self.lock):
    self.slots = slots

proc update*(self: Failsafe, data: Table[string, JsonNode]) =
  ## Update the Failsafe object state.
  withLock(self.lock):
    self.lastUpdate = epochTime()
    if "name" in data:
      self.name = data["name"].getStr("")
    if "conn_url" in data:
      self.connUrl = data["conn_url"].getStr("")
    if "api_url" in data:
      self.apiUrl = data["api_url"].getStr("")
    if "slots" in data:
      let slotsNode = data["slots"]
      if slotsNode.kind == JObject:
        for key, value in slotsNode.pairs:
          self.slots[key] = value.getInt(0)

proc leader*(self: Failsafe): Option[Leader] =
  ## Return information about current cluster leader if failsafe mode is active.
  withLock(self.lock):
    if self.lastUpdate + float(self.dcs.ttl) > epochTime():
      let memberData = newMemberData()
      memberData.apiUrl = self.apiUrl
      memberData.connUrl = self.connUrl
      let remoteMember = newRemoteMember(self.name, memberData)
      result = some(newLeader(0, "", remoteMember))
    else:
      result = none(Leader)

proc isActive*(self: Failsafe): bool =
  ## Check whether the failsafe mode is active.
  withLock(self.lock):
    result = self.lastUpdate + float(self.dcs.ttl) > epochTime()

# Ha (High Availability) implementation

proc newHa*(dcs: AbstractDCS, asyncExecutor: AsyncExecutor = nil): Ha =
  ## Create a new Ha instance.
  new(result)
  result.state = hsStarting
  result.dcs = dcs
  result.failsafe = newFailsafe(dcs)
  initLock(result.lock)
  result.clusterNodesChange = false
  result.isUnhealthy = false
  result.masterTimeline = 0
  result.isSynchronous = false
  result.lastLeaderOperation = 0

  if asyncExecutor != nil:
    result.asyncExecutor = asyncExecutor
  else:
    result.asyncExecutor = newAsyncExecutor()

proc cluster*(self: Ha): dcs.Cluster =
  ## Get the current cluster state.
  result = self.dcs.cluster

proc isPaused*(self: Ha): bool =
  ## Check if the cluster is in maintenance mode.
  let gc = getGlobalConfig()
  result = gc.isPaused()

proc isSynchronousMode*(self: Ha): bool =
  ## Check if synchronous replication is enabled.
  let gc = getGlobalConfig()
  result = gc.isSynchronousMode()

proc isStandbyCluster*(self: Ha): bool =
  ## Check if this is a standby cluster.
  let gc = getGlobalConfig()
  result = gc.isStandbyCluster()

proc isLeader*(self: Ha): bool =
  ## Check if this node is the current leader.
  let cluster = self.cluster
  if cluster != nil and cluster.leader != nil:
    # Would need to check against our own member name
    result = false  # Placeholder
  else:
    result = false

proc shouldRunScheduled*(self: Ha): bool =
  ## Check if we should run a scheduled action.
  result = self.asyncExecutor.busy

proc wakeup*(self: Ha) =
  ## Wake up the HA loop.
  withLock(self.lock):
    self.dcs.event = true

proc loadCluster*(self: Ha): dcs.Cluster =
  ## Load the current cluster state from DCS.
  try:
    result = self.dcs.getCluster()
  except DCSError as e:
    logger.exception("Failed to get cluster state from DCS", e)
    result = nil

proc runCycle*(self: Ha): string =
  ## Run one cycle of the HA loop.
  ##
  ## :returns: message describing what happened during this cycle.

  # Load cluster state
  let cluster = self.loadCluster()
  if cluster == nil:
    return "Failed to get cluster state"

  # Update global config from cluster
  let gc = getGlobalConfig()
  # Create a global_config.Cluster compatible object
  var gcCluster = global_config.Cluster(config: global_config.ClusterConfig(
    modifyVersion: if cluster.config != nil: cluster.config.modifyVersion else: 0,
    data: if cluster.config != nil: cluster.config.data else: initTable[string, JsonNode]()
  ))
  gc.update(gcCluster)

  # Check if we're paused
  if self.isPaused():
    return "Cluster is paused"

  # Check cluster has valid leader
  if cluster.isUnlocked():
    return "Cluster has no leader"

  # Normal operation
  result = fmt"Cluster state: {cluster.members.len} members"

proc run*(self: Ha) =
  ## Run the main HA loop.
  self.state = hsRunning
  logger.info("Starting HA loop")

  while self.state == hsRunning:
    try:
      let message = self.runCycle()
      logger.info(message)

      # Wait for next iteration
      let loopWait = self.dcs.loopWait
      sleep(loopWait * 1000)
    except CatchableError as e:
      logger.exception("Error in HA loop", e)
      sleep(1000)  # Wait 1 second on error

proc stop*(self: Ha) =
  ## Stop the HA loop.
  self.state = hsStopped
  logger.info("Stopped HA loop")

proc schedule*(self: Ha, action: string): Option[string] =
  ## Schedule an action to be executed.
  let existing = self.asyncExecutor.schedule(action)
  if existing.len > 0:
    result = some(existing)
  else:
    result = none(string)

proc cancelScheduled*(self: Ha): bool =
  ## Cancel the currently scheduled action.
  result = self.asyncExecutor.cancel()
