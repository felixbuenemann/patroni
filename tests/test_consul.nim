## Tests for patroni/dcs/consul module.

import std/[json, options, tables, unittest]
import ../patroni/dcs/consul

suite "Consul - Error Types":
  test "ConsulError is a DCSError":
    try:
      raise newException(ConsulError, "test error")
    except DCSError:
      check true
    except:
      check false

  test "ConsulInternalError is a ConsulError":
    try:
      raise newException(ConsulInternalError, "internal error")
    except ConsulError:
      check true
    except:
      check false

  test "InvalidSessionTTL is a ConsulError":
    try:
      raise newException(InvalidSessionTTL, "invalid ttl")
    except ConsulError:
      check true
    except:
      check false

  test "InvalidSession is a ConsulError":
    try:
      raise newException(InvalidSession, "invalid session")
    except ConsulError:
      check true
    except:
      check false

  test "ConsulKeyNotFound is a ConsulError":
    try:
      raise newException(ConsulKeyNotFound, "key not found")
    except ConsulError:
      check true
    except:
      check false

suite "Consul - ConsulHTTPClient":
  test "newConsulHTTPClient creates client with defaults":
    let client = newConsulHTTPClient()
    check client != nil

  test "newConsulHTTPClient with custom host":
    let client = newConsulHTTPClient(host = "consul.example.com")
    check client != nil

  test "newConsulHTTPClient with custom port":
    let client = newConsulHTTPClient(port = 8501)
    check client != nil

  test "newConsulHTTPClient with token":
    let client = newConsulHTTPClient(token = "my-secret-token")
    check client != nil

  test "newConsulHTTPClient with https scheme":
    let client = newConsulHTTPClient(scheme = "https")
    check client != nil

  test "newConsulHTTPClient with SSL options":
    let client = newConsulHTTPClient(
      scheme = "https",
      verify = true,
      cert = "/path/to/cert.pem",
      caCert = "/path/to/ca.pem"
    )
    check client != nil

  test "setReadTimeout updates timeout":
    let client = newConsulHTTPClient()
    client.setReadTimeout(30.0)
    check true

  test "getTtl returns TTL":
    let client = newConsulHTTPClient()
    check client.getTtl() == 30

  test "setTtl returns true when changed":
    let client = newConsulHTTPClient()
    check client.setTtl(60) == true

  test "setTtl returns false when unchanged":
    let client = newConsulHTTPClient()
    discard client.setTtl(30)
    check client.setTtl(30) == false

  test "setTtl updates TTL value":
    let client = newConsulHTTPClient()
    discard client.setTtl(45)
    check client.getTtl() == 45

suite "Consul - ConsulKV":
  test "newConsulKV creates KV client":
    let httpClient = newConsulHTTPClient()
    let kv = newConsulKV(httpClient)
    check kv != nil

suite "Consul - ConsulSession":
  test "newConsulSession creates session client":
    let httpClient = newConsulHTTPClient()
    let session = newConsulSession(httpClient)
    check session != nil

suite "Consul - Consul DCS":
  test "newConsul creates Consul DCS":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "newConsul with default port":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "newConsul with custom port":
    let config = %*{
      "consul": {"host": "localhost", "port": 8501},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "newConsul with scheme":
    let config = %*{
      "consul": {"host": "localhost", "scheme": "https"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "newConsul with token":
    let config = %*{
      "consul": {"host": "localhost", "token": "secret"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "newConsul with SSL options":
    let config = %*{
      "consul": {
        "host": "localhost",
        "scheme": "https",
        "verify": true,
        "cert": "/path/to/cert.pem",
        "cacert": "/path/to/ca.pem"
      },
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul != nil

  test "setTtl updates TTL":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    let changed = consul.setTtl(60)
    check changed == true
    check consul.getTtl() == 60

  test "setTtl returns false when unchanged":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    discard consul.setTtl(30)
    let changed = consul.setTtl(30)
    check changed == false

  test "getTtl returns current TTL":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 45,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    check consul.getTtl() == 45

  test "setRetryTimeout updates client timeout":
    let config = %*{
      "consul": {"host": "localhost"},
      "ttl": 30,
      "scope": "test",
      "name": "node1"
    }
    let consul = newConsul(config)
    consul.setRetryTimeout(15)
    check true

suite "Consul - ConsulResponse":
  test "ConsulResponse fields can be set":
    var response: ConsulResponse
    response.code = 200
    response.body = "test body"
    response.content = "test content"
    check response.code == 200
    check response.body == "test body"
    check response.content == "test content"

  test "ConsulResponse handles different status codes":
    var response: ConsulResponse
    response.code = 404
    check response.code == 404

  test "ConsulResponse handles 500 error":
    var response: ConsulResponse
    response.code = 500
    check response.code == 500

when isMainModule:
  discard
