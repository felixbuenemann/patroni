## Tests for patroni/dcs/raft module.

import std/[json, options, tables, unittest]
import ../patroni/dcs/raft

suite "Raft - Error Types":
  test "RaftError is a DCSError":
    try:
      raise newException(RaftError, "test error")
    except DCSError:
      check true
    except:
      check false

suite "Raft - FailReason Enum":
  test "frSuccess value":
    check ord(frSuccess) == 0

  test "frRequestDenied value":
    check ord(frRequestDenied) == 1

  test "frTimeout value":
    check ord(frTimeout) == 2

  test "frNetworkError value":
    check ord(frNetworkError) == 3

  test "frNotLeader value":
    check ord(frNotLeader) == 4

suite "Raft - KVEntry":
  test "KVEntry can be created":
    var entry: KVEntry
    entry.value = "test value"
    entry.created = 100.0
    entry.updated = 200.0
    entry.index = 1
    check entry.value == "test value"
    check entry.created == 100.0
    check entry.updated == 200.0
    check entry.index == 1

  test "KVEntry with expire":
    var entry: KVEntry
    entry.value = "test"
    entry.expire = some(300.0)
    check entry.expire.isSome
    check entry.expire.get == 300.0

  test "KVEntry without expire":
    var entry: KVEntry
    entry.value = "test"
    entry.expire = none(float)
    check entry.expire.isNone

suite "Raft - KVStoreTTL":
  test "newKVStoreTTL creates store":
    let config = %*{
      "self_addr": "localhost:4001",
      "data_dir": "/tmp/raft_test"
    }
    let store = newKVStoreTTL(config)
    check store != nil

  test "newKVStoreTTL with partner addresses":
    let config = %*{
      "self_addr": "localhost:4001",
      "partner_addrs": ["localhost:4002", "localhost:4003"]
    }
    let store = newKVStoreTTL(config)
    check store != nil

  test "setRetryTimeout updates timeout":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    store.setRetryTimeout(30)
    check store.retryTimeout == 30

  test "set creates entry":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    check store.set("/test/key", "value") == true

  test "get returns set value":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key", "value")
    let result = store.get("/test/key")
    check result.isSome
    check result.get["/test/key"].value == "value"

  test "get returns none for missing key":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    let result = store.get("/nonexistent")
    check result.isNone

  test "get recursive returns multiple keys":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key1", "value1")
    discard store.set("/test/key2", "value2")
    discard store.set("/other/key", "other")
    let result = store.get("/test/", recursive = true)
    check result.isSome
    check result.get.len == 2

  test "delete removes key":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key", "value")
    check store.delete("/test/key") == true
    check store.get("/test/key").isNone

  test "delete recursive removes multiple keys":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key1", "value1")
    discard store.set("/test/key2", "value2")
    check store.delete("/test/", recursive = true) == true
    check store.get("/test/key1").isNone
    check store.get("/test/key2").isNone

  test "set with prevExist false succeeds for new key":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    var kwargs = initTable[string, string]()
    kwargs["prevExist"] = "false"
    check store.set("/new/key", "value", 0, kwargs) == true

  test "set with prevExist false fails for existing key":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/existing/key", "value1")
    var kwargs = initTable[string, string]()
    kwargs["prevExist"] = "false"
    check store.set("/existing/key", "value2", 0, kwargs) == false

  test "set with prevValue succeeds when value matches":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key", "oldvalue")
    var kwargs = initTable[string, string]()
    kwargs["prevValue"] = "oldvalue"
    check store.set("/test/key", "newvalue", 0, kwargs) == true

  test "set with prevValue fails when value doesn't match":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    discard store.set("/test/key", "oldvalue")
    var kwargs = initTable[string, string]()
    kwargs["prevValue"] = "wrongvalue"
    check store.set("/test/key", "newvalue", 0, kwargs) == false

  test "isLeader returns true when selfAddr set":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    check store.isLeader() == true

  test "isLeader returns false when selfAddr empty":
    let config = %*{"self_addr": ""}
    let store = newKVStoreTTL(config)
    check store.isLeader() == false

  test "destroy stops store":
    let config = %*{"self_addr": "localhost:4001"}
    let store = newKVStoreTTL(config)
    store.destroy()
    check store.running == false

suite "Raft - Raft DCS":
  test "newRaft creates Raft DCS":
    let config = %*{
      "raft": {"self_addr": "localhost:4001"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let raft = newRaft(config)
    check raft != nil

  test "setTtl updates TTL":
    let config = %*{
      "raft": {"self_addr": "localhost:4001"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let raft = newRaft(config)
    discard raft.setTtl(60)
    check raft.getTtl() == 60

  test "getTtl returns current TTL":
    let config = %*{
      "raft": {"self_addr": "localhost:4001"},
      "ttl": 45,
      "scope": "test",
      "name": "node1"
    }
    let raft = newRaft(config)
    check raft.getTtl() == 45

  test "setRetryTimeout updates timeout":
    let config = %*{
      "raft": {"self_addr": "localhost:4001"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let raft = newRaft(config)
    raft.setRetryTimeout(15)
    check true

when isMainModule:
  discard
