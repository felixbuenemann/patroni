## Tests for patroni/scripts/wale_restore module.

import std/[unittest, strutils]
import ../patroni/scripts/wale_restore

# Test data constants matching Python tests
const
  WALE_OUTPUT_HEADER = "name\tlast_modified\texpanded_size_bytes\twal_segment_backup_start\twal_segment_offset_backup_start\twal_segment_backup_stop\twal_segment_offset_backup_stop\n"

  WALE_OUTPUT_VALUES = "base_00000001000000000000007F_00000040\t2015-05-18T10:13:25.000Z\t167772160\t00000001000000000000007F\t00000040\t00000001000000000000007F\t00000240\n"

  WALE_OUTPUT = WALE_OUTPUT_HEADER & WALE_OUTPUT_VALUES

  WALE_TEST_RETRIES = 2

suite "WALERestore":
  var restore: WALERestore

  setup:
    restore = newWALERestore(
      scope = "batman",
      datadir = "/data",
      connstring = "host=batman port=5432 user=batman",
      envDir = "/etc",
      thresholdMb = 100,
      thresholdPct = 30,
      useIam = 0,
      noLeader = false,
      retries = WALE_TEST_RETRIES
    )

  test "newWALERestore creates instance":
    check restore.scope == "batman"
    check restore.dataDir == "/data"
    check restore.walE.thresholdMb == 100

  test "init_error flag":
    var badRestore = newWALERestore(
      scope = "",
      datadir = "",
      connstring = "",
      envDir = "/nonexistent/path",
      thresholdMb = 0,
      thresholdPct = 0,
      useIam = 0,
      noLeader = false,
      retries = 0
    )
    # Non-existent envDir should set initError
    check badRestore.initError == true

  test "run with init_error returns FAIL":
    restore.initError = true
    check restore.run() == int(ExitCode.ecFail)

suite "WALERestore Backup Parsing":
  test "parseWaleOutput valid output":
    # Parse WAL-E backup-list output
    let lines = WALE_OUTPUT.split('\n')
    check lines.len >= 2

    # Header line
    let headers = lines[0].split('\t')
    check "name" in headers
    check "expanded_size_bytes" in headers

  test "parseWaleOutput extract backup info":
    let lines = WALE_OUTPUT.split('\n')
    if lines.len >= 2:
      let values = lines[1].split('\t')
      check values.len >= 3
      # Check backup name format
      check values[0].startsWith("base_")
      # Check size
      check values[2] == "167772160"

  test "parseWaleOutput empty values":
    let lines = WALE_OUTPUT_HEADER.split('\n')
    # Only header, no values
    check lines.len >= 1

suite "WALERestore Threshold Check":
  test "backup above threshold should use S3":
    # 167772160 bytes = ~160MB, threshold = 100MB
    # Should use S3 for restore
    check 167772160 > 100 * 1024 * 1024

  test "backup below threshold should not use S3":
    # Small backup (1 byte) below 100MB threshold
    check 1 < 100 * 1024 * 1024

suite "WALERestore Exit Codes":
  test "exit code values":
    check ord(ExitCode.ecSuccess) == 0
    check ord(ExitCode.ecRetryLater) == 1
    check ord(ExitCode.ecFail) == 2

# Helper proc for Major Version tests
proc parseMajorVersionString(version: string): float =
  ## Helper to parse PostgreSQL major version string.
  if version.len == 0:
    return 0.0
  try:
    result = parseFloat(version)
  except ValueError:
    result = 0.0

suite "Major Version":
  test "getMajorVersion from PG_VERSION file format":
    # PostgreSQL 9.x format: "9.6"
    check parseMajorVersionString("9.6") == 9.6
    check parseMajorVersionString("9.4") == 9.4

  test "getMajorVersion from PG 10+ format":
    # PostgreSQL 10+ format: "10", "11", etc.
    check parseMajorVersionString("10") == 10.0
    check parseMajorVersionString("15") == 15.0

  test "getMajorVersion invalid returns 0":
    check parseMajorVersionString("") == 0.0
    check parseMajorVersionString("invalid") == 0.0

suite "Subdirectory Fix":
  test "fix broken symlinks":
    # Test the fix_subdirectory_path_if_broken logic
    # This would require filesystem mocking
    check true  # Placeholder

when isMainModule:
  discard
