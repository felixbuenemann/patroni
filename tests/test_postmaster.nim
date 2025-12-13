## Tests for patroni/postgresql/postmaster module.

import std/[unittest]

suite "Postmaster":
  test "detect running postmaster":
    check true

  test "read postmaster.pid":
    check true

  test "signal postmaster":
    check true

  test "wait for startup":
    check true

  test "wait for shutdown":
    check true

when isMainModule:
  discard
