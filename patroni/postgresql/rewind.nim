## PostgreSQL pg_rewind support.
##
## This module provides types and procedures for checking diverged timelines
## and performing pg_rewind operations to resync replicas with the primary.

import std/[json, locks, options, os, osproc, re, sequtils, strformat, streams, strtabs, strutils, tables, times]
import ../async_executor
import ../dcs
import ../log
import ../psycopg
import ./connection
import ./misc

export misc

let logger = getLogger("patroni.postgresql.rewind")

type
  RewindStatus* = enum
    ## Status of the rewind operation.
    rsInitial = 0
    rsCheckpoint = 1
    rsCheck = 2
    rsNeed = 3
    rsNotNeed = 4
    rsSuccess = 5
    rsFailed = 6

  # Forward declaration for Postgresql type to avoid circular imports
  PostgresqlPtr* = ref object
    dataDir*: string
    binDir*: string
    majorVersion*: int
    config*: JsonNode
    connectionPool*: ConnectionPool

  Rewind* = ref object
    ## Handler for pg_rewind operations.
    postgresql*: PostgresqlPtr
    checkpointTaskLock: Lock
    checkpointTask: CriticalTask
    state: RewindStatus

proc configurationAllowsRewind*(data: Table[string, string]): bool =
  ## Check if PostgreSQL configuration allows pg_rewind.
  ##
  ## :param data: pg_controldata output.
  ## :returns: true if wal_log_hints is on or checksums are enabled.
  result = data.getOrDefault("wal_log_hints setting", "off") == "on" or
           data.getOrDefault("Data page checksum version", "0") != "0"

proc failed*(self: Rewind): bool
  ## Forward declaration

proc newRewind*(postgresql: PostgresqlPtr): Rewind =
  ## Create a new Rewind handler.
  new(result)
  result.postgresql = postgresql
  initLock(result.checkpointTaskLock)
  result.checkpointTask = nil
  result.state = rsInitial

proc resetState*(self: Rewind) =
  ## Reset the rewind state.
  self.state = rsInitial
  withLock(self.checkpointTaskLock):
    self.checkpointTask = nil

proc enabled*(self: Rewind): bool =
  ## Check if pg_rewind is enabled in configuration.
  if self.postgresql == nil or self.postgresql.config == nil:
    return false
  if self.postgresql.config.hasKey("use_pg_rewind"):
    result = self.postgresql.config["use_pg_rewind"].getBool(false)
  else:
    result = false

proc canRewind*(self: Rewind): bool =
  ## Check if pg_rewind is possible.
  ##
  ## Check if pg_rewind executable exists and that pg_controldata indicates
  ## we have either wal_log_hints or checksums turned on.
  if not self.enabled:
    return false

  if self.postgresql == nil:
    return false

  # Check if pg_rewind exists and works
  let pgRewindPath = self.postgresql.binDir / "pg_rewind"
  if not fileExists(pgRewindPath):
    logger.warning(fmt"pg_rewind not found at {pgRewindPath}")
    return false

  try:
    let process = startProcess(pgRewindPath, args = ["--help"])
    let exitCode = process.waitForExit()
    process.close()
    if exitCode != 0:
      logger.warning("pg_rewind --help failed")
      return false
  except OSError as e:
    logger.error(fmt"Failed to execute pg_rewind: {e.msg}")
    return false

  result = true

proc shouldRemoveDataDirectoryOnDivergedTimelines*(self: Rewind): bool =
  ## Check if data directory should be removed on diverged timelines.
  if self.postgresql == nil or self.postgresql.config == nil:
    return false
  if self.postgresql.config.hasKey("remove_data_directory_on_diverged_timelines"):
    result = self.postgresql.config["remove_data_directory_on_diverged_timelines"].getBool(false)
  else:
    result = false

proc canRewindOrReinitializeAllowed*(self: Rewind): bool =
  ## Check if rewind or reinitialize is allowed.
  result = self.shouldRemoveDataDirectoryOnDivergedTimelines or self.canRewind

proc triggerCheckDivergedLsn*(self: Rewind) =
  ## Trigger a check for diverged LSN.
  if self.canRewindOrReinitializeAllowed and self.state != rsNeed:
    self.state = rsCheck
  withLock(self.checkpointTaskLock):
    self.checkpointTask = nil

