## PostgreSQL management module for Patroni.
##
## This module provides the main PostgreSQL class that handles all PostgreSQL operations
## including starting, stopping, promoting, and monitoring the PostgreSQL instance.

import std/[json, locks, options, os, osproc, re, sequtils, strformat, strutils, tables, times]
import ../async_executor
import ../collections
import ../config as patroni_config
import ../dcs
import ../exceptions
import ../global_config
import ../log
import ../psycopg
import ../tags
import ../utils
import ./callback_executor
import ./cancellable
import ./connection
import ./misc

export callback_executor, cancellable, connection, misc

let logger = getLogger("patroni.postgresql")

const
  STOP_POLLING_INTERVAL* = 1

type
  PgIsReadyStatus* = enum
    ## Possible PostgreSQL connection status pg_isready utility can report.
    pisRunning = 0     ## PostgreSQL is accepting connections normally.
    pisReject = 1      ## PostgreSQL is rejecting connections.
    pisNoResponse = 2  ## There was no response to the connection attempt.
    pisUnknown = 3     ## No connection attempt was made, something went wrong.

  Postgresql* = ref object
    ## Main PostgreSQL management class.
    name*: string
    scope*: string
    dataDir: string
    database: string
    versionFile: string
    pgControl: string
    connectionString: string
    proxyUrl: Option[string]
    majorVersion: int
    stateLock: Lock
    state: PostgresqlState
    pendingRestartReason: CaseInsensitiveDict[JsonNode]
    connectionPool: ConnectionPool
    connection: Connection
    binDir: string
    roleLock: Lock
    role: PostgresqlRole
    config*: JsonNode
    bootstrapping*: bool
    slotsHandler: pointer  # SlotsHandler
    syncHandler: pointer   # SyncHandler
    callbackExecutor: CallbackExecutor
    cbCalled: bool
    cbPending: Option[CallbackAction]
    cancellable*: CancellableSubprocess
    sysid: string
    stateEntryTimestamp: float
    clusterInfoState: Table[string, JsonNode]
    shouldQuerySlots: bool
    enforceHotStandbyFeedback: bool
    cachedReplicaTimeline: Option[int]
    postmasterProc: Option[Process]
    availableGucs: Option[CaseInsensitiveSet]

const
  POSTMASTER_START_TIME* = "pg_catalog.pg_postmaster_start_time()"

proc newPostgresql*(config: JsonNode): Postgresql =
  ## Create a new Postgresql instance.
  new(result)
  result.name = config["name"].getStr("")
  result.scope = config["scope"].getStr("")
  result.dataDir = config["data_dir"].getStr("")
  result.database = config.getOrDefault("database").getStr("postgres")
  result.versionFile = result.dataDir / "PG_VERSION"
  result.pgControl = result.dataDir / "global" / "pg_control"
  result.majorVersion = 0
  result.bootstrapping = false
  result.sysid = ""
  result.stateEntryTimestamp = 0
  result.shouldQuerySlots = true
  result.enforceHotStandbyFeedback = false
  result.cachedReplicaTimeline = none(int)
  result.postmasterProc = none(Process)
  result.availableGucs = none(CaseInsensitiveSet)
  result.clusterInfoState = initTable[string, JsonNode]()
  result.pendingRestartReason = newCaseInsensitiveDict[JsonNode]()
  result.config = config
  result.cbCalled = false
  result.cbPending = none(CallbackAction)

  initLock(result.stateLock)
  initLock(result.roleLock)

  result.setState(psStopped)
  result.connectionPool = newConnectionPool()
  result.connection = result.connectionPool.get("heartbeat")
  result.callbackExecutor = newCallbackExecutor()
  result.cancellable = newCancellableSubprocess()

  result.binDir = config.getOrDefault("bin_dir").getStr("")

  result.setRole(prUninitialized)
  result.majorVersion = result.getMajorVersion()
  result.setRole(result.getPostgresRoleFromDataDirectory())

proc walName*(self: Postgresql): string =
  ## Get the WAL directory name based on version.
  if self.majorVersion >= 100000:
    result = "wal"
  else:
    result = "xlog"

