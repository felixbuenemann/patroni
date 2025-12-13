## Citus MPP (Massively Parallel Processing) handler.
##
## This module implements the Citus distributed PostgreSQL extension handler,
## managing pg_dist_node metadata and worker coordination.

import std/[hashes, json, locks, options, re, sequtils, sets, strformat, strutils, tables, times, uri]
import ../../dcs
import ../../psycopg
import ../../utils
import ../../log
import ../misc
import ../mpp

export mpp

let logger = getLogger("patroni.postgresql.mpp.citus")

const
  CITUS_COORDINATOR_GROUP_ID* = 0
  CITUS_SLOT_NAME_PATTERN* = "^citus_shard_(move|split)_slot(_[1-9][0-9]*){2,3}$"

var citusSlotNameRe: Regex

proc getCitusSlotNameRe(): Regex =
  ## Get the Citus slot name regex (lazy initialization).
  if citusSlotNameRe.isNil:
    citusSlotNameRe = re(CITUS_SLOT_NAME_PATTERN)
  result = citusSlotNameRe

type
  PgDistNode* = ref object
    ## Represents a single row in "pg_dist_node" table.
    ##
    ## Unlike "noderole" possible values of role are primary, secondary, and demoted.
    host*: string
    port*: int
    role*: string
    nodeid*: Option[int]

  PgDistGroup* = ref object of RootObj
    ## A set-like object that represents a Citus group in "pg_dist_node" table.
    failover*: bool
    groupid*: int
    nodes: HashSet[PgDistNode]

  PgDistTask* = ref object of PgDistGroup
    ## A "task" that represents the current or desired state of "pg_dist_node".
    event*: string
    timeout*: Option[float]
    cooldown*: float
    deadline*: float
    ready: bool
    cond: Cond
    lock: Lock

  Citus* = ref object of AbstractMPP
    ## Citus MPP configuration.

  CitusHandler* = ref object of AbstractMPPHandler
    ## Define the interfaces for handling an underlying Citus cluster.
    citusMpp*: Citus
    connection*: Connection
    pgDistGroup*: Table[int, PgDistTask]
    tasks*: seq[PgDistTask]
    inFlight*: PgDistTask
    scheduleLoadPgDistGroup*: bool
    condition*: Cond
    condLock*: Lock
    running*: bool

# PgDistNode implementation

proc newPgDistNode*(host: string, port: int, role: string, nodeid: Option[int] = none(int)): PgDistNode =
  ## Create a PgDistNode object.
  new(result)
  result.host = host
  result.port = port
  result.role = role
  result.nodeid = nodeid

proc hash*(self: PgDistNode): Hash =
  ## Hash function for set membership.
  result = hash((self.host, self.port))

proc `==`*(a, b: PgDistNode): bool =
  ## Equality comparison.
  a.host == b.host and a.port == b.port

proc `$`*(self: PgDistNode): string =
  ## String representation.
  fmt"PgDistNode(nodeid={self.nodeid},host={self.host},port={self.port},role={self.role})"

proc isPrimary*(self: PgDistNode): bool =
  ## Check if this node is a primary.
  self.role in ["primary", "demoted"]

proc asTuple*(self: PgDistNode, includeNodeid: bool = false): (string, int, string, Option[int]) =
  ## Helper method to compare nodes.
  (self.host, self.port, self.role, if includeNodeid: self.nodeid else: none(int))

# PgDistGroup implementation

proc newPgDistGroup*(groupid: int, nodes: seq[PgDistNode] = @[]): PgDistGroup =
  ## Create a PgDistGroup object.
  new(result)
  result.failover = false
  result.groupid = groupid
  result.nodes = initHashSet[PgDistNode]()
  for n in nodes:
    result.nodes.incl(n)

proc add*(self: PgDistGroup, node: PgDistNode) =
  ## Add a node to the group.
  self.nodes.incl(node)

proc contains*(self: PgDistGroup, node: PgDistNode): bool =
  ## Check if node is in group.
  node in self.nodes

