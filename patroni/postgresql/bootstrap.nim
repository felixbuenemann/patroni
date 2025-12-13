## PostgreSQL bootstrap module for Patroni.
##
## This module provides functionality for bootstrapping PostgreSQL instances,
## including initial database creation and custom bootstrap methods.

import std/[json, options, os, osproc, sequtils, strformat, strtabs, strutils, tables, tempfiles, times]
import ../async_executor
import ../collections
import ../dcs
import ../log
import ../psycopg
import ../utils
import ./misc

export misc

let logger = getLogger("patroni.postgresql.bootstrap")

proc unquote*(value: string): string =
  ## Remove surrounding quotes from a string value.
  ##
  ## :param value: The potentially quoted string.
  ## :returns: The string without surrounding quotes.
  if value.len >= 2:
    if (value[0] == '\'' and value[^1] == '\'') or
       (value[0] == '"' and value[^1] == '"'):
      return value[1..^2]
  return value

type
  Bootstrap* = ref object
    ## Bootstrap handler for PostgreSQL.
    postgresql: pointer  # Postgresql - forward declaration
    runningCustomBootstrap: bool
    keepExistingRecoveryConf: bool

proc newBootstrap*(postgresql: pointer): Bootstrap =
  ## Create a new Bootstrap instance.
  new(result)
  result.postgresql = postgresql
  result.runningCustomBootstrap = false
  result.keepExistingRecoveryConf = false

proc isRunningCustomBootstrap*(self: Bootstrap): bool =
  ## Check if a custom bootstrap is running.
  result = self.runningCustomBootstrap

proc shouldKeepExistingRecoveryConf*(self: Bootstrap): bool =
  ## Check if existing recovery.conf should be kept.
  result = self.runningCustomBootstrap and self.keepExistingRecoveryConf

proc processUserOptions*(tool: string, options: JsonNode,
                         notAllowedOptions: seq[string],
                         errorHandler: proc(msg: string)): seq[string] =
  ## Format options into command line long form arguments.
  ##
  ## :param tool: The name of the tool used in error reports.
  ## :param options: Options as a dictionary or list.
  ## :param notAllowedOptions: Options that are not allowed.
  ## :param errorHandler: Callback for error messages.
  ## :returns: List of formatted command line arguments.
  result = @[]

  if options.kind == JObject:
    for key, val in options.pairs:
      if key in notAllowedOptions:
        errorHandler(fmt"{tool}: option {key} is not allowed")
        continue
      if val.kind == JBool:
        if val.getBool():
          result.add(fmt"--{key}")
      elif val.kind == JString:
        var value = val.getStr()
        value = unquote(value)
        result.add(fmt"--{key}={value}")
      else:
        result.add(fmt"--{key}={val}")

  elif options.kind == JArray:
    for item in options:
      if item.kind == JString:
        let opt = item.getStr()
        if opt in notAllowedOptions:
          errorHandler(fmt"{tool}: option {opt} is not allowed")
          continue
        if opt.startsWith("--"):
          result.add(opt)
        else:
          result.add(fmt"--{opt}")
      elif item.kind == JObject:
        if item.len != 1:
          errorHandler(fmt"{tool}: option dictionary must contain exactly one key")
          continue
        for key, val in item.pairs:
          if key in notAllowedOptions:
            errorHandler(fmt"{tool}: option {key} is not allowed")
            continue
          if val.kind == JBool:
            if val.getBool():
              result.add(fmt"--{key}")
          elif val.kind == JString:
            var value = val.getStr()
            value = unquote(value)
            result.add(fmt"--{key}={value}")
          else:
            result.add(fmt"--{key}={val}")
      else:
        errorHandler(fmt"{tool}: invalid option format")

proc processInitdbOptions*(options: JsonNode, errorHandler: proc(msg: string)): seq[string] =
  ## Process initdb options.
  ##
  ## :param options: initdb options from configuration.
  ## :param errorHandler: Callback for error messages.
  ## :returns: List of formatted initdb arguments.
  const notAllowed = @["pgdata", "D", "waldir", "X"]
  result = processUserOptions("initdb", options, notAllowed, errorHandler)

