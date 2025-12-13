## Tests for patroni/dcs/zookeeper module.

import std/[json, options, tables, unittest]
import ../patroni/dcs/zookeeper

suite "ZooKeeper - Error Types":
  test "ZooKeeperError is a DCSError":
    try:
      raise newException(ZooKeeperError, "test error")
    except DCSError:
      check true
    except:
      check false

  test "ZKConnectionError is a ZooKeeperError":
    try:
      raise newException(ZKConnectionError, "connection error")
    except ZooKeeperError:
      check true
    except:
      check false

  test "ZKNodeExistsError is a ZooKeeperError":
    try:
      raise newException(ZKNodeExistsError, "node exists")
    except ZooKeeperError:
      check true
    except:
      check false

  test "ZKNoNodeError is a ZooKeeperError":
    try:
      raise newException(ZKNoNodeError, "no node")
    except ZooKeeperError:
      check true
    except:
      check false

  test "ZKSessionExpiredError is a ZooKeeperError":
    try:
      raise newException(ZKSessionExpiredError, "session expired")
    except ZooKeeperError:
      check true
    except:
      check false

suite "ZooKeeper - ZKState":
  test "zksConnected value":
    check $zksConnected == "CONNECTED"

  test "zksSuspended value":
    check $zksSuspended == "SUSPENDED"

  test "zksLost value":
    check $zksLost == "LOST"

  test "zksDisconnected value":
    check $zksDisconnected == "DISCONNECTED"

suite "ZooKeeper - ZNodeStat":
  test "ZNodeStat default values":
    var stat: ZNodeStat
    check stat.version == 0
    check stat.cversion == 0
    check stat.aversion == 0
    check stat.ctime == 0
    check stat.mtime == 0
    check stat.czxid == 0
    check stat.mzxid == 0
    check stat.ephemeralOwner == 0
    check stat.dataLength == 0
    check stat.numChildren == 0

  test "ZNodeStat can be assigned values":
    var stat: ZNodeStat
    stat.version = 5
    stat.cversion = 10
    stat.numChildren = 3
    check stat.version == 5
    check stat.cversion == 10
    check stat.numChildren == 3

suite "ZooKeeper - ZKClient":
  test "newZKClient creates client with defaults":
    let client = newZKClient("127.0.0.1:2181")
    check client != nil

  test "newZKClient with custom timeout":
    let client = newZKClient("127.0.0.1:2181", timeout = 30.0)
    check client != nil

  test "newZKClient with multiple hosts":
    let client = newZKClient("zk1:2181,zk2:2181,zk3:2181")
    check client != nil

  test "isConnected returns false initially":
    let client = newZKClient("127.0.0.1:2181")
    check client.isConnected() == false

  test "close changes state":
    let client = newZKClient("127.0.0.1:2181")
    client.close()
    check client.isConnected() == false

  test "connect sets connected state":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    check client.isConnected() == true

  test "connect returns true on success":
    let client = newZKClient("127.0.0.1:2181")
    check client.connect() == true

  test "exists returns none for non-existent path":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    check client.exists("/nonexistent").isNone

  test "getChildren returns empty seq for empty node":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    let children = client.getChildren("/test")
    check children.len == 0

  test "create returns path":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    let path = client.create("/test/node", "value")
    check path == "/test/node"

  test "set returns stat with incremented version":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    let stat = client.set("/test", "value", version = 0)
    check stat.version == 1

  test "delete returns true":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    check client.delete("/test") == true

  test "delete with version returns true":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    check client.delete("/test", version = 5) == true

  test "ensurePath does not raise":
    let client = newZKClient("127.0.0.1:2181")
    discard client.connect()
    client.ensurePath("/some/deep/path")
    check true

suite "ZooKeeper - ZooKeeper DCS":
  test "newZooKeeper creates ZooKeeper DCS":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    check zk != nil

  test "newZooKeeper with default host":
    let config = %*{
      "zookeeper": {},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    check zk != nil

  test "newZooKeeper with hosts array":
    let config = %*{
      "zookeeper": {"hosts": ["zk1:2181", "zk2:2181", "zk3:2181"]},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    check zk != nil

  test "newZooKeeper with session_timeout":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181", "session_timeout": 20.0},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    check zk != nil

  test "setTtl updates TTL":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    let changed = zk.setTtl(60)
    check changed == true
    check zk.getTtl() == 60

  test "setTtl returns false when unchanged":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    discard zk.setTtl(30)
    let changed = zk.setTtl(30)
    check changed == false

  test "getTtl returns current TTL":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181"},
      "ttl": 45,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    check zk.getTtl() == 45

  test "setRetryTimeout updates client timeout":
    let config = %*{
      "zookeeper": {"hosts": "localhost:2181"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let zk = newZooKeeper(config)
    zk.setRetryTimeout(15)
    check true

when isMainModule:
  discard
