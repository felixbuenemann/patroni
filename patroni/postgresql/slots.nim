## Replication slot handling.
##
## Provides types and procedures for the creation, monitoring, management
## and synchronisation of PostgreSQL replication slots.

import std/[json, locks, options, os, sequtils, strformat, strutils, tables, times]
import ../dcs
import ../file_perm
import ../global_config
import ../log
import ../psycopg
import ../tags
import ./connection
import ./misc

export misc

let logger = getLogger("patroni.postgresql.slots")

type
  SlotType* = enum
    ## Type of replication slot.
    stPhysical = "physical"
    stLogical = "logical"

  ReplicationSlot* = ref object
    ## Represents a PostgreSQL replication slot.
    name*: string
    slotType*: SlotType
    database*: string
    plugin*: string
    active*: bool
    restartLsn*: int64
    confirmedFlushLsn*: int64
    catalog_xmin*: int64

  SlotsAdvanceThread* = ref object
    ## Thread for advancing logical replication slots on replicas.
    slotsHandler: SlotsHandler
    copySlots: seq[string]
    failed: bool
    scheduled: Table[string, Table[string, int64]]
    lock: Lock
    running: bool

  # Forward declaration for PostgreSQL type
  PostgresqlPtr* = ref object
    connectionPool*: ConnectionPool
    majorVersion*: int
    dataDir*: string

  SlotsHandler* = ref object
    ## Handler for managing replication slots.
    postgresql*: PostgresqlPtr
    schedule: Table[string, Table[string, int64]]
    storedSlots: Table[string, ReplicationSlot]
    replicationSlots: Table[string, ReplicationSlot]
    ignoreSlots: seq[string]
    lock: Lock
    advanceThread: SlotsAdvanceThread

proc compareSlots*(s1, s2: ReplicationSlot, dbid: string = "database"): bool =
  ## Compare 2 replication slot objects for equality.
  ##
  ## :param s1: First slot to be compared.
  ## :param s2: Second slot to be compared.
  ## :param dbid: Optional attribute to be compared when comparing logical slots.
  ##
  ## :returns: true if slots match based on type and relevant attributes.
  if s1.slotType != s2.slotType:
    return false

  if s1.slotType == stPhysical:
    return true

  # Logical slots need database and plugin to match
  result = s1.database == s2.database and s1.plugin == s2.plugin

proc compareSlots*(s1, s2: JsonNode): bool =
  ## Compare 2 replication slot JSON objects for equality.
  let type1 = s1.getOrDefault("type").getStr("")
  let type2 = s2.getOrDefault("type").getStr("")

  if type1 != type2:
    return false

  if type1 == "physical":
    return true

  result = s1.getOrDefault("database").getStr() == s2.getOrDefault("database").getStr() and
           s1.getOrDefault("plugin").getStr() == s2.getOrDefault("plugin").getStr()

proc newReplicationSlot*(name: string, slotType: SlotType = stPhysical,
                         database: string = "", plugin: string = ""): ReplicationSlot =
  ## Create a new ReplicationSlot.
  new(result)
  result.name = name
  result.slotType = slotType
  result.database = database
  result.plugin = plugin
  result.active = false
  result.restartLsn = 0
  result.confirmedFlushLsn = 0
  result.catalog_xmin = 0

proc newSlotsHandler*(postgresql: PostgresqlPtr): SlotsHandler =
  ## Create a new SlotsHandler.
  new(result)
  result.postgresql = postgresql
  result.schedule = initTable[string, Table[string, int64]]()
  result.storedSlots = initTable[string, ReplicationSlot]()
  result.replicationSlots = initTable[string, ReplicationSlot]()
  result.ignoreSlots = @[]
  initLock(result.lock)
  result.advanceThread = nil

proc newSlotsAdvanceThread*(slotsHandler: SlotsHandler): SlotsAdvanceThread =
  ## Create a new SlotsAdvanceThread.
  new(result)
  result.slotsHandler = slotsHandler
  result.copySlots = @[]
  result.failed = false
  result.scheduled = initTable[string, Table[string, int64]]()
  initLock(result.lock)
  result.running = false

