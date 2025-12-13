## Implement facilities for executing asynchronous tasks.

import std/[locks, options, os, times]
import ./log

type
  CriticalTask* = ref object
    ## Represents a critical task in a background process that we either need to cancel or get the result of.
    ##
    ## Fields of this object may be accessed only when holding a lock on it. To perform the critical task the background
    ## thread must, while holding lock on this object, check `isCancelled` flag, run the task and mark the task as
    ## complete using `complete`.
    ##
    ## The main thread must hold async lock to prevent the task from completing, hold lock on critical task object,
    ## call `cancel`. If the task has completed `cancel` will return false and `result` field will
    ## contain the result of the task. When `cancel` returns true it is guaranteed that the background task will
    ## notice the `isCancelled` flag.
    ##
    ## :ivar isCancelled: if the critical task has been cancelled.
    ## :ivar result: contains the result of the task, if it has already been completed.
    lock: Lock
    isCancelled*: bool
    result*: string

  AsyncExecutor* = ref object
    ## Asynchronous executor of (long) tasks.
    ##
    ## :ivar criticalTask: a CriticalTask instance to handle execution of critical background tasks.
    cancellable*: pointer  # CancellableSubprocess
    haWakeup*: proc()
    threadLock: Lock
    scheduledAction: string
    scheduledActionLock: Lock
    isCancelledFlag: bool
    finishEvent: bool
    criticalTask*: CriticalTask
    logger: Logger

let logger = getLogger("patroni.async_executor")

proc newCriticalTask*(): CriticalTask =
  ## Create a new instance of CriticalTask.
  ##
  ## Instantiate the lock and the task control attributes.
  new(result)
  initLock(result.lock)
  result.isCancelled = false
  result.result = ""

proc reset*(ct: CriticalTask) =
  ## Must be called every time the background task is finished.
  ##
  ## .. note::
  ##     Must be called from async thread. Caller must hold lock on async executor when calling.
  ct.isCancelled = false
  ct.result = ""

proc cancel*(ct: CriticalTask): bool =
  ## Tries to cancel the task.
  ##
  ## .. note::
  ##     Caller must hold lock on async executor and the task when calling.
  ##
  ## :returns: false if the task has already run, or true it has been cancelled.
  if ct.result.len > 0:
    return false
  ct.isCancelled = true
  return true

proc complete*(ct: CriticalTask, resultVal: string) =
  ## Mark task as completed along with a result.
  ##
  ## .. note::
  ##     Must be called from async thread. Caller must hold lock on task when calling.
  ct.result = resultVal

proc acquire*(ct: CriticalTask) =
  ## Acquire the object lock.
  acquire(ct.lock)

proc release*(ct: CriticalTask) =
  ## Release the object lock.
  release(ct.lock)

template withCriticalTask*(ct: CriticalTask, body: untyped) =
  ## Execute body while holding the critical task lock.
  ct.acquire()
  try:
    body
  finally:
    ct.release()

proc newAsyncExecutor*(cancellable: pointer = nil, haWakeup: proc() = nil): AsyncExecutor =
  ## Create a new instance of AsyncExecutor.
  ##
  ## Configure the given cancellable and haWakeup, initializes the control attributes, and instantiate the lock
  ## and event objects that are used to access attributes and manage communication between threads.
  ##
  ## :param cancellable: a subprocess that supports being cancelled.
  ## :param haWakeup: function to wake up the HA loop.
  new(result)
  result.cancellable = cancellable
  result.haWakeup = haWakeup
  initLock(result.threadLock)
  result.scheduledAction = ""
  initLock(result.scheduledActionLock)
  result.isCancelledFlag = false
  result.finishEvent = false
  result.criticalTask = newCriticalTask()
  result.logger = logger

proc busy*(ae: AsyncExecutor): bool =
  ## true if there is an action scheduled to occur, else false.
  result = ae.scheduledAction.len > 0

proc schedule*(ae: AsyncExecutor, action: string): string =
  ## Schedule action to be executed.
  ##
  ## .. note::
  ##     Must be called before executing a task.
  ##
  ## .. note::
  ##     action can only be scheduled if there is no other action currently scheduled.
  ##
  ## :param action: action to be executed.
  ##
  ## :returns: empty string if action has been successfully scheduled, or the previously scheduled action, if any.
  withLock(ae.scheduledActionLock):
    if ae.scheduledAction.len > 0:
      return ae.scheduledAction
    ae.scheduledAction = action
    ae.isCancelledFlag = false
    ae.finishEvent = true
  return ""

