## Patroni configuration generator.
##
## Provides machinery for `patroni --generate-config` to create configuration
## files either from running PostgreSQL instances or with sample defaults.

import std/[json, nativesockets, net, options, os, osproc, sequtils, strformat, strutils, symlinks, tables, terminal]
import ./collections
import ./config
import ./exceptions
import ./log
import ./psycopg
import ./postgresql/config
import ./postgresql/misc
import ./utils

let logger = getLogger("patroni.config_generator")

const
  NO_VALUE_MSG* = "#FIXME"

  # Mapping between libpq connection parameters and environment variables
  AUTH_ALLOWED_PARAMETERS_MAPPING* = {
    "user": "PGUSER",
    "password": "PGPASSWORD",
    "sslmode": "PGSSLMODE",
    "sslcert": "PGSSLCERT",
    "sslkey": "PGSSLKEY",
    "sslpassword": "",
    "sslrootcert": "PGSSLROOTCERT",
    "sslcrl": "PGSSLCRL",
    "sslcrldir": "PGSSLCRLDIR",
    "gssencmode": "PGGSSENCMODE",
    "channel_binding": "PGCHANNELBINDING",
    "sslnegotiation": "PGSSLNEGOTIATION"
  }.toTable

proc getAddress*(): tuple[hostname: string, ip: string] =
  ## Get hostname and IP address.
  ##
  ## :returns: Tuple of (hostname, ip). Uses NO_VALUE_MSG on failure.
  var hostname = NO_VALUE_MSG
  var ip = NO_VALUE_MSG

  try:
    hostname = nativesockets.getHostname()
    # Try to resolve hostname to IP
    try:
      let addrInfo = nativesockets.getAddrInfo(hostname, Port(0))
      var sockAddr: Sockaddr_storage
      var sockLen: SockLen = SockLen(sizeof(sockAddr))
      copyMem(addr sockAddr, addrInfo.ai_addr, addrInfo.ai_addrlen)
      ip = nativesockets.getAddrString(cast[ptr SockAddr](addr sockAddr))
      nativesockets.freeAddrInfo(addrInfo)
    except:
      # If hostname resolution fails, use the hostname itself
      ip = NO_VALUE_MSG
  except OSError as e:
    logger.warning(fmt"Failed to obtain address: {e.msg}")

  result = (hostname, ip)

type
  AbstractConfigGenerator* = ref object of RootObj
    ## Base class for config generation.
    outputFile*: Option[string]
    pgMajor*: int
    config*: JsonNode

proc getTemplateConfig*(): JsonNode =
  ## Generate a template configuration.
  ##
  ## :returns: JSON object with template configuration.
  let (hostname, ip) = getAddress()

  result = %*{
    "scope": NO_VALUE_MSG,
    "name": hostname,
    "restapi": {
      "connect_address": fmt"{ip}:8008",
      "listen": fmt"{ip}:8008"
    },
    "log": {
      "type": "plain",
      "level": "INFO",
      "traceback_level": "ERROR",
      "format": "%(asctime)s %(levelname)s: %(message)s",
      "max_queue_size": 1000
    },
    "postgresql": {
      "data_dir": NO_VALUE_MSG,
      "connect_address": fmt"{ip}:5432",
      "listen": fmt"{ip}:5432",
      "bin_dir": "",
      "authentication": {
        "superuser": {
          "username": "postgres",
          "password": NO_VALUE_MSG
        },
        "replication": {
          "username": "replicator",
          "password": NO_VALUE_MSG
        }
      }
    },
    "tags": {
      "failover_priority": 1,
      "sync_priority": 1,
      "noloadbalance": false,
      "clonefrom": true,
      "nosync": false,
      "nostream": false
    }
  }

proc formatBlock*(self: AbstractConfigGenerator, data: JsonNode, linePrefix: string = ""): string =
  ## Format a JSON block as YAML-like output.
  ##
  ## :param data: JSON data to format.
  ## :param linePrefix: Prefix for indentation.
  ## :returns: Formatted string.
  # Simple JSON pretty-print for now
  result = linePrefix & pretty(data).replace("\n", "\n" & linePrefix)

proc formatConfigSection*(self: AbstractConfigGenerator, sectionName: string): seq[string] =
  ## Format a single configuration section.
  ##
  ## :param sectionName: Section name to format.
  ## :returns: List of formatted lines.
  result = @[]
  if sectionName in self.config:
    let section = self.config[sectionName]
    if section.kind == JObject:
      result.add("")
    result.add(self.formatBlock(%*{sectionName: section}))

proc writeConfigToFile*(self: AbstractConfigGenerator, filename: string) =
  ## Write configuration to a file.
  ##
  ## :param filename: Output filename.
  let dirPath = parentDir(filename)
  if dirPath.len > 0 and not dirExists(dirPath):
    createDir(dirPath)

  writeFile(filename, pretty(self.config))

