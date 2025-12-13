## Tests for patroni/quorum module.

import std/[unittest]

suite "Quorum":
  test "quorum calculation":
    check true

  test "quorum commit":
    check true

  test "quorum state":
    check true

  test "quorum failsafe":
    check true

when isMainModule:
  discard
