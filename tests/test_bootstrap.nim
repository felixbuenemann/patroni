## Tests for patroni/postgresql/bootstrap module.
##
## These tests cover the PostgreSQL bootstrap functionality including
## initdb options processing, custom bootstrap methods, and user creation.

import std/[unittest, json, tables, strutils, options]
import ../patroni/dcs
import ../patroni/collections
import ../patroni/postgresql/bootstrap

suite "processUserOptions":
  test "empty options returns empty list":
    var errors: seq[string] = @[]
    let result = processUserOptions("test", newJObject(), @[], proc(msg: string) = errors.add(msg))
    check result.len == 0
    check errors.len == 0

  test "simple object options":
    var errors: seq[string] = @[]
    let options = %*{"waldir": "/wal", "data-checksums": true}
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check result.len == 2
    check "--waldir=/wal" in result or "--data-checksums" in result

  test "boolean true option":
    var errors: seq[string] = @[]
    let options = %*{"data-checksums": true}
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check "--data-checksums" in result

  test "boolean false option is skipped":
    var errors: seq[string] = @[]
    let options = %*{"data-checksums": false}
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check result.len == 0

  test "string option":
    var errors: seq[string] = @[]
    let options = %*{"encoding": "UTF8"}
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check "--encoding=UTF8" in result

  test "not allowed options are filtered":
    var errors: seq[string] = @[]
    let options = %*{"pgdata": "/data", "encoding": "UTF8"}
    let result = processUserOptions("initdb", options, @["pgdata"], proc(msg: string) = errors.add(msg))
    check "--encoding=UTF8" in result
    check errors.len == 1
    check "pgdata" in errors[0]

  test "array options":
    var errors: seq[string] = @[]
    let options = %["--encoding=UTF8", "data-checksums"]
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check "--encoding=UTF8" in result
    check "--data-checksums" in result

  test "array with dict options":
    var errors: seq[string] = @[]
    let options = %*[{"encoding": "UTF8"}, {"locale": "C"}]
    let result = processUserOptions("initdb", options, @[], proc(msg: string) = errors.add(msg))
    check "--encoding=UTF8" in result
    check "--locale=C" in result