proc writeConfig*(self: AbstractConfigGenerator) =
  ## Write configuration to output file or stdout.
  if self.outputFile.isSome:
    self.writeConfigToFile(self.outputFile.get)
  else:
    echo pretty(self.config)

method generate*(self: AbstractConfigGenerator) {.base.} =
  ## Generate configuration. Override in subclasses.
  discard

# SampleConfigGenerator

type
  SampleConfigGenerator* = ref object of AbstractConfigGenerator
    ## Generate a sample configuration with sane defaults.

proc getAuthMethod*(self: SampleConfigGenerator): string =
  ## Get preferred authentication method based on PG version.
  if self.pgMajor >= 100000:
    result = "scram-sha-256"
  else:
    result = "md5"

proc getIntMajorVersion(self: SampleConfigGenerator): int =
  ## Get PostgreSQL major version from binary.
  result = 0
  let binDir = self.config{"postgresql", "bin_dir"}.getStr("")
  # Try to run postgres --version to get version string
  if binDir.len > 0:
    let pgCmd = binDir / "postgres"
    if fileExists(pgCmd):
      try:
        let (output, exitCode) = execCmdEx(pgCmd & " --version")
        if exitCode == 0:
          # Parse version from output like "postgres (PostgreSQL) 15.0"
          let parts = output.strip().split(' ')
          if parts.len >= 3:
            let versionStr = parts[^1].split('.')[0]
            result = postgresMajorVersionToInt(versionStr)
      except:
        discard

proc newSampleConfigGenerator*(outputFile: Option[string]): SampleConfigGenerator =
  ## Create a new SampleConfigGenerator.
  new(result)
  result.outputFile = outputFile
  result.pgMajor = 0
  result.config = getTemplateConfig()
  result.generate()

method generate*(self: SampleConfigGenerator) =
  ## Generate sample configuration with sane defaults.
  self.pgMajor = self.getIntMajorVersion()

  let authMethod = self.getAuthMethod()

  # Set authentication parameters
  self.config["postgresql"]["parameters"] = %*{
    "password_encryption": authMethod
  }

  let username = self.config{"postgresql", "authentication", "replication", "username"}.getStr("replicator")
  self.config["postgresql"]["pg_hba"] = %*[
    fmt"host all all all {authMethod}",
    fmt"host replication {username} all {authMethod}"
  ]

  # Add version-specific configuration
  let walKeepParam = if self.pgMajor < 130000: "wal_keep_segments" else: "wal_keep_size"
  let walKeepValue = if self.pgMajor < 130000: "8" else: "128MB"

  if "bootstrap" notin self.config:
    self.config["bootstrap"] = newJObject()
  if "dcs" notin self.config["bootstrap"]:
    self.config["bootstrap"]["dcs"] = newJObject()
  if "postgresql" notin self.config["bootstrap"]["dcs"]:
    self.config["bootstrap"]["dcs"]["postgresql"] = newJObject()
  if "parameters" notin self.config["bootstrap"]["dcs"]["postgresql"]:
    self.config["bootstrap"]["dcs"]["postgresql"]["parameters"] = newJObject()

  self.config["bootstrap"]["dcs"]["postgresql"]["parameters"][walKeepParam] = %walKeepValue

  let walLevel = if self.pgMajor < 90600: "hot_standby" else: "replica"
  self.config["bootstrap"]["dcs"]["postgresql"]["parameters"]["wal_level"] = %walLevel

  self.config["bootstrap"]["dcs"]["postgresql"]["use_pg_rewind"] = %true

  if self.pgMajor >= 110000:
    if "rewind" notin self.config{"postgresql", "authentication"}:
      self.config["postgresql"]["authentication"]["rewind"] = %*{
        "username": "rewind_user",
        "password": NO_VALUE_MSG
      }

# RunningClusterConfigGenerator

type
  RunningClusterConfigGenerator* = ref object of AbstractConfigGenerator
    ## Generate configuration from a running PostgreSQL instance.
    dsn*: Option[string]
    parsedDsn*: Table[string, string]

proc newRunningClusterConfigGenerator*(outputFile: Option[string],
                                        dsn: Option[string] = none(string)): RunningClusterConfigGenerator =
  ## Create a new RunningClusterConfigGenerator.
  new(result)
  result.outputFile = outputFile
  result.dsn = dsn
  result.parsedDsn = initTable[string, string]()
  result.pgMajor = 0
  result.config = getTemplateConfig()
  result.generate()

proc getHbaConnTypes*(self: RunningClusterConfigGenerator): seq[string] =
  ## Get allowed HBA connection types.
  result = @["local", "host", "hostssl", "hostnossl", "hostgssenc", "hostnogssenc"]
  if self.pgMajor >= 160000:
    result.add(@["include", "include_if_exists", "include_dir"])

proc requiredPgParams*(self: RunningClusterConfigGenerator): seq[string] =
  ## Get required PostgreSQL parameters.
  result = @["hba_file", "ident_file", "config_file", "data_directory",
             "listen_addresses", "port", "cluster_name", "wal_level",
             "hot_standby", "max_connections", "max_wal_senders",
             "max_prepared_transactions", "max_locks_per_transaction",
             "max_replication_slots", "max_worker_processes"]

