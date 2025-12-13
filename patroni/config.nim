## Facilities related to Patroni configuration.

import std/[os, json, strutils, tables, options, strformat]
import ./collections
import ./dcs/dcs
import ./exceptions
import ./log
import ./utils

let logger = getLogger("patroni.config")

const
  PATRONI_ENV_PREFIX* = "PATRONI_"
  PATRONI_CONFIG_VARIABLE* = PATRONI_ENV_PREFIX & "CONFIGURATION"
  CACHE_FILENAME = "patroni.dynamic.json"

  AUTH_ALLOWED_PARAMETERS* = [
    "username", "password", "sslmode", "sslcert", "sslkey",
    "sslpassword", "sslrootcert", "sslcrl", "sslcrldir",
    "gssencmode", "channel_binding", "sslnegotiation"
  ]

  DEFAULT_TTL* = 30
  DEFAULT_LOOP_WAIT* = 10
  DEFAULT_RETRY_TIMEOUT* = 10

type
  Config* = ref object
    ## Handle Patroni configuration.
    ##
    ## This class is responsible for:
    ##   1) Building and giving access to effective_configuration from:
    ##      * DEFAULT_CONFIG -- some sane default values;
    ##      * dynamic_configuration -- configuration stored in DCS;
    ##      * local_configuration -- configuration from config.yml or environment.
    ##   2) Saving and loading dynamic_configuration into 'patroni.dynamic.json' file
    ##   3) Loading of configuration file in the old format and converting it into new format.
    modifyVersion: int
    dynamicConfiguration: Table[string, JsonNode]
    localConfiguration: Table[string, JsonNode]
    environmentConfiguration: Table[string, JsonNode]
    effectiveConfiguration: Table[string, JsonNode]
    configFile: string
    dataDir: string
    cacheFile: string
    cacheNeedsSaving: bool

proc getDefaultConfig*(): Table[string, JsonNode] =
  ## Get default configuration values.
  result = initTable[string, JsonNode]()
  result["ttl"] = newJInt(DEFAULT_TTL)
  result["loop_wait"] = newJInt(DEFAULT_LOOP_WAIT)
  result["retry_timeout"] = newJInt(DEFAULT_RETRY_TIMEOUT)

  # Standby cluster defaults
  var standbyCluster = newJObject()
  standbyCluster["create_replica_methods"] = newJString("")
  standbyCluster["host"] = newJString("")
  standbyCluster["port"] = newJString("")
  standbyCluster["primary_slot_name"] = newJString("")
  standbyCluster["restore_command"] = newJString("")
  standbyCluster["archive_cleanup_command"] = newJString("")
  standbyCluster["recovery_min_apply_delay"] = newJString("")
  result["standby_cluster"] = standbyCluster

  # PostgreSQL defaults
  var postgresql = newJObject()
  postgresql["use_slots"] = newJBool(true)
  postgresql["parameters"] = newJObject()
  result["postgresql"] = postgresql

proc defaultValidator*(conf: Table[string, JsonNode]): seq[string] =
  ## Ensure conf is not empty.
  ##
  ## Designed to be used as default validator for Config objects.
  ##
  ## :param conf: configuration to be validated.
  ## :returns: an empty list on success.
  ## :raises: ConfigParseError if conf is empty.
  if conf.len == 0:
    raise newException(ConfigParseError, "Config is empty.")
  result = @[]

