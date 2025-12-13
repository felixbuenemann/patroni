## Tests for patroni/utils module.

import std/[unittest, os, options, strutils, times, tables, json]
import ../patroni/utils

suite "Parse Functions":
  test "parseBoolValue true values":
    check parseBoolValue("true") == true
    check parseBoolValue("True") == true
    check parseBoolValue("TRUE") == true
    check parseBoolValue("on") == true
    check parseBoolValue("On") == true
    check parseBoolValue("ON") == true
    check parseBoolValue("yes") == true
    check parseBoolValue("1") == true

  test "parseBoolValue false values":
    check parseBoolValue("false") == false
    check parseBoolValue("False") == false
    check parseBoolValue("FALSE") == false
    check parseBoolValue("off") == false
    check parseBoolValue("Off") == false
    check parseBoolValue("OFF") == false
    check parseBoolValue("no") == false
    check parseBoolValue("0") == false
    check parseBoolValue("") == false
    check parseBoolValue("random") == false

  test "parseIntValue basic":
    check parseIntValue("123").get() == 123
    check parseIntValue("-456").get() == -456
    check parseIntValue("0").get() == 0

  test "parseIntValue with units":
    check parseIntValue("1kB", "kB").get() == 1
    check parseIntValue("1MB", "kB").get() == 1024
    check parseIntValue("1GB", "kB").get() == 1048576

  test "parseIntValue invalid":
    check parseIntValue("abc").isNone
    check parseIntValue("").isNone

  test "parseRealValue basic":
    check parseRealValue("1.5").get() == 1.5
    check parseRealValue("-2.5").get() == -2.5
    check parseRealValue("0.0").get() == 0.0

  test "parseRealValue with units":
    check parseRealValue("1.5s", "ms").get() == 1500.0
    check parseRealValue("100ms", "ms").get() == 100.0

  test "parseRealValue invalid":
    check parseRealValue("abc").isNone
    check parseRealValue("").isNone

suite "Split Address":
  test "splitAddress host only":
    let (host, port) = splitAddress("localhost")
    check host == "localhost"
    check port == 0

  test "splitAddress host and port":
    let (host, port) = splitAddress("localhost:5432")
    check host == "localhost"
    check port == 5432

  test "splitAddress IPv4 and port":
    let (host, port) = splitAddress("127.0.0.1:5432")
    check host == "127.0.0.1"
    check port == 5432

  test "splitAddress IPv6 with port":
    let (host, port) = splitAddress("[::1]:5432")
    check host == "::1"
    check port == 5432

  test "splitAddress IPv6 without port":
    let (host, port) = splitAddress("[::1]")
    check host == "::1"
    check port == 0

suite "URI Builder":
  test "uri basic":
    check uri("http", "localhost", 8080) == "http://localhost:8080"

  test "uri postgresql":
    check uri("postgresql", "127.0.0.1", 5432) == "postgresql://127.0.0.1:5432"

suite "Deep Compare":
  test "deepCompare same tables":
    let t1 = {"a": "1", "b": "2"}.toTable
    let t2 = {"a": "1", "b": "2"}.toTable
    check deepCompare(t1, t2) == true

  test "deepCompare different tables":
    let t1 = {"a": "1"}.toTable
    let t2 = {"a": "2"}.toTable
    check deepCompare(t1, t2) == false

  test "deepCompare JSON objects":
    let j1 = %*{"a": 1, "b": 2}
    let j2 = %*{"a": 1, "b": 2}
    check deepCompare(j1, j2) == true

  test "deepCompare different JSON":
    let j1 = %*{"a": 1}
    let j2 = %*{"a": 2}
    check deepCompare(j1, j2) == false

suite "Polling Loop":
  test "polling_loop basic":
    var iterations: seq[float] = @[]
    for elapsed in pollingLoop(0.01, interval = 0.001):
      iterations.add(elapsed)
      if iterations.len >= 1:
        break
    check iterations.len >= 1
    check iterations[0] >= 0.0

suite "String Parsing":
  test "strtol basic":
    let (num, rest) = strtol("123")
    check num.isSome
    check num.get() == 123
    check rest == ""

  test "strtol with trailing":
    let (num, rest) = strtol("123abc")
    check num.isSome
    check num.get() == 123
    check rest == "abc"

  test "strtol invalid":
    let (num, rest) = strtol("abc")
    check num.isNone

  test "strtod basic":
    let (num, rest) = strtod("1.5")
    check num.isSome
    check num.get() == 1.5
    check rest == ""

  test "strtod with trailing":
    let (num, rest) = strtod("1.5abc")
    check num.isSome
    check num.get() == 1.5
    check rest == "abc"

suite "Utility Functions":
  test "tzutc returns UTC timezone":
    let tz = tzutc()
    check "UTC" in tz.name  # Can be "UTC" or "Etc/UTC"

  test "getHostname returns string":
    let hostname = getHostname()
    check hostname.len > 0

  test "isRunningAsRoot":
    # Just verify it doesn't crash
    let isRoot = isRunningAsRoot()
    check isRoot == true or isRoot == false

when isMainModule:
  echo "test_utils.nim tests completed"
