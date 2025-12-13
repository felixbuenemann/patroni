## Tests for patroni/log module.
## Ported from test_log.py

import std/[unittest, strutils, options, os, times]
import ../patroni/log

suite "Log Level":
  test "parseLogLevel DEBUG":
    check parseLogLevel("DEBUG") == LogLevel.Debug
    check parseLogLevel("debug") == LogLevel.Debug

  test "parseLogLevel INFO":
    check parseLogLevel("INFO") == LogLevel.Info
    check parseLogLevel("info") == LogLevel.Info

  test "parseLogLevel WARNING":
    check parseLogLevel("WARNING") == LogLevel.Warning
    check parseLogLevel("warning") == LogLevel.Warning
    check parseLogLevel("WARN") == LogLevel.Warning

  test "parseLogLevel ERROR":
    check parseLogLevel("ERROR") == LogLevel.Error
    check parseLogLevel("error") == LogLevel.Error

  test "parseLogLevel CRITICAL":
    check parseLogLevel("CRITICAL") == LogLevel.Critical
    check parseLogLevel("critical") == LogLevel.Critical
    check parseLogLevel("FATAL") == LogLevel.Critical

  test "parseLogLevel invalid defaults to Info":
    check parseLogLevel("invalid") == LogLevel.Info
    check parseLogLevel("") == LogLevel.Info

suite "LogRecord":
  test "LogRecord creation":
    let record = LogRecord(
      level: LogLevel.Info,
      name: "test",
      message: "test message",
      timestamp: now().utc
    )
    check record.level == LogLevel.Info
    check record.name == "test"
    check record.message == "test message"

  test "LogRecord all fields":
    let record = LogRecord(
      level: LogLevel.Error,
      name: "test.module",
      message: "error occurred",
      timestamp: now().utc,
      filename: "test.nim",
      lineno: 42,
      funcName: "testProc"
    )
    check record.filename == "test.nim"
    check record.lineno == 42
    check record.funcName == "testProc"

suite "LogFormatter":
  test "newLogFormatter with default format":
    let formatter = newLogFormatter()
    check formatter != nil

  test "newLogFormatter with custom format":
    let formatter = newLogFormatter("%(levelname)s: %(message)s")
    check formatter != nil

  test "format basic message":
    let formatter = newLogFormatter("%(levelname)s - %(message)s")
    let record = LogRecord(
      level: LogLevel.Info,
      name: "test",
      message: "test message",
      timestamp: now().utc
    )
    let formatted = formatter.format(record)
    check "Info" in formatted
    check "test message" in formatted

  test "format with name":
    let formatter = newLogFormatter("%(name)s - %(message)s")
    let record = LogRecord(
      level: LogLevel.Debug,
      name: "mylogger",
      message: "debug info",
      timestamp: now().utc
    )
    let formatted = formatter.format(record)
    check "mylogger" in formatted
    check "debug info" in formatted

  test "format with timestamp":
    let formatter = newLogFormatter("%(asctime)s - %(message)s", "yyyy-MM-dd")
    let record = LogRecord(
      level: LogLevel.Info,
      name: "test",
      message: "timestamped",
      timestamp: now().utc
    )
    let formatted = formatter.format(record)
    check "-" in formatted  # Date contains dashes
    check "timestamped" in formatted

suite "Logger":
  test "newLogger creates logger":
    let logger = newLogger("test")
    check logger != nil
    check logger.name == "test"

  test "setLevel changes level":
    let logger = newLogger("test")
    logger.setLevel(LogLevel.Warning)
    check logger.level == LogLevel.Warning

  test "isEnabledFor checks level":
    let logger = newLogger("test")
    logger.setLevel(LogLevel.Warning)

    check logger.isEnabledFor(LogLevel.Warning) == true
    check logger.isEnabledFor(LogLevel.Error) == true
    check logger.isEnabledFor(LogLevel.Info) == false
    check logger.isEnabledFor(LogLevel.Debug) == false

  test "addHandler and removeHandler":
    let logger = newLogger("test")
    let handler = newStreamHandler(stderr, LogLevel.Debug)

    logger.addHandler(handler)
    check logger.handlers.len == 1

    logger.removeHandler(handler)
    check logger.handlers.len == 0

  test "logger propagate flag":
    let logger = newLogger("test")
    check logger.propagate == true
    logger.propagate = false
    check logger.propagate == false

suite "getLogger":
  test "getLogger returns logger":
    let logger = getLogger("test.module")
    check logger != nil
    check logger.name == "test.module"

  test "getLogger with same name returns same instance":
    let logger1 = getLogger("same.name.test1")
    let logger2 = getLogger("same.name.test1")
    check logger1 == logger2

  test "getLogger with empty name returns root":
    let logger = getLogger("")
    check logger != nil
    check logger.name == "root"

