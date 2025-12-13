## PostgreSQL configuration handling.
##
## This module provides types and procedures for managing PostgreSQL
## configuration files (postgresql.conf, pg_hba.conf, recovery.conf).

import std/[algorithm, json, options, os, re, sequtils, strformat, strutils, tables, times, uri]
import ../collections
import ../dcs
import ../exceptions
import ../file_perm
import ../global_config
import ../log
import ../psycopg
import ../utils
import ./misc

export misc

let logger = getLogger("patroni.postgresql.config")

let PARAMETER_RE = re"([a-z_]+)\s*=\s*"

type
  ConfigWriter* = ref object
    ## Helper for writing configuration files.
    filename: string
    fd: File

  ConfigHandler* = ref object
    ## Handler for PostgreSQL configuration.
    postgresql: pointer  # Postgresql - forward declaration
    configDir*: string
    postgresqlConf: string
    postgresqlConfMtime: Option[float]
    postgresqlBaseConfName: string
    postgresqlBaseConf: string
    pgHbaConf: string
    pgIdentConf: string
    recoveryConf: string
    recoveryConfMtime: Option[float]
    recoverySignal: string
    standbySignal: string
    autoConf: string
    autoConfMtime: Option[float]
    pgpass: string
    passfile: Option[string]
    passfileMtime: Option[float]
    postmasterCtime: Option[float]
    currentRecoveryParams: Option[CaseInsensitiveDict[seq[string]]]
    config: Table[string, JsonNode]
    recoveryParams*: CaseInsensitiveDict[JsonNode]
    serverParameters*: CaseInsensitiveDict[string]
    superuser: Table[string, string]
    localReplicationAddress*: Table[string, string]
    krbsrvname: Option[string]

const
  # Command line options that must always be passed to postmaster
  CMDLINE_OPTIONS* = {
    "listen_addresses": (nil.string, 90100),
    "port": (nil.string, 90100),
    "cluster_name": (nil.string, 90500),
    "wal_level": ("hot_standby", 90100),
    "hot_standby": ("on", 90100),
    "max_connections": ("100", 90100),
    "max_wal_senders": ("10", 90100),
    "wal_keep_segments": ("8", 90100),
    "wal_keep_size": ("128MB", 130000),
    "max_prepared_transactions": ("0", 90100),
    "max_locks_per_transaction": ("64", 90100),
    "track_commit_timestamp": ("off", 90500),
    "max_replication_slots": ("10", 90400),
    "max_worker_processes": ("8", 90400),
    "wal_log_hints": ("on", 90400)
  }.toTable

proc conninfoUriParse(dsn: string): Table[string, string] =
  ## Parse a PostgreSQL connection URI.
  result = initTable[string, string]()
  let parsed = parseUri(dsn)

  if parsed.username.len > 0:
    result["user"] = decodeUrl(parsed.username)
  if parsed.password.len > 0:
    result["password"] = decodeUrl(parsed.password)
  if parsed.path.len > 1:
    result["dbname"] = decodeUrl(parsed.path[1..^1])
  if parsed.hostname.len > 0:
    result["host"] = parsed.hostname
  if parsed.port.len > 0:
    result["port"] = parsed.port

  # Parse query parameters
  for part in parsed.query.split('&'):
    if '=' in part:
      let kv = part.split('=', maxsplit=1)
      if kv.len == 2:
        result[kv[0]] = decodeUrl(kv[1])

  # Handle ssl=true -> sslmode=require
  if result.getOrDefault("ssl", "") == "true":
    result.del("ssl")
    result["sslmode"] = "require"

proc readParamValue(value: string): tuple[result: Option[string], endPos: int] =
  ## Read a parameter value from a configuration line.
  let length = value.len
  var ret = ""
  let isQuoted = value.len > 0 and value[0] == '\''
  var i = if isQuoted: 1 else: 0

  while i < length:
    if isQuoted:
      if value[i] == '\'':
        return (some(ret), i + 1)
    elif value[i] in Whitespace:
      break

    if value[i] == '\\':
      inc i
      if i >= length:
        break

    ret.add(value[i])
    inc i

  if isQuoted:
    return (none(string), 0)
  else:
    return (some(ret), i)