proc buildEnvironmentConfiguration(): Table[string, JsonNode] =
  ## Get local configuration settings that were specified through environment variables.
  result = initTable[string, JsonNode]()

  proc popenv(name: string): string =
    let envName = PATRONI_ENV_PREFIX & name.toUpperAscii()
    result = getEnv(envName, "")
    if result.len > 0:
      delEnv(envName)

  for param in ["name", "namespace", "scope"]:
    let value = popenv(param)
    if value.len > 0:
      result[param] = newJString(value)

  # Log configuration
  let logLevel = popenv("LOG_LEVEL")
  if logLevel.len > 0:
    if "log" notin result:
      result["log"] = newJObject()
    result["log"]["level"] = newJString(logLevel)

  let logFormat = popenv("LOG_FORMAT")
  if logFormat.len > 0:
    if "log" notin result:
      result["log"] = newJObject()
    result["log"]["format"] = newJString(logFormat)

  # PostgreSQL configuration
  let pgListen = popenv("POSTGRESQL_LISTEN")
  if pgListen.len > 0:
    if "postgresql" notin result:
      result["postgresql"] = newJObject()
    result["postgresql"]["listen"] = newJString(pgListen)

  let pgConnectAddress = popenv("POSTGRESQL_CONNECT_ADDRESS")
  if pgConnectAddress.len > 0:
    if "postgresql" notin result:
      result["postgresql"] = newJObject()
    result["postgresql"]["connect_address"] = newJString(pgConnectAddress)

  let pgDataDir = popenv("POSTGRESQL_DATA_DIR")
  if pgDataDir.len > 0:
    if "postgresql" notin result:
      result["postgresql"] = newJObject()
    result["postgresql"]["data_dir"] = newJString(pgDataDir)

  # REST API configuration
  let restApiListen = popenv("RESTAPI_LISTEN")
  if restApiListen.len > 0:
    if "restapi" notin result:
      result["restapi"] = newJObject()
    result["restapi"]["listen"] = newJString(restApiListen)

  let restApiConnectAddress = popenv("RESTAPI_CONNECT_ADDRESS")
  if restApiConnectAddress.len > 0:
    if "restapi" notin result:
      result["restapi"] = newJObject()
    result["restapi"]["connect_address"] = newJString(restApiConnectAddress)

proc loadYamlFile(path: string): Table[string, JsonNode] =
  ## Load a YAML/JSON configuration file.
  ## Note: For simplicity, we treat YAML as JSON here since JSON is valid YAML.
  result = initTable[string, JsonNode]()
  try:
    let content = readFile(path)
    let parsed = parseJson(content)
    if parsed.kind == JObject:
      for key, value in parsed.pairs:
        result[key] = value
  except IOError as e:
    logger.exception(fmt"Failed to load config file: {path}", e)
    raise newException(ConfigParseError, fmt"Cannot load config file: {path}")
  except JsonParsingError as e:
    logger.exception(fmt"Failed to parse config file: {path}", e)
    raise newException(ConfigParseError, fmt"Invalid config file: {path}")

proc loadConfigPath(path: string): Table[string, JsonNode] =
  ## Load Patroni configuration file(s) from path.
  ##
  ## If path is a file, load the yml file pointed to by path.
  ## If path is a directory, load all yml files in that directory in alphabetical order.
  result = initTable[string, JsonNode]()

  if fileExists(path):
    result = loadYamlFile(path)
  elif dirExists(path):
    for kind, filePath in walkDir(path):
      if kind == pcFile and (filePath.endsWith(".yml") or filePath.endsWith(".yaml")):
        let fileConfig = loadYamlFile(filePath)
        for key, value in fileConfig.pairs:
          result[key] = value
  else:
    logger.error(fmt"config path {path} is neither directory nor file")
    raise newException(ConfigParseError, "invalid config path")

proc patchConfig(base: var Table[string, JsonNode], patch: Table[string, JsonNode]) =
  ## Recursively merge patch into base configuration.
  for key, value in patch.pairs:
    if key in base and base[key].kind == JObject and value.kind == JObject:
      var baseObj = base[key]
      var baseTable = initTable[string, JsonNode]()
      for k, v in baseObj.pairs:
        baseTable[k] = v
      var patchTable = initTable[string, JsonNode]()
      for k, v in value.pairs:
        patchTable[k] = v
      patchConfig(baseTable, patchTable)
      base[key] = newJObject()
      for k, v in baseTable.pairs:
        base[key][k] = v
    else:
      base[key] = value

proc buildEffectiveConfiguration(dynamicConfig, localConfig: Table[string, JsonNode]): Table[string, JsonNode] =
  ## Build effective configuration from dynamic and local configs.
  result = getDefaultConfig()

  # Apply dynamic configuration
  for key, value in dynamicConfig.pairs:
    if key in result:
      if result[key].kind == JObject and value.kind == JObject:
        for k, v in value.pairs:
          result[key][k] = v
      else:
        result[key] = value

  # Apply local configuration (takes precedence)
  for key, value in localConfig.pairs:
    if key in result:
      if result[key].kind == JObject and value.kind == JObject:
        for k, v in value.pairs:
          result[key][k] = v
      else:
        result[key] = value
    else:
      result[key] = value

