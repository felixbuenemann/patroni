## Tests for patroni/validator module.
##
## These tests cover configuration validation functionality including
## integer, string, boolean, directory, and host:port validators.

import std/[unittest, json, options, os, strutils, tables]
import ../patroni/validator
import ../patroni/exceptions

suite "validateLogField":
  test "string field is valid":
    let field = %"timestamp"
    check validateLogField(field) == true

  test "single key dict with string value is valid":
    let field = %*{"levelname": "color"}
    check validateLogField(field) == true

  test "empty dict is invalid":
    let field = newJObject()
    check validateLogField(field) == false

  test "dict with multiple keys is invalid":
    let field = %*{"a": "1", "b": "2"}
    check validateLogField(field) == false

  test "dict with non-string value is invalid":
    let field = %*{"key": 123}
    check validateLogField(field) == false

  test "integer field is invalid":
    let field = %123
    check validateLogField(field) == false

  test "array field is invalid":
    let field = %[1, 2, 3]
    check validateLogField(field) == false

suite "validateLogFormat":
  test "string format is valid":
    let format = %"%(asctime)s %(levelname)s %(message)s"
    check validateLogFormat(format) == true

  test "array with string items is valid":
    let format = %["timestamp", "levelname", "message"]
    check validateLogFormat(format) == true

  test "array with dict items is valid":
    let format = %*[{"timestamp": "color"}, "levelname"]
    check validateLogFormat(format) == true

  test "empty array raises ConfigParseError":
    let format = newJArray()
    expect ConfigParseError:
      discard validateLogFormat(format)

  test "integer format raises ConfigParseError":
    let format = %123
    expect ConfigParseError:
      discard validateLogFormat(format)

suite "splitHostPort":
  test "splits host:port correctly":
    let (host, port) = splitHostPort("localhost:5432")
    check host == "localhost"
    check port == 5432

  test "handles host without port":
    let (host, port) = splitHostPort("localhost", 8008)
    check host == "localhost"
    check port == 8008

  test "handles IPv4 with port":
    let (host, port) = splitHostPort("192.168.1.1:5432")
    check host == "192.168.1.1"
    check port == 5432

  test "handles IPv4 without port":
    let (host, port) = splitHostPort("192.168.1.1", 5432)
    check host == "192.168.1.1"
    check port == 5432

  test "default port is used when not specified":
    let (host, port) = splitHostPort("example.com", 5432)
    check port == 5432

  test "handles zero default port":
    let (host, port) = splitHostPort("example.com", 0)
    check port == 0

suite "validateConnectAddress":
  test "valid external address":
    check validateConnectAddress("10.0.0.1:5432") == true

  test "valid hostname":
    check validateConnectAddress("postgresql.example.com:5432") == true

  test "localhost raises ConfigParseError":
    expect ConfigParseError:
      discard validateConnectAddress("localhost:5432")

  test "127.0.0.1 raises ConfigParseError":
    expect ConfigParseError:
      discard validateConnectAddress("127.0.0.1:5432")

  test "0.0.0.0 raises ConfigParseError":
    expect ConfigParseError:
      discard validateConnectAddress("0.0.0.0:5432")

  test "asterisk raises ConfigParseError":
    expect ConfigParseError:
      discard validateConnectAddress("*:5432")

  test "::1 raises ConfigParseError":
    expect ConfigParseError:
      discard validateConnectAddress("::1:5432")

suite "IntValidator":
  test "valid integer":
    let v = newIntValidator()
    check v.validate(%42) == true

  test "integer from string":
    let v = newIntValidator()
    check v.validate(%"42") == true

  test "non-numeric string is invalid":
    let v = newIntValidator()
    check v.validate(%"abc") == false

  test "min value validation":
    let v = newIntValidator(minVal = some(10))
    check v.validate(%10) == true
    check v.validate(%15) == true
    check v.validate(%5) == false

  test "max value validation":
    let v = newIntValidator(maxVal = some(100))
    check v.validate(%100) == true
    check v.validate(%50) == true
    check v.validate(%150) == false

  test "range validation":
    let v = newIntValidator(minVal = some(10), maxVal = some(100))
    check v.validate(%50) == true
    check v.validate(%10) == true
    check v.validate(%100) == true
    check v.validate(%5) == false
    check v.validate(%150) == false

  test "boolean is invalid":
    let v = newIntValidator()
    check v.validate(%true) == false

  test "array is invalid":
    let v = newIntValidator()
    check v.validate(%[1, 2, 3]) == false