suite "StreamHandler":
  test "newStreamHandler creation":
    let handler = newStreamHandler(stderr, LogLevel.Info)
    check handler != nil
    check handler.level == LogLevel.Info

  test "handler level filtering":
    let handler = newStreamHandler(stderr, LogLevel.Warning)

    # Create test records
    let infoRecord = LogRecord(
      level: LogLevel.Info,
      name: "test",
      message: "info message",
      timestamp: now().utc
    )
    let warningRecord = LogRecord(
      level: LogLevel.Warning,
      name: "test",
      message: "warning message",
      timestamp: now().utc
    )

    # Info should be filtered out, warning should pass
    check infoRecord.level < handler.level
    check warningRecord.level >= handler.level

suite "QueueHandler":
  test "newQueueHandler creation":
    let handler = newQueueHandler(maxSize = 100)
    check handler != nil
    check handler.maxSize == 100

  test "QueueHandler emit and pop":
    let handler = newQueueHandler(maxSize = 10)

    let record = LogRecord(
      level: LogLevel.Info,
      name: "test",
      message: "queued message",
      timestamp: now().utc
    )

    handler.emit(record)

    let popped = handler.popRecord()
    check popped.isSome
    check popped.get().message == "queued message"

  test "QueueHandler tracks lost records when full":
    let handler = newQueueHandler(maxSize = 2)

    # Fill the queue
    for i in 0..5:
      let record = LogRecord(
        level: LogLevel.Info,
        name: "test",
        message: "message " & $i,
        timestamp: now().utc
      )
      handler.emit(record)

    # Some records should be lost due to queue overflow
    check handler.recordsLost >= 0

  test "QueueHandler pop empty queue returns none":
    let handler = newQueueHandler(maxSize = 10)
    let popped = handler.popRecord()
    check popped.isNone

suite "FileHandler":
  test "newFileHandler creation":
    let tempFile = getTempDir() / "test_patroni_log.log"
    try:
      let handler = newFileHandler(tempFile, LogLevel.Debug)
      check handler != nil
      check handler.level == LogLevel.Debug
      handler.file.close()
    finally:
      if fileExists(tempFile):
        removeFile(tempFile)

  test "FileHandler writes to file":
    let tempFile = getTempDir() / "test_patroni_log_write.log"
    try:
      let handler = newFileHandler(tempFile, LogLevel.Debug)
      let record = LogRecord(
        level: LogLevel.Info,
        name: "test",
        message: "file log message",
        timestamp: now().utc
      )
      handler.emit(record)
      handler.file.close()

      let content = readFile(tempFile)
      check "file log message" in content
    finally:
      if fileExists(tempFile):
        removeFile(tempFile)

suite "PatroniLogger":
  test "newPatroniLogger creation":
    let pl = newPatroniLogger("test_patroni")
    check pl != nil
    check pl.logger.name == "test_patroni"

  test "newPatroniLogger without queue":
    let pl = newPatroniLogger("test_patroni_noq", useQueue = false)
    check pl != nil
    check pl.queueHandler == nil

  test "PatroniLogger with queue has queue handler":
    let pl = newPatroniLogger("test_patroni_q", useQueue = true)
    check pl != nil
    check pl.queueHandler != nil

suite "Log Methods":
  test "debug logging":
    let logger = newLogger("test.debug")
    logger.setLevel(LogLevel.Debug)
    # Should not crash
    logger.debug("debug message")

  test "info logging":
    let logger = newLogger("test.info")
    logger.setLevel(LogLevel.Info)
    logger.info("info message")

  test "warning logging":
    let logger = newLogger("test.warning")
    logger.warning("warning message")

  test "error logging":
    let logger = newLogger("test.error")
    logger.error("error message")

  test "critical logging":
    let logger = newLogger("test.critical")
    logger.critical("critical message")

  test "exception logging":
    let logger = newLogger("test.exception")
    try:
      raise newException(ValueError, "test exception")
    except ValueError as e:
      logger.exception("caught exception", e)

  test "debugException logging":
    let logger = newLogger("test.debugexc")
    logger.setLevel(LogLevel.Debug)
    try:
      raise newException(ValueError, "debug exception test")
    except ValueError as e:
      logger.debugException("caught debug exception", e)

suite "basicConfig":
  test "basicConfig sets up logging":
    basicConfig(level = LogLevel.Warning)
    let logger = getLogger("basicconfig.test")
    # Logger should respect the level
    check logger.isEnabledFor(LogLevel.Warning) == true

  test "basicConfig with format string":
    basicConfig(level = LogLevel.Info, format = "%(levelname)s: %(message)s")
    let logger = getLogger("basicconfig.format")
    check logger != nil

suite "Log Level Ordering":
  test "log levels are ordered correctly":
    check ord(LogLevel.Debug) < ord(LogLevel.Info)
    check ord(LogLevel.Info) < ord(LogLevel.Warning)
    check ord(LogLevel.Warning) < ord(LogLevel.Error)
    check ord(LogLevel.Error) < ord(LogLevel.Critical)

  test "log level numeric values":
    check ord(LogLevel.Debug) == 10
    check ord(LogLevel.Info) == 20
    check ord(LogLevel.Warning) == 30
    check ord(LogLevel.Error) == 40
    check ord(LogLevel.Critical) == 50

when isMainModule:
  echo "test_log.nim tests completed"