proc walFlush*(self: Postgresql): string =
  ## For PostgreSQL 9.6 onwards we want to use pg_current_wal_flush_lsn().
  if self.majorVersion >= 90600:
    result = "_flush"
  else:
    result = ""

proc lsnName*(self: Postgresql): string =
  ## Get the LSN column name based on version.
  if self.majorVersion >= 100000:
    result = "lsn"
  else:
    result = "location"

proc walDir*(self: Postgresql): string =
  ## Get the WAL directory path.
  result = self.dataDir / ("pg_" & self.walName)

proc supportsQuorumCommit*(self: Postgresql): bool =
  ## True if quorum commit is supported by Postgres.
  result = self.majorVersion >= 100000

proc supportsMultipleSync*(self: Postgresql): bool =
  ## True if Postgres version supports more than one synchronous node.
  result = self.majorVersion >= 90600

proc canAdvanceSlots*(self: Postgresql): bool =
  ## True if majorVersion is greater than 110000.
  result = self.majorVersion >= 110000

proc versionFileExists(self: Postgresql): bool =
  ## Check if PG_VERSION file exists.
  result = not self.dataDirectoryEmpty() and fileExists(self.versionFile)

proc getMajorVersion*(self: Postgresql): int =
  ## Reads major version from PG_VERSION file.
  ##
  ## :returns: major PostgreSQL version in integer format or 0 in case of missing file or errors.
  if self.versionFileExists():
    try:
      let content = readFile(self.versionFile).strip()
      result = postgresMajorVersionToInt(content)
    except IOError as e:
      logger.exception(fmt"Failed to read PG_VERSION from {self.dataDir}", e)
      result = 0
  else:
    result = 0

proc pgCommand*(self: Postgresql, cmd: string): string =
  ## Return path to the specified PostgreSQL command.
  ##
  ## :param cmd: the Postgres binary name to get path to.
  ## :returns: path to Postgres binary named cmd.
  result = self.binDir / cmd

proc pgCtl*(self: Postgresql, cmd: string, args: varargs[string]): bool =
  ## Builds and executes pg_ctl command.
  ##
  ## :returns: true when return_code == 0, otherwise false.
  var cmdArgs = @[self.pgCommand("pg_ctl"), cmd, "-D", self.dataDir]
  for arg in args:
    cmdArgs.add(arg)

  let process = startProcess(cmdArgs[0], args = cmdArgs[1..^1])
  let exitCode = process.waitForExit()
  process.close()
  result = exitCode == 0

proc initdb*(self: Postgresql, args: varargs[string]): bool =
  ## Builds and executes the initdb command.
  ##
  ## :returns: true if the exit code is 0.
  var cmdArgs = @[self.pgCommand("initdb")]
  for arg in args:
    cmdArgs.add(arg)
  cmdArgs.add(self.dataDir)

  let process = startProcess(cmdArgs[0], args = cmdArgs[1..^1])
  let exitCode = process.waitForExit()
  process.close()
  result = exitCode == 0

proc pgIsReady*(self: Postgresql): PgIsReadyStatus =
  ## Runs pg_isready to see if PostgreSQL is accepting connections.
  ##
  ## :returns: one of PgIsReadyStatus values.
  let connKwargs = self.connectionPool.connKwargs
  var cmdArgs = @[self.pgCommand("pg_isready"), "-p", connKwargs.getOrDefault("port", "5432"), "-d", self.database]

  if "host" in connKwargs:
    cmdArgs.add("-h")
    cmdArgs.add(connKwargs["host"])

  if "user" in connKwargs:
    cmdArgs.add("-U")
    cmdArgs.add(connKwargs["user"])

  let process = startProcess(cmdArgs[0], args = cmdArgs[1..^1])
  let exitCode = process.waitForExit()
  process.close()

  case exitCode
  of 0: result = pisRunning
  of 1: result = pisReject
  of 2: result = pisNoResponse
  else: result = pisUnknown

proc pgControlExists*(self: Postgresql): bool =
  ## Check if pg_control file exists.
  result = fileExists(self.pgControl)