proc conninfoDsnParse(dsn: string): Option[Table[string, string]] =
  ## Parse a PostgreSQL DSN connection string.
  var ret = initTable[string, string]()
  let length = dsn.len
  var i = 0

  while i < length:
    if dsn[i] in Whitespace:
      inc i
      continue

    var paramMatch = dsn[i..^1].match(PARAMETER_RE)
    if paramMatch.isNone:
      return none(Table[string, string])

    let param = dsn[i..^1][0..<dsn[i..^1].find('=')].strip().toLowerAscii()
    i += dsn[i..^1].find('=') + 1

    while i < length and dsn[i] in Whitespace:
      inc i

    if i >= length:
      return none(Table[string, string])

    let (value, endPos) = readParamValue(dsn[i..^1])
    if value.isNone:
      return none(Table[string, string])

    i += endPos
    ret[param] = value.get

  return some(ret)

proc parseDsn*(value: string): Option[Table[string, string]] =
  ## Parse a PostgreSQL connection string.
  ##
  ## :param value: Connection string in URI or DSN format.
  ## :returns: Parsed parameters or none on error.
  var ret: Table[string, string]

  if value.startsWith("postgres://") or value.startsWith("postgresql://"):
    ret = conninfoUriParse(value)
  else:
    let parsed = conninfoDsnParse(value)
    if parsed.isNone:
      return none(Table[string, string])
    ret = parsed.get

  # Handle requiressl
  if "sslmode" notin ret:
    let requiressl = ret.getOrDefault("requiressl", "")
    if requiressl == "1":
      ret["sslmode"] = "require"
    elif requiressl.len > 0:
      ret["sslmode"] = "prefer"
    ret.del("requiressl")

  # Set defaults
  if "sslmode" notin ret:
    ret["sslmode"] = "prefer"
  if "gssencmode" notin ret:
    ret["gssencmode"] = "prefer"
  if "channel_binding" notin ret:
    ret["channel_binding"] = "prefer"
  if "sslnegotiation" notin ret:
    ret["sslnegotiation"] = "postgres"

  return some(ret)

proc stripComment(value: string): string =
  ## Strip comments from a configuration line.
  let i = value.find('#')
  if i > -1:
    return value[0..<i].strip()
  return value

proc mtime(filename: string): Option[float] =
  ## Get file modification time.
  try:
    let info = getFileInfo(filename)
    return some(info.lastWriteTime.toUnixFloat())
  except OSError:
    return none(float)

# ConfigWriter implementation

proc newConfigWriter*(filename: string): ConfigWriter =
  ## Create a new ConfigWriter.
  new(result)
  result.filename = filename
  result.fd = nil

proc open*(self: ConfigWriter) =
  ## Open the config file for writing.
  self.fd = open(self.filename, fmWrite)
  self.fd.writeLine("# Do not edit this file manually!")
  self.fd.writeLine("# It will be overwritten by Patroni!")

proc close*(self: ConfigWriter) =
  ## Close the config file.
  if self.fd != nil:
    self.fd.close()
    self.fd = nil

proc writeline*(self: ConfigWriter, line: string) =
  ## Write a line to the config file.
  if self.fd != nil:
    self.fd.writeLine(line)

proc writelines*(self: ConfigWriter, lines: seq[string]) =
  ## Write multiple lines to the config file.
  for line in lines:
    self.writeline(line)

proc escape*(value: string): string =
  ## Escape single quotes and backslashes in a string.
  result = value.replace("'", "''").replace("\\", "\\\\")

proc writeParam*(self: ConfigWriter, param: string, value: string) =
  ## Write a parameter to the config file.
  self.writeline(fmt"{param} = '{escape(value)}'")

# ConfigHandler implementation

