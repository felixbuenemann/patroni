## Tests for patroni/postgresql/cancellable module.
## Ported from test_cancellable.py

import std/[unittest, options]
import ../patroni/postgresql/cancellable
import ../patroni/exceptions

suite "CancellableExecutor":
  test "newCancellableExecutor creates instance":
    let ce = newCancellableExecutor()
    check ce != nil
    check ce.process == nil
    check ce.processCmd.len == 0
    check ce.processChildren.len == 0

suite "CancellableSubprocess":
  test "newCancellableSubprocess creates instance":
    let cs = newCancellableSubprocess()
    check cs != nil
    check cs.process == nil
    check cs.processCmd.len == 0
    check cs.processChildren.len == 0
    check cs.isCancelled == false

  test "isCancelled flag":
    let cs = newCancellableSubprocess()
    check cs.isCancelled == false

  test "resetIsCancelled clears flag":
    let cs = newCancellableSubprocess()
    # Cancel first
    cs.cancel()
    check cs.isCancelled == true
    # Reset
    cs.resetIsCancelled()
    check cs.isCancelled == false

  test "cancel sets flag":
    let cs = newCancellableSubprocess()
    check cs.isCancelled == false
    cs.cancel()
    check cs.isCancelled == true

  test "call when cancelled returns none":
    let cs = newCancellableSubprocess()
    cs.cancel()
    # Calling after cancel should handle the cancelled state
    let result = cs.call(@["echo", "test"])
    check result.isNone

  test "call with nonexistent command":
    let cs = newCancellableSubprocess()
    # Nonexistent command should return none
    let result = cs.call(@["/nonexistent/command/that/does/not/exist"])
    check result.isNone

  when defined(posix):
    test "call with valid command":
      let cs = newCancellableSubprocess()
      let result = cs.call(@["echo", "test"])
      check result.isSome
      check result.get() == 0  # echo should exit with 0

    test "call with failing command":
      let cs = newCancellableSubprocess()
      let result = cs.call(@["false"])  # false command exits with 1
      check result.isSome
      check result.get() != 0

    test "call with input":
      let cs = newCancellableSubprocess()
      # cat reads from stdin and exits
      let result = cs.call(@["cat"], input = "hello")
      check result.isSome
      check result.get() == 0

suite "Cancel Workflow":
  test "cancel before call":
    let cs = newCancellableSubprocess()
    cs.cancel()
    let result = cs.call(@["echo", "test"])
    check result.isNone
    check cs.isCancelled == true

  test "reset and call again":
    let cs = newCancellableSubprocess()
    cs.cancel()
    check cs.isCancelled == true

    cs.resetIsCancelled()
    check cs.isCancelled == false

    when defined(posix):
      let result = cs.call(@["echo", "after reset"])
      check result.isSome
      check result.get() == 0

suite "Process Management":
  test "process is nil initially":
    let cs = newCancellableSubprocess()
    check cs.process == nil

  test "processCmd is empty initially":
    let cs = newCancellableSubprocess()
    check cs.processCmd.len == 0

  test "processChildren is empty initially":
    let cs = newCancellableSubprocess()
    check cs.processChildren.len == 0

when isMainModule:
  echo "test_cancellable.nim tests completed"
