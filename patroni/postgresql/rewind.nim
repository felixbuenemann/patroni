## PostgreSQL pg_rewind support.
##
## This module provides types and procedures for checking diverged timelines
## and performing pg_rewind operations to resync replicas with the primary.

import std/[json, locks, options, os, osproc, re, sequtils, strformat, strutils, tables, times]
import ../async_executor
import ../dcs
import ../log
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

  Rewind* = ref object
    ## Handler for pg_rewind operations.
    postgresql: pointer  # Postgresql - forward declaration
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

proc newRewind*(postgresql: pointer): Rewind =
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
  # Would check self.postgresql.config.get("use_pg_rewind")
  result = false

proc canRewind*(self: Rewind): bool =
  ## Check if pg_rewind is possible.
  ##
  ## Check if pg_rewind executable exists and that pg_controldata indicates
  ## we have either wal_log_hints or checksums turned on.
  if not self.enabled:
    return false

  # Check if pg_rewind exists and works
  # Would execute: pg_rewind --help
  result = false

proc shouldRemoveDataDirectoryOnDivergedTimelines*(self: Rewind): bool =
  ## Check if data directory should be removed on diverged timelines.
  # Would check self.postgresql.config.get("remove_data_directory_on_diverged_timelines")
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
    # Would execute: SELECT pg_catalog.pg_is_in_recovery()
    # For now, return none to indicate we couldn't check
    return none(bool)
  except:
    logger.error("Exception when working with leader")
    return none(bool)

proc checkLeaderHasRunCheckpoint*(connKwargs: Table[string, string]): Option[string] =
  ## Check if the leader has run a checkpoint.
  ##
  ## :param connKwargs: Connection parameters.
  ## :returns: none if checkpoint was run, some(error_message) otherwise.
  try:
    # Would execute checkpoint verification query
    return none(string)
  except:
    logger.error("Exception when working with leader")
    return some("not accessible or not healthy")

proc getCheckpointEnd(self: Rewind, timeline: int, lsn: int64): int64 =
  ## Get the end of checkpoint record from WAL.
  ##
  ## :param timeline: The checkpoint timeline from pg_controldata.
  ## :param lsn: The checkpoint location as int64 from pg_controldata.
  ## :returns: The end of checkpoint record as int64 or 0 if failed.
  let lsnStr = formatLsn(lsn)
  # Would execute pg_waldump to find the checkpoint end
  result = 0

proc getLocalTimelineLsnFromControldata(self: Rewind): tuple[inRecovery: Option[bool], timeline: Option[int], lsn: Option[int64]] =
  ## Get local timeline and LSN from pg_controldata.
  result.inRecovery = none(bool)
  result.timeline = none(int)
  result.lsn = none(int64)

  # Would call self.postgresql.controldata()
  # Parse the output to get timeline and LSN information

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
  # Would call self.postgresql.getGucValue("archive_mode") and ("archive_command")
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

  while i < length:
    if command[i] == '%' and i + 1 < length:
      inc i
      case command[i]
      of 'p':
        # Would use self.postgresql.walDir / walFilename
        result.add(walFilename)
      of 'f':
        result.add(walFilename)
      of 'r':
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
  # Would execute the command
  result = false

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

  # Would list archive_status directory and archive .ready files

proc maybeCleanPgReplslot(self: Rewind) =
  ## Clean pg_replslot directory if pg version is less than 11.
  # Would check major version and clean if needed
  discard

proc pgRewind*(self: Rewind, connKwargs: Table[string, string]): bool =
  ## Perform pg_rewind.
  ##
  ## :param connKwargs: Connection parameters to the source server.
  ## :returns: true if pg_rewind finished successfully.

  # Build connection string
  var dsnParts: seq[string] = @[]
  for key, val in connKwargs:
    if key != "password":
      dsnParts.add(fmt"{key}={val}")
  let dsn = dsnParts.join(" ")

  logger.info(fmt"running pg_rewind from {dsn}")

  # Would execute pg_rewind command
  # Handle missing WAL files by fetching them with restore_command

  result = false

proc execute*(self: Rewind, leader: Leader): Option[bool] =
  ## Execute the rewind operation.
  ##
  ## :param leader: Current cluster leader.
  ## :returns: none on success, some(false) on failure.

  # Would check if postgresql is running and stop it if needed
  self.archiveReadyWals()

  # Build connection parameters
  var connKwargs = initTable[string, string]()
  # Would get connection info from leader

  if self.pgRewind(connKwargs):
    self.maybeCleanPgReplslot()
    self.state = rsSuccess
  else:
    # Check if leader is still accessible
    let leaderCheck = checkLeaderIsNotInRecovery(connKwargs)
    if leaderCheck.isNone:
      logger.warning(fmt"Failed to rewind because primary became unreachable")
      if not self.canRewind:
        self.state = rsFailed
    else:
      logger.error("Failed to rewind from healthy primary")
      self.state = rsFailed

    if self.failed:
      # Check for remove_data_directory_on_rewind_failure config
      # Would remove data directory if configured
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
  # Would run checkpoint and update state
  discard

proc checkpointAfterPromote*(self: Rewind): bool =
  ## Check if checkpoint after promote is done.
  result = self.state == rsCheckpoint

proc readPostmasterOpts*(self: Rewind): Table[string, string] =
  ## Read postmaster.opts file.
  ##
  ## :returns: Dictionary of option names/values from postgres.opts.
  result = initTable[string, string]()
  # Would read from self.postgresql.dataDir / "postmaster.opts"

proc singleUserMode*(self: Rewind, communicate: Option[Table[string, string]] = none(Table[string, string]),
                     options: Option[Table[string, string]] = none(Table[string, string])): Option[int] =
  ## Run a command in single-user mode.
  ##
  ## :param communicate: Optional input/output communication.
  ## :param options: Optional postgres options.
  ## :returns: Exit code or none on error.

  # Would build and execute postgres --single command
  result = none(int)

proc cleanupArchiveStatus*(self: Rewind) =
  ## Clean up archive_status directory.
  # Would remove files from archive_status directory
  discard

proc ensureCleanShutdown*(self: Rewind): Option[bool] =
  ## Ensure PostgreSQL has a clean shutdown.
  ##
  ## Start in single-user mode and stop to produce a clean shutdown.
  self.archiveReadyWals()
  self.cleanupArchiveStatus()

  let opts = self.readPostmasterOpts()
  # Would modify opts and run single_user_mode

  result = none(bool)

proc archiveShutdownCheckpointWal*(self: Rewind, archiveCmd: string) =
  ## Archive WAL file with the shutdown checkpoint.
  ##
  ## :param archiveCmd: Archiver command to use.
  # Would get latest checkpoint WAL file from controldata and archive it
  discard