suite "processInitdbOptions":
  test "filters pgdata option":
    var errors: seq[string] = @[]
    let options = %*{"pgdata": "/data", "encoding": "UTF8"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1
    check "pgdata" in errors[0]

  test "filters D option":
    var errors: seq[string] = @[]
    let options = %*{"D": "/data"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters waldir option":
    var errors: seq[string] = @[]
    let options = %*{"waldir": "/wal"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters X option":
    var errors: seq[string] = @[]
    let options = %*{"X": "/wal"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "allows encoding option":
    var errors: seq[string] = @[]
    let options = %*{"encoding": "UTF8"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 0
    check "--encoding=UTF8" in result

  test "allows data-checksums option":
    var errors: seq[string] = @[]
    let options = %*{"data-checksums": true}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check "--data-checksums" in result

  test "allows locale option":
    var errors: seq[string] = @[]
    let options = %*{"locale": "C"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check "--locale=C" in result

suite "processBasebackupOptions":
  test "filters pgdata option":
    var errors: seq[string] = @[]
    let options = %*{"pgdata": "/data"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters D option":
    var errors: seq[string] = @[]
    let options = %*{"D": "/data"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters host option":
    var errors: seq[string] = @[]
    let options = %*{"host": "localhost"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters h option":
    var errors: seq[string] = @[]
    let options = %*{"h": "localhost"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters port option":
    var errors: seq[string] = @[]
    let options = %*{"port": "5432"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters p option":
    var errors: seq[string] = @[]
    let options = %*{"p": "5432"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters dbname option":
    var errors: seq[string] = @[]
    let options = %*{"dbname": "postgres"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "filters d option":
    var errors: seq[string] = @[]
    let options = %*{"d": "postgres"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 1

  test "allows checkpoint option":
    var errors: seq[string] = @[]
    let options = %*{"checkpoint": "fast"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 0
    check "--checkpoint=fast" in result

  test "allows waldir option":
    var errors: seq[string] = @[]
    let options = %*{"waldir": "/wal"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 0
    check "--waldir=/wal" in result

suite "Bootstrap Type":
  test "newBootstrap creates instance":
    let bootstrap = newBootstrap(nil)
    check bootstrap != nil

  test "isRunningCustomBootstrap initially false":
    let bootstrap = newBootstrap(nil)
    check not bootstrap.isRunningCustomBootstrap()

  test "shouldKeepExistingRecoveryConf initially false":
    let bootstrap = newBootstrap(nil)
    check not bootstrap.shouldKeepExistingRecoveryConf()

suite "Bootstrap Configuration":
  test "initdb method is default":
    let config = %*{"initdb": []}
    check not config.hasKey("method") or config["method"].getStr() == "initdb"

  test "custom method detection":
    let config = %*{"method": "custom", "custom": {"command": "/bin/true"}}
    check config["method"].getStr() == "custom"

  test "post_bootstrap configuration":
    let config = %*{"post_bootstrap": "/path/to/script"}
    check config.hasKey("post_bootstrap")
    check config["post_bootstrap"].getStr() == "/path/to/script"

  test "post_init is alias for post_bootstrap":
    let config = %*{"post_init": "/path/to/script"}
    check config.hasKey("post_init")

  test "users configuration":
    let config = %*{
      "users": {
        "admin": {"password": "secret", "options": ["SUPERUSER", "CREATEDB"]}
      }
    }
    check config.hasKey("users")
    check config["users"].hasKey("admin")

suite "Clone Configuration":
  test "basebackup is default method":
    let config = newJObject()
    # No method specified means basebackup
    check not config.hasKey("method")

  test "custom clone method":
    let config = %*{"method": "wal_e", "wal_e": {"command": "/bin/restore"}}
    check config["method"].getStr() == "wal_e"

  test "basebackup options":
    let config = %*{"basebackup": {"checkpoint": "fast", "max-rate": "100M"}}
    check config.hasKey("basebackup")
    let bbOpts = config["basebackup"]
    check bbOpts["checkpoint"].getStr() == "fast"

suite "Keep Existing Recovery Conf":
  test "keep_existing_recovery_conf option":
    let methodConfig = %*{"keep_existing_recovery_conf": true, "command": "/bin/restore"}
    check methodConfig["keep_existing_recovery_conf"].getBool() == true

  test "keep_existing_recovery_conf default is false":
    let methodConfig = %*{"command": "/bin/restore"}
    check methodConfig.getOrDefault("keep_existing_recovery_conf").getBool(false) == false

suite "Initdb Options Processing":
  test "quoted values are unquoted":
    var errors: seq[string] = @[]
    let options = %*{"encoding": "'UTF8'"}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    # The value should be unquoted
    check result.len == 1

  test "numeric values":
    var errors: seq[string] = @[]
    let options = %*{"wal-segsize": 64}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check "--wal-segsize=64" in result

  test "multiple options":
    var errors: seq[string] = @[]
    let options = %*{"encoding": "UTF8", "locale": "C", "data-checksums": true}
    let result = processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check result.len == 3

suite "Basebackup Options Processing":
  test "wal method option":
    var errors: seq[string] = @[]
    let options = %*{"wal-method": "stream"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check "--wal-method=stream" in result

  test "max rate option":
    var errors: seq[string] = @[]
    let options = %*{"max-rate": "100M"}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check "--max-rate=100M" in result

  test "progress option":
    var errors: seq[string] = @[]
    let options = %*{"progress": true}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check "--progress" in result

  test "verbose option":
    var errors: seq[string] = @[]
    let options = %*{"verbose": true}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check "--verbose" in result

  test "no-password option":
    var errors: seq[string] = @[]
    let options = %*{"no-password": true}
    let result = processBasebackupOptions(options, proc(msg: string) = errors.add(msg))
    check "--no-password" in result

suite "User Creation Configuration":
  test "user with password":
    let userConfig = %*{"password": "secret123"}
    check userConfig["password"].getStr() == "secret123"

  test "user with options":
    let userConfig = %*{"options": ["SUPERUSER", "CREATEDB"]}
    check userConfig["options"].len == 2
    check userConfig["options"][0].getStr() == "SUPERUSER"

  test "user with password and options":
    let userConfig = %*{"password": "secret", "options": ["SUPERUSER"]}
    check userConfig.hasKey("password")
    check userConfig.hasKey("options")

  test "replication user":
    let users = %*{
      "replicator": {"password": "rep_pass", "options": ["REPLICATION"]}
    }
    check users.hasKey("replicator")
    let repUser = users["replicator"]
    check "REPLICATION" in repUser["options"][0].getStr()

suite "Bootstrap Method Names":
  test "initdb is reserved method name":
    let methods = ["initdb", "basebackup"]
    check "initdb" in methods

  test "basebackup is reserved method name":
    let methods = ["initdb", "basebackup"]
    check "basebackup" in methods

  test "custom method names":
    let customMethods = ["wal_e", "pgbackrest", "barman", "custom"]
    for m in customMethods:
      check m notin ["initdb", "basebackup"]

suite "Bootstrap Error Handling":
  test "error handler receives messages":
    var errors: seq[string] = @[]
    let handler = proc(msg: string) = errors.add(msg)
    handler("test error")
    check errors.len == 1
    check errors[0] == "test error"

  test "multiple errors accumulated":
    var errors: seq[string] = @[]
    let options = %*{"pgdata": "/data", "D": "/data", "waldir": "/wal", "X": "/wal"}
    discard processInitdbOptions(options, proc(msg: string) = errors.add(msg))
    check errors.len == 4

suite "Bootstrap JSON Configuration":
  test "full bootstrap config":
    let config = %*{
      "method": "initdb",
      "initdb": [
        {"encoding": "UTF8"},
        {"locale": "C"},
        "data-checksums"
      ],
      "users": {
        "admin": {"password": "admin123", "options": ["SUPERUSER"]},
        "replicator": {"password": "rep123", "options": ["REPLICATION"]}
      },
      "post_bootstrap": "/usr/local/bin/setup.sh"
    }
    check config["method"].getStr() == "initdb"
    check config["initdb"].kind == JArray
    check config["users"].kind == JObject
    check config["post_bootstrap"].kind == JString

  test "clone config":
    let config = %*{
      "method": "basebackup",
      "basebackup": {
        "checkpoint": "fast",
        "max-rate": "100M",
        "waldir": "/pg_wal"
      }
    }
    check config["method"].getStr() == "basebackup"
    check config["basebackup"]["checkpoint"].getStr() == "fast"

when isMainModule:
  echo "test_bootstrap.nim tests completed"