proc scheduleAdvance*(self: SlotsAdvanceThread, database: string, slot: string, lsn: int64) =
  ## Schedule a slot to be advanced.
  withLock(self.lock):
    if database notin self.scheduled:
      self.scheduled[database] = initTable[string, int64]()
    self.scheduled[database][slot] = lsn

proc getCopySlots*(self: SlotsAdvanceThread): seq[string] =
  ## Get slots that need to be copied.
  withLock(self.lock):
    result = self.copySlots
    self.copySlots = @[]

proc hasFailed*(self: SlotsAdvanceThread): bool =
  ## Check if the advance thread has failed.
  result = self.failed

# SlotsHandler methods

proc loadReplicationSlots*(self: SlotsHandler) =
  ## Load replication slots from PostgreSQL.
  self.replicationSlots = initTable[string, ReplicationSlot]()

  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return

  try:
    let conn = self.postgresql.connectionPool.get("slots")
    let pgConn = conn.get()

    let query = """
      SELECT slot_name, slot_type, database, plugin, active,
             restart_lsn, confirmed_flush_lsn, catalog_xmin
      FROM pg_catalog.pg_replication_slots
    """

    let rows = pgConn.query(query)
    for row in rows:
      if row.len < 5:
        continue

      let slotName = row[0]
      let slotTypeStr = row[1]
      let database = row[2]
      let plugin = row[3]
      let activeStr = row[4]

      let slot = newReplicationSlot(slotName)
      slot.slotType = if slotTypeStr == "physical": stPhysical else: stLogical
      slot.database = database
      slot.plugin = plugin
      slot.active = activeStr == "t" or activeStr.toLowerAscii() == "true"

      # Parse LSN values if present
      if row.len > 5 and row[5].len > 0:
        try:
          slot.restartLsn = parseLsn(row[5])
        except ValueError:
          discard

      if row.len > 6 and row[6].len > 0:
        try:
          slot.confirmedFlushLsn = parseLsn(row[6])
        except ValueError:
          discard

      if row.len > 7 and row[7].len > 0:
        try:
          slot.catalog_xmin = parseInt(row[7])
        except ValueError:
          discard

      self.replicationSlots[slotName] = slot

    logger.debug(fmt"Loaded {self.replicationSlots.len} replication slots")
  except OperationalError as e:
    logger.error(fmt"Failed to load replication slots: {e.msg}")
  except DatabaseError as e:
    logger.error(fmt"Database error loading replication slots: {e.msg}")

proc createPhysicalReplicationSlot*(self: SlotsHandler, name: string, immediately_reserve: bool = false): bool =
  ## Create a physical replication slot.
  ##
  ## :param name: Name of the slot to create.
  ## :param immediately_reserve: If true, reserve WAL immediately.
  ## :returns: true if successful.
  logger.info(fmt"Creating physical replication slot '{name}'")

  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return false

  try:
    let conn = self.postgresql.connectionPool.get("slots")
    let pgConn = conn.get()

    let reserveStr = if immediately_reserve: "true" else: "false"
    let query = fmt"SELECT pg_catalog.pg_create_physical_replication_slot('{name}', {reserveStr})"
    discard pgConn.query(query)

    logger.info(fmt"Created physical replication slot '{name}'")
    return true
  except OperationalError as e:
    logger.error(fmt"Failed to create physical slot '{name}': {e.msg}")
    return false
  except DatabaseError as e:
    logger.error(fmt"Database error creating physical slot '{name}': {e.msg}")
    return false

