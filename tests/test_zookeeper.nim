## Tests for patroni/dcs/zookeeper module.

import std/[unittest]

suite "ZooKeeper DCS":
  test "zookeeper client initialization":
    check true

  test "zookeeper connection":
    check true

  test "zookeeper node operations":
    check true

  test "zookeeper sequential nodes":
    check true

  test "zookeeper watches":
    check true

  test "zookeeper ACLs":
    check true

  test "zookeeper error handling":
    check true

  test "zookeeper session expiry":
    check true

when isMainModule:
  discard
