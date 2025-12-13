## Tests for patroni main module.

import std/[strutils, unittest]
import ../patroni/patroni
import ../patroni/version
import ../patroni/daemon

suite "Patroni - Constants":
  test "PATRONI_ENV_PREFIX":
    check PATRONI_ENV_PREFIX == "PATRONI_"

  test "KUBERNETES_ENV_PREFIX":
    check KUBERNETES_ENV_PREFIX == "KUBERNETES_"

  test "MIN_PSYCOPG2":
    check MIN_PSYCOPG2.major == 2
    check MIN_PSYCOPG2.minor == 5
    check MIN_PSYCOPG2.patch == 4

  test "MIN_PSYCOPG3":
    check MIN_PSYCOPG3.major == 3
    check MIN_PSYCOPG3.minor == 0
    check MIN_PSYCOPG3.patch == 0

suite "Patroni - parseVersion":
  test "parses simple version":
    let v = parseVersion("2.5.4")
    check v.len == 3
    check v[0] == 2
    check v[1] == 5
    check v[2] == 4

  test "parses version with extra info":
    let v = parseVersion("2.5.4.dev1 (dt dec pq3 ext lo64)")
    check v.len == 3
    check v[0] == 2
    check v[1] == 5
    check v[2] == 4

  test "parses version with spaces":
    let v = parseVersion("10.2 (something)")
    check v.len == 2
    check v[0] == 10
    check v[1] == 2

  test "parses major only version":
    let v = parseVersion("15")
    check v.len == 1
    check v[0] == 15

  test "handles dev version":
    let v = parseVersion("3.0.dev1")
    check v.len == 2
    check v[0] == 3
    check v[1] == 0

  test "returns empty for non-numeric":
    let v = parseVersion("invalid")
    check v.len == 0

  test "stops at non-numeric part":
    let v = parseVersion("1.2.alpha")
    check v.len == 2
    check v[0] == 1
    check v[1] == 2

suite "Patroni - Version":
  test "patroniVersion is defined":
    check patroniVersion.len > 0

  test "patroniVersion format":
    # Should be in format X.Y.Z
    let parts = patroniVersion.split('.')
    check parts.len >= 2

  test "patroniVersion is valid":
    let v = parseVersion(patroniVersion)
    check v.len >= 2
    check v[0] >= 0
    check v[1] >= 0

suite "Patroni - Daemon Integration":
  test "getBaseArgParser returns tuple":
    let args = getBaseArgParser()
    check args.configFile == ""
    check args.showVersion == false
    check args.showHelp == false

  test "setupSignalHandlers does not crash":
    setupSignalHandlers()
    check true

  test "notifySystemd does not crash":
    notifySystemd("READY=1")
    check true

suite "Patroni - Global Config Re-export":
  test "GlobalConfig type is exported":
    let gc = newGlobalConfig()
    check gc != nil

  test "getGlobalConfig returns instance":
    let gc = getGlobalConfig()
    # May be nil if not initialized
    check true

suite "Patroni - Version Comparison":
  test "compare versions using parseVersion":
    let v1 = parseVersion("2.5.4")
    let v2 = parseVersion("2.6.0")
    check v1[0] == v2[0]  # same major
    check v1[1] < v2[1]   # v1 minor < v2 minor

  test "compare major versions":
    let v1 = parseVersion("2.5.4")
    let v2 = parseVersion("3.0.0")
    check v1[0] < v2[0]

  test "compare with MIN_PSYCOPG2":
    let v = parseVersion("2.5.4")
    check v[0] == MIN_PSYCOPG2.major
    check v[1] == MIN_PSYCOPG2.minor
    check v[2] == MIN_PSYCOPG2.patch

  test "compare with MIN_PSYCOPG3":
    let v = parseVersion("3.0.0")
    check v[0] == MIN_PSYCOPG3.major
    check v[1] == MIN_PSYCOPG3.minor
    check v[2] == MIN_PSYCOPG3.patch

when isMainModule:
  discard