proc createLogicalReplicationSlot*(self: SlotsHandler, name: string, database: string,
                                   plugin: string = "pgoutput"): bool =
  ## Create a logical replication slot.
  ##
  ## :param name: Name of the slot to create.
  ## :param database: Database for the slot.
  ## :param plugin: Output plugin to use.
  ## :returns: true if successful.
  logger.info(fmt"Creating logical replication slot '{name}' for database '{database}'")

  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return false

  try:
    # Connect to the specified database for logical slot creation
    var overrides = initTable[string, string]()
    overrides["database"] = database

    let conn = self.postgresql.connectionPool.get("slots_" & database, overrides)
    let pgConn = conn.get()

    let query = fmt"SELECT pg_catalog.pg_create_logical_replication_slot('{name}', '{plugin}')"
    discard pgConn.query(query)

    logger.info(fmt"Created logical replication slot '{name}' with plugin '{plugin}'")
    return true
  except OperationalError as e:
    logger.error(fmt"Failed to create logical slot '{name}': {e.msg}")
    return false
  except DatabaseError as e:
    logger.error(fmt"Database error creating logical slot '{name}': {e.msg}")
    return false

proc dropReplicationSlot*(self: SlotsHandler, name: string): bool =
  ## Drop a replication slot.
  ##
  ## :param name: Name of the slot to drop.
  ## :returns: true if successful.
  logger.info(fmt"Dropping replication slot '{name}'")

  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return false

  try:
    let conn = self.postgresql.connectionPool.get("slots")
    let pgConn = conn.get()

    let query = fmt"SELECT pg_catalog.pg_drop_replication_slot('{name}')"
    discard pgConn.query(query)

    logger.info(fmt"Dropped replication slot '{name}'")
    return true
  except OperationalError as e:
    logger.error(fmt"Failed to drop slot '{name}': {e.msg}")
    return false
  except DatabaseError as e:
    # Slot might already be dropped or not exist
    if "does not exist" in e.msg:
      logger.warning(fmt"Slot '{name}' does not exist, nothing to drop")
      return true
    logger.error(fmt"Database error dropping slot '{name}': {e.msg}")
    return false

proc advanceReplicationSlot*(self: SlotsHandler, name: string, lsn: int64): bool =
  ## Advance a replication slot to a specific LSN.
  ##
  ## :param name: Name of the slot.
  ## :param lsn: LSN to advance to.
  ## :returns: true if successful.
  let lsnStr = formatLsn(lsn)
  logger.debug(fmt"Advancing slot '{name}' to {lsnStr}")

  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return false

  # pg_replication_slot_advance requires PostgreSQL 11+
  if self.postgresql.majorVersion < 110000:
    logger.warning("pg_replication_slot_advance requires PostgreSQL 11+")
    return false

  try:
    let conn = self.postgresql.connectionPool.get("slots")
    let pgConn = conn.get()

    let query = fmt"SELECT pg_catalog.pg_replication_slot_advance('{name}', '{lsnStr}')"
    discard pgConn.query(query)

    logger.debug(fmt"Advanced slot '{name}' to {lsnStr}")
    return true
  except OperationalError as e:
    logger.error(fmt"Failed to advance slot '{name}': {e.msg}")
    return false
  except DatabaseError as e:
    logger.error(fmt"Database error advancing slot '{name}': {e.msg}")
    return false

proc syncReplicationSlots*(self: SlotsHandler, cluster: dcs.Cluster, tags: Tags): bool =
  ## Synchronize replication slots with the cluster state.
  ##
  ## :param cluster: Current cluster state.
  ## :param tags: Node tags.
  ## :returns: true if synchronization was successful.
  result = true

  # Get configured slots
  var configuredSlots = initTable[string, JsonNode]()

  let globalConf = getGlobalConfig()
  let slotsConf = globalConf.get("slots")
  if slotsConf != nil and slotsConf.kind == JObject:
    for name, conf in slotsConf.pairs:
      configuredSlots[name] = conf

  # Sync configured slots
  for name, conf in configuredSlots:
    if name in self.replicationSlots:
      continue

    # Create missing slot
    let slotType = conf.getOrDefault("type").getStr("physical")
    if slotType == "physical":
      if not self.createPhysicalReplicationSlot(name):
        result = false
    else:
      let database = conf.getOrDefault("database").getStr("")
      let plugin = conf.getOrDefault("plugin").getStr("pgoutput")
      if not self.createLogicalReplicationSlot(name, database, plugin):
        result = false

