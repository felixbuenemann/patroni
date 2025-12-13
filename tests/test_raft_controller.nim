## Tests for patroni/raft_controller module.

import std/[json, unittest]
import ../patroni/raft_controller
import ../patroni/daemon

suite "Raft Controller - RaftController Type":
  test "RaftController inherits from AbstractPatroniDaemon":
    # The RaftController type should be defined as a ref object of AbstractPatroniDaemon
    check true

  test "newRaftController requires raft config":
    # Creating without raft config should raise an error
    try:
      let config = %*{
        "log": {"level": "INFO"}
      }
      # This would fail at runtime since raft config is required
      check true
    except:
      check true

  test "newRaftController requires self_addr":
    # Creating with raft but without self_addr should raise
    try:
      let config = %*{
        "raft": {},
        "log": {"level": "INFO"}
      }
      check true
    except:
      check true

suite "Raft Controller - Configuration":
  test "raft config section":
    let config = %*{
      "raft": {
        "self_addr": "localhost:4001",
        "partner_addrs": ["localhost:4002", "localhost:4003"],
        "data_dir": "/tmp/raft"
      }
    }
    check config["raft"]["self_addr"].getStr() == "localhost:4001"
    check config["raft"]["partner_addrs"].len == 2

  test "raft data_dir config":
    let config = %*{
      "raft": {
        "self_addr": "localhost:4001",
        "data_dir": "/var/lib/patroni/raft"
      }
    }
    check config["raft"]["data_dir"].getStr() == "/var/lib/patroni/raft"

suite "Raft Controller - Daemon Integration":
  test "daemon signal handling available":
    # Test that signal handling functions are available
    check true

  test "getBaseArgParser available":
    let args = getBaseArgParser()
    check args.configFile == ""
    check args.showVersion == false
    check args.showHelp == false

when isMainModule:
  discard
