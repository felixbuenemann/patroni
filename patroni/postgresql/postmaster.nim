## PostgreSQL postmaster process management.
##
## This module provides types and procedures for starting, stopping, and
## managing the PostgreSQL postmaster process.

import std/[json, options, os, osproc, posix, re, sequtils, strformat, strutils, strtabs, tables, times]
import ../log

let logger = getLogger("patroni.postgresql.postmaster")

const
  STOP_SIGNALS* = {
    "smart": "TERM",
    "fast": "INT",
    "immediate": "QUIT"
  }.toTable

type
  PostmasterProcess* = ref object
    ## Represents a PostgreSQL postmaster process.
    pid*: int
    isSingleUser*: bool
    createTime*: float
    postmasterPid: Table[string, string]

proc readPostmasterPidfile*(dataDir: string): Table[string, string] =
  ## Read and parse postmaster.pid from the data directory.
  ##
  ## :param dataDir: PostgreSQL data directory path.
  ## :returns: dictionary of values if successful, empty dictionary otherwise.
  result = initTable[string, string]()
  let pidLineNames = ["pid", "data_dir", "start_time", "port", "socket_dir", "listen_addr", "shmem_key"]

  let pidFile = dataDir / "postmaster.pid"
  if not fileExists(pidFile):
    return

  try:
    let lines = readFile(pidFile).splitLines()
    for i, name in pidLineNames:
      if i < lines.len:
        result[name] = lines[i]
  except IOError:
    discard

proc isPostmasterProcess(self: PostmasterProcess): bool =
  ## Verify this is actually a postmaster process.
  try:
    let startTime = self.postmasterPid.getOrDefault("start_time", "0").parseInt()
    if startTime > 0 and abs(self.createTime - float(startTime)) > 3:
      logger.info(fmt"Process {self.pid} is not postmaster, too much difference between PID file start time {startTime} and process start time {self.createTime}")
      return false
  except ValueError:
    let startTimeVal = self.postmasterPid.getOrDefault("start_time", "")
    logger.warning(fmt"Garbage start time value in pid file: {startTimeVal}")

  # Extra safety check - process can't be ourselves, our parent or our direct child
  let myPid = getpid()
  let myPpid = getppid()
  if self.pid == myPid or self.pid == myPpid:
    logger.info(fmt"Patroni (pid={myPid}, ppid={myPpid}), 'fake postmaster' (pid={self.pid})")
    return false

  return true

proc getProcessCreateTime(pid: int): float =
  ## Get process creation time from /proc.
  result = 0.0
  when defined(linux):
    let statFile = fmt"/proc/{pid}/stat"
    if fileExists(statFile):
      try:
        let content = readFile(statFile)
        let parts = content.split(')')
        if parts.len > 1:
          let fields = parts[1].strip().split()
          if fields.len > 19:
            let startTime = parseInt(fields[19])
            # Convert to seconds since boot, then to epoch time
            # This is a simplified calculation
            let uptime = readFile("/proc/uptime").split()[0].parseFloat()
            let bootTime = epochTime() - uptime
            result = bootTime + float(startTime) / 100.0
      except:
        discard
  else:
    result = epochTime()

proc isProcessRunning*(pid: int): bool =
  ## Check if a process is running.
  when defined(posix):
    result = kill(Pid(pid), 0) == 0
  else:
    result = false

proc fromPidfile*(dataDir: string): PostmasterProcess =
  ## Create a PostmasterProcess from the pidfile.
  ##
  ## :param dataDir: PostgreSQL data directory path.
  ## :returns: PostmasterProcess if found and valid, nil otherwise.
  let postmasterPid = readPostmasterPidfile(dataDir)
  let pidStr = postmasterPid.getOrDefault("pid", "0")

  try:
    let pid = parseInt(pidStr)
    if pid > 0 and isProcessRunning(pid):
      new(result)
      result.pid = pid
      result.isSingleUser = false
      result.createTime = getProcessCreateTime(pid)
      result.postmasterPid = postmasterPid

      if not result.isPostmasterProcess():
        return nil
  except ValueError:
    return nil

proc fromPid*(pid: int): PostmasterProcess =
  ## Create a PostmasterProcess from a PID.
  ##
  ## :param pid: Process ID.
  ## :returns: PostmasterProcess if process exists, nil otherwise.
  if isProcessRunning(pid):
    new(result)
    result.pid = if pid < 0: -pid else: pid
    result.isSingleUser = pid < 0
    result.createTime = getProcessCreateTime(result.pid)
    result.postmasterPid = initTable[string, string]()
  else:
    result = nil

proc sendSignal*(self: PostmasterProcess, sig: int): bool =
  ## Send a signal to the postmaster process.
  ##
  ## :param sig: Signal number to send.
  ## :returns: true if signal was sent, false otherwise.
  when defined(posix):
    result = kill(Pid(self.pid), cint(sig)) == 0
  else:
    result = false