proc newConfig*(configFile: string,
                validator: proc(conf: Table[string, JsonNode]): seq[string] = defaultValidator): Config =
  ## Create a new instance of Config and validate the loaded configuration.
  ##
  ## :param configFile: path to Patroni configuration file.
  ## :param validator: function used to validate Patroni configuration.
  ## :raises: ConfigParseError if any issue is reported by validator.
  new(result)
  result.modifyVersion = -1
  result.dynamicConfiguration = initTable[string, JsonNode]()
  result.cacheNeedsSaving = false

  result.environmentConfiguration = buildEnvironmentConfiguration()

  if configFile.len > 0 and fileExists(configFile):
    result.configFile = configFile
    result.localConfiguration = loadConfigPath(configFile)
    patchConfig(result.localConfiguration, result.environmentConfiguration)
  else:
    let configEnv = getEnv(PATRONI_CONFIG_VARIABLE, "")
    if configEnv.len > 0:
      try:
        let parsed = parseJson(configEnv)
        if parsed.kind == JObject:
          for key, value in parsed.pairs:
            result.localConfiguration[key] = value
      except JsonParsingError:
        result.localConfiguration = result.environmentConfiguration
    else:
      result.localConfiguration = result.environmentConfiguration

  if validator != nil:
    let errors = validator(result.localConfiguration)
    if errors.len > 0:
      raise newException(ConfigParseError, errors.join("\n"))

  result.effectiveConfiguration = buildEffectiveConfiguration(
    result.dynamicConfiguration, result.localConfiguration)

  # Extract data directory
  if "postgresql" in result.effectiveConfiguration:
    let pg = result.effectiveConfiguration["postgresql"]
    if pg.kind == JObject and "data_dir" in pg:
      result.dataDir = pg["data_dir"].getStr("")

  result.cacheFile = result.dataDir / CACHE_FILENAME

proc configFilePath*(self: Config): string =
  ## Path to Patroni configuration file, if any.
  result = self.configFile

proc dynamicConfiguration*(self: Config): Table[string, JsonNode] =
  ## Get cached Patroni dynamic configuration.
  result = self.dynamicConfiguration

proc localConfiguration*(self: Config): Table[string, JsonNode] =
  ## Get cached Patroni local configuration.
  result = self.localConfiguration

proc effectiveConfiguration*(self: Config): Table[string, JsonNode] =
  ## Get effective configuration.
  result = self.effectiveConfiguration

proc get*(self: Config, key: string): JsonNode =
  ## Get a configuration value by key.
  if key in self.effectiveConfiguration:
    result = self.effectiveConfiguration[key]
  else:
    result = nil

proc getStr*(self: Config, key: string, default: string = ""): string =
  ## Get a configuration value as string.
  let value = self.get(key)
  if value != nil and value.kind == JString:
    result = value.getStr()
  else:
    result = default

proc getInt*(self: Config, key: string, default: int = 0): int =
  ## Get a configuration value as integer.
  let value = self.get(key)
  if value != nil:
    case value.kind
    of JInt: result = value.getInt()
    of JString:
      try:
        result = parseInt(value.getStr())
      except ValueError:
        result = default
    else: result = default
  else:
    result = default

proc getBool*(self: Config, key: string, default: bool = false): bool =
  ## Get a configuration value as boolean.
  let value = self.get(key)
  if value != nil:
    case value.kind
    of JBool: result = value.getBool()
    of JString: result = parseBool(value.getStr())
    of JInt: result = value.getInt() != 0
    else: result = default
  else:
    result = default

proc loadCache(self: Config) =
  ## Load dynamic configuration from patroni.dynamic.json.
  if fileExists(self.cacheFile):
    try:
      let content = readFile(self.cacheFile)
      let parsed = parseJson(content)
      if parsed.kind == JObject:
        for key, value in parsed.pairs:
          self.dynamicConfiguration[key] = value
    except IOError as e:
      logger.exception(fmt"Exception when loading file: {self.cacheFile}", e)
    except JsonParsingError as e:
      logger.exception(fmt"Exception when loading file: {self.cacheFile}", e)

proc saveCache*(self: Config) =
  ## Save dynamic configuration to patroni.dynamic.json.
  if self.cacheNeedsSaving:
    try:
      var jsonObj = newJObject()
      for key, value in self.dynamicConfiguration.pairs:
        jsonObj[key] = value
      writeFile(self.cacheFile, $jsonObj)
      self.cacheNeedsSaving = false
    except IOError as e:
      logger.exception(fmt"Exception when saving file: {self.cacheFile}", e)

