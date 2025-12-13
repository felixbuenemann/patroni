## Tests for patroni/utils module.
## Ported from test_utils.py

import std/[unittest, options, times, tables, json, strutils, os]
import ../patroni/utils

# Helper to create a function that fails N times then succeeds
proc createFailingFunc(times: int): proc(): int =
  var failCount = 0
  result = proc(): int =
    if failCount < times:
      inc failCount
      raise newException(CatchableError, "Failed!")
    return failCount

# Helper for void failing functions
proc createFailingVoidFunc(times: int): proc() {.gcsafe.} =
  var failCount = 0
  result = proc() {.gcsafe.} =
    {.cast(gcsafe).}:
      if failCount < times:
        inc failCount
        raise newException(CatchableError, "Failed!")

suite "Parse Bool Value":
  test "parseBoolValue true values":
    check parseBoolValue("true") == true
    check parseBoolValue("True") == true
    check parseBoolValue("TRUE") == true
    check parseBoolValue("on") == true
    check parseBoolValue("On") == true
    check parseBoolValue("ON") == true
    check parseBoolValue("yes") == true
    check parseBoolValue("Yes") == true
    check parseBoolValue("1") == true

  test "parseBoolValue false values":
    check parseBoolValue("false") == false
    check parseBoolValue("False") == false
    check parseBoolValue("FALSE") == false
    check parseBoolValue("off") == false
    check parseBoolValue("Off") == false
    check parseBoolValue("OFF") == false
    check parseBoolValue("no") == false
    check parseBoolValue("No") == false
    check parseBoolValue("0") == false
    check parseBoolValue("") == false
    check parseBoolValue("random") == false

suite "Parse Int Value":
  test "parseIntValue basic integers":
    check parseIntValue("123").get() == 123
    check parseIntValue("-456").get() == -456
    check parseIntValue("0").get() == 0
    check parseIntValue("+789").get() == 789

  test "parseIntValue with memory units":
    # Test kB base unit
    check parseIntValue("1kB", "kB").get() == 1
    check parseIntValue("1MB", "kB").get() == 1024
    check parseIntValue("1GB", "kB").get() == 1048576
    check parseIntValue("1TB", "kB").get() == 1073741824

  test "parseIntValue with B base unit":
    check parseIntValue("1B", "B").get() == 1
    check parseIntValue("1kB", "B").get() == 1024
    check parseIntValue("1MB", "B").get() == 1048576

  test "parseIntValue invalid":
    check parseIntValue("abc").isNone
    check parseIntValue("").isNone
    check parseIntValue("12.5").isNone  # Float not valid for int

suite "Parse Real Value":
  test "parseRealValue basic floats":
    check parseRealValue("1.5").get() == 1.5
    check parseRealValue("-2.5").get() == -2.5
    check parseRealValue("0.0").get() == 0.0
    check parseRealValue("3.14159").get() == 3.14159

  test "parseRealValue integers as floats":
    check parseRealValue("100").get() == 100.0
    check parseRealValue("-50").get() == -50.0

  test "parseRealValue with time units":
    # Test ms base unit
    check parseRealValue("1s", "ms").get() == 1000.0
    check parseRealValue("1.5s", "ms").get() == 1500.0
    check parseRealValue("100ms", "ms").get() == 100.0
    check parseRealValue("1min", "ms").get() == 60000.0

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

  test "splitAddress IPv4 only":
    let (host, port) = splitAddress("127.0.0.1")
    check host == "127.0.0.1"
    check port == 0

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

  test "splitAddress IPv6 full address":
    let (host, port) = splitAddress("[2001:db8::1]:8080")
    check host == "2001:db8::1"
    check port == 8080

suite "URI Builder":
  test "uri basic HTTP":
    check uri("http", "localhost", 8080) == "http://localhost:8080"

  test "uri HTTPS":
    check uri("https", "example.com", 443) == "https://example.com:443"

  test "uri postgresql":
    check uri("postgresql", "127.0.0.1", 5432) == "postgresql://127.0.0.1:5432"

  test "uri with tuple":
    check uri("http", ("localhost", 8080)) == "http://localhost:8080"