proc dataDirectoryEmpty*(self: Postgresql): bool =
  ## Check if the data directory is empty or doesn't have pg_control.
  if self.pgControlExists():
    return false
  result = dataDirectoryIsEmpty(self.dataDir)

proc getPostgresRoleFromDataDirectory*(self: Postgresql): PostgresqlRole =
  ## Determine PostgreSQL role from data directory state.
  if self.dataDirectoryEmpty():
    return prUninitialized

  # Check for recovery.conf or standby.signal to determine if replica
  let recoveryConf = self.dataDir / "recovery.conf"
  let standbySignal = self.dataDir / "standby.signal"

  if fileExists(recoveryConf) or fileExists(standbySignal):
    return prReplica
  else:
    return prPrimary

proc getState*(self: Postgresql): PostgresqlState =
  ## Get the current state.
  withLock(self.stateLock):
    result = self.state

proc setState*(self: Postgresql, value: PostgresqlState) =
  ## Set the current state.
  withLock(self.stateLock):
    self.state = value
    self.stateEntryTimestamp = epochTime()

proc getRole*(self: Postgresql): PostgresqlRole =
  ## Get the current role.
  withLock(self.roleLock):
    result = self.role

proc setRole*(self: Postgresql, value: PostgresqlRole) =
  ## Set the current role.
  withLock(self.roleLock):
    self.role = value

proc timeInState*(self: Postgresql): float =
  ## Get time spent in current state.
  result = epochTime() - self.stateEntryTimestamp

proc isStarting*(self: Postgresql): bool =
  ## Check if PostgreSQL is starting.
  result = self.getState() in [psStarting, psBootstrapStarting]

proc isRunning*(self: Postgresql): bool =
  ## Check if PostgreSQL is running.
  if self.postmasterProc.isSome:
    let proc = self.postmasterProc.get()
    if proc.running:
      return true
    self.postmasterProc = none(Process)
    self.availableGucs = none(CaseInsensitiveSet)

  # Try to find postmaster process from pid file
  let pidFile = self.dataDir / "postmaster.pid"
  if fileExists(pidFile):
    try:
      let content = readFile(pidFile)
      let lines = content.splitLines()
      if lines.len > 0:
        let pid = parseInt(lines[0])
        # Check if process exists
        when defined(posix):
          import std/posix
          if kill(Pid(pid), 0) == 0:
            return true
    except ValueError, IOError:
      discard

  result = false

proc controldata*(self: Postgresql): Table[string, string] =
  ## Return the contents of pg_controldata.
  result = initTable[string, string]()

  if not self.versionFileExists():
    return

  if self.getState() == psCreatingReplica:
    return

  try:
    var env = newStringTable()
    env["LANG"] = "C"
    env["LC_ALL"] = "C"

    let process = startProcess(self.pgCommand("pg_controldata"), args = [self.dataDir], env = env)
    let (output, _) = process.readLines()
    let exitCode = process.waitForExit()
    process.close()

    if exitCode == 0:
      for line in output:
        if ':' in line:
          let parts = line.split(':', 1)
          if parts.len == 2:
            var key = parts[0].strip()
            # Remove "Current " prefix if present
            if key.startsWith("Current "):
              key = key[8..^1]
            result[key] = parts[1].strip()
  except OSError as e:
    logger.error(fmt"Error when calling pg_controldata: {e.msg}")

proc getSysid*(self: Postgresql): string =
  ## Get the database system identifier.
  if self.sysid.len == 0 and not self.bootstrapping:
    let data = self.controldata()
    self.sysid = data.getOrDefault("Database system identifier", "")
  result = self.sysid

proc isPrimary*(self: Postgresql): bool =
  ## Check if this instance is a primary.
  try:
    # Simple check based on role
    result = self.isRunning() and self.getRole() == prPrimary
  except PostgresConnectionException:
    logger.warning("Failed to determine PostgreSQL state from the connection, falling back to cached role")
    result = self.isRunning() and self.getRole() == prPrimary

proc isHealthy*(self: Postgresql): bool =
  ## Check if PostgreSQL is healthy.
  if not self.isRunning():
    logger.warning("Postgresql is not running.")
    return false
  result = true

