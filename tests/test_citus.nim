## Tests for patroni/postgresql/mpp/citus module.

import std/[hashes, json, options, sets, strutils, tables, unittest]
import ../patroni/postgresql/mpp/citus

suite "Citus - PgDistNode":
  test "newPgDistNode creates node with required fields":
    let node = newPgDistNode("host1", 5432, "primary")
    check node.host == "host1"
    check node.port == 5432
    check node.role == "primary"
    check node.nodeid.isNone

  test "newPgDistNode creates node with nodeid":
    let node = newPgDistNode("host1", 5432, "secondary", some(1))
    check node.host == "host1"
    check node.port == 5432
    check node.role == "secondary"
    check node.nodeid.isSome
    check node.nodeid.get == 1

  test "PgDistNode equality based on host and port":
    let node1 = newPgDistNode("host1", 5432, "primary")
    let node2 = newPgDistNode("host1", 5432, "secondary")
    let node3 = newPgDistNode("host2", 5432, "primary")
    check node1 == node2  # Same host:port, different role
    check node1 != node3  # Different host

  test "PgDistNode hash based on host and port":
    let node1 = newPgDistNode("host1", 5432, "primary")
    let node2 = newPgDistNode("host1", 5432, "secondary")
    check hash(node1) == hash(node2)

  test "PgDistNode isPrimary for primary role":
    let node = newPgDistNode("host1", 5432, "primary")
    check node.isPrimary == true

  test "PgDistNode isPrimary for demoted role":
    let node = newPgDistNode("host1", 5432, "demoted")
    check node.isPrimary == true

  test "PgDistNode isPrimary false for secondary":
    let node = newPgDistNode("host1", 5432, "secondary")
    check node.isPrimary == false

  test "PgDistNode string representation":
    let node = newPgDistNode("host1", 5432, "primary", some(1))
    let str = $node
    check "host1" in str
    check "5432" in str
    check "primary" in str

  test "PgDistNode asTuple without nodeid":
    let node = newPgDistNode("host1", 5432, "primary", some(1))
    let t = node.asTuple(false)
    check t[0] == "host1"
    check t[1] == 5432
    check t[2] == "primary"
    check t[3].isNone

  test "PgDistNode asTuple with nodeid":
    let node = newPgDistNode("host1", 5432, "primary", some(1))
    let t = node.asTuple(true)
    check t[0] == "host1"
    check t[1] == 5432
    check t[2] == "primary"
    check t[3].isSome
    check t[3].get == 1

suite "Citus - PgDistGroup":
  test "newPgDistGroup creates empty group":
    let group = newPgDistGroup(0)
    check group.groupid == 0
    check group.failover == false
    check group.len == 0

  test "newPgDistGroup creates group with nodes":
    let nodes = @[
      newPgDistNode("host1", 5432, "primary"),
      newPgDistNode("host2", 5432, "secondary")
    ]
    let group = newPgDistGroup(1, nodes)
    check group.groupid == 1
    check group.len == 2

  test "PgDistGroup add node":
    let group = newPgDistGroup(0)
    let node = newPgDistNode("host1", 5432, "primary")
    group.add(node)
    check group.len == 1
    check node in group

  test "PgDistGroup contains node":
    let node = newPgDistNode("host1", 5432, "primary")
    let group = newPgDistGroup(0, @[node])
    check node in group

  test "PgDistGroup primary returns primary node":
    let primary = newPgDistNode("host1", 5432, "primary")
    let secondary = newPgDistNode("host2", 5432, "secondary")
    let group = newPgDistGroup(0, @[primary, secondary])
    let p = group.primary()
    check p.isSome
    check p.get == primary

  test "PgDistGroup primary returns none when no primary":
    let node = newPgDistNode("host1", 5432, "secondary")
    let group = newPgDistGroup(0, @[node])
    check group.primary().isNone

  test "PgDistGroup get returns matching node":
    let node = newPgDistNode("host1", 5432, "primary")
    let group = newPgDistGroup(0, @[node])
    let query = newPgDistNode("host1", 5432, "secondary")  # Different role
    let found = group.get(query)
    check found.isSome
    check found.get.role == "primary"  # Returns the actual stored node

  test "PgDistGroup get returns none for missing node":
    let group = newPgDistGroup(0)
    let query = newPgDistNode("host1", 5432, "primary")
    check group.get(query).isNone

  test "PgDistGroup equals with same nodes":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let group1 = newPgDistGroup(0, nodes)
    let group2 = newPgDistGroup(0, nodes)
    check group1.equals(group2)

  test "PgDistGroup equals false for different groupid":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let group1 = newPgDistGroup(0, nodes)
    let group2 = newPgDistGroup(1, nodes)
    check not group1.equals(group2)

  test "PgDistGroup set difference":
    let node1 = newPgDistNode("host1", 5432, "primary")
    let node2 = newPgDistNode("host2", 5432, "secondary")
    let group1 = newPgDistGroup(0, @[node1, node2])
    let group2 = newPgDistGroup(0, @[node1])
    let diff = group1 - group2
    check diff.len == 1
    check node2 in diff

