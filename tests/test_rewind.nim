## Tests for patroni/postgresql/rewind module.

import std/[options, tables, unittest]
import ../patroni/postgresql/rewind

suite "Rewind - RewindStatus Enum":
  test "rsInitial is 0":
    check ord(rsInitial) == 0

  test "rsCheckpoint is 1":
    check ord(rsCheckpoint) == 1

  test "rsCheck is 2":
    check ord(rsCheck) == 2

  test "rsNeed is 3":
    check ord(rsNeed) == 3

  test "rsNotNeed is 4":
    check ord(rsNotNeed) == 4

  test "rsSuccess is 5":
    check ord(rsSuccess) == 5

  test "rsFailed is 6":
    check ord(rsFailed) == 6

suite "Rewind - configurationAllowsRewind":
  test "returns true when wal_log_hints is on":
    var data = initTable[string, string]()
    data["wal_log_hints setting"] = "on"
    data["Data page checksum version"] = "0"
    check configurationAllowsRewind(data) == true

  test "returns true when checksums are enabled":
    var data = initTable[string, string]()
    data["wal_log_hints setting"] = "off"
    data["Data page checksum version"] = "1"
    check configurationAllowsRewind(data) == true

  test "returns false when neither enabled":
    var data = initTable[string, string]()
    data["wal_log_hints setting"] = "off"
    data["Data page checksum version"] = "0"
    check configurationAllowsRewind(data) == false

  test "returns false with empty data":
    var data = initTable[string, string]()
    check configurationAllowsRewind(data) == false

  test "returns true when both enabled":
    var data = initTable[string, string]()
    data["wal_log_hints setting"] = "on"
    data["Data page checksum version"] = "2"
    check configurationAllowsRewind(data) == true

suite "Rewind - Rewind Type":
  test "newRewind creates Rewind with initial state":
    let r = newRewind(nil)
    check r != nil
    check r.isNeeded == false
    check r.executed == false
    check r.failed == false

  test "resetState resets to initial":
    let r = newRewind(nil)
    # Can't directly set state but we can test resetState doesn't crash
    r.resetState()
    check r.isNeeded == false

suite "Rewind - State Methods":
  test "isNeeded returns false for initial state":
    let r = newRewind(nil)
    check r.isNeeded == false

  test "executed returns false for initial state":
    let r = newRewind(nil)
    check r.executed == false

  test "failed returns false for initial state":
    let r = newRewind(nil)
    check r.failed == false

  test "checkpointAfterPromote returns false for initial state":
    let r = newRewind(nil)
    check r.checkpointAfterPromote == false

suite "Rewind - findMissingWal":
  test "finds WAL filename from pg_rewind error":
    let r = newRewind(nil)
    let errorMsg = """
pg_rewind: error: could not open file "/pg_wal/000000010000000000000001": No such file or directory
"""
    let result = r.findMissingWal(errorMsg)
    check result.isSome
    check result.get == "000000010000000000000001"

  test "returns none for no matching error":
    let r = newRewind(nil)
    let errorMsg = "some other error message"
    let result = r.findMissingWal(errorMsg)
    check result.isNone

  test "returns none for short filename":
    let r = newRewind(nil)
    let errorMsg = """
pg_rewind: error: could not open file "/pg_wal/short": No such file or directory
"""
    let result = r.findMissingWal(errorMsg)
    check result.isNone

  test "returns none for path without slash":
    let r = newRewind(nil)
    let errorMsg = """
pg_rewind: error: could not open file "nopath": No such file or directory
"""
    let result = r.findMissingWal(errorMsg)
    check result.isNone

suite "Rewind - buildArchiverCommand":
  test "replaces %f with WAL filename":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("cp %f /backup/", "000000010000000000000001")
    check result == "cp 000000010000000000000001 /backup/"

  test "replaces %p with WAL filename":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("cp %p /backup/", "000000010000000000000001")
    check result == "cp 000000010000000000000001 /backup/"

  test "replaces %r with default segment":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("test %r", "ignored")
    check result == "test 000000010000000000000001"

  test "replaces %% with single percent":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("100%% complete", "test")
    check result == "100% complete"

  test "handles unknown percent sequences":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("test %x value", "wal")
    check result == "test %x value"

  test "handles complex command":
    let r = newRewind(nil)
    let result = r.buildArchiverCommand("gzip < %p > /archive/%f.gz", "mywal")
    check result == "gzip < mywal > /archive/mywal.gz"

suite "Rewind - readPostmasterOpts":
  test "returns empty table by default":
    let r = newRewind(nil)
    let opts = r.readPostmasterOpts()
    check opts.len == 0

suite "Rewind - canRewindOrReinitializeAllowed":
  test "returns false when neither rewind nor reinit allowed":
    let r = newRewind(nil)
    check r.canRewindOrReinitializeAllowed == false

suite "Rewind - checkLeaderIsNotInRecovery":
  test "returns none for empty connection":
    var connKwargs = initTable[string, string]()
    let result = checkLeaderIsNotInRecovery(connKwargs)
    check result.isNone

suite "Rewind - checkLeaderHasRunCheckpoint":
  test "returns error for invalid connection":
    var connKwargs = initTable[string, string]()
    let result = checkLeaderHasRunCheckpoint(connKwargs)
    # Without valid connection, should return an error message
    check result.isSome

when isMainModule:
  discard