proc start*(self: Postgresql, timeout: float = 0, blockCallbacks: bool = false,
            role: Option[PostgresqlRole] = none(PostgresqlRole)): Option[bool] =
  ## Start PostgreSQL.
  ##
  ## :returns: some(true) if start was successful, some(false) if failed, none if still starting.
  self.connectionPool.close()

  let state = if self.bootstrapping: psBootstrapStarting else: psStarting

  if self.isRunning():
    logger.error("Cannot start PostgreSQL because one is already running.")
    self.setState(state)
    return some(true)

  if not blockCallbacks:
    self.cbPending = some(caOnStart)

  if role.isSome:
    self.setRole(role.get())
  else:
    self.setRole(self.getPostgresRoleFromDataDirectory())

  self.setState(state)
  self.pendingRestartReason = newCaseInsensitiveDict[JsonNode]()

  # Build postgres command
  let pgCmd = self.pgCommand("postgres")
  var args = @["-D", self.dataDir]

  let process = startProcess(pgCmd, args = args)
  self.postmasterProc = some(process)

  let startTimeout = if timeout > 0: timeout else: 60.0

  # Wait for PostgreSQL to start accepting connections
  let startTime = epochTime()
  while epochTime() - startTime < startTimeout:
    if self.cancellable.isCancelled:
      return some(false)

    if not self.isRunning():
      logger.error("postmaster is not running")
      self.setState(psStartFailed)
      return some(false)

    let isready = self.pgIsReady()
    case isready
    of pisRunning, pisReject:
      self.setState(psRunning)
      if not blockCallbacks and self.cbPending.isSome:
        self.callNowait(self.cbPending.get())
        self.cbPending = none(CallbackAction)
      return some(true)
    of pisNoResponse:
      sleep(100)
    of pisUnknown:
      logger.warning("Can't determine PostgreSQL startup status, assuming running")
      self.setState(psRunning)
      return some(true)

  logger.warning("Timed out waiting for PostgreSQL to start")
  result = none(bool)

proc stop*(self: Postgresql, mode: string = "fast", blockCallbacks: bool = false,
           checkpoint: bool = true, stopTimeout: int = 0): bool =
  ## Stop PostgreSQL.
  ##
  ## :returns: true if stop was successful.
  if not self.isRunning():
    return true

  if not blockCallbacks:
    self.setState(psStopping)

  # Stop using pg_ctl
  var args = @["-m", mode]
  if stopTimeout > 0:
    args.add("-t")
    args.add($stopTimeout)

  let success = self.pgCtl("stop", args)

  if success:
    if not blockCallbacks:
      self.setState(psStopped)
      self.callNowait(caOnStop)
  else:
    logger.warning("pg_ctl stop failed")
    self.setState(psStopFailed)

  result = success

proc restart*(self: Postgresql, timeout: float = 0, blockCallbacks: bool = false,
              role: Option[PostgresqlRole] = none(PostgresqlRole)): Option[bool] =
  ## Restart PostgreSQL.
  ##
  ## :returns: some(true) when restart was successful.
  self.setState(psRestarting)
  if not blockCallbacks:
    self.cbPending = some(caOnRestart)

  let stopSuccess = self.stop(mode = "fast", blockCallbacks = true)
  if not stopSuccess:
    logger.warning(fmt"restart failed ({self.getState()})")
    self.setState(psRestartFailed)
    return some(false)

  result = self.start(timeout, blockCallbacks = true, role = role)
  if result.isNone or not result.get():
    if not self.isStarting():
      logger.warning(fmt"restart failed ({self.getState()})")
      self.setState(psRestartFailed)

proc reload*(self: Postgresql, blockCallbacks: bool = false): bool =
  ## Reload PostgreSQL configuration.
  result = self.pgCtl("reload")
  if result and not blockCallbacks:
    self.callNowait(caOnReload)