suite "Citus - PgDistTask":
  test "newPgDistTask creates task":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task = newPgDistTask(0, nodes, "after_promote")
    check task.groupid == 0
    check task.event == "after_promote"
    check task.timeout.isNone

  test "newPgDistTask with timeout":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task = newPgDistTask(0, nodes, "after_promote", timeout = some(30.0))
    check task.timeout.isSome
    check task.timeout.get == 30.0

  test "newPgDistTask with cooldown":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task = newPgDistTask(0, nodes, "after_promote", cooldown = some(5000.0))
    check task.cooldown == 5000.0

  test "newPgDistTask default cooldown":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task = newPgDistTask(0, nodes, "after_promote")
    check task.cooldown == 10000.0

  test "PgDistTask equality based on event and nodes":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task1 = newPgDistTask(0, nodes, "after_promote")
    let task2 = newPgDistTask(0, nodes, "after_promote")
    check task1 == task2

  test "PgDistTask inequality for different events":
    let nodes = @[newPgDistNode("host1", 5432, "primary")]
    let task1 = newPgDistTask(0, nodes, "after_promote")
    let task2 = newPgDistTask(0, nodes, "before_demote")
    check task1 != task2

suite "Citus - Citus MPP":
  test "newCitus creates Citus MPP":
    var config = initTable[string, JsonNode]()
    config["database"] = newJString("citus")
    config["group"] = newJInt(0)
    let citus = newCitus(config)
    check citus != nil

  test "Citus validateConfig requires database":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    var validConfig = newJObject()
    validConfig["group"] = newJInt(0)
    check citus.validateConfig(validConfig) == false

  test "Citus validateConfig requires group":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    var validConfig = newJObject()
    validConfig["database"] = newJString("citus")
    check citus.validateConfig(validConfig) == false

  test "Citus validateConfig accepts valid config":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    var validConfig = newJObject()
    validConfig["database"] = newJString("citus")
    validConfig["group"] = newJInt(0)
    check citus.validateConfig(validConfig) == true

  test "Citus validateConfig accepts string group":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    var validConfig = newJObject()
    validConfig["database"] = newJString("citus")
    validConfig["group"] = newJString("0")
    check citus.validateConfig(validConfig) == true

  test "Citus validateConfig rejects non-numeric string group":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    var validConfig = newJObject()
    validConfig["database"] = newJString("citus")
    validConfig["group"] = newJString("abc")
    check citus.validateConfig(validConfig) == false

  test "Citus group returns int value":
    var config = initTable[string, JsonNode]()
    config["group"] = newJInt(1)
    let citus = newCitus(config)
    let grp = citus.group()
    check grp.isSome
    check grp.get == 1

  test "Citus group returns parsed string value":
    var config = initTable[string, JsonNode]()
    config["group"] = newJString("2")
    let citus = newCitus(config)
    let grp = citus.group()
    check grp.isSome
    check grp.get == 2

  test "Citus group returns none when not set":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    check citus.group().isNone

  test "Citus coordinatorGroupId returns 0":
    var config = initTable[string, JsonNode]()
    let citus = newCitus(config)
    let coordId = citus.coordinatorGroupId()
    check coordId.isSome
    check coordId.get == CITUS_COORDINATOR_GROUP_ID
    check coordId.get == 0

  test "Citus isCoordinator when group is 0":
    var config = initTable[string, JsonNode]()
    config["group"] = newJInt(0)
    config["database"] = newJString("citus")
    let citus = newCitus(config)
    check citus.isCoordinator == true

  test "Citus isWorker when group is not 0":
    var config = initTable[string, JsonNode]()
    config["group"] = newJInt(1)
    config["database"] = newJString("citus")
    let citus = newCitus(config)
    check citus.isWorker == true
    check citus.isCoordinator == false