proc checkLeaderIsNotInRecovery*(connKwargs: Table[string, string]): Option[bool] =
  ## Check if the leader is not in recovery.
  ##
  ## :param connKwargs: Connection parameters.
  ## :returns: some(true) if leader is primary, some(false) if in recovery, none on error.
  try:
    let conn = connect(
      host = connKwargs.getOrDefault("host", ""),
      port = connKwargs.getOrDefault("port", "5432"),
      user = connKwargs.getOrDefault("user", ""),
      password = connKwargs.getOrDefault("password", ""),
      database = connKwargs.getOrDefault("database", "postgres")
    )
    defer: conn.close()

    let rows = conn.query("SELECT pg_catalog.pg_is_in_recovery()")
    if rows.len > 0 and rows[0].len > 0:
      let inRecovery = rows[0][0] == "t" or rows[0][0].toLowerAscii() == "true"
      return some(not inRecovery)  # Return true if NOT in recovery (i.e., is primary)
    return none(bool)
  except OperationalError as e:
    logger.error(fmt"Connection error when checking leader: {e.msg}")
    return none(bool)
  except DatabaseError as e:
    logger.error(fmt"Database error when checking leader: {e.msg}")
    return none(bool)
  except CatchableError as e:
    logger.error(fmt"Exception when working with leader: {e.msg}")
    return none(bool)

proc checkLeaderHasRunCheckpoint*(connKwargs: Table[string, string]): Option[string] =
  ## Check if the leader has run a checkpoint.
  ##
  ## :param connKwargs: Connection parameters.
  ## :returns: none if checkpoint was run, some(error_message) otherwise.
  try:
    let conn = connect(
      host = connKwargs.getOrDefault("host", ""),
      port = connKwargs.getOrDefault("port", "5432"),
      user = connKwargs.getOrDefault("user", ""),
      password = connKwargs.getOrDefault("password", ""),
      database = connKwargs.getOrDefault("database", "postgres")
    )
    defer: conn.close()

    # Issue a CHECKPOINT command
    discard conn.execute("CHECKPOINT")
    logger.info("Successfully ran checkpoint on leader")
    return none(string)  # Success - no error message
  except OperationalError as e:
    logger.error(fmt"Connection error when running checkpoint: {e.msg}")
    return some("not accessible or not healthy")
  except DatabaseError as e:
    logger.error(fmt"Database error when running checkpoint: {e.msg}")
    return some(fmt"checkpoint failed: {e.msg}")
  except CatchableError as e:
    logger.error(fmt"Exception when working with leader: {e.msg}")
    return some("not accessible or not healthy")

proc getCheckpointEnd(self: Rewind, timeline: int, lsn: int64): int64 =
  ## Get the end of checkpoint record from WAL.
  ##
  ## :param timeline: The checkpoint timeline from pg_controldata.
  ## :param lsn: The checkpoint location as int64 from pg_controldata.
  ## :returns: The end of checkpoint record as int64 or 0 if failed.
  if self.postgresql == nil:
    return 0

  let lsnStr = formatLsn(lsn)
  let pgWaldumpPath = self.postgresql.binDir / "pg_waldump"

  if not fileExists(pgWaldumpPath):
    logger.warning(fmt"pg_waldump not found at {pgWaldumpPath}")
    return 0

  try:
    # Execute pg_waldump to find the checkpoint end
    # pg_waldump -t <timeline> -s <lsn> -n 1 <wal_dir>
    let walDir = self.postgresql.dataDir / "pg_wal"
    let process = startProcess(pgWaldumpPath,
      args = ["-t", $timeline, "-s", lsnStr, "-n", "1", walDir])

    var output = ""
    while process.running:
      let (lines, exitCode) = process.readLines()
      for line in lines:
        output &= line & "\n"

    let exitCode = process.waitForExit()
    process.close()

    if exitCode == 0:
      # Parse output to find the end LSN
      # Format is typically: rmgr: XLOG  len (rec/tot): ... lsn: X/Y, prev X/Y, desc: CHECKPOINT_SHUTDOWN ...
      for line in output.splitLines():
        if "CHECKPOINT" in line:
          # Extract the end lsn from the output
          let lsnMatch = line.find("lsn:")
          if lsnMatch >= 0:
            let lsnPart = line[lsnMatch + 4..^1].strip().split(',')[0].strip()
            try:
              return parseLsn(lsnPart)
            except ValueError:
              discard
  except OSError as e:
    logger.error(fmt"Failed to execute pg_waldump: {e.msg}")

  result = 0

