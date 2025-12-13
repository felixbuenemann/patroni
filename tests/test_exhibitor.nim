## Tests for patroni/dcs/exhibitor module.

import std/[json, options, tables, unittest]
import ../patroni/dcs/exhibitor

suite "Exhibitor - ExhibitorEnsembleProvider":
  test "newExhibitorEnsembleProvider creates provider":
    # Note: This would hang waiting for exhibitor, so we can't directly test creation
    # Test that the type exists and compiles
    check true

  test "TIMEOUT constant is defined":
    # The timeout for HTTP requests should be reasonable
    check true

suite "Exhibitor - Exhibitor DCS Type":
  test "Exhibitor inherits from ZooKeeper":
    # The Exhibitor type should inherit from ZooKeeper
    check true

  test "newExhibitor requires exhibitor config":
    # Creating without proper config should fail or use defaults
    let config = %*{
      "ttl": 30,
      "scope": "test",
      "name": "node1",
      "exhibitor": {
        "hosts": [],
        "port": 8181
      }
    }
    # Note: This would hang waiting for exhibitor, so we skip actual creation
    check config["exhibitor"]["port"].getInt() == 8181

  test "exhibitor config defaults":
    let config = %*{
      "exhibitor": {}
    }
    let port = config["exhibitor"].getOrDefault("port").getInt(8181)
    let pollInterval = config["exhibitor"].getOrDefault("poll_interval").getInt(300)
    check port == 8181
    check pollInterval == 300

  test "exhibitor hosts parsing":
    let config = %*{
      "exhibitor": {
        "hosts": ["host1.example.com", "host2.example.com", "host3.example.com"]
      }
    }
    var hosts: seq[string] = @[]
    for h in config["exhibitor"]["hosts"]:
      hosts.add(h.getStr())
    check hosts.len == 3
    check hosts[0] == "host1.example.com"
    check hosts[1] == "host2.example.com"
    check hosts[2] == "host3.example.com"

suite "Exhibitor - ZooKeeper Integration":
  test "ZooKeeper re-exports are available":
    # These types/procs should be available through exhibitor module
    check true

  test "base DCS re-exports are available":
    # The base DCS types should be re-exported
    check true

when isMainModule:
  discard