proc scheduledAction*(ae: AsyncExecutor): string =
  ## The currently scheduled action, if any, else empty string.
  withLock(ae.scheduledActionLock):
    result = ae.scheduledAction

proc resetScheduledAction*(ae: AsyncExecutor) =
  ## Unschedule a previously scheduled action, if any.
  ##
  ## .. note::
  ##     Must be called once the scheduled task finishes or is cancelled.
  withLock(ae.scheduledActionLock):
    ae.scheduledAction = ""

proc run*(ae: AsyncExecutor, fn: proc(): string, wakeup: bool = true): string =
  ## Run fn and optionally wake up HA loop.
  ##
  ## .. note::
  ##     Expected to be executed through a thread.
  ##
  ## :param fn: function to be run. If it returns anything other than empty string, HA loop will be woken up at the end
  ##     if wakeup is true.
  ##
  ## :returns: the value returned by fn.
  withLock(ae.threadLock):
    try:
      result = fn()
    except CatchableError as e:
      ae.logger.exception("Exception during task execution", e)
      result = ""
    finally:
      ae.resetScheduledAction()
      ae.criticalTask.reset()

    if result.len > 0 and wakeup and ae.haWakeup != nil:
      ae.haWakeup()

proc runAsync*(ae: AsyncExecutor, fn: proc(): string): string =
  ## Run fn asynchronously.
  ##
  ## .. note::
  ##     This is a non-blocking call. The task is executed in a background thread.
  ##
  ## :param fn: function to be run.
  ##
  ## :returns: empty string immediately; actual result available through criticalTask.
  withLock(ae.threadLock):
    try:
      result = fn()
      ae.criticalTask.complete(result)
    except CatchableError as e:
      ae.logger.exception("Exception during async task execution", e)
      ae.criticalTask.complete("")
    finally:
      ae.resetScheduledAction()

  return ""

proc cancel*(ae: AsyncExecutor): bool =
  ## Cancel the currently scheduled action.
  ##
  ## :returns: true if action was successfully cancelled, false otherwise.
  withLock(ae.scheduledActionLock):
    if ae.scheduledAction.len == 0:
      return false
    ae.isCancelledFlag = true

  ae.criticalTask.withCriticalTask:
    result = ae.criticalTask.cancel()

  return true

proc isCancelled*(ae: AsyncExecutor): bool =
  ## Check if the current action has been cancelled.
  withLock(ae.scheduledActionLock):
    result = ae.isCancelledFlag

proc tryRunScheduled*(ae: AsyncExecutor, fn: proc(): string): bool =
  ## Try to run fn if an action is scheduled.
  ##
  ## .. note::
  ##     This is useful for integrating with the main HA loop.
  ##
  ## :param fn: function to be run.
  ##
  ## :returns: true if an action was scheduled and fn was run, false otherwise.
  let action = ae.scheduledAction()
  if action.len == 0:
    return false

  ae.logger.info("Running scheduled action: " & action)
  discard ae.run(fn)
  return true

proc waitForResult*(ae: AsyncExecutor, timeout: float = 10.0): Option[string] =
  ## Wait for the critical task to complete.
  ##
  ## :param timeout: maximum time to wait in seconds.
  ##
  ## :returns: the result if completed within timeout, none otherwise.
  let deadline = epochTime() + timeout

  while epochTime() < deadline:
    ae.criticalTask.withCriticalTask:
      if ae.criticalTask.result.len > 0:
        return some(ae.criticalTask.result)
    sleep(10)  # 10ms polling

  return none(string)

proc acquireThread*(ae: AsyncExecutor) =
  ## Acquire the thread lock.
  acquire(ae.threadLock)

proc releaseThread*(ae: AsyncExecutor) =
  ## Release the thread lock.
  release(ae.threadLock)

template withThreadLock*(ae: AsyncExecutor, body: untyped) =
  ## Execute body while holding the thread lock.
  ae.acquireThread()
  try:
    body
  finally:
    ae.releaseThread()