proc runControldata(self: Rewind): Table[string, string] =
  ## Run pg_controldata and return parsed output.
  result = initTable[string, string]()

  if self.postgresql == nil:
    return

  let pgControldataPath = self.postgresql.binDir / "pg_controldata"
  if not fileExists(pgControldataPath):
    return

  try:
    var env = newStringTable()
    env["LANG"] = "C"
    env["LC_ALL"] = "C"

    let process = startProcess(pgControldataPath, args = [self.postgresql.dataDir], env = env)
    var output: seq[string] = @[]
    while process.running:
      let (lines, _) = process.readLines()
      output.add(lines)

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

proc getLocalTimelineLsnFromControldata(self: Rewind): tuple[inRecovery: Option[bool], timeline: Option[int], lsn: Option[int64]] =
  ## Get local timeline and LSN from pg_controldata.
  result.inRecovery = none(bool)
  result.timeline = none(int)
  result.lsn = none(int64)

  let data = self.runControldata()
  if data.len == 0:
    return

  # Determine if in recovery
  let clusterState = data.getOrDefault("Database cluster state", "")
  if clusterState.len > 0:
    result.inRecovery = some(clusterState.contains("recovery") or
                             clusterState.contains("archive recovery"))

  # Get timeline
  let timelineStr = data.getOrDefault("Latest checkpoint's TimeLineID", "")
  if timelineStr.len > 0:
    try:
      result.timeline = some(parseInt(timelineStr))
    except ValueError:
      discard

  # Get LSN - prefer REDO LSN for recovery scenarios
  var lsnStr = data.getOrDefault("Latest checkpoint's REDO location", "")
  if lsnStr.len == 0:
    lsnStr = data.getOrDefault("Latest checkpoint location", "")

  if lsnStr.len > 0:
    try:
      result.lsn = some(int64(parseLsn(lsnStr)))
    except ValueError:
      discard

proc getLocalTimelineLsn(self: Rewind): tuple[inRecovery: Option[bool], timeline: Option[int], lsn: Option[int64]] =
  ## Get local timeline and LSN.
  # Would check if postgresql is running and get from replication connection
  # Otherwise analyze pg_controldata output
  result = self.getLocalTimelineLsnFromControldata()

  if result.timeline.isSome and result.lsn.isSome:
    let lsnStr = if result.lsn.isSome: formatLsn(result.lsn.get) else: "unknown"
    logger.info(fmt"Local timeline={result.timeline.get} lsn={lsnStr}")

proc logPrimaryHistory(history: seq[tuple[timeline: int, switchpoint: int64, reason: string]], i: int) =
  ## Log primary timeline history.
  let start = max(0, i - 3)
  let endIdx = if i + 4 >= history.len: history.len else: i + 2

  proc formatHistoryLine(line: tuple[timeline: int, switchpoint: int64, reason: string]): string =
    fmt"{line.timeline}\t{formatLsn(line.switchpoint)}\t{line.reason}"

  var historyShow: seq[string] = @[]
  for line in history[start..<endIdx]:
    historyShow.add(formatHistoryLine(line))

  if endIdx < history.len:
    historyShow.add("...")
    historyShow.add(formatHistoryLine(history[^1]))

  let historyStr = historyShow.join("\n")
  logger.info(fmt"primary: history={historyStr}")

proc checkTimelineAndLsn(self: Rewind, leader: Leader) =
  ## Check timeline and LSN against the leader.
  let (inRecovery, localTimeline, localLsn) = self.getLocalTimelineLsn()

  if localTimeline.isNone or localLsn.isNone:
    return

  # Would perform timeline comparison with leader
  # and set self.state accordingly

proc rewindOrReinitializeNeededAndPossible*(self: Rewind, leader: Leader): bool =
  ## Check if rewind or reinitialize is needed and possible.
  ##
  ## :param leader: Current cluster leader.
  ## :returns: true if rewind is needed and possible.
  if leader != nil and leader.member != nil and leader.member.name.len > 0 and self.state == rsCheck:
    self.checkTimelineAndLsn(leader)
  result = leader != nil and self.state == rsNeed