proc primary*(self: PgDistGroup): Option[PgDistNode] =
  ## Get the primary node.
  for v in self.nodes:
    if v.isPrimary():
      return some(v)
  return none(PgDistNode)

proc get*(self: PgDistGroup, value: PgDistNode): Option[PgDistNode] =
  ## Get actual node from set.
  for v in self.nodes:
    if v == value:
      return some(v)
  return none(PgDistNode)

proc len*(self: PgDistGroup): int =
  ## Get number of nodes.
  self.nodes.len

iterator items*(self: PgDistGroup): PgDistNode =
  ## Iterate over nodes.
  for item in self.nodes:
    yield item

proc `-`*(a, b: PgDistGroup): HashSet[PgDistNode] =
  ## Set difference.
  a.nodes - b.nodes

proc `-`*(a: PgDistGroup, b: HashSet[PgDistNode]): HashSet[PgDistNode] =
  ## Set difference with HashSet.
  a.nodes - b

proc equals*(self, other: PgDistGroup, checkNodeid: bool = false): bool =
  ## Compare two groups.
  if self.groupid != other.groupid:
    return false

  var selfTuples, otherTuples: HashSet[(string, int, string, Option[int])]
  for v in self.nodes:
    selfTuples.incl(v.asTuple(checkNodeid))
  for v in other.nodes:
    otherTuples.incl(v.asTuple(checkNodeid))

  return selfTuples == otherTuples

# PgDistTask implementation

proc newPgDistTask*(groupid: int, nodes: seq[PgDistNode], event: string,
                    timeout: Option[float] = none(float),
                    cooldown: Option[float] = none(float)): PgDistTask =
  ## Create a PgDistTask object.
  new(result)
  result.failover = false
  result.groupid = groupid
  result.nodes = initHashSet[PgDistNode]()
  for n in nodes:
    result.nodes.incl(n)
  result.event = event
  result.timeout = timeout
  result.cooldown = if cooldown.isSome: cooldown.get else: 10000.0
  result.deadline = 0.0
  result.ready = false
  initCond(result.cond)
  initLock(result.lock)

proc wait*(self: PgDistTask) =
  ## Wait until task is processed.
  acquire(self.lock)
  while not self.ready:
    wait(self.cond, self.lock)
  release(self.lock)

proc wakeup*(self: PgDistTask) =
  ## Notify that task was processed.
  acquire(self.lock)
  self.ready = true
  signal(self.cond)
  release(self.lock)

proc `==`*(a, b: PgDistTask): bool =
  ## Task equality.
  a.event == b.event and PgDistGroup(a).equals(PgDistGroup(b))

# Citus implementation

proc newCitus*(config: Table[string, JsonNode]): Citus =
  ## Create a Citus MPP configuration.
  new(result)
  result.config = config
  result.groupRe = re"^(0|[1-9][0-9]*)$"

method validateConfig*(self: Citus, config: JsonNode): bool =
  ## Check whether provided config is good for Citus.
  if config.kind != JObject:
    return false
  if "database" notin config or config["database"].kind != JString:
    return false
  if "group" notin config:
    return false
  let grp = config["group"]
  if grp.kind == JInt:
    return true
  if grp.kind == JString:
    try:
      discard parseInt(grp.getStr())
      return true
    except:
      return false
  return false

method group*(self: Citus): Option[int] =
  ## Get the group of this Citus node.
  if "group" in self.config:
    let g = self.config["group"]
    if g.kind == JInt:
      return some(g.getInt())
    elif g.kind == JString:
      try:
        return some(parseInt(g.getStr()))
      except:
        discard
  return none(int)

method coordinatorGroupId*(self: Citus): Option[int] =
  ## Get the coordinator group ID.
  return some(CITUS_COORDINATOR_GROUP_ID)

# CitusHandler implementation