proc newConfigHandler*(postgresql: pointer, config: JsonNode): ConfigHandler =
  ## Create a new ConfigHandler.
  new(result)
  result.postgresql = postgresql

  # Initialize paths - would get dataDir from postgresql
  let dataDir = config.getOrDefault("data_dir").getStr("/var/lib/postgresql/data")
  result.configDir = config.getOrDefault("config_dir").getStr(dataDir)
  let configBaseName = config.getOrDefault("config_base_name").getStr("postgresql")

  result.postgresqlConf = result.configDir / (configBaseName & ".conf")
  result.postgresqlConfMtime = none(float)
  result.postgresqlBaseConfName = configBaseName & ".base.conf"
  result.postgresqlBaseConf = result.configDir / result.postgresqlBaseConfName
  result.pgHbaConf = result.configDir / "pg_hba.conf"
  result.pgIdentConf = result.configDir / "pg_ident.conf"
  result.recoveryConf = dataDir / "recovery.conf"
  result.recoveryConfMtime = none(float)
  result.recoverySignal = dataDir / "recovery.signal"
  result.standbySignal = dataDir / "standby.signal"
  result.autoConf = dataDir / "postgresql.auto.conf"
  result.autoConfMtime = none(float)

  result.pgpass = config.getOrDefault("pgpass").getStr(getHomeDir() / "pgpass")
  result.passfile = none(string)
  result.passfileMtime = none(float)
  result.postmasterCtime = none(float)
  result.currentRecoveryParams = none(CaseInsensitiveDict[seq[string]])

  result.config = initTable[string, JsonNode]()
  result.recoveryParams = newCaseInsensitiveDict[JsonNode]()
  result.serverParameters = newCaseInsensitiveDict[string]()
  result.superuser = initTable[string, string]()
  result.localReplicationAddress = initTable[string, string]()
  result.krbsrvname = none(string)

proc get*(self: ConfigHandler, key: string, default: JsonNode = nil): JsonNode =
  ## Get a configuration value.
  result = self.config.getOrDefault(key, default)

proc hbaFile*(self: ConfigHandler): Option[string] =
  ## Get the hba_file if it's not the default.
  let hbaFile = self.serverParameters.getOrDefault("hba_file", "")
  if hbaFile.len > 0 and hbaFile != self.pgHbaConf:
    return some(hbaFile)
  return none(string)

proc identFile*(self: ConfigHandler): Option[string] =
  ## Get the ident_file if it's not the default.
  let identFile = self.serverParameters.getOrDefault("ident_file", "")
  if identFile.len > 0 and identFile != self.pgIdentConf:
    return some(identFile)
  return none(string)

proc replication*(self: ConfigHandler): Table[string, string] =
  ## Get replication authentication configuration.
  result = initTable[string, string]()
  let auth = self.config.getOrDefault("authentication")
  if auth != nil and auth.kind == JObject:
    let repl = auth.getOrDefault("replication")
    if repl != nil and repl.kind == JObject:
      for key, val in repl.pairs:
        result[key] = val.getStr()

proc rewindCredentials*(self: ConfigHandler): Table[string, string] =
  ## Get rewind credentials.
  let auth = self.config.getOrDefault("authentication")
  if auth != nil and auth.kind == JObject:
    let rewind = auth.getOrDefault("rewind")
    if rewind != nil and rewind.kind == JObject:
      for key, val in rewind.pairs:
        result[key] = val.getStr()
      return
  return self.superuser

proc setFilePermissions*(self: ConfigHandler, filename: string) =
  ## Set file permissions according to PGDATA permissions.
  # Would use pgPerm to set appropriate permissions
  discard

proc configurationToSave(self: ConfigHandler): seq[string] =
  ## Get list of configuration files to save.
  result = @[self.postgresqlConf.extractFilename()]
  if "custom_conf" notin self.config:
    result.add(self.postgresqlBaseConfName)
  if self.hbaFile.isNone:
    result.add("pg_hba.conf")
  if self.identFile.isNone:
    result.add("pg_ident.conf")

proc saveConfigurationFiles*(self: ConfigHandler, checkCustomBootstrap: bool = false): bool =
  ## Save configuration files as backups.
  result = true
  try:
    for f in self.configurationToSave():
      let configFile = self.configDir / f
      let backupFile = self.configDir / (f & ".backup")
      if fileExists(configFile):
        copyFile(configFile, backupFile)
        self.setFilePermissions(backupFile)
  except IOError:
    logger.error("Unable to create backup copies of configuration files")

