## Patroni configuration validation helpers.
##
## This module contains facilities for validating configuration of Patroni processes.

import std/[os, json, strutils, tables, net, options, strformat]
import ./collections
import ./exceptions
import ./log
import ./utils

type
  ValidatorProc* = proc(value: JsonNode): bool

  Validator* = ref object of RootObj
    ## Base validator class.

  IntValidator* = ref object of Validator
    ## Integer value validator.
    minVal*: Option[int]
    maxVal*: Option[int]

  StringValidator* = ref object of Validator
    ## String value validator.
    allowedValues*: seq[string]

  BoolValidator* = ref object of Validator
    ## Boolean value validator.

  DirectoryValidator* = ref object of Validator
    ## Directory path validator.
    mustExist*: bool
    mustBeEmpty*: bool

  HostPortValidator* = ref object of Validator
    ## Host:port validator.
    listen*: bool
    multipleHosts*: bool

# Validation parameters
var validationParams: Table[string, bool]

proc populateValidateParams*(ignoreListenPort: bool = false) =
  ## Populate parameters used to fine-tune the validation of the Patroni config.
  ##
  ## :param ignoreListenPort: ignore the bind failures for the ports marked as `listen`.
  validationParams["ignore_listen_port"] = ignoreListenPort

proc validateLogField*(field: JsonNode): bool =
  ## Check if log field is valid.
  ##
  ## :param field: A log field to be validated.
  ## :returns: true if the field is either a string or a dictionary with exactly one key
  ##           that has string value, false otherwise.
  case field.kind
  of JString:
    result = true
  of JObject:
    result = field.len == 1
    if result:
      for key, value in field.pairs:
        if value.kind != JString:
          result = false
          break
  else:
    result = false

proc validateLogFormat*(logformat: JsonNode): bool =
  ## Check if log format is valid.
  ##
  ## :param logformat: A log format to be validated.
  ## :returns: true if the log format is valid.
  ## :raises: ConfigParseError if validation fails.
  case logformat.kind
  of JString:
    result = true
  of JArray:
    if logformat.len == 0:
      raise newException(ConfigParseError, "should contain at least one item")
    for item in logformat.items:
      if not validateLogField(item):
        raise newException(ConfigParseError, "each item should be a string or a dictionary with string values")
    result = true
  else:
    raise newException(ConfigParseError, "Should be a string or a list")

proc dataDirectoryEmpty*(dataDir: string): bool =
  ## Check if PostgreSQL data directory is empty.
  ##
  ## :param dataDir: path to the PostgreSQL data directory to be checked.
  ## :returns: true if the data directory is empty.
  let pgControl = dataDir / "global" / "pg_control"
  if fileExists(pgControl):
    return false

  if not dirExists(dataDir):
    return true

  for kind, path in walkDir(dataDir):
    return false

  result = true

proc splitHostPort*(address: string, defaultPort: int = 0): tuple[host: string, port: int] =
  ## Split a host:port string into components.
  let lastColon = address.rfind(':')
  if lastColon < 0:
    result.host = address
    result.port = defaultPort
  else:
    result.host = address[0..<lastColon]
    let portStr = address[lastColon + 1..^1]
    let p = parseInt(portStr)
    if p.isSome:
      result.port = p.get()
    else:
      result.port = defaultPort

proc validateConnectAddress*(address: string): bool =
  ## Check if options related to connection address were properly configured.
  ##
  ## :param address: address to be validated in the format ``host:ip``.
  ## :returns: true if the address is valid.
  ## :raises: ConfigParseError if validation fails.
  try:
    let (host, _) = splitHostPort(address, 1)
    if host in ["127.0.0.1", "0.0.0.0", "*", "::1", "localhost"]:
      raise newException(ConfigParseError, "must not contain '127.0.0.1', '0.0.0.0', '*', '::1', 'localhost'")
    result = true
  except ValueError:
    raise newException(ConfigParseError, "contains a wrong value")