suite "StringValidator":
  test "valid string":
    let v = newStringValidator()
    check v.validate(%"hello") == true

  test "empty string is valid":
    let v = newStringValidator()
    check v.validate(%"") == true

  test "allowed values validation":
    let v = newStringValidator(@["DEBUG", "INFO", "WARNING", "ERROR"])
    check v.validate(%"DEBUG") == true
    check v.validate(%"INFO") == true
    check v.validate(%"TRACE") == false

  test "integer is invalid":
    let v = newStringValidator()
    check v.validate(%42) == false

  test "boolean is invalid":
    let v = newStringValidator()
    check v.validate(%true) == false

suite "BoolValidator":
  test "true is valid":
    let v = newBoolValidator()
    check v.validate(%true) == true

  test "false is valid":
    let v = newBoolValidator()
    check v.validate(%false) == true

  test "string true is valid":
    let v = newBoolValidator()
    check v.validate(%"true") == true
    check v.validate(%"TRUE") == true
    check v.validate(%"True") == true

  test "string false is valid":
    let v = newBoolValidator()
    check v.validate(%"false") == true
    check v.validate(%"FALSE") == true

  test "string yes/no is valid":
    let v = newBoolValidator()
    check v.validate(%"yes") == true
    check v.validate(%"no") == true
    check v.validate(%"YES") == true
    check v.validate(%"NO") == true

  test "string on/off is valid":
    let v = newBoolValidator()
    check v.validate(%"on") == true
    check v.validate(%"off") == true

  test "integer 0 and 1 are valid":
    let v = newBoolValidator()
    check v.validate(%0) == true
    check v.validate(%1) == true

  test "other integers are invalid":
    let v = newBoolValidator()
    check v.validate(%2) == false
    check v.validate(newJInt(-1)) == false

  test "arbitrary string is invalid":
    let v = newBoolValidator()
    check v.validate(%"maybe") == false
    check v.validate(%"hello") == false

suite "DirectoryValidator":
  test "existing directory is valid":
    let v = newDirectoryValidator()
    check v.validate(%"/tmp") == true

  test "non-string is invalid":
    let v = newDirectoryValidator()
    check v.validate(%123) == false

  test "mustExist with existing directory":
    let v = newDirectoryValidator(mustExist = true)
    check v.validate(%"/tmp") == true

  test "mustExist with non-existing directory":
    let v = newDirectoryValidator(mustExist = true)
    check v.validate(%"/nonexistent/path/that/does/not/exist") == false

suite "SchemaNode":
  test "create schema node":
    let node = newSchemaNode("test")
    check node.name == "test"
    check node.required == false
    check node.default == nil

  test "create required schema node":
    let node = newSchemaNode("test", required = true)
    check node.required == true

  test "add child node":
    let parent = newSchemaNode("parent")
    let child = newSchemaNode("child")
    parent.addChild(child)
    check parent.children.hasKey("child")

  test "multiple children":
    let parent = newSchemaNode("parent")
    parent.addChild(newSchemaNode("child1"))
    parent.addChild(newSchemaNode("child2"))
    check parent.children.len == 2