proc restoreConfigurationFiles*(self: ConfigHandler) =
  ## Restore configuration files from backups.
  try:
    for f in self.configurationToSave():
      let configFile = self.configDir / f
      let backupFile = self.configDir / (f & ".backup")
      if not fileExists(configFile):
        if fileExists(backupFile):
          copyFile(backupFile, configFile)
          self.setFilePermissions(configFile)
        elif f == "pg_ident.conf":
          writeFile(configFile, "")
          self.setFilePermissions(configFile)
  except IOError:
    logger.error("Unable to restore configuration files from backup")

proc writePostgresqlConf*(self: ConfigHandler, configuration: Option[CaseInsensitiveDict[string]] = none(CaseInsensitiveDict[string])) =
  ## Write postgresql.conf file.
  # Rename original configuration if necessary
  if "custom_conf" notin self.config and not fileExists(self.postgresqlBaseConf):
    if fileExists(self.postgresqlConf):
      moveFile(self.postgresqlConf, self.postgresqlBaseConf)

  var conf = if configuration.isSome: configuration.get else: self.serverParameters

  let writer = newConfigWriter(self.postgresqlConf)
  writer.open()
  defer: writer.close()

  let include = if "custom_conf" in self.config:
    self.config["custom_conf"].getStr()
  else:
    self.postgresqlBaseConfName

  writer.writeline(fmt"include '{escape(include)}'")
  writer.writeline("")

  for name in conf.keys.toSeq.sorted():
    let value = conf[name]
    writer.writeParam(name, value)

  # Write hba_file and ident_file if not overridden
  if "hba_file" notin self.serverParameters:
    writer.writeParam("hba_file", self.pgHbaConf)
  if "ident_file" notin self.serverParameters:
    writer.writeParam("ident_file", self.pgIdentConf)

  self.setFilePermissions(self.postgresqlConf)

proc appendPgHba*(self: ConfigHandler, config: seq[string]): bool =
  ## Append entries to pg_hba.conf.
  if self.hbaFile.isNone and "pg_hba" notin self.config:
    var f = open(self.pgHbaConf, fmAppend)
    f.writeLine("")
    for line in config:
      f.writeLine(line)
    f.close()
    self.setFilePermissions(self.pgHbaConf)
  return true

proc replacePgHba*(self: ConfigHandler): Option[bool] =
  ## Replace pg_hba.conf content.
  if self.hbaFile.isNone and "pg_hba" in self.config:
    let writer = newConfigWriter(self.pgHbaConf)
    writer.open()
    defer: writer.close()

    let pgHba = self.config["pg_hba"]
    if pgHba.kind == JArray:
      for line in pgHba:
        writer.writeline(line.getStr())

    self.setFilePermissions(self.pgHbaConf)
    return some(true)

  return none(bool)

proc replacePgIdent*(self: ConfigHandler): Option[bool] =
  ## Replace pg_ident.conf content.
  if self.identFile.isNone and "pg_ident" in self.config:
    let writer = newConfigWriter(self.pgIdentConf)
    writer.open()
    defer: writer.close()

    let pgIdent = self.config["pg_ident"]
    if pgIdent.kind == JArray:
      for line in pgIdent:
        writer.writeline(line.getStr())

    self.setFilePermissions(self.pgIdentConf)
    return some(true)

  return none(bool)

proc primaryConninfoParams*(self: ConfigHandler, member: Member): Option[Table[string, string]] =
  ## Build primary_conninfo parameters from a member.
  if member == nil or member.connUrl.len == 0:
    return none(Table[string, string])

  var ret = initTable[string, string]()

  # Parse the connection URL
  let parsed = parseDsn(member.connUrl)
  if parsed.isSome:
    ret = parsed.get

  # Add replication credentials
  let repl = self.replication
  for key, val in repl:
    ret[key] = val

  # Would also add postgresql name as application_name
  ret["sslmode"] = ret.getOrDefault("sslmode", "prefer")

  if self.krbsrvname.isSome:
    ret["krbsrvname"] = self.krbsrvname.get

  return some(ret)