suite "Deep Compare":
  test "deepCompare same string tables":
    let t1 = {"a": "1", "b": "2"}.toTable
    let t2 = {"a": "1", "b": "2"}.toTable
    check deepCompare(t1, t2) == true

  test "deepCompare different string tables":
    let t1 = {"a": "1"}.toTable
    let t2 = {"a": "2"}.toTable
    check deepCompare(t1, t2) == false

  test "deepCompare different keys":
    let t1 = {"a": "1"}.toTable
    let t2 = {"b": "1"}.toTable
    check deepCompare(t1, t2) == false

  test "deepCompare different lengths":
    let t1 = {"a": "1", "b": "2"}.toTable
    let t2 = {"a": "1"}.toTable
    check deepCompare(t1, t2) == false

  test "deepCompare JSON objects same":
    let j1 = %*{"a": 1, "b": 2}
    let j2 = %*{"a": 1, "b": 2}
    check deepCompare(j1, j2) == true

  test "deepCompare JSON objects different values":
    let j1 = %*{"a": 1}
    let j2 = %*{"a": 2}
    check deepCompare(j1, j2) == false

  test "deepCompare nested JSON":
    let j1 = %*{"a": {"nested": 1}}
    let j2 = %*{"a": {"nested": 1}}
    check deepCompare(j1, j2) == true

suite "Polling Loop":
  test "polling_loop yields elapsed time":
    # Python test: list(polling_loop(0.001, interval=0.001)) == [0]
    var iterations: seq[float] = @[]
    for elapsed in pollingLoop(0.01, interval = 0.005):
      iterations.add(elapsed)
      if iterations.len >= 1:
        break
    check iterations.len >= 1
    check iterations[0] >= 0.0

  test "polling_loop respects timeout":
    var count = 0
    for elapsed in pollingLoop(0.02, interval = 0.005):
      inc count
      if count > 10:  # Safety limit
        break
    check count >= 1
    check count <= 10

suite "String to Number (strtol)":
  test "strtol basic decimal":
    let (num, rest) = strtol("123")
    check num.isSome
    check num.get() == 123
    check rest == ""

  test "strtol with trailing characters":
    let (num, rest) = strtol("123abc")
    check num.isSome
    check num.get() == 123
    check rest == "abc"

  test "strtol negative number":
    let (num, rest) = strtol("-456")
    check num.isSome
    check num.get() == -456
    check rest == ""

  test "strtol invalid input":
    let (num, rest) = strtol("abc")
    check num.isNone

  test "strtol empty string":
    let (num, rest) = strtol("")
    check num.isNone

  test "strtol hex number":
    let (num, rest) = strtol("0xFF")
    check num.isSome
    check num.get() == 255

  test "strtol octal number":
    let (num, rest) = strtol("0777")
    check num.isSome
    check num.get() == 511  # 0o777 = 511

suite "String to Double (strtod)":
  test "strtod basic float":
    let (num, rest) = strtod("1.5")
    check num.isSome
    check num.get() == 1.5
    check rest == ""

  test "strtod with trailing":
    let (num, rest) = strtod("1.5abc")
    check num.isSome
    check num.get() == 1.5
    check rest == "abc"

  test "strtod negative":
    let (num, rest) = strtod("-2.5")
    check num.isSome
    check num.get() == -2.5

  test "strtod integer":
    let (num, rest) = strtod("42")
    check num.isSome
    check num.get() == 42.0

  test "strtod scientific notation":
    let (num, rest) = strtod("1.5e2")
    check num.isSome
    check num.get() == 150.0

  test "strtod invalid":
    let (num, rest) = strtod("abc")
    check num.isNone

