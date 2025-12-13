## Cancellable subprocess execution.

import std/[locks, os, osproc, strformat, options, streams]
when defined(posix):
  import posix
import ../exceptions
import ../log
import ../utils

let logger = getLogger("patroni.postgresql.cancellable")

type
  CancellableExecutor* = ref object of RootObj
    ## There must be only one such process so that AsyncExecutor can easily cancel it.
    process*: Process
    processCmd*: seq[string]
    processChildren*: seq[int]  # PIDs of child processes
    lock*: Lock

  CancellableSubprocess* = ref object of CancellableExecutor
    isCancelledFlag*: bool

proc newCancellableExecutor*(): CancellableExecutor =
  ## Create a new CancellableExecutor instance.
  new(result)
  result.process = nil
  result.processCmd = @[]
  result.processChildren = @[]
  initLock(result.lock)

proc startProcess(ce: CancellableExecutor, cmd: seq[string],
                  options: set[ProcessOption] = {poUsePath}): bool =
  ## Start a process. This method must be executed only when the lock is acquired.
  try:
    ce.processChildren = @[]
    ce.processCmd = cmd
    ce.process = startProcess(cmd[0], args = cmd[1..^1], options = options)
    result = true
  except OSError, IOError:
    logger.exception(fmt"Failed to execute {cmd}", nil)
    result = false

proc killProcess(ce: CancellableExecutor) =
  ## Kill the running process.
  withLock(ce.lock):
    if ce.process != nil and running(ce.process):
      try:
        terminate(ce.process)
        logger.warning(fmt"Killed {ce.processCmd} because it was still running")
      except OSError:
        discard

proc killChildren*(ce: CancellableExecutor) =
  ## Kill child processes.
  withLock(ce.lock):
    for pid in ce.processChildren:
      try:
        when defined(posix):
          discard posix.kill(Pid(pid), SIGKILL)
      except OSError:
        discard
    ce.processChildren = @[]

proc newCancellableSubprocess*(): CancellableSubprocess =
  ## Create a new CancellableSubprocess instance.
  new(result)
  result.process = nil
  result.processCmd = @[]
  result.processChildren = @[]
  initLock(result.lock)
  result.isCancelledFlag = false

proc call*(cs: CancellableSubprocess, cmd: seq[string],
           input: string = ""): Option[int] =
  ## Execute command and wait for it to finish.
  ##
  ## :param cmd: command to execute.
  ## :param input: optional input to send to stdin.
  ##
  ## :returns: exit code of the process or none if cancelled.
  try:
    withLock(cs.lock):
      if cs.isCancelledFlag:
        raise newException(PostgresException, "cancelled")

      cs.isCancelledFlag = false
      var options: set[ProcessOption] = {poUsePath}
      if input.len > 0:
        options.incl(poStdErrToStdOut)

      if not cs.startProcess(cmd, options):
        return none(int)

    if cs.process != nil:
      if input.len > 0:
        let inputStream = inputStream(cs.process)
        inputStream.write(input)
        if input[^1] != '\n':
          inputStream.write("\n")
        inputStream.close()

      let exitCode = waitForExit(cs.process)
      return some(exitCode)
  except PostgresException:
    return none(int)
  finally:
    withLock(cs.lock):
      if cs.process != nil:
        close(cs.process)
        cs.process = nil
    cs.killChildren()

proc resetIsCancelled*(cs: CancellableSubprocess) =
  ## Reset the cancelled flag.
  withLock(cs.lock):
    cs.isCancelledFlag = false

proc isCancelled*(cs: CancellableSubprocess): bool =
  ## Check if the subprocess is cancelled.
  withLock(cs.lock):
    result = cs.isCancelledFlag

proc cancel*(cs: CancellableSubprocess, kill: bool = false) =
  ## Cancel the running subprocess.
  ##
  ## :param kill: if true, force kill after timeout.
  withLock(cs.lock):
    cs.isCancelledFlag = true
    if cs.process == nil or not running(cs.process):
      return

    logger.info(fmt"Terminating {cs.processCmd}")
    terminate(cs.process)

  # Wait for process to terminate
  for i in 0..<100:  # ~10 seconds with 100ms sleep
    withLock(cs.lock):
      if cs.process == nil or not running(cs.process):
        return
    if kill:
      break
    sleep(100)

  cs.killProcess()