proc validateHostPort*(hostPort: string, listen: bool = false, multipleHosts: bool = false): bool =
  ## Check if host(s) and port are valid and available for usage.
  ##
  ## :param hostPort: the host(s) and port to be validated.
  ## :param listen: if the address is expected to be available for binding.
  ## :param multipleHosts: if hostPort can contain multiple hosts.
  ## :returns: true if the host(s) and port are valid.
  try:
    let (hostsStr, port) = splitHostPort(hostPort, 1)
    var hosts: seq[string]

    if multipleHosts:
      hosts = hostsStr.split(",")
    else:
      hosts = @[hostsStr]

    if "*" in hosts:
      if hosts.len != 1:
        raise newException(ConfigParseError, "expecting '*' alone")
      # For "*", we'd need to get all available addresses
      hosts = @["0.0.0.0"]

    for host in hosts:
      if listen:
        # Check if we can bind to the address
        if not validationParams.getOrDefault("ignore_listen_port", false):
          try:
            let socket = newSocket()
            socket.bindAddr(Port(port), host)
            socket.close()
          except OSError:
            raise newException(ConfigParseError, fmt"Unable to bind to '{host}:{port}'")
      else:
        # Check if we can connect to the address
        try:
          let socket = newSocket()
          socket.connect(host, Port(port))
          socket.close()
        except OSError:
          raise newException(ConfigParseError, fmt"Unable to connect to '{host}:{port}'")

    result = true
  except ValueError:
    raise newException(ConfigParseError, "contains a wrong value")

proc validateDirectory*(path: string, mustExist: bool = false, mustBeEmpty: bool = false): bool =
  ## Validate a directory path.
  ##
  ## :param path: path to validate.
  ## :param mustExist: if true, the directory must exist.
  ## :param mustBeEmpty: if true, the directory must be empty.
  ## :returns: true if the directory is valid.
  if mustExist and not dirExists(path):
    raise newException(ConfigParseError, fmt"Directory '{path}' does not exist")

  if mustBeEmpty and dirExists(path):
    for kind, p in walkDir(path):
      raise newException(ConfigParseError, fmt"Directory '{path}' is not empty")

  result = true

# Validator implementations

proc newIntValidator*(minVal: Option[int] = none(int), maxVal: Option[int] = none(int)): IntValidator =
  ## Create a new integer validator.
  new(result)
  result.minVal = minVal
  result.maxVal = maxVal

method validate*(v: Validator, value: JsonNode): bool {.base.} =
  ## Validate a value.
  result = true

method validate*(v: IntValidator, value: JsonNode): bool =
  ## Validate an integer value.
  var intVal: int
  case value.kind
  of JInt:
    intVal = value.getInt()
  of JString:
    let parsed = parseInt(value.getStr())
    if parsed.isNone:
      return false
    intVal = parsed.get()
  else:
    return false

  if v.minVal.isSome and intVal < v.minVal.get():
    return false
  if v.maxVal.isSome and intVal > v.maxVal.get():
    return false

  result = true

proc newStringValidator*(allowedValues: seq[string] = @[]): StringValidator =
  ## Create a new string validator.
  new(result)
  result.allowedValues = allowedValues

method validate*(v: StringValidator, value: JsonNode): bool =
  ## Validate a string value.
  if value.kind != JString:
    return false

  if v.allowedValues.len > 0:
    return value.getStr() in v.allowedValues

  result = true

proc newBoolValidator*(): BoolValidator =
  ## Create a new boolean validator.
  new(result)

method validate*(v: BoolValidator, value: JsonNode): bool =
  ## Validate a boolean value.
  case value.kind
  of JBool:
    result = true
  of JString:
    let s = value.getStr().toLowerAscii()
    result = s in ["true", "false", "yes", "no", "on", "off", "1", "0"]
  of JInt:
    result = value.getInt() in [0, 1]
  else:
    result = false

proc newDirectoryValidator*(mustExist: bool = false, mustBeEmpty: bool = false): DirectoryValidator =
  ## Create a new directory validator.
  new(result)
  result.mustExist = mustExist
  result.mustBeEmpty = mustBeEmpty

