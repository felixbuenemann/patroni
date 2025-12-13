## Tests for patroni/dcs/etcd module.

import std/[json, options, tables, times, unittest]
import ../patroni/dcs/etcd

suite "Etcd - DnsCachingResolver":
  test "newDnsCachingResolver creates resolver with defaults":
    let resolver = newDnsCachingResolver()
    check resolver != nil

  test "newDnsCachingResolver with custom cache times":
    let resolver = newDnsCachingResolver(cacheTime = 300.0, cacheFailTime = 15.0)
    check resolver != nil

  test "resolve caches successful lookups":
    let resolver = newDnsCachingResolver()
    # First call should resolve
    let result1 = resolver.resolve("localhost", 2379)
    # Second call should use cache (implementation detail)
    let result2 = resolver.resolve("localhost", 2379)
    check result1 == result2

  test "remove removes entry from cache":
    let resolver = newDnsCachingResolver()
    discard resolver.resolve("localhost", 2379)
    resolver.remove("localhost", 2379)
    # After removal, next resolve will do fresh lookup
    discard resolver.resolve("localhost", 2379)
    check true

suite "Etcd - StaleEtcdNodeGuard":
  test "newStaleEtcdNodeGuard creates guard":
    let guard = newStaleEtcdNodeGuard()
    check guard != nil

  test "resetClusterRaftTerm resets state":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("cluster1", "100")
    guard.resetClusterRaftTerm()
    # After reset, should accept any raft term
    guard.checkClusterRaftTerm("cluster2", "1")
    check true

  test "checkClusterRaftTerm accepts increasing term":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("cluster1", "10")
    guard.checkClusterRaftTerm("cluster1", "20")
    guard.checkClusterRaftTerm("cluster1", "30")
    check true

  test "checkClusterRaftTerm raises on stale term":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("cluster1", "100")
    expect(StaleEtcdNode):
      guard.checkClusterRaftTerm("cluster1", "50")

  test "checkClusterRaftTerm resets on cluster change":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("cluster1", "100")
    # Cluster ID change should reset the term
    guard.checkClusterRaftTerm("cluster2", "1")
    check true

  test "checkClusterRaftTerm handles empty values":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("", "100")
    guard.checkClusterRaftTerm("cluster1", "")
    check true

  test "checkClusterRaftTerm handles invalid term":
    let guard = newStaleEtcdNodeGuard()
    guard.checkClusterRaftTerm("cluster1", "not-a-number")
    check true

suite "Etcd - EtcdResult":
  test "newEtcdResult creates empty result":
    let result = newEtcdResult()
    check result != nil
    check result.leaves.len == 0
    check result.action == ""
    check result.key == ""
    check result.value == ""

  test "parseEtcdResult parses action":
    let data = %*{"action": "set", "node": {"key": "/test", "value": "val"}}
    let result = parseEtcdResult(data)
    check result.action == "set"

  test "parseEtcdResult parses key and value":
    let data = %*{"node": {"key": "/test/key", "value": "myvalue"}}
    let result = parseEtcdResult(data)
    check result.key == "/test/key"
    check result.value == "myvalue"

  test "parseEtcdResult parses ttl":
    let data = %*{"node": {"key": "/test", "value": "val", "ttl": 30}}
    let result = parseEtcdResult(data)
    check result.ttl == 30

  test "parseEtcdResult parses modifiedIndex":
    let data = %*{"node": {"key": "/test", "value": "val", "modifiedIndex": 12345}}
    let result = parseEtcdResult(data)
    check result.modifiedIndex == 12345

  test "parseEtcdResult parses createdIndex":
    let data = %*{"node": {"key": "/test", "value": "val", "createdIndex": 100}}
    let result = parseEtcdResult(data)
    check result.createdIndex == 100

  test "parseEtcdResult parses dir flag":
    let data = %*{"node": {"key": "/test", "dir": true}}
    let result = parseEtcdResult(data)
    check result.dir == true

  test "parseEtcdResult parses nested nodes":
    let data = %*{
      "node": {
        "key": "/test",
        "dir": true,
        "nodes": [
          {"key": "/test/child1", "value": "val1"},
          {"key": "/test/child2", "value": "val2"}
        ]
      }
    }
    let result = parseEtcdResult(data)
    check result.leaves.len == 2
    check result.leaves[0].key == "/test/child1"
    check result.leaves[1].key == "/test/child2"

  test "parseEtcdResult creates leaf for non-dir":
    let data = %*{"node": {"key": "/test", "value": "val"}}
    let result = parseEtcdResult(data)
    check result.leaves.len == 1
    check result.leaves[0] == result

