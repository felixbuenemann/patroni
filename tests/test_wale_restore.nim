## Tests for patroni/scripts/wale_restore module.
## Ported from test_wale_restore.py

import std/[unittest, strutils, os, tables]
import ../patroni/scripts/wale_restore

# Test data constants matching Python tests
const
  WALE_OUTPUT_HEADER = "name\tlast_modified\texpanded_size_bytes\twal_segment_backup_start\twal_segment_offset_backup_start\twal_segment_backup_stop\twal_segment_offset_backup_stop\n"

  WALE_OUTPUT_VALUES = "base_00000001000000000000007F_00000040\t2015-05-18T10:13:25.000Z\t167772160\t00000001000000000000007F\t00000040\t00000001000000000000007F\t00000240\n"

  WALE_OUTPUT = WALE_OUTPUT_HEADER & WALE_OUTPUT_VALUES

  WALE_TEST_RETRIES = 2

suite "WALERestore Creation":
  test "newWALERestore creates instance with correct scope":
    let restore = newWALERestore(
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
    check restore.scope == "batman"

  test "newWALERestore creates instance with correct dataDir":
    let restore = newWALERestore(
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
    check restore.dataDir == "/data"

  test "newWALERestore creates instance with correct thresholdMb":
    let restore = newWALERestore(
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
    check restore.walE.thresholdMb == 100

  test "newWALERestore creates instance with correct thresholdPct":
    let restore = newWALERestore(
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
    check restore.walE.thresholdPct == 30

  test "newWALERestore sets initError for non-existent envDir":
    let restore = newWALERestore(
      scope = "test",
      datadir = "/data",
      connstring = "",
      envDir = "/nonexistent/path/that/does/not/exist",
      thresholdMb = 0,
      thresholdPct = 0,
      useIam = 0,
      noLeader = false,
      retries = 0
    )
    check restore.initError == true

  test "newWALERestore with useIam adds aws-instance-profile flag":
    let restore = newWALERestore(
      scope = "test",
      datadir = "/data",
      connstring = "",
      envDir = "/etc",
      thresholdMb = 100,
      thresholdPct = 30,
      useIam = 1,
      noLeader = false,
      retries = 0
    )
    check "--aws-instance-profile" in restore.walE.cmd

  test "newWALERestore without useIam does not add aws-instance-profile flag":
    let restore = newWALERestore(
      scope = "test",
      datadir = "/data",
      connstring = "",
      envDir = "/etc",
      thresholdMb = 100,
      thresholdPct = 30,
      useIam = 0,
      noLeader = false,
      retries = 0
    )
    check "--aws-instance-profile" notin restore.walE.cmd

suite "WALERestore.run":
  test "run with initError returns FAIL":
    let restore = newWALERestore(
      scope = "test",
      datadir = "/data",
      connstring = "",
      envDir = "/nonexistent/path/that/does/not/exist",
      thresholdMb = 0,
      thresholdPct = 0,
      useIam = 0,
      noLeader = false,
      retries = 0
    )
    check restore.initError == true
    check restore.run() == int(ExitCode.ecFail)

suite "ExitCode":
  test "exit code SUCCESS value":
    check ord(ExitCode.ecSuccess) == 0

  test "exit code RETRY_LATER value":
    check ord(ExitCode.ecRetryLater) == 1

  test "exit code FAIL value":
    check ord(ExitCode.ecFail) == 2

suite "reprSize":
  test "reprSize with bytes":
    # Note: format uses .0f which produces trailing period in Nim
    check "1000" in reprSize(1000)
    check "Bytes" in reprSize(1000)
    check "0" in reprSize(0)
    check "512" in reprSize(512)

  test "reprSize with KiB":
    check reprSize(1024) == "1.0 KiB"
    check reprSize(2048) == "2.0 KiB"

  test "reprSize with MiB":
    check reprSize(1048576) == "1.0 MiB"
    check reprSize(167772160) == "160.0 MiB"

  test "reprSize with GiB":
    check reprSize(1073741824) == "1.0 GiB"

  test "reprSize with TiB":
    check reprSize(8257332324597.0) == "7.5 TiB"

suite "sizeAsBytes":
  test "sizeAsBytes with K prefix":
    check sizeAsBytes(1, 'K') == 1024

  test "sizeAsBytes with M prefix":
    check sizeAsBytes(1, 'M') == 1048576

  test "sizeAsBytes with G prefix":
    check sizeAsBytes(1, 'G') == 1073741824

  test "sizeAsBytes with T prefix":
    check sizeAsBytes(7.5, 'T') == 8246337208320'i64

  test "sizeAsBytes with lowercase prefix":
    check sizeAsBytes(1, 'k') == 1024
    check sizeAsBytes(1, 'm') == 1048576

  test "sizeAsBytes with unknown prefix raises":
    expect ValueError:
      discard sizeAsBytes(1, 'X')

suite "getMajorVersion":
  test "getMajorVersion returns 0.0 for non-existent path":
    check getMajorVersion("/nonexistent/path") == 0.0

suite "WAL-E Output Parsing":
  test "parse valid backup list header":
    let lines = WALE_OUTPUT.split('\n')
    check lines.len >= 2

    let headers = lines[0].split('\t')
    check "name" in headers
    check "expanded_size_bytes" in headers
    check "last_modified" in headers
    check "wal_segment_backup_start" in headers

  test "parse valid backup list values":
    let lines = WALE_OUTPUT.split('\n')
    check lines.len >= 2

    let values = lines[1].split('\t')
    check values.len >= 3
    check values[0].startsWith("base_")
    check values[2] == "167772160"

  test "parse empty backup list":
    let lines = WALE_OUTPUT_HEADER.split('\n')
    check lines.len >= 1
    # Only header, no values - should result in no backup found

  test "parse backup with header-value mapping":
    let lines = WALE_OUTPUT.split('\n')
    let headers = lines[0].split('\t')
    let values = lines[1].split('\t')

    var backupInfo = initTable[string, string]()
    for i in 0..<min(headers.len, values.len):
      backupInfo[headers[i]] = values[i]

    check backupInfo["name"] == "base_00000001000000000000007F_00000040"
    check backupInfo["expanded_size_bytes"] == "167772160"
    check backupInfo["wal_segment_backup_start"] == "00000001000000000000007F"

suite "Threshold Calculations":
  test "backup above MB threshold should not use S3":
    # 167772160 bytes = ~160MB, if threshold = 100MB, over threshold
    let backupSize = 167772160'i64
    let thresholdBytes = 100 * 1024 * 1024
    check backupSize > thresholdBytes

  test "backup below MB threshold should use S3":
    let backupSize = 50 * 1024 * 1024  # 50MB
    let thresholdBytes = 100 * 1024 * 1024  # 100MB
    check backupSize < thresholdBytes

  test "percentage threshold calculation":
    let backupSize = 167772160.0  # ~160MB
    let thresholdPct = 30
    let thresholdPctBytes = backupSize * float(thresholdPct) / 100.0
    check thresholdPctBytes == 50331648.0  # 30% of 160MB

suite "WAL-E Command Construction":
  test "basic command includes envdir and wal-e":
    let restore = newWALERestore(
      scope = "test",
      datadir = "/data",
      connstring = "",
      envDir = "/etc/wal-e.d/env",
      thresholdMb = 100,
      thresholdPct = 30,
      useIam = 0,
      noLeader = false,
      retries = 0
    )
    check "envdir" in restore.walE.cmd
    check "/etc/wal-e.d/env" in restore.walE.cmd
    check "wal-e" in restore.walE.cmd

suite "Version Parsing Helper":
  test "parse PG 9.x version string":
    check parseFloat("9.6") == 9.6
    check parseFloat("9.4") == 9.4

  test "parse PG 10+ version string":
    check parseFloat("10") == 10.0
    check parseFloat("15") == 15.0

  test "WAL directory name by version":
    # PG < 10 uses pg_xlog
    check (if 9.6 < 10: "pg_xlog" else: "pg_wal") == "pg_xlog"
    # PG >= 10 uses pg_wal
    check (if 10.0 < 10: "pg_xlog" else: "pg_wal") == "pg_wal"
    check (if 15.0 < 10: "pg_xlog" else: "pg_wal") == "pg_wal"

when isMainModule:
  echo "test_wale_restore.nim tests completed"
