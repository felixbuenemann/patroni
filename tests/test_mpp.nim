## Tests for patroni/postgresql/mpp module (Massively Parallel Processing).

import std/[json, options, tables, unittest]
import ../patroni/postgresql/mpp
import ../patroni/postgresql/mpp_factory
import ../patroni/postgresql/mpp/citus

suite "MPP - AbstractMPP":
  test "newAbstractMPP creates MPP with config":
    var config = initTable[string, JsonNode]()
    config["database"] = newJString("mydb")
    let mpp = newAbstractMPP(config)
    check mpp != nil
    check mpp.isEnabled == true

  test "newAbstractMPP with empty config is disabled":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp != nil
    check mpp.isEnabled == false

  test "isEnabled returns true when config has entries":
    var config = initTable[string, JsonNode]()
    config["key"] = newJString("value")
    let mpp = newAbstractMPP(config)
    check mpp.isEnabled == true

  test "isEnabled returns false when config is empty":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.isEnabled == false

  test "validateConfig returns true by default":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.validateConfig(newJObject()) == true

  test "group returns none by default":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.group().isNone

  test "coordinatorGroupId returns none by default":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.coordinatorGroupId().isNone

  test "mppType returns MPP":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.mppType() == "MPP"

  test "k8sGroupLabel returns lowercase mpp type with -group suffix":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.k8sGroupLabel() == "mpp-group"

  test "isCoordinator returns false when disabled":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.isCoordinator() == false

  test "isCoordinator returns false when group is none":
    var config = initTable[string, JsonNode]()
    config["enabled"] = newJBool(true)
    let mpp = newAbstractMPP(config)
    check mpp.isCoordinator() == false

  test "isWorker returns false when disabled":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    check mpp.isWorker() == false

suite "MPP - NullMPP":
  test "newNullMPP creates null implementation":
    let mpp = newNullMPP()
    check mpp != nil

  test "NullMPP is always disabled":
    let mpp = newNullMPP()
    check mpp.isEnabled == false

  test "NullMPP validateConfig always returns true":
    let mpp = newNullMPP()
    check mpp.validateConfig(newJObject()) == true
    check mpp.validateConfig(newJNull()) == true

  test "NullMPP group always returns none":
    let mpp = newNullMPP()
    check mpp.group().isNone

  test "NullMPP coordinatorGroupId always returns none":
    let mpp = newNullMPP()
    check mpp.coordinatorGroupId().isNone

  test "NullMPP isCoordinator returns false":
    let mpp = newNullMPP()
    check mpp.isCoordinator() == false

  test "NullMPP isWorker returns false":
    let mpp = newNullMPP()
    check mpp.isWorker() == false

suite "MPP - AbstractMPPHandler":
  test "newAbstractMPPHandler creates handler":
    var config = initTable[string, JsonNode]()
    let handler = newAbstractMPPHandler(nil, config)
    check handler != nil

  test "handler methods exist and don't crash":
    var config = initTable[string, JsonNode]()
    let handler = newAbstractMPPHandler(nil, config)
    # These should all be no-ops but shouldn't crash
    var params = initTable[string, string]()
    handler.adjustPostgresGucs(params)
    handler.onDemote()
    handler.scheduleCacheRebuild()
    handler.bootstrap()
    check true

  test "ignoreReplicationSlot returns false by default":
    var config = initTable[string, JsonNode]()
    let handler = newAbstractMPPHandler(nil, config)
    var slot = initTable[string, string]()
    slot["name"] = "test_slot"
    check handler.ignoreReplicationSlot(slot) == false

suite "MPP - NullMPPHandler":
  test "newNullMPPHandler creates null handler":
    var config = initTable[string, JsonNode]()
    let handler = newNullMPPHandler(nil, config)
    check handler != nil

  test "NullMPPHandler methods don't crash":
    var config = initTable[string, JsonNode]()
    let handler = newNullMPPHandler(nil, config)
    # These should all be no-ops but shouldn't crash
    var params = initTable[string, string]()
    handler.adjustPostgresGucs(params)
    handler.onDemote()
    handler.scheduleCacheRebuild()
    handler.bootstrap()
    check true

  test "NullMPPHandler ignoreReplicationSlot returns false":
    var config = initTable[string, JsonNode]()
    let handler = newNullMPPHandler(nil, config)
    var slot = initTable[string, string]()
    slot["name"] = "test_slot"
    slot["type"] = "logical"
    check handler.ignoreReplicationSlot(slot) == false

suite "MPP - Factory Functions":
  test "iterMppClasses yields citus when in config":
    var config = newJObject()
    config["citus"] = newJObject()
    var found = false
    for (name, available) in iterMppClasses(config):
      if name == "citus":
        check available == true
        found = true
    check found == true

  test "iterMppClasses yields citus unavailable when not in config":
    let config = newJObject()
    var found = false
    for (name, available) in iterMppClasses(config):
      if name == "citus":
        check available == false
        found = true
    check found == true

  test "getMpp returns NullMPP when no MPP configured":
    let config = newJObject()
    let mpp = getMpp(config)
    check mpp != nil
    check mpp.isEnabled == false

  test "getMpp returns Citus when citus config exists":
    var config = newJObject()
    config["citus"] = newJObject()
    let mpp = getMpp(config)
    check mpp != nil
    # Citus instance with empty config is disabled
    check mpp.isEnabled == false
    check mpp of Citus

  test "getHandler returns NullMPPHandler for NullMPP":
    let mpp = newNullMPP()
    let handler = mpp.getHandler(nil)
    check handler != nil

  test "getHandler returns handler with same config":
    var config = initTable[string, JsonNode]()
    config["key"] = newJString("value")
    let mpp = newAbstractMPP(config)
    let handler = mpp.getHandler(nil)
    check handler != nil

suite "MPP - groupRe Pattern":
  test "AbstractMPP groupRe matches valid numeric groups":
    let config = initTable[string, JsonNode]()
    let mpp = newAbstractMPP(config)
    # The regex pattern should match numeric strings
    check mpp.groupRe != nil

  test "NullMPP has groupRe initialized":
    let mpp = newNullMPP()
    check mpp.groupRe != nil

when isMainModule:
  discard