method validate*(v: DirectoryValidator, value: JsonNode): bool =
  ## Validate a directory path.
  if value.kind != JString:
    return false

  try:
    result = validateDirectory(value.getStr(), v.mustExist, v.mustBeEmpty)
  except ConfigParseError:
    result = false

proc newHostPortValidator*(listen: bool = false, multipleHosts: bool = false): HostPortValidator =
  ## Create a new host:port validator.
  new(result)
  result.listen = listen
  result.multipleHosts = multipleHosts

method validate*(v: HostPortValidator, value: JsonNode): bool =
  ## Validate a host:port value.
  if value.kind != JString:
    return false

  try:
    result = validateHostPort(value.getStr(), v.listen, v.multipleHosts)
  except ConfigParseError:
    result = false

# Schema validation

type
  SchemaNode* = ref object
    ## Configuration schema node.
    name*: string
    required*: bool
    default*: JsonNode
    validator*: Validator
    children*: Table[string, SchemaNode]

proc newSchemaNode*(name: string, required: bool = false, default: JsonNode = nil,
                    validator: Validator = nil): SchemaNode =
  ## Create a new schema node.
  new(result)
  result.name = name
  result.required = required
  result.default = default
  result.validator = validator
  result.children = initTable[string, SchemaNode]()

proc addChild*(parent: SchemaNode, child: SchemaNode) =
  ## Add a child node to a schema node.
  parent.children[child.name] = child

proc validateConfig*(config: JsonNode, schema: SchemaNode): seq[string] =
  ## Validate a configuration against a schema.
  ##
  ## :param config: the configuration to validate.
  ## :param schema: the schema to validate against.
  ## :returns: a list of validation errors.
  result = @[]

  if config.kind != JObject:
    result.add("Configuration must be an object")
    return

  # Check required fields
  for name, child in schema.children.pairs:
    if child.required and name notin config:
      result.add(fmt"Missing required field: {name}")

  # Validate each field
  for key, value in config.pairs:
    if key in schema.children:
      let child = schema.children[key]
      if child.validator != nil and not child.validator.validate(value):
        result.add(fmt"Invalid value for field: {key}")
      if child.children.len > 0 and value.kind == JObject:
        let childErrors = validateConfig(value, child)
        for err in childErrors:
          result.add(fmt"{key}.{err}")

# Default schema for Patroni configuration

proc buildDefaultSchema*(): SchemaNode =
  ## Build the default Patroni configuration schema.
  result = newSchemaNode("root")

  # Scope is required
  let scope = newSchemaNode("scope", required = true, validator = newStringValidator())
  result.addChild(scope)

  # Name is required
  let name = newSchemaNode("name", required = true, validator = newStringValidator())
  result.addChild(name)

  # Namespace is optional
  let namespace = newSchemaNode("namespace", validator = newStringValidator())
  result.addChild(namespace)

  # Log configuration
  let log = newSchemaNode("log")
  log.addChild(newSchemaNode("level", validator = newStringValidator(
    @["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"])))
  log.addChild(newSchemaNode("dir", validator = newDirectoryValidator()))
  result.addChild(log)

  # PostgreSQL configuration
  let postgresql = newSchemaNode("postgresql", required = true)
  postgresql.addChild(newSchemaNode("listen", required = true, validator = newHostPortValidator(listen = true)))
  postgresql.addChild(newSchemaNode("connect_address", required = true))
  postgresql.addChild(newSchemaNode("data_dir", required = true, validator = newDirectoryValidator()))
  result.addChild(postgresql)

  # REST API configuration
  let restapi = newSchemaNode("restapi")
  restapi.addChild(newSchemaNode("listen", validator = newHostPortValidator(listen = true)))
  restapi.addChild(newSchemaNode("connect_address"))
  result.addChild(restapi)

  # Timeout configuration
  result.addChild(newSchemaNode("ttl", validator = newIntValidator(some(10), some(3600))))
  result.addChild(newSchemaNode("loop_wait", validator = newIntValidator(some(1), some(120))))
  result.addChild(newSchemaNode("retry_timeout", validator = newIntValidator(some(3), some(600))))