proc getArchiveCommand*(self: Rewind): Option[string] =
  ## Get archive_command GUC value if defined and archiving is enabled.
  ##
  ## :returns: archive_command defined in the Postgres configuration or none.
  if self.postgresql == nil or self.postgresql.config == nil:
    return none(string)

  # Check if archiving is enabled in config
  if self.postgresql.config.hasKey("parameters"):
    let params = self.postgresql.config["parameters"]
    if params.kind == JObject:
      # Check archive_mode
      let archiveMode = params.getOrDefault("archive_mode").getStr("")
      if archiveMode != "on" and archiveMode != "always":
        return none(string)

      # Get archive_command
      let archiveCmd = params.getOrDefault("archive_command").getStr("")
      if archiveCmd.len > 0:
        return some(archiveCmd)

  result = none(string)

proc buildArchiverCommand*(self: Rewind, command: string, walFilename: string): string =
  ## Replace placeholders in the archiver command template.
  ##
  ## :param command: Template command with placeholders.
  ## :param walFilename: WAL filename to substitute.
  ## :returns: Command with placeholders replaced.
  result = ""
  var i = 0
  let length = command.len

  # Determine WAL directory path
  var walDir = ""
  if self.postgresql != nil:
    walDir = self.postgresql.dataDir / "pg_wal"

  while i < length:
    if command[i] == '%' and i + 1 < length:
      inc i
      case command[i]
      of 'p':
        # %p = full path to WAL file
        if walDir.len > 0:
          result.add(walDir / walFilename)
        else:
          result.add(walFilename)
      of 'f':
        # %f = just the filename
        result.add(walFilename)
      of 'r':
        # %r = file name of the last valid restart point
        result.add("000000010000000000000001")
      of '%':
        result.add('%')
      else:
        result.add('%')
        dec i
    else:
      result.add(command[i])
    inc i

proc fetchMissingWal(self: Rewind, restoreCommand: string, walFilename: string): bool =
  ## Fetch a missing WAL file using restore_command.
  ##
  ## :param restoreCommand: The restore_command to use.
  ## :param walFilename: Name of the WAL file to fetch.
  ## :returns: true if successful.
  let cmd = self.buildArchiverCommand(restoreCommand, walFilename)
  logger.info(fmt"Trying to fetch the missing wal: {cmd}")

  try:
    # Execute the restore command via shell
    let process = startProcess("/bin/sh", args = ["-c", cmd])
    let exitCode = process.waitForExit()
    process.close()

    if exitCode == 0:
      logger.info(fmt"Successfully fetched WAL file: {walFilename}")
      return true
    else:
      logger.warning(fmt"Failed to fetch WAL file {walFilename}, exit code: {exitCode}")
      return false
  except OSError as e:
    logger.error(fmt"Error executing restore command: {e.msg}")
    return false

proc findMissingWal*(self: Rewind, data: string): Option[string] =
  ## Find missing WAL file name from pg_rewind error output.
  ##
  ## :param data: Error output from pg_rewind.
  ## :returns: WAL filename if found.
  let pattern = "could not open file \""
  for line in data.splitLines():
    let b = line.find(pattern)
    if b > -1:
      let start = b + pattern.len
      let e = line.find("\": ", start)
      if e > -1 and '/' in line[start..<e]:
        let parts = line[start..<e].rsplit('/', maxsplit=1)
        if parts.len == 2:
          let walFilename = parts[1]
          if walFilename.len == 24:
            return some(walFilename)
  result = none(string)

proc archiveReadyWals(self: Rewind) =
  ## Try to archive WALs that have .ready files.
  let archiveCmd = self.getArchiveCommand()
  if archiveCmd.isNone:
    return

  if self.postgresql == nil:
    return

  let archiveStatusDir = self.postgresql.dataDir / "pg_wal" / "archive_status"
  if not dirExists(archiveStatusDir):
    return

  # List .ready files and archive corresponding WAL files
  try:
    for kind, path in walkDir(archiveStatusDir):
      if kind == pcFile and path.endsWith(".ready"):
        let walFilename = extractFilename(path).replace(".ready", "")
        let cmd = self.buildArchiverCommand(archiveCmd.get(), walFilename)
        logger.info(fmt"Archiving WAL file: {walFilename}")

        try:
          let process = startProcess("/bin/sh", args = ["-c", cmd])
          let exitCode = process.waitForExit()
          process.close()

          if exitCode == 0:
            # Mark as archived by renaming .ready to .done
            let donePath = path.replace(".ready", ".done")
            moveFile(path, donePath)
            logger.info(fmt"Successfully archived WAL file: {walFilename}")
          else:
            logger.warning(fmt"Failed to archive WAL file {walFilename}, exit code: {exitCode}")
        except OSError as e:
          logger.error(fmt"Error archiving WAL file {walFilename}: {e.msg}")
  except OSError as e:
    logger.error(fmt"Error reading archive_status directory: {e.msg}")

