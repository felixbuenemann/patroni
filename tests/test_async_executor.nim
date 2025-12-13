## Tests for patroni/async_executor module.
## Ported from test_async_executor.py

import std/[unittest, options]
import ../patroni/async_executor

suite "CriticalTask":
  test "newCriticalTask creates empty task":
    let ct = newCriticalTask()
    check ct.isCancelled == false
    check ct.result == ""

  test "complete sets result":
    let ct = newCriticalTask()
    ct.complete("success")
    check ct.result == "success"

  test "completed task cannot be cancelled":
    let ct = newCriticalTask()
    ct.complete("done")
    check ct.cancel() == false

  test "incomplete task can be cancelled":
    let ct = newCriticalTask()
    check ct.isCancelled == false
    check ct.cancel() == true
    check ct.isCancelled == true

  test "reset clears state":
    let ct = newCriticalTask()
    ct.complete("result")
    ct.isCancelled = true
    ct.reset()
    check ct.result == ""
    check ct.isCancelled == false

  test "withCriticalTask template":
    let ct = newCriticalTask()
    ct.withCriticalTask:
      ct.complete("locked")
    check ct.result == "locked"

suite "AsyncExecutor":
  test "newAsyncExecutor creates executor":
    let ae = newAsyncExecutor()
    check ae.criticalTask != nil
    check ae.busy == false

  test "schedule action":
    let ae = newAsyncExecutor()
    check ae.scheduledAction == ""

    let prev = ae.schedule("test_action")
    check prev == ""
    check ae.scheduledAction == "test_action"
    check ae.busy == true

  test "schedule blocks when action already scheduled":
    let ae = newAsyncExecutor()
    discard ae.schedule("first")
    let prev = ae.schedule("second")
    check prev == "first"
    check ae.scheduledAction == "first"

  test "resetScheduledAction clears action":
    let ae = newAsyncExecutor()
    discard ae.schedule("action")
    ae.resetScheduledAction()
    check ae.scheduledAction == ""
    check ae.busy == false

  test "cancel no scheduled action returns false":
    let ae = newAsyncExecutor()
    check ae.cancel() == false

  test "cancel scheduled action":
    let ae = newAsyncExecutor()
    discard ae.schedule("action")
    check ae.cancel() == true
    check ae.isCancelled == true

  test "isCancelled check":
    let ae = newAsyncExecutor()
    check ae.isCancelled == false
    discard ae.schedule("action")
    discard ae.cancel()
    check ae.isCancelled == true

  test "run executes function and resets":
    var called = false
    let ae = newAsyncExecutor()
    discard ae.schedule("action")

    let result = ae.run(proc(): string =
      called = true
      return "result"
    )

    check result == "result"
    check called == true
    check ae.scheduledAction == ""  # Reset after run

  test "run handles exception":
    let ae = newAsyncExecutor()
    discard ae.schedule("action")

    let result = ae.run(proc(): string =
      raise newException(ValueError, "test error")
    )

    check result == ""
    check ae.scheduledAction == ""

  test "runAsync executes and completes task":
    let ae = newAsyncExecutor()
    discard ae.schedule("action")

    discard ae.runAsync(proc(): string =
      return "async_result"
    )

    check ae.criticalTask.result == "async_result"

  test "run with haWakeup callback":
    var wakeupCalled = false
    let ae = newAsyncExecutor(nil, proc() = wakeupCalled = true)
    discard ae.schedule("action")

    discard ae.run(proc(): string = "result", wakeup = true)
    check wakeupCalled == true

  test "run without wakeup flag":
    var wakeupCalled = false
    let ae = newAsyncExecutor(nil, proc() = wakeupCalled = true)
    discard ae.schedule("action")

    discard ae.run(proc(): string = "result", wakeup = false)
    check wakeupCalled == false

  test "tryRunScheduled with no action":
    let ae = newAsyncExecutor()
    let ran = ae.tryRunScheduled(proc(): string = "")
    check ran == false

  test "tryRunScheduled with scheduled action":
    let ae = newAsyncExecutor()
    discard ae.schedule("action")

    var called = false
    let ran = ae.tryRunScheduled(proc(): string =
      called = true
      return ""
    )

    check ran == true
    check called == true

  test "withThreadLock template":
    let ae = newAsyncExecutor()
    var executed = false
    ae.withThreadLock:
      executed = true
    check executed == true

suite "CriticalTask Lock Operations":
  test "acquire and release":
    let ct = newCriticalTask()
    ct.acquire()
    # Should be able to complete while holding lock
    ct.complete("locked")
    ct.release()
    check ct.result == "locked"

suite "AsyncExecutor Integration":
  test "full workflow: schedule, run, complete":
    var steps: seq[string] = @[]
    let ae = newAsyncExecutor(nil, proc() = steps.add("wakeup"))

    steps.add("schedule")
    discard ae.schedule("test_task")
    check ae.busy == true

    steps.add("run")
    discard ae.run(proc(): string =
      steps.add("executing")
      return "done"
    )

    check ae.busy == false
    check steps == @["schedule", "run", "executing", "wakeup"]

when isMainModule:
  echo "test_async_executor.nim tests completed"
