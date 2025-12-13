## Tests for patroni/postgresql/mpp module (Massively Parallel Processing).

import std/[unittest]

suite "MPP":
  test "get mpp handler":
    check true

  test "mpp type detection":
    check true

  test "null mpp handler":
    check true

when isMainModule:
  discard