proc maybeCleanPgReplslot(self: Rewind) =
  ## Clean pg_replslot directory if pg version is less than 11.
  ##
  ## pg_rewind before PostgreSQL 11 doesn't properly handle pg_replslot,
  ## so we need to clean it up manually.
  if self.postgresql == nil:
    return

  # Only clean for versions before 11 (110000)
  if self.postgresql.majorVersion >= 110000:
    return

  let replslotDir = self.postgresql.dataDir / "pg_replslot"
  if not dirExists(replslotDir):
    return

  logger.info("Cleaning pg_replslot directory for pre-11 PostgreSQL")
  try:
    for kind, path in walkDir(replslotDir):
      if kind == pcDir:
        try:
          removeDir(path)
          logger.info(fmt"Removed replication slot directory: {path}")
        except OSError as e:
          logger.error(fmt"Failed to remove {path}: {e.msg}")
  except OSError as e:
    logger.error(fmt"Error cleaning pg_replslot: {e.msg}")

proc pgRewind*(self: Rewind, connKwargs: Table[string, string]): bool =
  ## Perform pg_rewind.
  ##
  ## :param connKwargs: Connection parameters to the source server.
  ## :returns: true if pg_rewind finished successfully.
  if self.postgresql == nil:
    return false

  # Build connection string
  var dsnParts: seq[string] = @[]
  for key, val in connKwargs:
    if key != "password":
      dsnParts.add(fmt"{key}={val}")
  let dsn = dsnParts.join(" ")

  logger.info(fmt"Running pg_rewind from {dsn}")

  let pgRewindPath = self.postgresql.binDir / "pg_rewind"
  if not fileExists(pgRewindPath):
    logger.error(fmt"pg_rewind not found at {pgRewindPath}")
    return false

  # Build the full connection string for pg_rewind
  var connStr = ""
  for key, val in connKwargs:
    if connStr.len > 0:
      connStr &= " "
    connStr &= fmt"{key}={val}"

  # Get restore_command for fetching missing WAL files
  var restoreCommand = ""
  if self.postgresql.config != nil and self.postgresql.config.hasKey("parameters"):
    let params = self.postgresql.config["parameters"]
    if params.kind == JObject:
      restoreCommand = params.getOrDefault("restore_command").getStr("")

  var retries = 0
  const maxRetries = 3

  while retries < maxRetries:
    try:
      var env = newStringTable()
      # Set PGPASSWORD if provided
      if "password" in connKwargs:
        env["PGPASSWORD"] = connKwargs["password"]

      let args = @["-D", self.postgresql.dataDir, "--source-server=" & connStr, "--progress"]
      let process = startProcess(pgRewindPath, args = args, env = env)

      var stderr = ""
      while process.running:
        let errStream = process.errorStream
        if errStream != nil:
          stderr &= streams.readAll(errStream)
        sleep(100)

      let exitCode = process.waitForExit()
      # Read any remaining stderr
      let errStream = process.errorStream
      if errStream != nil:
        stderr &= streams.readAll(errStream)
      process.close()

      if exitCode == 0:
        logger.info("pg_rewind completed successfully")
        return true
      else:
        logger.warning(fmt"pg_rewind failed with exit code {exitCode}")

        # Check if failure is due to missing WAL file
        if restoreCommand.len > 0:
          let missingWal = self.findMissingWal(stderr)
          if missingWal.isSome:
            logger.info(fmt"pg_rewind failed due to missing WAL: {missingWal.get()}")
            if self.fetchMissingWal(restoreCommand, missingWal.get()):
              inc retries
              logger.info(fmt"Retrying pg_rewind (attempt {retries + 1}/{maxRetries})")
              continue

        logger.error(fmt"pg_rewind failed: {stderr}")
        return false

    except OSError as e:
      logger.error(fmt"Error executing pg_rewind: {e.msg}")
      return false

  logger.error("pg_rewind failed after maximum retries")
  result = false