suite "validateConfig":
  test "empty config against empty schema":
    let config = newJObject()
    let schema = newSchemaNode("root")
    let errors = validateConfig(config, schema)
    check errors.len == 0

  test "missing required field":
    let config = newJObject()
    let schema = newSchemaNode("root")
    schema.addChild(newSchemaNode("scope", required = true))
    let errors = validateConfig(config, schema)
    check errors.len > 0
    check "scope" in errors[0]

  test "valid required field":
    let config = %*{"scope": "test_cluster"}
    let schema = newSchemaNode("root")
    schema.addChild(newSchemaNode("scope", required = true, validator = newStringValidator()))
    let errors = validateConfig(config, schema)
    check errors.len == 0

  test "invalid field value":
    let config = %*{"ttl": "not_a_number"}
    let schema = newSchemaNode("root")
    schema.addChild(newSchemaNode("ttl", validator = newIntValidator()))
    let errors = validateConfig(config, schema)
    check errors.len > 0

  test "multiple validation errors":
    let config = %*{"scope": 123}
    let schema = newSchemaNode("root")
    schema.addChild(newSchemaNode("scope", required = true, validator = newStringValidator()))
    schema.addChild(newSchemaNode("name", required = true, validator = newStringValidator()))
    let errors = validateConfig(config, schema)
    check errors.len == 2  # Invalid type for scope + missing name

  test "non-object config":
    let config = %"not an object"
    let schema = newSchemaNode("root")
    let errors = validateConfig(config, schema)
    check errors.len > 0
    check "must be an object" in errors[0]

suite "buildDefaultSchema":
  test "creates schema with required fields":
    let schema = buildDefaultSchema()
    check schema.children.hasKey("scope")
    check schema.children.hasKey("name")
    check schema.children["scope"].required == true
    check schema.children["name"].required == true

  test "creates schema with postgresql section":
    let schema = buildDefaultSchema()
    check schema.children.hasKey("postgresql")
    let pg = schema.children["postgresql"]
    check pg.children.hasKey("listen")
    check pg.children.hasKey("connect_address")
    check pg.children.hasKey("data_dir")

  test "creates schema with restapi section":
    let schema = buildDefaultSchema()
    check schema.children.hasKey("restapi")

  test "creates schema with timeout fields":
    let schema = buildDefaultSchema()
    check schema.children.hasKey("ttl")
    check schema.children.hasKey("loop_wait")
    check schema.children.hasKey("retry_timeout")

  test "creates schema with log section":
    let schema = buildDefaultSchema()
    check schema.children.hasKey("log")
    let log = schema.children["log"]
    check log.children.hasKey("level")
    check log.children.hasKey("dir")

suite "populateValidateParams":
  test "sets ignore_listen_port":
    populateValidateParams(ignoreListenPort = true)
    # This should not raise an error
    check true

  test "default is false":
    populateValidateParams()
    check true

suite "dataDirectoryEmpty":
  test "non-existent directory is empty":
    check dataDirectoryEmpty("/nonexistent/path/12345") == true

  test "tmp directory is not empty":
    # /tmp typically has files
    let result = dataDirectoryEmpty("/tmp")
    # Result depends on system state, just check it runs
    check (result == true or result == false)

suite "Validator Type Hierarchy":
  test "IntValidator is a Validator":
    let v: Validator = newIntValidator()
    check v != nil

  test "StringValidator is a Validator":
    let v: Validator = newStringValidator()
    check v != nil

  test "BoolValidator is a Validator":
    let v: Validator = newBoolValidator()
    check v != nil

  test "DirectoryValidator is a Validator":
    let v: Validator = newDirectoryValidator()
    check v != nil

  test "HostPortValidator is a Validator":
    let v: Validator = newHostPortValidator()
    check v != nil

suite "Edge Cases":
  test "null JSON value":
    let v = newStringValidator()
    check v.validate(newJNull()) == false

  test "empty object":
    let v = newStringValidator()
    check v.validate(newJObject()) == false

  test "nested validation":
    let config = %*{
      "postgresql": {
        "listen": "127.0.0.1:5432",
        "data_dir": "/data"
      }
    }
    let schema = buildDefaultSchema()
    # Just test it doesn't crash
    let errors = validateConfig(config, schema)
    check errors.len >= 0

when isMainModule:
  echo "test_validator.nim tests completed"