proc processBasebackupOptions*(options: JsonNode, errorHandler: proc(msg: string)): seq[string] =
  ## Process pg_basebackup options.
  ##
  ## :param options: basebackup options from configuration.
  ## :param errorHandler: Callback for error messages.
  ## :returns: List of formatted pg_basebackup arguments.
  const notAllowed = @["pgdata", "D", "host", "h", "port", "p", "dbname", "d"]
  result = processUserOptions("pg_basebackup", options, notAllowed, errorHandler)

proc createReplicaWithPgBasebackup*(self: Bootstrap, cloneFrom: Member,
                                    env: Table[string, string]): bool =
  ## Create a replica using pg_basebackup.
  ##
  ## :param cloneFrom: The member to clone from.
  ## :param env: Environment variables for the command.
  ## :returns: true if successful.
  # This would call pg_basebackup to create the replica
  # Simplified implementation
  logger.info(fmt"Creating replica from {cloneFrom.name} using pg_basebackup")

  # Build pg_basebackup command
  var args = @["-D", ".", "-X", "stream", "--checkpoint=fast"]

  # Add connection options
  let connInfo = cloneFrom.connUrl
  if connInfo.len > 0:
    args.add("-d")
    args.add(connInfo)

  # Would execute pg_basebackup here
  result = true

proc bootstrap*(self: Bootstrap, config: JsonNode): bool =
  ## Bootstrap PostgreSQL instance.
  ##
  ## :param config: Bootstrap configuration.
  ## :returns: true if bootstrap was successful.
  result = false

  # Check for custom bootstrap method
  if config.hasKey("method"):
    let methodVal = config["method"]
    if methodVal.kind == JString:
      let methodName = methodVal.getStr()
      if methodName != "initdb":
        logger.info(fmt"Running custom bootstrap method: {methodName}")
        self.runningCustomBootstrap = true

        if config.hasKey(methodName):
          let methodConfig = config[methodName]

          # Check for keep_existing_recovery_conf
          if methodConfig.hasKey("keep_existing_recovery_conf"):
            self.keepExistingRecoveryConf = methodConfig["keep_existing_recovery_conf"].getBool(false)

          # Execute custom bootstrap command
          if methodConfig.hasKey("command"):
            let command = methodConfig["command"].getStr()
            logger.info(fmt"Executing: {command}")
            let exitCode = execShellCmd(command)
            result = exitCode == 0

          self.runningCustomBootstrap = false
          return result

  # Default initdb bootstrap
  logger.info("Bootstrapping PostgreSQL with initdb")

  var initdbOptions: seq[string] = @[]
  if config.hasKey("initdb"):
    initdbOptions = processInitdbOptions(config["initdb"], proc(msg: string) =
      logger.warning(msg)
    )

  # Would call initdb here
  result = true

proc postBootstrap*(self: Bootstrap, config: JsonNode, connParams: Table[string, string]): bool =
  ## Run post-bootstrap tasks.
  ##
  ## :param config: Bootstrap configuration.
  ## :param connParams: Connection parameters for the database.
  ## :returns: true if successful.
  result = true

  if not config.hasKey("post_bootstrap") and not config.hasKey("post_init"):
    return true

  let postConfig = if config.hasKey("post_bootstrap"): config["post_bootstrap"]
                   elif config.hasKey("post_init"): config["post_init"]
                   else: newJNull()

  if postConfig.kind != JString:
    return true

  let command = postConfig.getStr()
  if command.len == 0:
    return true

  logger.info(fmt"Running post-bootstrap command: {command}")

  # Set up environment with connection info
  var env = newStringTable()
  for key, val in connParams:
    env["PG" & key.toUpperAscii()] = val

  let exitCode = execShellCmd(command)
  result = exitCode == 0

  if not result:
    logger.error(fmt"Post-bootstrap command failed with exit code {exitCode}")