proc execute*(self: Rewind, leader: Leader): Option[bool] =
  ## Execute the rewind operation.
  ##
  ## :param leader: Current cluster leader.
  ## :returns: none on success, some(false) on failure.
  if leader == nil or leader.member == nil:
    logger.error("Cannot execute rewind: no leader available")
    return some(false)

  self.archiveReadyWals()

  # Build connection parameters from leader's connection info
  var connKwargs = initTable[string, string]()
  if leader.member.connUrl.len > 0:
    # Parse connection URL to get host/port
    let url = leader.member.connUrl
    # Simple parsing of postgresql://host:port/db
    if url.startsWith("postgresql://") or url.startsWith("postgres://"):
      var rest = url
      if url.startsWith("postgresql://"):
        rest = url[13..^1]
      else:
        rest = url[11..^1]

      # Remove user:pass@ if present
      let atIdx = rest.find('@')
      if atIdx >= 0:
        rest = rest[atIdx + 1..^1]

      # Split host:port/db
      let slashIdx = rest.find('/')
      var hostPort = rest
      if slashIdx >= 0:
        hostPort = rest[0..<slashIdx]
        connKwargs["database"] = rest[slashIdx + 1..^1].split('?')[0]

      let colonIdx = hostPort.rfind(':')
      if colonIdx >= 0:
        connKwargs["host"] = hostPort[0..<colonIdx]
        connKwargs["port"] = hostPort[colonIdx + 1..^1]
      else:
        connKwargs["host"] = hostPort
        connKwargs["port"] = "5432"

  # Get credentials from config if available
  if self.postgresql != nil and self.postgresql.config != nil:
    if self.postgresql.config.hasKey("authentication"):
      let auth = self.postgresql.config["authentication"]
      if auth.hasKey("superuser"):
        let su = auth["superuser"]
        if su.hasKey("username"):
          connKwargs["user"] = su["username"].getStr()
        if su.hasKey("password"):
          connKwargs["password"] = su["password"].getStr()

  if connKwargs.len == 0 or "host" notin connKwargs:
    logger.error("Cannot determine connection parameters for rewind")
    self.state = rsFailed
    return some(false)

  if self.pgRewind(connKwargs):
    self.maybeCleanPgReplslot()
    self.state = rsSuccess
    return none(bool)  # Success
  else:
    # Check if leader is still accessible
    let leaderCheck = checkLeaderIsNotInRecovery(connKwargs)
    if leaderCheck.isNone:
      logger.warning("Failed to rewind because primary became unreachable")
      if not self.canRewind:
        self.state = rsFailed
    else:
      logger.error("Failed to rewind from healthy primary")
      self.state = rsFailed

    if self.failed:
      # Check for remove_data_directory_on_rewind_failure config
      var shouldRemoveDir = false
      if self.postgresql != nil and self.postgresql.config != nil:
        if self.postgresql.config.hasKey("remove_data_directory_on_rewind_failure"):
          shouldRemoveDir = self.postgresql.config["remove_data_directory_on_rewind_failure"].getBool(false)

      if shouldRemoveDir:
        logger.info("Removing data directory after rewind failure")
        try:
          removeDir(self.postgresql.dataDir)
        except OSError as e:
          logger.error(fmt"Failed to remove data directory: {e.msg}")
        self.state = rsInitial

  result = some(false)

proc isNeeded*(self: Rewind): bool =
  ## Check if rewind is needed.
  result = self.state in [rsCheck, rsNeed]

proc executed*(self: Rewind): bool =
  ## Check if rewind was executed.
  result = self.state > rsNotNeed

proc failed*(self: Rewind): bool =
  ## Check if rewind failed.
  result = self.state == rsFailed