proc getBinDirFromRunningInstance(self: RunningClusterConfigGenerator): string =
  ## Get bin directory from running PostgreSQL instance.
  result = ""
  let dataDir = self.config{"postgresql", "data_dir"}.getStr("")

  if dataDir.len == 0:
    raise newException(PatroniException, "data_dir not set")

  let pidFile = dataDir / "postmaster.pid"
  if not fileExists(pidFile):
    raise newException(PatroniException, "postmaster.pid not found")

  try:
    let content = readFile(pidFile)
    let lines = content.splitLines()
    if lines.len > 0:
      let pid = parseInt(lines[0].strip())
      # Would need to get exe path from /proc/{pid}/exe
      when defined(linux):
        let exePath = expandSymlink(fmt"/proc/{pid}/exe")
        result = parentDir(exePath)
  except IOError as e:
    raise newException(PatroniException, fmt"Error reading postmaster.pid: {e.msg}")

proc setSuParams(self: RunningClusterConfigGenerator) =
  ## Set superuser authentication parameters.
  var suParams = initTable[string, string]()

  for connParam, envVar in AUTH_ALLOWED_PARAMETERS_MAPPING:
    var val = self.parsedDsn.getOrDefault(connParam, "")
    if val.len == 0 and envVar.len > 0:
      val = getEnv(envVar)
    if val.len > 0:
      suParams[connParam] = val

  # Get username
  var username = suParams.getOrDefault("user", "")
  if username.len == 0:
    username = getEnv("USER", "postgres")
  suParams["username"] = username
  suParams.del("user")

  # Get password - would prompt interactively
  if "password" notin suParams:
    suParams["password"] = NO_VALUE_MSG

  self.config["postgresql"]["authentication"]["superuser"] = newJObject()
  for key, val in suParams:
    self.config["postgresql"]["authentication"]["superuser"][key] = %val

  self.config["postgresql"]["authentication"]["replication"] = %*{
    "username": NO_VALUE_MSG,
    "password": NO_VALUE_MSG
  }

method generate*(self: RunningClusterConfigGenerator) =
  ## Generate configuration from running instance.
  if self.dsn.isSome:
    let parsed = parseDsn(self.dsn.get)
    if parsed.isSome:
      self.parsedDsn = parsed.get
    else:
      raise newException(PatroniException, "Failed to parse DSN string")

  self.setSuParams()

  # Connect to database and query settings
  try:
    let conn = connect(
      host = self.parsedDsn.getOrDefault("host", ""),
      port = self.parsedDsn.getOrDefault("port", "5432"),
      user = self.parsedDsn.getOrDefault("user", "postgres"),
      password = self.parsedDsn.getOrDefault("password", ""),
      database = self.parsedDsn.getOrDefault("database", "postgres")
    )
    defer: conn.close()

    # Query important PostgreSQL settings
    let settingsQuery = """
      SELECT name, setting FROM pg_settings
      WHERE name IN ('data_directory', 'config_file', 'hba_file', 'ident_file',
                     'max_connections', 'shared_buffers', 'wal_level', 'max_wal_senders',
                     'max_replication_slots', 'hot_standby', 'listen_addresses', 'port')
    """
    let rows = conn.query(settingsQuery)
    var pgParams = newJObject()
    for row in rows:
      if row.len >= 2:
        let (name, setting) = (row[0], row[1])
        case name
        of "data_directory":
          self.config["postgresql"]["data_dir"] = %setting
        of "listen_addresses":
          self.config["postgresql"]["listen"] = %setting
        of "port":
          self.config["postgresql"]["port"] = %setting
        else:
          pgParams[name] = %setting

    if pgParams.len > 0:
      self.config["postgresql"]["parameters"] = pgParams

  except PostgresConnectionException as e:
    logger.warning(fmt"Could not connect to database to query settings: {e.msg}")
  except CatchableError as e:
    logger.warning(fmt"Error querying database settings: {e.msg}")

  self.config["postgresql"]["bin_dir"] = %self.getBinDirFromRunningInstance()

proc generateConfig*(outputFile: string, sample: bool, dsn: Option[string] = none(string)) =
  ## Generate Patroni configuration file.
  ##
  ## :param outputFile: Output file path. Empty for stdout.
  ## :param sample: If true, generate sample config instead of from running instance.
  ## :param dsn: Optional DSN for connecting to running instance.
  try:
    let outFile = if outputFile.len > 0: some(outputFile) else: none(string)

    var generator: AbstractConfigGenerator
    if sample:
      generator = newSampleConfigGenerator(outFile)
    else:
      generator = newRunningClusterConfigGenerator(outFile, dsn)

    generator.writeConfig()
  except PatroniException as e:
    quit(e.msg, 1)
  except Exception as e:
    quit(fmt"Unexpected exception: {e.msg}", 1)

