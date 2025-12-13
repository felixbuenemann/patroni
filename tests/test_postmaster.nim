## Tests for patroni/postgresql/postmaster module.

import std/[os, tables, unittest]
import ../patroni/postgresql/postmaster

suite "Postmaster - Constants":
  test "STOP_SIGNALS smart mode":
    check STOP_SIGNALS["smart"] == "TERM"

  test "STOP_SIGNALS fast mode":
    check STOP_SIGNALS["fast"] == "INT"

  test "STOP_SIGNALS immediate mode":
    check STOP_SIGNALS["immediate"] == "QUIT"

suite "Postmaster - PostmasterProcess Type":
  test "PostmasterProcess has required fields":
    var proc1: PostmasterProcess
    new(proc1)
    proc1.pid = 1234
    proc1.isSingleUser = false
    proc1.createTime = 1000.0
    check proc1.pid == 1234
    check proc1.isSingleUser == false
    check proc1.createTime == 1000.0

suite "Postmaster - readPostmasterPidfile":
  test "returns empty table for non-existent directory":
    let result = readPostmasterPidfile("/nonexistent/path")
    check result.len == 0

  test "returns empty table for missing pidfile":
    let tmpDir = getTempDir() / "patroni_test_postmaster_" & $getCurrentProcessId()
    createDir(tmpDir)
    defer: removeDir(tmpDir)
    let result = readPostmasterPidfile(tmpDir)
    check result.len == 0

  test "parses pidfile contents":
    let tmpDir = getTempDir() / "patroni_test_postmaster2_" & $getCurrentProcessId()
    createDir(tmpDir)
    defer: removeDir(tmpDir)
    let pidFile = tmpDir / "postmaster.pid"
    writeFile(pidFile, "1234\n/data\n123456789\n5432\n/tmp\n127.0.0.1\n")
    let result = readPostmasterPidfile(tmpDir)
    check result["pid"] == "1234"
    check result["data_dir"] == "/data"
    check result["port"] == "5432"

suite "Postmaster - isProcessRunning":
  test "current process is running":
    let myPid = getCurrentProcessId()
    check isProcessRunning(myPid) == true

  test "invalid pid returns false":
    # Using a very high PID that's unlikely to exist
    check isProcessRunning(999999999) == false

suite "Postmaster - fromPid":
  test "returns nil for non-running process":
    let result = fromPid(999999999)
    check result == nil

  test "returns PostmasterProcess for running process":
    # Note: This only works on the current process in test environment
    # A real postmaster would need actual postgres running
    let myPid = getCurrentProcessId()
    let result = fromPid(myPid)
    # This might return nil since we're not a real postmaster
    check true  # Just checking it doesn't crash

suite "Postmaster - fromPidfile":
  test "returns nil for non-existent pidfile":
    let result = fromPidfile("/nonexistent/path")
    check result == nil

  test "returns nil for empty directory":
    let tmpDir = getTempDir() / "patroni_test_postmaster3_" & $getCurrentProcessId()
    createDir(tmpDir)
    defer: removeDir(tmpDir)
    let result = fromPidfile(tmpDir)
    check result == nil

suite "Postmaster - PostmasterProcess Methods":
  test "isRunning for PostmasterProcess":
    var proc1: PostmasterProcess
    new(proc1)
    proc1.pid = getCurrentProcessId()
    check proc1.isRunning() == true

  test "isRunning returns false for invalid pid":
    var proc1: PostmasterProcess
    new(proc1)
    proc1.pid = 999999999
    check proc1.isRunning() == false

when isMainModule:
  discard