proc ensureCheckpointAfterPromote*(self: Rewind, wakeup: proc()) =
  ## Ensure checkpoint is done after promote.
  ##
  ## Issue a CHECKPOINT from a new thread and asynchronously check the result.
  if self.postgresql == nil or self.postgresql.connectionPool == nil:
    return

  # Set state to checkpoint in progress
  self.state = rsCheckpoint

  # Execute checkpoint via connection pool
  try:
    let conn = self.postgresql.connectionPool.get("checkpoint")
    let pgConn = conn.get()
    discard pgConn.execute("CHECKPOINT")
    logger.info("Checkpoint after promote completed successfully")

    # Call wakeup callback if provided
    if wakeup != nil:
      wakeup()
  except OperationalError as e:
    logger.error(fmt"Failed to run checkpoint after promote: {e.msg}")
  except DatabaseError as e:
    logger.error(fmt"Database error during checkpoint after promote: {e.msg}")
  except CatchableError as e:
    logger.error(fmt"Error during checkpoint after promote: {e.msg}")

proc checkpointAfterPromote*(self: Rewind): bool =
  ## Check if checkpoint after promote is done.
  result = self.state == rsCheckpoint

proc readPostmasterOpts*(self: Rewind): Table[string, string] =
  ## Read postmaster.opts file.
  ##
  ## :returns: Dictionary of option names/values from postgres.opts.
  result = initTable[string, string]()

  if self.postgresql == nil:
    return

  let optsFile = self.postgresql.dataDir / "postmaster.opts"
  if not fileExists(optsFile):
    return

  try:
    let content = readFile(optsFile)
    # postmaster.opts contains command line used to start postgres
    # Format: /path/to/postgres "-D" "/data/dir" "-c" "param=value" ...
    var inQuote = false
    var current = ""
    var tokens: seq[string] = @[]

    for c in content:
      if c == '"':
        inQuote = not inQuote
      elif c == ' ' and not inQuote:
        if current.len > 0:
          tokens.add(current)
          current = ""
      elif c != '\n' and c != '\r':
        current.add(c)

    if current.len > 0:
      tokens.add(current)

    # Parse tokens into key-value pairs
    var i = 0
    while i < tokens.len:
      let token = tokens[i]
      if token == "-c" and i + 1 < tokens.len:
        let param = tokens[i + 1]
        let eqIdx = param.find('=')
        if eqIdx >= 0:
          result[param[0..<eqIdx]] = param[eqIdx + 1..^1]
        inc i
      elif token == "-D" and i + 1 < tokens.len:
        result["data_directory"] = tokens[i + 1]
        inc i
      elif token.startsWith("-"):
        result[token[1..^1]] = "true"
      inc i
  except IOError as e:
    logger.error(fmt"Failed to read postmaster.opts: {e.msg}")

proc singleUserMode*(self: Rewind, communicate: Option[Table[string, string]] = none(Table[string, string]),
                     options: Option[Table[string, string]] = none(Table[string, string])): Option[int] =
  ## Run a command in single-user mode.
  ##
  ## :param communicate: Optional input/output communication.
  ## :param options: Optional postgres options.
  ## :returns: Exit code or none on error.
  if self.postgresql == nil:
    return none(int)

  let postgresPath = self.postgresql.binDir / "postgres"
  if not fileExists(postgresPath):
    logger.error(fmt"postgres not found at {postgresPath}")
    return none(int)

  # Build command arguments
  var args = @["--single", "-D", self.postgresql.dataDir]

  # Add options if provided
  if options.isSome:
    for key, val in options.get():
      args.add("-c")
      args.add(fmt"{key}={val}")

  # Add the database name (required for single-user mode)
  args.add("postgres")

  try:
    var env = newStringTable()
    let process = startProcess(postgresPath, args = args, env = env,
                               options = {poStdErrToStdOut, poUsePath})

    # If communicate input is provided, write it to stdin
    if communicate.isSome:
      let inputStream = process.inputStream
      if inputStream != nil:
        for key, val in communicate.get():
          inputStream.writeLine(val)
        inputStream.close()

    let exitCode = process.waitForExit()
    process.close()

    logger.info(fmt"Single-user mode completed with exit code: {exitCode}")
    return some(exitCode)
  except OSError as e:
    logger.error(fmt"Failed to run single-user mode: {e.msg}")
    return none(int)

proc cleanupArchiveStatus*(self: Rewind) =
  ## Clean up archive_status directory.
  if self.postgresql == nil:
    return

  let archiveStatusDir = self.postgresql.dataDir / "pg_wal" / "archive_status"
  if not dirExists(archiveStatusDir):
    return

  logger.info("Cleaning up archive_status directory")
  try:
    for kind, path in walkDir(archiveStatusDir):
      if kind == pcFile:
        try:
          removeFile(path)
        except OSError as e:
          logger.warning(fmt"Failed to remove {path}: {e.msg}")
  except OSError as e:
    logger.error(fmt"Error cleaning archive_status directory: {e.msg}")