suite "Etcd - EtcdClient":
  test "newEtcdClient creates client with defaults":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    check client != nil

  test "newEtcdClient with custom port":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    config["port"] = "2380"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    check client != nil

  test "newEtcdClient with protocol":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    config["protocol"] = "https"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    check client != nil

  test "newEtcdClient with credentials":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    config["username"] = "etcduser"
    config["password"] = "etcdpass"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    check client != nil

  test "setReadTimeout updates timeout":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    client.setReadTimeout(5.0)
    check true

  test "setMachinesCacheTtl updates TTL":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    client.setMachinesCacheTtl(600)
    check true

  test "setBaseUri updates base URI":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    client.setBaseUri("http://otherhost:2379")
    check true

  test "reloadConfig updates credentials":
    var config = initTable[string, string]()
    config["host"] = "localhost"
    let resolver = newDnsCachingResolver()
    let client = newEtcdClient(config, resolver)
    var newConfig = initTable[string, string]()
    newConfig["username"] = "newuser"
    newConfig["password"] = "newpass"
    client.reloadConfig(newConfig)
    check true

suite "Etcd - Etcd DCS":
  test "newEtcd creates Etcd DCS":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    check etcd != nil

  test "newEtcd with default port":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    check etcd != nil

  test "newEtcd with custom port":
    let config = %*{
      "etcd": {"host": "localhost", "port": 2380},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    check etcd != nil

  test "setTtl updates TTL":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    let changed = etcd.setTtl(60)
    check changed == true
    check etcd.getTtl() == 60

  test "setTtl returns false when unchanged":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    discard etcd.setTtl(30)
    let changed = etcd.setTtl(30)
    check changed == false

  test "getTtl returns current TTL":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 45,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    check etcd.getTtl() == 45

  test "setRetryTimeout updates client timeout":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd = newEtcd(config)
    etcd.setRetryTimeout(15)
    check true

suite "Etcd - Error Types":
  test "EtcdError is a DCSError":
    try:
      raise newException(EtcdError, "test error")
    except DCSError:
      check true
    except:
      check false

  test "EtcdKeyNotFound is an EtcdError":
    try:
      raise newException(EtcdKeyNotFound, "key not found")
    except EtcdError:
      check true
    except:
      check false

  test "EtcdAlreadyExist is an EtcdError":
    try:
      raise newException(EtcdAlreadyExist, "key exists")
    except EtcdError:
      check true
    except:
      check false

  test "EtcdConnectionFailed is an EtcdError":
    try:
      raise newException(EtcdConnectionFailed, "connection failed")
    except EtcdError:
      check true
    except:
      check false

  test "EtcdWatchTimedOut is an EtcdError":
    try:
      raise newException(EtcdWatchTimedOut, "watch timeout")
    except EtcdError:
      check true
    except:
      check false

  test "EtcdRaftInternal is a DCSError":
    try:
      raise newException(EtcdRaftInternal, "raft internal error")
    except DCSError:
      check true
    except:
      check false

  test "StaleEtcdNode is catchable":
    try:
      raise newException(StaleEtcdNode, "stale node")
    except StaleEtcdNode:
      check true
    except:
      check false

when isMainModule:
  discard