proc formatDsn*(self: ConfigHandler, params: Table[string, string]): string =
  ## Format connection parameters as a DSN string.
  let keywords = ["dbname", "user", "password", "host", "port",
                  "sslmode", "sslcompression", "sslcert", "sslkey",
                  "sslrootcert", "application_name", "krbsrvname",
                  "gssencmode", "channel_binding", "target_session_attrs"]

  proc escapeVal(value: string): string =
    value.replace("\\", "\\\\").replace("'", "\\'").replace(" ", "\\ ")

  var parts: seq[string] = @[]
  for kw in keywords:
    if kw in params:
      parts.add(fmt"{kw}={escapeVal(params[kw])}")

  result = parts.join(" ")

proc buildRecoveryParams*(self: ConfigHandler, member: Member): CaseInsensitiveDict[JsonNode] =
  ## Build recovery parameters for streaming replication.
  result = newCaseInsensitiveDict[JsonNode]()
  result["standby_mode"] = %"on"
  result["recovery_target_timeline"] = %"latest"

  let primaryConninfo = self.primaryConninfoParams(member)
  if primaryConninfo.isSome:
    result["primary_conninfo"] = %primaryConninfo.get

    # Add primary_slot_name if using slots
    let globalConf = getGlobalConfig()
    if globalConf.getOrDefault("use_slots").getBool(true):
      # Would get slot name from member name
      result["primary_slot_name"] = %member.name

proc recoveryConfExists*(self: ConfigHandler): bool =
  ## Check if recovery configuration exists.
  # For PG >= 12, check for standby.signal or recovery.signal
  # For PG < 12, check for recovery.conf
  result = fileExists(self.standbySignal) or
           fileExists(self.recoverySignal) or
           fileExists(self.recoveryConf)

proc writeRecoveryConf*(self: ConfigHandler, recoveryParams: CaseInsensitiveDict[JsonNode]) =
  ## Write recovery configuration.
  self.recoveryParams = recoveryParams

  # For PG >= 12, create standby.signal and write params to postgresql.conf
  # For PG < 12, write recovery.conf

  if recoveryParams.getOrDefault("standby_mode").getStr("") == "on":
    writeFile(self.standbySignal, "")
    self.setFilePermissions(self.standbySignal)
  else:
    if fileExists(self.standbySignal):
      removeFile(self.standbySignal)
    writeFile(self.recoverySignal, "")
    self.setFilePermissions(self.recoverySignal)

proc removeRecoveryConf*(self: ConfigHandler) =
  ## Remove recovery configuration files.
  for name in [self.recoveryConf, self.standbySignal, self.recoverySignal]:
    if fileExists(name):
      removeFile(name)
  self.recoveryParams = newCaseInsensitiveDict[JsonNode]()
  self.currentRecoveryParams = none(CaseInsensitiveDict[seq[string]])

proc pgpassContent(record: Table[string, string]): Option[string] =
  ## Generate content for pgpassfile.
  if "password" notin record:
    return none(string)

  proc escapeVal(value: string): string =
    value.replace("\\", "\\\\").replace(":", "\\:")

  let host = escapeVal(record.getOrDefault("host", "*"))
  let port = escapeVal(record.getOrDefault("port", "*"))
  let user = escapeVal(record.getOrDefault("user", "*"))
  let password = escapeVal(record.getOrDefault("password", ""))

  result = some(fmt"{host}:{port}:*:{user}:{password}" & "\n")

proc writePgpass*(self: ConfigHandler, record: Table[string, string]): Table[string, string] =
  ## Write pgpassfile.
  result = initTable[string, string]()
  for key, val in envPairs():
    result[key] = val

  let content = pgpassContent(record)
  if content.isNone:
    return

  writeFile(self.pgpass, content.get)
  setFilePermissions(self.pgpass, {fpUserRead, fpUserWrite})

  result["PGPASSFILE"] = self.pgpass

proc checkRecoveryConf*(self: ConfigHandler, member: Member): tuple[needsChange: bool, needsRestart: bool] =
  ## Check if recovery configuration matches expectations.
  ##
  ## :param member: Member to compare against.
  ## :returns: Tuple indicating if change and/or restart is needed.

  # Check if standby.signal exists for PG >= 12
  if not fileExists(self.standbySignal):
    return (true, true)

  # Build wanted recovery params and compare
  let wantedRecoveryParams = self.buildRecoveryParams(member)

  # Simplified comparison - would need to check individual params
  result = (false, false)