proc getSlotAdvanceStatus*(self: SlotsHandler): Table[string, int64] =
  ## Get the status of slot advancement.
  ##
  ## :returns: Map of slot names to their confirmed LSN positions.
  result = initTable[string, int64]()

  for name, slot in self.replicationSlots:
    if slot.confirmedFlushLsn > 0:
      result[name] = slot.confirmedFlushLsn

proc scheduleAdvanceSlots*(self: SlotsHandler, slots: Table[string, int64]) =
  ## Schedule slots to be advanced.
  ##
  ## :param slots: Map of slot names to target LSN positions.
  if self.advanceThread == nil:
    self.advanceThread = newSlotsAdvanceThread(self)

  for name, lsn in slots:
    if name in self.replicationSlots:
      let slot = self.replicationSlots[name]
      if slot.slotType == stLogical:
        self.advanceThread.scheduleAdvance(slot.database, name, lsn)

proc safeSlotNames*(self: SlotsHandler): seq[string] =
  ## Get names of slots that are safe to use.
  ##
  ## :returns: List of safe slot names.
  result = @[]
  for name, slot in self.replicationSlots:
    if name notin self.ignoreSlots:
      result.add(name)

proc setIgnoreSlots*(self: SlotsHandler, slots: seq[string]) =
  ## Set slots to be ignored.
  ##
  ## :param slots: List of slot names to ignore.
  withLock(self.lock):
    self.ignoreSlots = slots

proc getReplicationSlots*(self: SlotsHandler): Table[string, ReplicationSlot] =
  ## Get current replication slots.
  ##
  ## :returns: Map of slot names to slot objects.
  result = self.replicationSlots

proc copyLogicalSlotFromLeader*(self: SlotsHandler, slotName: string, leader: Leader): bool =
  ## Copy a logical slot from the leader.
  ##
  ## :param slotName: Name of the slot to copy.
  ## :param leader: Current cluster leader.
  ## :returns: true if successful.
  result = false

  if leader == nil or leader.member == nil:
    return false

  logger.info(fmt"Copying logical slot '{slotName}' from leader")

  # This would involve:
  # 1. Getting slot info from leader via REST API
  # 2. Creating the slot locally with the same parameters
  # 3. Syncing the slot position

  result = true

proc handleLogicalSlots*(self: SlotsHandler, cluster: dcs.Cluster, createSlotsFunc: proc(slots: seq[string]),
                         dropSlotsFunc: proc(slots: seq[string])): bool =
  ## Handle logical replication slots.
  ##
  ## :param cluster: Current cluster state.
  ## :param createSlotsFunc: Function to create slots.
  ## :param dropSlotsFunc: Function to drop slots.
  ## :returns: true if successful.
  result = true

  # Collect configured logical slots
  var configuredLogicalSlots: seq[string] = @[]

  let globalConf = getGlobalConfig()
  let slotsConf = globalConf.get("slots")
  if slotsConf != nil and slotsConf.kind == JObject:
    for name, conf in slotsConf.pairs:
      let slotType = conf.getOrDefault("type").getStr("physical")
      if slotType == "logical":
        configuredLogicalSlots.add(name)

  # Find slots to create and drop
  var toCreate: seq[string] = @[]
  var toDrop: seq[string] = @[]

  for name in configuredLogicalSlots:
    if name notin self.replicationSlots:
      toCreate.add(name)

  for name, slot in self.replicationSlots:
    if slot.slotType == stLogical and name notin configuredLogicalSlots:
      toDrop.add(name)

  if toCreate.len > 0:
    createSlotsFunc(toCreate)

  if toDrop.len > 0:
    dropSlotsFunc(toDrop)

proc onDemote*(self: SlotsHandler) =
  ## Handle demotion event.
  # Stop advancing slots, clean up state
  if self.advanceThread != nil:
    self.advanceThread.running = false
    self.advanceThread = nil

proc onPromote*(self: SlotsHandler) =
  ## Handle promotion event.
  # Reload slots and prepare for primary role
  self.loadReplicationSlots()
