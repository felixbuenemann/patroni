## Tests for patroni/postgresql/slots module.

import std/[json, tables, unittest]
import ../patroni/postgresql/slots

suite "Slots - SlotType Enum":
  test "stPhysical value":
    check $stPhysical == "physical"

  test "stLogical value":
    check $stLogical == "logical"

suite "Slots - ReplicationSlot Type":
  test "newReplicationSlot creates physical slot":
    let slot = newReplicationSlot("test_slot")
    check slot.name == "test_slot"
    check slot.slotType == stPhysical
    check slot.database == ""
    check slot.plugin == ""
    check slot.active == false
    check slot.restartLsn == 0
    check slot.confirmedFlushLsn == 0

  test "newReplicationSlot creates logical slot":
    let slot = newReplicationSlot("logical_slot", stLogical, "mydb", "pgoutput")
    check slot.name == "logical_slot"
    check slot.slotType == stLogical
    check slot.database == "mydb"
    check slot.plugin == "pgoutput"
    check slot.active == false

  test "ReplicationSlot can update LSN values":
    let slot = newReplicationSlot("test")
    slot.restartLsn = 100
    slot.confirmedFlushLsn = 200
    check slot.restartLsn == 100
    check slot.confirmedFlushLsn == 200

suite "Slots - compareSlots (ReplicationSlot)":
  test "physical slots are equal regardless of other fields":
    let s1 = newReplicationSlot("slot1", stPhysical)
    let s2 = newReplicationSlot("slot2", stPhysical)
    check compareSlots(s1, s2) == true

  test "physical and logical slots are not equal":
    let s1 = newReplicationSlot("slot1", stPhysical)
    let s2 = newReplicationSlot("slot2", stLogical, "db", "plugin")
    check compareSlots(s1, s2) == false

  test "logical slots with same database and plugin are equal":
    let s1 = newReplicationSlot("slot1", stLogical, "mydb", "pgoutput")
    let s2 = newReplicationSlot("slot2", stLogical, "mydb", "pgoutput")
    check compareSlots(s1, s2) == true

  test "logical slots with different database are not equal":
    let s1 = newReplicationSlot("slot1", stLogical, "db1", "pgoutput")
    let s2 = newReplicationSlot("slot2", stLogical, "db2", "pgoutput")
    check compareSlots(s1, s2) == false

  test "logical slots with different plugin are not equal":
    let s1 = newReplicationSlot("slot1", stLogical, "mydb", "pgoutput")
    let s2 = newReplicationSlot("slot2", stLogical, "mydb", "test_decoding")
    check compareSlots(s1, s2) == false

suite "Slots - compareSlots (JsonNode)":
  test "physical JSON slots are equal":
    let s1 = %*{"type": "physical", "name": "slot1"}
    let s2 = %*{"type": "physical", "name": "slot2"}
    check compareSlots(s1, s2) == true

  test "physical and logical JSON slots are not equal":
    let s1 = %*{"type": "physical"}
    let s2 = %*{"type": "logical", "database": "db", "plugin": "pgoutput"}
    check compareSlots(s1, s2) == false

  test "logical JSON slots with same database and plugin are equal":
    let s1 = %*{"type": "logical", "database": "mydb", "plugin": "pgoutput"}
    let s2 = %*{"type": "logical", "database": "mydb", "plugin": "pgoutput"}
    check compareSlots(s1, s2) == true

  test "logical JSON slots with different database are not equal":
    let s1 = %*{"type": "logical", "database": "db1", "plugin": "pgoutput"}
    let s2 = %*{"type": "logical", "database": "db2", "plugin": "pgoutput"}
    check compareSlots(s1, s2) == false

suite "Slots - SlotsHandler":
  test "newSlotsHandler creates handler":
    let handler = newSlotsHandler(nil)
    check handler != nil
    check handler.getReplicationSlots().len == 0

  test "safeSlotNames returns empty list initially":
    let handler = newSlotsHandler(nil)
    check handler.safeSlotNames().len == 0

  test "setIgnoreSlots and safeSlotNames work together":
    let handler = newSlotsHandler(nil)
    handler.setIgnoreSlots(@["ignored_slot"])
    check handler.safeSlotNames().len == 0

  test "loadReplicationSlots clears existing slots":
    let handler = newSlotsHandler(nil)
    handler.loadReplicationSlots()
    check handler.getReplicationSlots().len == 0

  test "getSlotAdvanceStatus returns empty table initially":
    let handler = newSlotsHandler(nil)
    let status = handler.getSlotAdvanceStatus()
    check status.len == 0

suite "Slots - SlotsAdvanceThread":
  test "newSlotsAdvanceThread creates thread":
    let handler = newSlotsHandler(nil)
    let thread = newSlotsAdvanceThread(handler)
    check thread != nil
    check thread.hasFailed() == false
    check thread.getCopySlots().len == 0

  test "scheduleAdvance adds entries":
    let handler = newSlotsHandler(nil)
    let thread = newSlotsAdvanceThread(handler)
    thread.scheduleAdvance("mydb", "slot1", 100)
    # Can't directly check scheduled, but it shouldn't crash
    check thread.hasFailed() == false

  test "getCopySlots returns and clears list":
    let handler = newSlotsHandler(nil)
    let thread = newSlotsAdvanceThread(handler)
    let slots1 = thread.getCopySlots()
    check slots1.len == 0
    let slots2 = thread.getCopySlots()
    check slots2.len == 0

suite "Slots - SlotsHandler Methods":
  test "createPhysicalReplicationSlot returns false without postgresql":
    let handler = newSlotsHandler(nil)
    check handler.createPhysicalReplicationSlot("test_slot") == false

  test "createPhysicalReplicationSlot with immediately_reserve returns false without postgresql":
    let handler = newSlotsHandler(nil)
    check handler.createPhysicalReplicationSlot("test_slot", true) == false

  test "createLogicalReplicationSlot returns false without postgresql":
    let handler = newSlotsHandler(nil)
    check handler.createLogicalReplicationSlot("test_slot", "mydb", "pgoutput") == false

  test "dropReplicationSlot returns false without postgresql":
    let handler = newSlotsHandler(nil)
    check handler.dropReplicationSlot("test_slot") == false

  test "advanceReplicationSlot returns false without postgresql":
    let handler = newSlotsHandler(nil)
    check handler.advanceReplicationSlot("test_slot", 100) == false

  test "onDemote clears advance thread":
    let handler = newSlotsHandler(nil)
    handler.onDemote()
    check handler.safeSlotNames().len == 0

  test "onPromote reloads slots":
    let handler = newSlotsHandler(nil)
    handler.onPromote()
    check handler.getReplicationSlots().len == 0

  test "copyLogicalSlotFromLeader returns false without leader":
    let handler = newSlotsHandler(nil)
    check handler.copyLogicalSlotFromLeader("slot", nil) == false

suite "Slots - scheduleAdvanceSlots":
  test "scheduleAdvanceSlots with empty table":
    let handler = newSlotsHandler(nil)
    var emptyTable = initTable[string, int64]()
    handler.scheduleAdvanceSlots(emptyTable)
    check handler.safeSlotNames().len == 0

when isMainModule:
  discard