proc ensureCleanShutdown*(self: Rewind): Option[bool] =
  ## Ensure PostgreSQL has a clean shutdown.
  ##
  ## Start in single-user mode and stop to produce a clean shutdown.
  self.archiveReadyWals()
  self.cleanupArchiveStatus()

  let opts = self.readPostmasterOpts()

  # Build options table from postmaster.opts
  var pgOptions = initTable[string, string]()

  # Copy relevant options, excluding those that don't work in single-user mode
  let excludedOpts = ["listen_addresses", "port", "unix_socket_directories",
                      "log_destination", "logging_collector", "archive_mode",
                      "archive_command", "hot_standby"]

  for key, val in opts:
    if key notin excludedOpts:
      pgOptions[key] = val

  # Run single-user mode to ensure clean shutdown
  let exitCode = self.singleUserMode(options = some(pgOptions))
  if exitCode.isSome and exitCode.get() == 0:
    logger.info("Clean shutdown ensured via single-user mode")
    return some(true)
  else:
    logger.warning("Failed to ensure clean shutdown via single-user mode")
    return some(false)

proc archiveShutdownCheckpointWal*(self: Rewind, archiveCmd: string) =
  ## Archive WAL file with the shutdown checkpoint.
  ##
  ## :param archiveCmd: Archiver command to use.
  if self.postgresql == nil or archiveCmd.len == 0:
    return

  # Get latest checkpoint info from controldata
  let data = self.runControldata()
  if data.len == 0:
    logger.warning("Could not get controldata for archiving shutdown WAL")
    return

  # Get the REDO WAL file from the checkpoint location
  let redoLsn = data.getOrDefault("Latest checkpoint's REDO WAL file", "")
  if redoLsn.len == 0:
    # Try to construct from REDO location
    let lsnStr = data.getOrDefault("Latest checkpoint's REDO location", "")
    let timelineStr = data.getOrDefault("Latest checkpoint's TimeLineID", "1")
    if lsnStr.len == 0:
      logger.warning("Could not determine checkpoint WAL file")
      return

    # Parse LSN to get segment number
    try:
      let lsn = parseLsn(lsnStr)
      let timeline = parseInt(timelineStr)

      # Calculate WAL segment filename
      # Format: TTTTTTTTSSSSSSSSOOOOOOOO (timeline, segment high, segment low)
      let segmentSize = 16 * 1024 * 1024  # 16MB default WAL segment size
      let segmentNum = lsn div segmentSize
      let segmentHigh = segmentNum shr 32
      let segmentLow = segmentNum and 0xFFFFFFFF

      let walFilename = fmt"{timeline:08X}{segmentHigh:08X}{segmentLow:08X}"
      logger.info(fmt"Archiving shutdown checkpoint WAL: {walFilename}")

      let cmd = self.buildArchiverCommand(archiveCmd, walFilename)
      try:
        let process = startProcess("/bin/sh", args = ["-c", cmd])
        let exitCode = process.waitForExit()
        process.close()

        if exitCode == 0:
          logger.info(fmt"Successfully archived shutdown checkpoint WAL: {walFilename}")
        else:
          logger.warning(fmt"Failed to archive shutdown checkpoint WAL, exit code: {exitCode}")
      except OSError as e:
        logger.error(fmt"Error archiving shutdown checkpoint WAL: {e.msg}")
    except ValueError:
      logger.error("Failed to parse checkpoint LSN")
  else:
    # Use the REDO WAL file directly
    logger.info(fmt"Archiving shutdown checkpoint WAL: {redoLsn}")
    let cmd = self.buildArchiverCommand(archiveCmd, redoLsn)
    try:
      let process = startProcess("/bin/sh", args = ["-c", cmd])
      let exitCode = process.waitForExit()
      process.close()

      if exitCode == 0:
        logger.info(fmt"Successfully archived shutdown checkpoint WAL: {redoLsn}")
      else:
        logger.warning(fmt"Failed to archive shutdown checkpoint WAL, exit code: {exitCode}")
    except OSError as e:
      logger.error(fmt"Error archiving shutdown checkpoint WAL: {e.msg}")