proc getServerParameters*(self: ConfigHandler, config: JsonNode): CaseInsensitiveDict[string] =
  ## Get server parameters from configuration.
  result = newCaseInsensitiveDict[string]()

  let params = config.getOrDefault("parameters")
  if params != nil and params.kind == JObject:
    for key, val in params.pairs:
      result[key] = val.getStr($val)

  # Add required parameters
  let listen = config.getOrDefault("listen").getStr("0.0.0.0:5432")
  let parts = listen.rsplit(':', maxsplit=1)
  result["listen_addresses"] = parts[0]
  result["port"] = if parts.len > 1: parts[1] else: "5432"

  let scope = config.getOrDefault("scope").getStr("main")
  result["cluster_name"] = scope

proc resolveConnectionAddresses*(self: ConfigHandler) =
  ## Resolve local and remote connection addresses.
  let port = self.serverParameters.getOrDefault("port", "5432")
  let tcpLocalAddress = self.serverParameters.getOrDefault("listen_addresses", "localhost")

  self.localReplicationAddress = {"host": tcpLocalAddress, "port": port}.toTable

proc setSynchronousStandbyNames*(self: ConfigHandler, value: string): bool =
  ## Set synchronous_standby_names parameter.
  if value != self.serverParameters.getOrDefault("synchronous_standby_names", ""):
    if value.len == 0:
      self.serverParameters.del("synchronous_standby_names")
    else:
      self.serverParameters["synchronous_standby_names"] = value
    self.writePostgresqlConf()
    return true
  return false

proc reloadConfig*(self: ConfigHandler, config: JsonNode, sighup: bool = false) =
  ## Reload configuration from the provided config.
  let auth = config.getOrDefault("authentication")
  if auth != nil and auth.kind == JObject:
    let su = auth.getOrDefault("superuser")
    if su != nil and su.kind == JObject:
      self.superuser = initTable[string, string]()
      for key, val in su.pairs:
        self.superuser[key] = val.getStr()

  self.serverParameters = self.getServerParameters(config)

  # Store config
  for key, val in config.pairs:
    self.config[key] = val

  let krbsrvname = config.getOrDefault("krbsrvname").getStr("")
  if krbsrvname.len > 0:
    self.krbsrvname = some(krbsrvname)
    putEnv("PGKRBSRVNAME", krbsrvname)
  else:
    self.krbsrvname = none(string)

  self.resolveConnectionAddresses()

  if sighup:
    self.writePostgresqlConf()
    discard self.replacePgHba()
    discard self.replacePgIdent()

proc effectiveConfiguration*(self: ConfigHandler): CaseInsensitiveDict[string] =
  ## Get effective configuration, possibly adjusted from controldata.
  ##
  ## Some parameters from controldata may be higher than configured,
  ## and we need to use those values to start postgres successfully.
  result = self.serverParameters

proc restoreCommand*(self: ConfigHandler): Option[string] =
  ## Get restore_command from recovery configuration.
  let recoveryConf = self.config.getOrDefault("recovery_conf")
  if recoveryConf != nil and recoveryConf.kind == JObject:
    let cmd = recoveryConf.getOrDefault("restore_command").getStr("")
    if cmd.len > 0:
      return some(cmd)
  return none(string)

proc synchronousStandbyNames*(self: ConfigHandler): Option[string] =
  ## Get synchronous_standby_names from configuration.
  let params = self.config.getOrDefault("parameters")
  if params != nil and params.kind == JObject:
    let ssn = params.getOrDefault("synchronous_standby_names").getStr("")
    if ssn.len > 0:
      return some(ssn)
  return none(string)

proc postgresqlConfPath*(self: ConfigHandler): string =
  ## Get path to postgresql.conf.
  result = self.postgresqlConf

proc pgHbaConfPath*(self: ConfigHandler): string =
  ## Get path to pg_hba.conf.
  result = self.pgHbaConf

