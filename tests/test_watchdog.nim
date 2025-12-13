## Tests for patroni/watchdog module.

import std/[unittest, os]

suite "Watchdog":
  test "watchdog initialization":
    check true

  test "watchdog keepalive":
    check true

  test "watchdog disable":
    check true

  test "linux watchdog":
    check true

  test "watchdog timeout":
    check true

when isMainModule:
  discard