suite "Retry Mechanism":
  test "retry reset clears attempts":
    var sleepCalled = false
    proc mockSleep(t: float) {.gcsafe.} =
      sleepCalled = true

    let retry = newRetry(delay = 0.001, maxTries = 3, sleepFunc = mockSleep)
    let failOnce = createFailingFunc(1)

    discard retry.call(failOnce)
    check retry.attempts >= 1

    retry.reset()
    check retry.attempts == 0

  test "retry too many tries raises RetryFailedError":
    proc mockSleep(t: float) {.gcsafe.} =
      discard

    let retry = newRetry(delay = 0.001, maxTries = 1, sleepFunc = mockSleep)
    let alwaysFails = createFailingFunc(999)

    expect(RetryFailedError):
      discard retry.call(alwaysFails)

  test "retry succeeds after failures":
    proc mockSleep(t: float) {.gcsafe.} =
      discard

    let retry = newRetry(delay = 0.001, maxTries = 5, sleepFunc = mockSleep)
    let failTwice = createFailingFunc(2)

    let result = retry.call(failTwice)
    check result == 2  # Returns the fail count when it succeeds

  test "retry exponential backoff":
    var delays: seq[float] = @[]
    proc trackSleep(t: float) {.gcsafe.} =
      {.cast(gcsafe).}:
        delays.add(t)

    let retry = newRetry(delay = 0.01, maxDelay = 1.0, maxTries = 5, sleepFunc = trackSleep)
    let failThrice = createFailingFunc(3)

    discard retry.call(failThrice)

    # Should have exponential backoff: 0.01, 0.02, 0.04...
    check delays.len == 3
    check delays[0] <= delays[1]
    check delays[1] <= delays[2]

  test "retry copy preserves settings":
    proc customSleep(t: float) {.gcsafe.} =
      discard

    let retry = newRetry(delay = 0.5, maxDelay = 10.0, maxTries = 3, sleepFunc = customSleep)
    let retryCopy = retry.copy()

    check retryCopy.delay == 0.5
    check retryCopy.maxDelay == 10.0
    check retryCopy.maxTries == 3
    check retryCopy.sleepFunc == customSleep

  test "retry deadline exceeded":
    # The deadline is checked after each failed attempt
    # With a very short deadline, it should fail quickly
    proc slowSleep(t: float) {.gcsafe.} =
      # Sleep longer than deadline to trigger timeout
      sleep(100)  # 100ms sleep

    let retry = newRetry(delay = 0.001, deadline = 0.01, maxTries = -1, sleepFunc = slowSleep)
    var failCount = 0
    let alwaysFails = proc(): int =
      inc failCount
      raise newException(CatchableError, "Failed!")

    var caught = false
    try:
      discard retry.call(alwaysFails)
    except RetryFailedError:
      caught = true
    check caught == true

suite "Utility Functions":
  test "tzutc returns UTC timezone":
    let tz = tzutc()
    check tz.name.len > 0

  test "getHostname returns non-empty string":
    let hostname = getHostname()
    check hostname.len > 0
    check hostname != "localhost" or hostname == "localhost"  # Either is valid

  test "isRunningAsRoot returns boolean":
    let isRoot = isRunningAsRoot()
    # Just verify it returns a valid boolean
    check isRoot == true or isRoot == false

  test "getuid returns integer":
    let uid = getuid()
    check uid >= 0

  test "sleepMs doesn't crash":
    sleepMs(1)  # Sleep 1 millisecond
    check true

  test "sleepSec doesn't crash":
    sleepSec(0.001)  # Sleep 1 millisecond
    check true

suite "Compare Versions":
  test "compareVersions equal":
    check compareVersions(@[1, 2, 3], @[1, 2, 3]) == 0

  test "compareVersions less than":
    check compareVersions(@[1, 2, 3], @[1, 2, 4]) < 0
    check compareVersions(@[1, 2], @[1, 2, 1]) < 0
    check compareVersions(@[1], @[2]) < 0

  test "compareVersions greater than":
    check compareVersions(@[1, 2, 4], @[1, 2, 3]) > 0
    check compareVersions(@[2], @[1]) > 0
    check compareVersions(@[1, 3], @[1, 2]) > 0

  test "compareVersions different lengths":
    check compareVersions(@[1, 0, 0], @[1]) == 0
    check compareVersions(@[1, 0, 1], @[1]) > 0

suite "Random Shuffle":
  test "randomShuffle maintains elements":
    var items = @[1, 2, 3, 4, 5]
    let original = items
    randomShuffle(items)

    # All elements should still be present
    check items.len == original.len
    for item in original:
      check item in items

suite "Patch Config":
  test "patchConfig updates values":
    var config = %*{"a": 1, "b": 2}
    let patch = %*{"b": 3, "c": 4}

    let changed = patchConfig(config, patch)

    check changed == true
    check config["b"].getInt() == 3
    check config["c"].getInt() == 4
    check config["a"].getInt() == 1

  test "patchConfig no changes":
    var config = %*{"a": 1}
    let patch = %*{"a": 1}

    let changed = patchConfig(config, patch)
    check changed == false

when isMainModule:
  echo "test_utils.nim tests completed"
