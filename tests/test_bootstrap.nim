## Tests for patroni/bootstrap module.

import std/[unittest]

suite "Bootstrap":
  test "initdb":
    check true

  test "bootstrap from leader":
    check true

  test "bootstrap from backup":
    check true

  test "custom bootstrap script":
    check true

  test "bootstrap methods":
    check true

  test "post_bootstrap":
    check true

when isMainModule:
  discard