proc newCitusHandler*(postgresql: pointer, config: Table[string, JsonNode]): CitusHandler =
  ## Create a new CitusHandler.
  new(result)
  result.postgresql = postgresql
  result.config = config
  result.groupRe = re"^(0|[1-9][0-9]*)$"
  result.citusMpp = newCitus(config)
  result.pgDistGroup = initTable[int, PgDistTask]()
  result.tasks = @[]
  result.inFlight = nil
  result.scheduleLoadPgDistGroup = true
  initCond(result.condition)
  initLock(result.condLock)
  result.running = false
  # Note: connection initialization would require postgresql object

method scheduleCacheRebuild*(self: CitusHandler) =
  ## Schedule cache rebuild.
  acquire(self.condLock)
  self.scheduleLoadPgDistGroup = true
  release(self.condLock)

method onDemote*(self: CitusHandler) =
  ## Handle demotion.
  acquire(self.condLock)
  self.pgDistGroup.clear()
  self.tasks = @[]
  self.inFlight = nil
  release(self.condLock)

proc query*(self: CitusHandler, sql: string, params: varargs[string]): seq[seq[string]] =
  ## Execute a query.
  try:
    logger.log(LogLevel.Debug, fmt"query({sql}, {params})")
    return self.connection.query(sql, @params)
  except PostgresError as e:
    logger.log(LogLevel.Error, fmt"Exception when executing query '{sql}': {e.msg}")
    self.connection.close()
    acquire(self.condLock)
    self.inFlight = nil
    release(self.condLock)
    self.scheduleCacheRebuild()
    raise e

proc loadPgDistGroup*(self: CitusHandler): bool =
  ## Read from pg_dist_node table into local cache.
  acquire(self.condLock)
  if not self.scheduleLoadPgDistGroup:
    release(self.condLock)
    return true
  self.scheduleLoadPgDistGroup = false
  release(self.condLock)

  try:
    let rows = self.query("SELECT groupid, nodename, nodeport, noderole, nodeid FROM pg_catalog.pg_dist_node")
    var pgDistGroup = initTable[int, PgDistTask]()

    for row in rows:
      let groupid = parseInt(row[0])
      if groupid notin pgDistGroup:
        pgDistGroup[groupid] = newPgDistTask(groupid, @[], "after_promote")
      pgDistGroup[groupid].add(newPgDistNode(row[1], parseInt(row[2]), row[3], some(parseInt(row[4]))))

    acquire(self.condLock)
    self.pgDistGroup = pgDistGroup
    release(self.condLock)
    return true
  except:
    return false

method syncMetaData*(self: CitusHandler, cluster: dcs.Cluster) =
  ## Maintain pg_dist_node from the coordinator leader.
  if not self.citusMpp.isCoordinator():
    return
  # Would start thread and sync metadata here

method adjustPostgresGucs*(self: CitusHandler, parameters: var Table[string, string]) =
  ## Adjust GUCs for Citus.
  # citus extension must be first in shared_preload_libraries
  var libs = parameters.getOrDefault("shared_preload_libraries", "").split(',')
  libs = libs.filterIt(it.strip() != "" and it.strip() != "citus")
  parameters["shared_preload_libraries"] = "citus," & libs.join(",")

  # if not explicitly set, Citus overrides max_prepared_transactions
  let maxPrepared = parseInt(parameters.getOrDefault("max_prepared_transactions", "0"))
  if maxPrepared == 0:
    let maxConn = parseInt(parameters.getOrDefault("max_connections", "100"))
    parameters["max_prepared_transactions"] = $(maxConn * 2)

  # Resharding uses logical replication
  parameters["wal_level"] = "logical"

method ignoreReplicationSlot*(self: CitusHandler, slot: Table[string, string]): bool =
  ## Check if a replication slot should be ignored.
  if slot.getOrDefault("type") == "logical":
    let slotName = slot.getOrDefault("name", "")
    if slotName.match(getCitusSlotNameRe()):
      return true
  return false

method bootstrap*(self: CitusHandler) =
  ## Bootstrap handler for new cluster.
  # Would create citus database and extension here
  discard

# Factory function

proc getCitusHandler*(postgresql: pointer, config: Table[string, JsonNode]): CitusHandler =
  ## Get a Citus handler for the given configuration.
  return newCitusHandler(postgresql, config)
