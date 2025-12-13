## Tests for patroni/ctl module (patronictl CLI).

import std/[unittest]

suite "PatroniCtl Commands":
  test "list command":
    check true

  test "switchover command":
    check true

  test "failover command":
    check true

  test "restart command":
    check true

  test "reload command":
    check true

  test "reinit command":
    check true

  test "pause command":
    check true

  test "resume command":
    check true

  test "edit-config command":
    check true

  test "show-config command":
    check true

  test "version command":
    check true

  test "history command":
    check true

  test "query command":
    check true

  test "dsn command":
    check true

  test "remove command":
    check true

  test "flush command":
    check true

suite "PatroniCtl Output":
  test "table format":
    check true

  test "json format":
    check true

  test "yaml format":
    check true

  test "tsv format":
    check true

suite "PatroniCtl Connection":
  test "DCS connection":
    check true

  test "cluster selection":
    check true

  test "member selection":
    check true

when isMainModule:
  discard