suite "Citus - CitusHandler":
  test "newCitusHandler creates handler":
    var config = initTable[string, JsonNode]()
    config["database"] = newJString("citus")
    config["group"] = newJInt(0)
    let handler = newCitusHandler(nil, config)
    check handler != nil

  test "CitusHandler scheduleCacheRebuild":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    handler.scheduleCacheRebuild()
    check handler.scheduleLoadPgDistGroup == true

  test "CitusHandler onDemote clears state":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    handler.onDemote()
    check handler.pgDistGroup.len == 0
    check handler.tasks.len == 0
    check handler.inFlight.isNil

  test "CitusHandler adjustPostgresGucs adds citus to shared_preload_libraries":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var params = initTable[string, string]()
    handler.adjustPostgresGucs(params)
    check "citus" in params["shared_preload_libraries"]

  test "CitusHandler adjustPostgresGucs puts citus first in libraries":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var params = initTable[string, string]()
    params["shared_preload_libraries"] = "pg_stat_statements,auto_explain"
    handler.adjustPostgresGucs(params)
    check params["shared_preload_libraries"].startsWith("citus,")

  test "CitusHandler adjustPostgresGucs removes duplicate citus":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var params = initTable[string, string]()
    params["shared_preload_libraries"] = "citus,pg_stat_statements"
    handler.adjustPostgresGucs(params)
    # Should have citus only once at the start
    check params["shared_preload_libraries"].startsWith("citus,")
    let libs = params["shared_preload_libraries"].split(',')
    var citusCount = 0
    for lib in libs:
      if lib.strip() == "citus":
        inc citusCount
    check citusCount == 1

  test "CitusHandler adjustPostgresGucs sets max_prepared_transactions":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var params = initTable[string, string]()
    params["max_connections"] = "100"
    handler.adjustPostgresGucs(params)
    check params["max_prepared_transactions"] == "200"

  test "CitusHandler adjustPostgresGucs sets wal_level to logical":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var params = initTable[string, string]()
    handler.adjustPostgresGucs(params)
    check params["wal_level"] == "logical"

  test "CitusHandler ignoreReplicationSlot for citus move slot":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var slot = initTable[string, string]()
    slot["type"] = "logical"
    slot["name"] = "citus_shard_move_slot_1_2"
    check handler.ignoreReplicationSlot(slot) == true

  test "CitusHandler ignoreReplicationSlot for citus split slot":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var slot = initTable[string, string]()
    slot["type"] = "logical"
    slot["name"] = "citus_shard_split_slot_1_2_3"
    check handler.ignoreReplicationSlot(slot) == true

  test "CitusHandler ignoreReplicationSlot false for regular slot":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var slot = initTable[string, string]()
    slot["type"] = "logical"
    slot["name"] = "my_logical_slot"
    check handler.ignoreReplicationSlot(slot) == false

  test "CitusHandler ignoreReplicationSlot false for physical slot":
    var config = initTable[string, JsonNode]()
    let handler = newCitusHandler(nil, config)
    var slot = initTable[string, string]()
    slot["type"] = "physical"
    slot["name"] = "citus_shard_move_slot_1_2"
    check handler.ignoreReplicationSlot(slot) == false

suite "Citus - Constants":
  test "CITUS_COORDINATOR_GROUP_ID is 0":
    check CITUS_COORDINATOR_GROUP_ID == 0

when isMainModule:
  discard
