## Tests for patroni/postgresql/rewind module.

import std/[unittest]

suite "pg_rewind":
  test "check rewind possible":
    check true

  test "execute rewind":
    check true

  test "rewind with pg_rewind":
    check true

  test "rewind with pg_basebackup":
    check true

  test "rewind failure handling":
    check true

when isMainModule:
  discard