proc validateAndAdjustTimeouts(self: Config, config: var Table[string, JsonNode]) =
  ## Validate and adjust loop_wait, retry_timeout, and ttl values.
  let minLoopWait = 1
  let minRetryTimeout = 3
  let minTtl = 20

  var loopWait = DEFAULT_LOOP_WAIT
  var retryTimeout = DEFAULT_RETRY_TIMEOUT
  var ttl = DEFAULT_TTL

  if "loop_wait" in config:
    loopWait = config["loop_wait"].getInt(DEFAULT_LOOP_WAIT)
  if "retry_timeout" in config:
    retryTimeout = config["retry_timeout"].getInt(DEFAULT_RETRY_TIMEOUT)
  if "ttl" in config:
    ttl = config["ttl"].getInt(DEFAULT_TTL)

  if loopWait < minLoopWait:
    logger.warning(fmt"loop_wait={loopWait} can't be smaller than {minLoopWait}, adjusting...")
    loopWait = minLoopWait
    config["loop_wait"] = newJInt(loopWait)

  if retryTimeout < minRetryTimeout:
    logger.warning(fmt"retry_timeout={retryTimeout} can't be smaller than {minRetryTimeout}, adjusting...")
    retryTimeout = minRetryTimeout
    config["retry_timeout"] = newJInt(retryTimeout)

  if ttl < minTtl:
    logger.warning(fmt"ttl={ttl} can't be smaller than {minTtl}, adjusting...")
    ttl = minTtl
    config["ttl"] = newJInt(ttl)

  # Check: loop_wait + 2 * retry_timeout <= ttl
  if minLoopWait + 2 * retryTimeout > ttl:
    config["loop_wait"] = newJInt(minLoopWait)
    config["retry_timeout"] = newJInt((ttl - minLoopWait) div 2)
    logger.warning(fmt"Violated rule 'loop_wait + 2*retry_timeout <= ttl'. Adjusting...")
  elif loopWait + 2 * retryTimeout > ttl:
    config["loop_wait"] = newJInt(ttl - 2 * retryTimeout)
    logger.warning(fmt"Violated rule 'loop_wait + 2*retry_timeout <= ttl'. Adjusting loop_wait...")

proc setDynamicConfiguration*(self: Config, configuration: Table[string, JsonNode]): bool =
  ## Set dynamic configuration values.
  ##
  ## :param configuration: new dynamic configuration values.
  ## :returns: true if changes have been detected, false otherwise.
  if not deepCompare(%self.dynamicConfiguration, %configuration):
    try:
      var configCopy = configuration
      self.validateAndAdjustTimeouts(configCopy)
      self.effectiveConfiguration = buildEffectiveConfiguration(configCopy, self.localConfiguration)
      self.dynamicConfiguration = configCopy
      self.cacheNeedsSaving = true
      return true
    except CatchableError as e:
      logger.exception("Exception when setting dynamic_configuration", e)
  return false

proc setDynamicConfiguration*(self: Config, configuration: dcs.ClusterConfig): bool =
  ## Set dynamic configuration from ClusterConfig.
  if self.modifyVersion == configuration.modifyVersion:
    return false
  self.modifyVersion = configuration.modifyVersion

  var configTable = initTable[string, JsonNode]()
  for key, value in configuration.data.pairs:
    configTable[key] = value
  return self.setDynamicConfiguration(configTable)

proc reloadLocalConfiguration*(self: Config): bool =
  ## Reload configuration values from the configuration file(s).
  ##
  ## :returns: true if changes have been detected.
  if self.configFile.len > 0:
    try:
      var configuration = loadConfigPath(self.configFile)
      patchConfig(configuration, self.environmentConfiguration)
      if not deepCompare(%self.localConfiguration, %configuration):
        self.localConfiguration = configuration
        self.effectiveConfiguration = buildEffectiveConfiguration(
          self.dynamicConfiguration, configuration)
        return true
      else:
        logger.info("No local configuration items changed.")
    except CatchableError as e:
      logger.exception(fmt"Exception when reloading local configuration from {self.configFile}", e)
  return false

proc `[]`*(self: Config, key: string): JsonNode =
  ## Get configuration value by key (dict-like interface).
  result = self.get(key)

proc contains*(self: Config, key: string): bool =
  ## Check if key exists in configuration.
  result = key in self.effectiveConfiguration

iterator pairs*(self: Config): tuple[key: string, value: JsonNode] =
  ## Iterate over configuration key-value pairs.
  for key, value in self.effectiveConfiguration.pairs:
    yield (key, value)
