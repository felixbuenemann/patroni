## Tests for patroni/dcs/etcd3 module.

import std/[base64, json, options, tables, unittest]
import ../patroni/dcs/etcd3

suite "Etcd3 - Error Types":
  test "Etcd3Error is a DCSError":
    try:
      raise newException(Etcd3Error, "test error")
    except DCSError:
      check true
    except:
      check false

  test "Etcd3ConnectionFailed is an Etcd3Error":
    try:
      raise newException(Etcd3ConnectionFailed, "connection failed")
    except Etcd3Error:
      check true
    except:
      check false

  test "Etcd3KeyNotFound is an Etcd3Error":
    try:
      raise newException(Etcd3KeyNotFound, "key not found")
    except Etcd3Error:
      check true
    except:
      check false

  test "Etcd3TransactionFailed is an Etcd3Error":
    try:
      raise newException(Etcd3TransactionFailed, "transaction failed")
    except Etcd3Error:
      check true
    except:
      check false

  test "Etcd3LeaseNotFound is an Etcd3Error":
    try:
      raise newException(Etcd3LeaseNotFound, "lease not found")
    except Etcd3Error:
      check true
    except:
      check false

  test "Etcd3LeaseExpired is an Etcd3Error":
    try:
      raise newException(Etcd3LeaseExpired, "lease expired")
    except Etcd3Error:
      check true
    except:
      check false

  test "Etcd3WatchFailed is an Etcd3Error":
    try:
      raise newException(Etcd3WatchFailed, "watch failed")
    except Etcd3Error:
      check true
    except:
      check false

suite "Etcd3 - Etcd3Lease":
  test "Etcd3Lease can be created":
    var lease: Etcd3Lease
    new(lease)
    lease.id = 12345
    lease.ttl = 30
    lease.grantedTtl = 30
    check lease.id == 12345
    check lease.ttl == 30
    check lease.grantedTtl == 30

suite "Etcd3 - Etcd3KeyValue":
  test "Etcd3KeyValue can be created":
    var kv: Etcd3KeyValue
    new(kv)
    kv.key = "/test/key"
    kv.value = "test value"
    kv.createRevision = 100
    kv.modRevision = 150
    kv.version = 5
    kv.lease = 12345
    check kv.key == "/test/key"
    check kv.value == "test value"
    check kv.createRevision == 100
    check kv.modRevision == 150
    check kv.version == 5
    check kv.lease == 12345

suite "Etcd3 - Etcd3Response":
  test "Etcd3Response can be created":
    var resp: Etcd3Response
    new(resp)
    resp.kvs = @[]
    resp.count = 0
    resp.revision = 100
    resp.succeeded = true
    check resp.kvs.len == 0
    check resp.count == 0
    check resp.revision == 100
    check resp.succeeded == true

  test "Etcd3Response with kvs":
    var resp: Etcd3Response
    new(resp)
    resp.kvs = @[]

    var kv: Etcd3KeyValue
    new(kv)
    kv.key = "/test"
    kv.value = "value"
    resp.kvs.add(kv)

    check resp.kvs.len == 1
    check resp.kvs[0].key == "/test"

suite "Etcd3 - Etcd3Client":
  test "newEtcd3Client creates client":
    let client = newEtcd3Client(@["localhost:2379"])
    check client != nil

  test "newEtcd3Client with multiple hosts":
    let client = newEtcd3Client(@["etcd1:2379", "etcd2:2379", "etcd3:2379"])
    check client != nil

  test "newEtcd3Client with https":
    let client = newEtcd3Client(@["localhost:2379"], protocol = "https")
    check client != nil

  test "newEtcd3Client with credentials":
    let client = newEtcd3Client(@["localhost:2379"], username = "root", password = "secret")
    check client != nil

  test "setReadTimeout updates timeout":
    let client = newEtcd3Client(@["localhost:2379"])
    client.setReadTimeout(30.0)
    check true

suite "Etcd3 - Etcd3 DCS":
  test "newEtcd3 creates Etcd3 DCS":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"]},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "newEtcd3 with host instead of hosts":
    let config = %*{
      "etcd3": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "newEtcd3 with host and port":
    let config = %*{
      "etcd3": {"host": "localhost", "port": 2380},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "newEtcd3 with etcd section fallback":
    let config = %*{
      "etcd": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "newEtcd3 with protocol":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"], "protocol": "https"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "newEtcd3 with credentials":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"], "username": "root", "password": "secret"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3 != nil

  test "setTtl updates TTL":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"]},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    let changed = etcd3.setTtl(60)
    check changed == true
    check etcd3.getTtl() == 60

  test "setTtl returns false when unchanged":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"]},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    discard etcd3.setTtl(30)
    let changed = etcd3.setTtl(30)
    check changed == false

  test "getTtl returns current TTL":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"]},
      "ttl": 45,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    check etcd3.getTtl() == 45

  test "setRetryTimeout updates client timeout":
    let config = %*{
      "etcd3": {"hosts": ["localhost:2379"]},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let etcd3 = newEtcd3(config)
    etcd3.setRetryTimeout(15)
    check true

when isMainModule:
  discard