proc promote*(self: Postgresql, waitSeconds: int): Option[bool] =
  ## Promote replica to primary.
  if self.getRole() in [prPromoted, prPrimary]:
    return some(true)

  if self.cancellable.isCancelled:
    logger.info("PostgreSQL promote cancelled.")
    return some(false)

  let success = self.pgCtl("promote", "-W")
  if success:
    self.setRole(prPromoted)
    self.callNowait(caOnRoleChange)

    # Wait for promotion to complete
    let startTime = epochTime()
    while epochTime() - startTime < float(waitSeconds):
      let data = self.controldata()
      if data.getOrDefault("Database cluster state", "") == "in production":
        self.setRole(prPrimary)
        return some(true)
      sleep(1000)

  result = some(success)

proc callNowait*(self: Postgresql, cbType: CallbackAction) =
  ## Call a callback without waiting for it to finish.
  if self.bootstrapping:
    return

  if cbType in [caOnStart, caOnStop, caOnRestart, caOnRoleChange]:
    self.cbCalled = true

  # Would execute callback here
  logger.info(fmt"Callback triggered: {cbType}")

proc getCbCalled*(self: Postgresql): bool =
  ## Check if callback was called.
  result = self.cbCalled

proc follow*(self: Postgresql, member: Option[Member], role: PostgresqlRole = prReplica,
             timeout: float = 0, doReload: bool = false): Option[bool] =
  ## Reconfigure postgres to follow a new member or use different recovery parameters.
  ##
  ## :param member: The member to follow
  ## :param role: The desired role
  ## :param timeout: start timeout
  ## :param doReload: indicates that after updating postgresql.conf we just need to do a reload
  ##
  ## :returns: some(true) if successful, some(false) if failed, none if still starting.
  let changeRole = self.cbCalled and
    (self.getRole() in [prPrimary, prDemoted] or
     not ({prStandbyLeader, prReplica} - {self.getRole(), role}).len > 0)

  if changeRole:
    self.cbPending = some(caNoOp)

  var ret = true
  if self.isRunning():
    if doReload:
      ret = self.reload(blockCallbacks = changeRole)
      if ret and changeRole:
        self.setRole(role)
    else:
      let restartResult = self.restart(blockCallbacks = changeRole, role = some(role))
      ret = restartResult.isSome and restartResult.get()
  else:
    let startResult = self.start(timeout = timeout, blockCallbacks = changeRole, role = some(role))
    if startResult.isNone:
      return none(bool)
    ret = startResult.get()

  if changeRole:
    self.callNowait(caOnRoleChange)

  result = some(ret)

proc lastOperation*(self: Postgresql): int64 =
  ## Get the last operation LSN.
  # Would query pg_current_wal_lsn() or replay position
  result = 0

proc getServerVersion*(self: Postgresql): int =
  ## Get the server version.
  result = self.connection.serverVersion

proc removeDataDirectory*(self: Postgresql) =
  ## Remove the data directory.
  self.setRole(prUninitialized)
  logger.info(fmt"Removing data directory: {self.dataDir}")
  try:
    if symlinkExists(self.dataDir):
      removeFile(self.dataDir)
    elif not dirExists(self.dataDir):
      return
    elif fileExists(self.dataDir):
      removeFile(self.dataDir)
    elif dirExists(self.dataDir):
      removeDir(self.dataDir)
  except OSError as e:
    logger.exception(fmt"Could not remove data directory {self.dataDir}", e)

proc moveDataDirectory*(self: Postgresql) =
  ## Move the data directory to a .failed suffix.
  if dirExists(self.dataDir) and not self.isRunning():
    try:
      let postfix = "failed"
      let newName = fmt"{self.dataDir}.{postfix}"
      logger.info(fmt"renaming data directory to {newName}")
      if dirExists(newName):
        removeDir(newName)
      moveDir(self.dataDir, newName)
    except OSError as e:
      logger.exception(fmt"Could not rename data directory {self.dataDir}", e)

proc scheduleSanityChecksAfterPause*(self: Postgresql) =
  ## Schedule sanity checks after coming out of pause.
  discard self.getMajorVersion()
  self.sysid = ""

# Re-export for convenience
export PostgresqlRole, PostgresqlState, CallbackAction
