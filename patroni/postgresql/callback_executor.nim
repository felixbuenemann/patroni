## Callback executor for PostgreSQL lifecycle events.

import std/[locks, os, osproc, strformat]
import ./cancellable
import ../log

let logger = getLogger("patroni.postgresql.callback_executor")

type
  CallbackAction* = enum
    ## Callback action types.
    caNoop = "noop"
    caOnStart = "on_start"
    caOnStop = "on_stop"
    caOnRestart = "on_restart"
    caOnReload = "on_reload"
    caOnRoleChange = "on_role_change"

  OnReloadExecutor* = ref object of CancellableSubprocess
    ## Executor for on_reload callbacks.

  CallbackExecutor* = ref object of CancellableExecutor
    ## Executor for lifecycle callbacks.
    onReloadExecutor: OnReloadExecutor
    cmd: seq[string]
    condition: Cond
    condLock: Lock
    thread: Thread[CallbackExecutor]
    running: bool

proc `$`*(action: CallbackAction): string =
  ## Get string representation of a CallbackAction.
  case action
  of caNoop: result = "noop"
  of caOnStart: result = "on_start"
  of caOnStop: result = "on_stop"
  of caOnRestart: result = "on_restart"
  of caOnReload: result = "on_reload"
  of caOnRoleChange: result = "on_role_change"

proc newOnReloadExecutor*(): OnReloadExecutor =
  ## Create a new OnReloadExecutor instance.
  new(result)
  result.process = nil
  result.processCmd = @[]
  result.processChildren = @[]
  initLock(result.lock)
  result.isCancelledFlag = false

proc callNowait*(ore: OnReloadExecutor, cmd: seq[string]) =
  ## Run one on_reload callback at most.
  ##
  ## To achieve it we always kill already running command including child processes.
  ore.cancel(kill = true)
  ore.killChildren()

  withLock(ore.lock):
    ore.processChildren = @[]
    ore.processCmd = cmd
    try:
      ore.process = startProcess(cmd[0], args = cmd[1..^1], options = {poUsePath})
    except OSError, IOError:
      logger.exception(fmt"Failed to execute {cmd}", nil)
      return

  # Wait for process in background (non-blocking)
  proc waitProc(ore: OnReloadExecutor) {.thread.} =
    if ore.process != nil:
      discard waitForExit(ore.process)
      close(ore.process)

  var waitThread: Thread[OnReloadExecutor]
  createThread(waitThread, waitProc, ore)

proc killProcess(ce: CallbackExecutor) =
  ## Kill the running process.
  withLock(ce.lock):
    if ce.process != nil and running(ce.process):
      try:
        terminate(ce.process)
        logger.warning(fmt"Killed {ce.processCmd} because it was still running")
      except OSError:
        discard

proc killChildren(ce: CallbackExecutor) =
  ## Kill child processes.
  withLock(ce.lock):
    for pid in ce.processChildren:
      try:
        when defined(posix):
          import posix
          discard posix.kill(Pid(pid), SIGKILL)
      except OSError:
        discard
    ce.processChildren = @[]

proc runLoop(ce: CallbackExecutor) {.thread.} =
  ## Main loop for the callback executor thread.
  while ce.running:
    var cmd: seq[string] = @[]

    withLock(ce.condLock):
      if ce.cmd.len == 0:
        wait(ce.condition, ce.condLock)
      cmd = ce.cmd
      ce.cmd = @[]

    if cmd.len > 0:
      withLock(ce.lock):
        ce.processChildren = @[]
        ce.processCmd = cmd
        try:
          ce.process = startProcess(cmd[0], args = cmd[1..^1], options = {poUsePath})
        except OSError, IOError:
          logger.exception(fmt"Failed to execute {cmd}", nil)
          continue

      if ce.process != nil:
        discard waitForExit(ce.process)
        close(ce.process)
        ce.killChildren()

proc newCallbackExecutor*(): CallbackExecutor =
  ## Create a new CallbackExecutor instance.
  new(result)
  result.process = nil
  result.processCmd = @[]
  result.processChildren = @[]
  initLock(result.lock)
  result.onReloadExecutor = newOnReloadExecutor()
  result.cmd = @[]
  initCond(result.condition)
  initLock(result.condLock)
  result.running = true
  createThread(result.thread, runLoop, result)

proc call*(ce: CallbackExecutor, cmd: seq[string]) =
  ## Execute one callback at a time.
  ##
  ## Already running command is killed (including child processes).
  ## If it couldn't be killed we wait until it finishes.
  ##
  ## :param cmd: command to be executed
  logger.debug(fmt"CallbackExecutor.call({cmd})")

  # Check if this is an on_reload callback
  if cmd.len >= 3 and cmd[^3] == $caOnReload:
    ce.onReloadExecutor.callNowait(cmd)
    return

  ce.killProcess()
  withLock(ce.condLock):
    ce.cmd = cmd
    signal(ce.condition)

proc stop*(ce: CallbackExecutor) =
  ## Stop the callback executor thread.
  ce.running = false
  withLock(ce.condLock):
    signal(ce.condition)
  joinThread(ce.thread)