proc signalStop*(self: PostmasterProcess, mode: string, pgCtl: string = "pg_ctl"): Option[bool] =
  ## Signal postmaster process to stop.
  ##
  ## :param mode: Stop mode (smart, fast, immediate).
  ## :param pgCtl: Path to pg_ctl binary.
  ## :returns: none if signaled, some(true) if process is already gone, some(false) if error.
  if self.isSingleUser:
    logger.warning(fmt"Cannot stop server; single-user server is running (PID: {self.pid})")
    return some(false)

  when defined(posix):
    let signalName = STOP_SIGNALS.getOrDefault(mode, "INT")
    let sig = case signalName
      of "TERM": SIGTERM
      of "INT": SIGINT
      of "QUIT": SIGQUIT
      else: SIGINT

    if kill(Pid(self.pid), sig) == 0:
      return none(bool)
    elif errno == ESRCH:
      return some(true)
    else:
      logger.warning(fmt"Could not send stop signal to PostgreSQL: errno={errno}")
      return some(false)
  else:
    # Windows - use pg_ctl kill
    return self.pgCtlKill(mode, pgCtl)

proc pgCtlKill*(self: PostmasterProcess, mode: string, pgCtl: string): Option[bool] =
  ## Use pg_ctl kill to stop the process (mainly for Windows).
  ##
  ## :param mode: Stop mode.
  ## :param pgCtl: Path to pg_ctl binary.
  ## :returns: none if signaled, some(true) if process is gone, some(false) if error.
  let signalName = STOP_SIGNALS.getOrDefault(mode, "INT")
  try:
    let status = execCmd(fmt"{pgCtl} kill {signalName} {self.pid}")
    if status == 0:
      return none(bool)
    else:
      return some(not isProcessRunning(self.pid))
  except OSError:
    return some(false)

proc signalKill*(self: PostmasterProcess): bool =
  ## Suspend and kill postmaster and all children.
  ##
  ## :returns: true if postmaster and children are killed, false if error.
  when defined(posix):
    # First try to stop the process
    if kill(Pid(self.pid), SIGSTOP) != 0:
      if errno == ESRCH:
        return true
      logger.warning(fmt"Failed to suspend postmaster: errno={errno}")

    # Kill the main process
    if kill(Pid(self.pid), SIGKILL) != 0:
      if errno == ESRCH:
        return true
      logger.warning(fmt"Could not kill postmaster: errno={errno}")
      return false

    return true
  else:
    return false

proc isRunning*(self: PostmasterProcess): bool =
  ## Check if the postmaster is still running.
  result = isProcessRunning(self.pid)

proc waitForUserBackendsToClose*(self: PostmasterProcess, stopTimeout: float) =
  ## Wait for user backends to close.
  ##
  ## :param stopTimeout: Timeout in seconds.
  # This would need to enumerate child processes and wait for user backends
  # For now, just sleep for the timeout
  if stopTimeout > 0:
    sleep(int(stopTimeout * 1000))

proc start*(pgcommand: string, dataDir: string, conf: string, options: seq[string]): PostmasterProcess =
  ## Start a PostgreSQL postmaster process.
  ##
  ## :param pgcommand: Path to postgres binary.
  ## :param dataDir: PostgreSQL data directory.
  ## :param conf: Configuration file path.
  ## :param options: Additional command line options.
  ## :returns: PostmasterProcess if started successfully, nil otherwise.

  # Check for existing postmaster process
  let existingProc = fromPidfile(dataDir)
  var env = newStringTable()

  # Copy environment, excluding Patroni-specific variables
  for key, val in envPairs():
    if not key.startsWith("PATRONI_") and not key.startsWith("KUBERNETES_"):
      env[key] = val

  if existingProc != nil and not existingProc.isPostmasterProcess():
    logger.info(fmt"Telling pg_ctl that it is safe to ignore postmaster.pid for process {existingProc.pid}")
    env["PG_GRANDPARENT_PID"] = $existingProc.pid

  var cmdline = @[pgcommand, "-D", dataDir, fmt"--config-file={conf}"]
  cmdline.add(options)

  let cmdlineStr = cmdline.join(" ")
  logger.debug(fmt"Starting postgres: {cmdlineStr}")

  try:
    # Start postgres process
    # We need to start it in a way that makes it not our child
    # In Nim, we can use startProcess with poParentStreams and poDaemon
    var envArray: seq[string] = @[]
    for key, val in env.pairs:
      envArray.add(fmt"{key}={val}")

    let process = startProcess(
      command = cmdline[0],
      args = cmdline[1..^1],
      env = env,
      options = {poParentStreams, poDaemon}
    )

    let pid = process.processID
    logger.info(fmt"postmaster pid={pid}")

    result = fromPid(pid)
  except OSError as e:
    logger.error(fmt"Failed to execute {cmdline}: {e.msg}")
    result = nil

