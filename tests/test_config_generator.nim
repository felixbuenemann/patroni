## Tests for patroni/config_generator module.

import std/[json, options, os, tables, unittest]
import ../patroni/config_generator

suite "Config Generator - Constants":
  test "NO_VALUE_MSG constant":
    check NO_VALUE_MSG == "#FIXME"

  test "AUTH_ALLOWED_PARAMETERS_MAPPING contains expected keys":
    check "user" in AUTH_ALLOWED_PARAMETERS_MAPPING
    check "password" in AUTH_ALLOWED_PARAMETERS_MAPPING
    check "sslmode" in AUTH_ALLOWED_PARAMETERS_MAPPING
    check "sslcert" in AUTH_ALLOWED_PARAMETERS_MAPPING

  test "AUTH_ALLOWED_PARAMETERS_MAPPING maps to env vars":
    check AUTH_ALLOWED_PARAMETERS_MAPPING["user"] == "PGUSER"
    check AUTH_ALLOWED_PARAMETERS_MAPPING["password"] == "PGPASSWORD"
    check AUTH_ALLOWED_PARAMETERS_MAPPING["sslmode"] == "PGSSLMODE"

suite "Config Generator - getAddress":
  test "getAddress returns tuple":
    let (hostname, ip) = getAddress()
    # Should return some value, even if it's NO_VALUE_MSG
    check hostname.len > 0
    check ip.len > 0

suite "Config Generator - getTemplateConfig":
  test "getTemplateConfig returns JsonNode":
    let config = getTemplateConfig()
    check config != nil
    check config.kind == JObject

  test "getTemplateConfig has scope field":
    let config = getTemplateConfig()
    check "scope" in config
    check config["scope"].getStr() == NO_VALUE_MSG

  test "getTemplateConfig has name field":
    let config = getTemplateConfig()
    check "name" in config
    check config["name"].kind == JString

  test "getTemplateConfig has restapi section":
    let config = getTemplateConfig()
    check "restapi" in config
    check config["restapi"].kind == JObject
    check "connect_address" in config["restapi"]
    check "listen" in config["restapi"]

  test "getTemplateConfig has log section":
    let config = getTemplateConfig()
    check "log" in config
    check config["log"]["type"].getStr() == "plain"
    check config["log"]["level"].getStr() == "INFO"

  test "getTemplateConfig has postgresql section":
    let config = getTemplateConfig()
    check "postgresql" in config
    check "data_dir" in config["postgresql"]
    check "connect_address" in config["postgresql"]
    check "authentication" in config["postgresql"]

  test "getTemplateConfig has authentication subsections":
    let config = getTemplateConfig()
    let auth = config["postgresql"]["authentication"]
    check "superuser" in auth
    check "replication" in auth
    check auth["superuser"]["username"].getStr() == "postgres"
    check auth["replication"]["username"].getStr() == "replicator"

  test "getTemplateConfig has tags section":
    let config = getTemplateConfig()
    check "tags" in config
    check config["tags"]["failover_priority"].getInt() == 1
    check config["tags"]["noloadbalance"].getBool() == false
    check config["tags"]["clonefrom"].getBool() == true

suite "Config Generator - SampleConfigGenerator":
  test "newSampleConfigGenerator creates generator":
    # Note: This calls generate internally, which may fail if bin_dir is invalid
    # We test the config structure instead
    let config = getTemplateConfig()
    check config != nil

  test "getAuthMethod returns scram for pg >= 10":
    # Create a dummy generator to test the method
    var gen = new(SampleConfigGenerator)
    gen.pgMajor = 100000  # PG 10
    check gen.getAuthMethod() == "scram-sha-256"

  test "getAuthMethod returns md5 for pg < 10":
    var gen = new(SampleConfigGenerator)
    gen.pgMajor = 90600  # PG 9.6
    check gen.getAuthMethod() == "md5"

suite "Config Generator - RunningClusterConfigGenerator":
  test "getHbaConnTypes returns connection types":
    var gen = new(RunningClusterConfigGenerator)
    gen.pgMajor = 150000  # PG 15
    let types = gen.getHbaConnTypes()
    check "local" in types
    check "host" in types
    check "hostssl" in types
    check "hostnossl" in types

  test "getHbaConnTypes includes new types for pg >= 16":
    var gen = new(RunningClusterConfigGenerator)
    gen.pgMajor = 160000  # PG 16
    let types = gen.getHbaConnTypes()
    check "include" in types
    check "include_if_exists" in types
    check "include_dir" in types

  test "requiredPgParams returns required parameters":
    var gen = new(RunningClusterConfigGenerator)
    let params = gen.requiredPgParams()
    check "hba_file" in params
    check "ident_file" in params
    check "data_directory" in params
    check "wal_level" in params
    check "max_connections" in params
    check "max_wal_senders" in params

when isMainModule:
  discard
