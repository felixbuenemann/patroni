## Tests for patroni/utils module.

import std/[unittest, os, options, strutils, times]
import ../patroni/utils
import ../patroni/exceptions

suite "Utils":
  test "polling_loop basic":
    var iterations: seq[float] = @[]
    for elapsed in pollingLoop(0.001, interval = 0.001):
      iterations.add(elapsed)
      if iterations.len >= 1:
        break
    check iterations.len >= 1
    check iterations[0] >= 0.0

  test "polling_loop with timeout":
    var count = 0
    for elapsed in pollingLoop(0.01, interval = 0.001):
      inc count
      if count > 100:
        break  # Safety limit
    check count > 0

  test "unquote plain value":
    check unquote("value") == "value"

  test "unquote value with spaces":
    check unquote("value with spaces") == "value with spaces"

  test "unquote double quoted value":
    check unquote("\"double quoted value\"") == "double quoted value"

  test "unquote single quoted value":
    check unquote("'single quoted value'") == "single quoted value"

  test "unquote value with embedded double quotes":
    check unquote("value \"with\" double quotes") == "value \"with\" double quotes"

  test "unquote value starting with double quotes but not ending":
    check unquote("\"value starting with\" double quotes") == "\"value starting with\" double quotes"

  test "unquote value starting with single quotes but not ending":
    check unquote("'value starting with' single quotes") == "'value starting with' single quotes"

  test "unquote value with embedded single quote":
    check unquote("value with a ' single quote") == "value with a ' single quote"

  test "unquote complex single quoted value":
    check unquote("'value with a '\"'\"' single quote'") == "value with a ' single quote"

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

suite "Compare Values":
  test "compareValues integers":
    check compareValues("100", "100") == true
    check compareValues("100", "200") == false

  test "compareValues with units":
    check compareValues("1GB", "1048576kB") == true
    check compareValues("1024MB", "1GB") == true

  test "compareValues booleans":
    check compareValues("on", "true") == true
    check compareValues("off", "false") == true
    check compareValues("yes", "on") == true

  test "compareValues strings":
    check compareValues("foo", "foo") == true
    check compareValues("foo", "bar") == false

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

suite "Retry":
  var sleepCalled: int = 0

  proc mockSleep(t: float) =
    inc sleepCalled

  test "Retry success on first try":
    sleepCalled = 0
    var retry = newRetry(delay = 0.0, maxTries = 3, sleepFunc = mockSleep)
    var attempts = 0

    proc successFunc(): bool =
      inc attempts
      return true

    let result = retry.call(successFunc)
    check result == true
    check attempts == 1

  test "Retry success after failures":
    sleepCalled = 0
    var retry = newRetry(delay = 0.001, maxTries = 5, sleepFunc = mockSleep)
    var attempts = 0

    proc failThenSucceed(): bool =
      inc attempts
      if attempts < 3:
        raise newException(PatroniException, "Failed!")
      return true

    let result = retry.call(failThenSucceed)
    check result == true
    check attempts == 3

  test "Retry max tries exceeded":
    sleepCalled = 0
    var retry = newRetry(delay = 0.0, maxTries = 2, sleepFunc = mockSleep)
    var attempts = 0

    proc alwaysFail(): bool =
      inc attempts
      raise newException(PatroniException, "Failed!")

    expect RetryFailedError:
      discard retry.call(alwaysFail)

    check attempts == 2

  test "Retry reset":
    var retry = newRetry(delay = 0.0, maxTries = 2)
    check retry.attempts == 0
    retry.attempts = 5
    retry.reset()
    check retry.attempts == 0

  test "Retry copy":
    var retry = newRetry(delay = 1.0, maxTries = 5, maxDelay = 10.0, sleepFunc = mockSleep)
    var retryCopy = retry.copy()
    check retryCopy.delay == retry.delay
    check retryCopy.maxTries == retry.maxTries
    check retryCopy.maxDelay == retry.maxDelay

suite "Deep Compare":
  test "deepCompare same dicts":
    check deepCompare({"a": "1", "b": "2"}, {"a": "1", "b": "2"}) == true

  test "deepCompare different dicts":
    check deepCompare({"a": "1"}, {"a": "2"}) == false

  test "deepCompare subset":
    check deepCompare({"a": "1"}, {"a": "1", "b": "2"}) == false

when isMainModule:
  # Run all test suites
  discard