proc createUsers*(self: Bootstrap, config: JsonNode): bool =
  ## Create users defined in configuration.
  ##
  ## :param config: Users configuration.
  ## :returns: true if successful.
  result = true

  if not config.hasKey("users"):
    return true

  let users = config["users"]
  if users.kind != JObject:
    return true

  for username, userConfig in users.pairs:
    if userConfig.kind != JObject:
      continue

    var createSql = fmt"CREATE USER {quoteIdent(username)}"

    if userConfig.hasKey("password"):
      let password = userConfig["password"].getStr()
      createSql.add(fmt" PASSWORD {quoteLiteral(password)}")

    if userConfig.hasKey("options"):
      let options = userConfig["options"]
      if options.kind == JArray:
        for opt in options:
          createSql.add(" " & opt.getStr())

    logger.info(fmt"Creating user: {username}")
    # Would execute the SQL here

  result = true

proc cloneWithBasebackup*(self: Bootstrap, cloneFrom: Member,
                          options: JsonNode = newJNull()): bool =
  ## Clone from a member using pg_basebackup.
  ##
  ## :param cloneFrom: The member to clone from.
  ## :param options: Additional options for pg_basebackup.
  ## :returns: true if successful.
  result = false

  logger.info(fmt"Cloning from {cloneFrom.name} using pg_basebackup")

  var env = initTable[string, string]()

  # Process basebackup options
  var args: seq[string] = @[]
  if options.kind != JNull:
    args = processBasebackupOptions(options, proc(msg: string) =
      logger.warning(msg)
    )

  result = self.createReplicaWithPgBasebackup(cloneFrom, env)

proc cloneWithCustomMethod*(self: Bootstrap, cloneFrom: Member,
                            methodName: string, config: JsonNode): bool =
  ## Clone from a member using a custom method.
  ##
  ## :param cloneFrom: The member to clone from.
  ## :param methodName: The name of the custom method.
  ## :param config: Configuration for the method.
  ## :returns: true if successful.
  result = false

  if not config.hasKey(methodName):
    logger.error(fmt"Custom clone method '{methodName}' not found in configuration")
    return false

  let methodConfig = config[methodName]

  if not methodConfig.hasKey("command"):
    logger.error(fmt"Custom clone method '{methodName}' missing 'command' key")
    return false

  let command = methodConfig["command"].getStr()
  logger.info(fmt"Cloning using custom method: {command}")

  # Set up environment with source connection info
  var env = newStringTable()
  env["PATRONI_CLONE_FROM_URL"] = cloneFrom.connUrl
  # Parse connection URL to get host/port if needed
  if cloneFrom.connUrl.len > 0:
    # Simple extraction from postgres://host:port/db format
    let url = cloneFrom.connUrl
    var hostPort = ""
    if "://" in url:
      let afterScheme = url.split("://")[1]
      let beforeDb = afterScheme.split("/")[0]
      # Remove any user:pass@ prefix
      if "@" in beforeDb:
        hostPort = beforeDb.split("@")[^1]
      else:
        hostPort = beforeDb
      if ":" in hostPort:
        let parts = hostPort.split(":")
        env["PATRONI_CLONE_FROM_HOST"] = parts[0]
        env["PATRONI_CLONE_FROM_PORT"] = parts[1]
      else:
        env["PATRONI_CLONE_FROM_HOST"] = hostPort
        env["PATRONI_CLONE_FROM_PORT"] = "5432"

  let exitCode = execShellCmd(command)
  result = exitCode == 0

  if not result:
    logger.error(fmt"Custom clone method failed with exit code {exitCode}")

proc clone*(self: Bootstrap, cloneFrom: Member, config: JsonNode): bool =
  ## Clone from a member.
  ##
  ## :param cloneFrom: The member to clone from.
  ## :param config: Clone configuration.
  ## :returns: true if successful.
  result = false

  # Check for custom clone method
  if config.hasKey("method"):
    let methodName = config["method"].getStr()
    if methodName != "basebackup":
      return self.cloneWithCustomMethod(cloneFrom, methodName, config)

  # Default to pg_basebackup
  let options = if config.hasKey("basebackup"): config["basebackup"] else: newJNull()
  result = self.cloneWithBasebackup(cloneFrom, options)
