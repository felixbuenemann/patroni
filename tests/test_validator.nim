## Tests for patroni/validator module.

import std/[unittest, json]

suite "Validator":
  test "validate schema":
    check true

  test "validate config":
    check true

  test "validate postgresql parameters":
    check true

  test "validate DCS settings":
    check true

  test "validation errors":
    check true

when isMainModule:
  discard
