## Patroni logging facilities.
##
## Daemon processes will use a 2-step logging handler. Whenever a log message is issued it is initially enqueued in-memory
## and is later asynchronously flushed by a thread to the final destination.

import std/[os, strutils, strformat, times, locks, deques, options, tables]
when defined(posix):
  import posix

type
  LogLevel* {.pure.} = enum
    ## Log levels in increasing order of severity
    Debug = 10
    Info = 20
    Warning = 30
    Error = 40
    Critical = 50

  LogRecord* = object
    ## Represents a single log record
    level*: LogLevel
    name*: string
    message*: string
    timestamp*: DateTime
    filename*: string
    lineno*: int
    funcName*: string
    formatted*: string

  LogHandler* = ref object of RootObj
    ## Base log handler
    level*: LogLevel
    formatter*: LogFormatter

  StreamHandler* = ref object of LogHandler
    ## Handler that writes to a stream (stdout/stderr)
    stream*: File

  FileHandler* = ref object of LogHandler
    ## Handler that writes to a file
    filename*: string
    file*: File
    mode*: int
    maxBytes*: int
    backupCount*: int
    currentBytes*: int

  QueueHandler* = ref object of LogHandler
    ## Queue-based logging handler for async logging
    queue*: Deque[LogRecord]
    lock*: Lock
    recordsLost*: int
    maxSize*: int

  LogFormatter* = ref object
    ## Formats log records
    format*: string
    datefmt*: string

  Logger* = ref object
    ## Logger instance
    name*: string
    level*: LogLevel
    handlers*: seq[LogHandler]
    parent*: Logger
    propagate*: bool

  PatroniLogger* = ref object
    ## Main Patroni logger with async queue support
    logger*: Logger
    queueHandler*: QueueHandler
    flushThread*: Thread[PatroniLogger]
    running*: bool
    lock*: Lock

var
  loggers {.threadvar.}: Table[string, Logger]
  rootLogger* {.threadvar.}: Logger
  loggersLock*: Lock

initLock(loggersLock)

# LogFormatter implementation

proc newLogFormatter*(format: string = "%(levelname)s - %(name)s - %(message)s",
                      datefmt: string = "yyyy-MM-dd HH:mm:ss"): LogFormatter =
  ## Create a new LogFormatter.
  new(result)
  result.format = format
  result.datefmt = datefmt

proc format*(f: LogFormatter, record: LogRecord): string =
  ## Format a log record.
  result = f.format
  result = result.replace("%(levelname)s", $record.level)
  result = result.replace("%(name)s", record.name)
  result = result.replace("%(message)s", record.message)
  result = result.replace("%(asctime)s", record.timestamp.format(f.datefmt))
  result = result.replace("%(filename)s", record.filename)
  result = result.replace("%(lineno)d", $record.lineno)
  result = result.replace("%(funcName)s", record.funcName)

# LogHandler implementations

proc newStreamHandler*(stream: File = stderr, level: LogLevel = LogLevel.Debug): StreamHandler =
  ## Create a new StreamHandler.
  new(result)
  result.stream = stream
  result.level = level
  result.formatter = newLogFormatter()

proc newFileHandler*(filename: string, level: LogLevel = LogLevel.Debug,
                     mode: int = 0o644, maxBytes: int = 0, backupCount: int = 0): FileHandler =
  ## Create a new FileHandler with optional rotation.
  new(result)
  result.filename = filename
  result.level = level
  result.mode = mode
  result.maxBytes = maxBytes
  result.backupCount = backupCount
  result.currentBytes = 0
  result.formatter = newLogFormatter()

  # Open the file
  result.file = open(filename, fmAppend)

  # Set file permissions
  when defined(posix):
    discard chmod(filename.cstring, Mode(mode))

proc newQueueHandler*(maxSize: int = 10000): QueueHandler =
  ## Create a new QueueHandler for async logging.
  new(result)
  result.queue = initDeque[LogRecord]()
  initLock(result.lock)
  result.recordsLost = 0
  result.maxSize = maxSize
  result.level = LogLevel.Debug
  result.formatter = newLogFormatter()

method emit*(h: LogHandler, record: LogRecord) {.base, gcsafe.} =
  ## Base emit method - must be overridden.
  discard

method emit*(h: StreamHandler, record: LogRecord) {.gcsafe.} =
  ## Emit a log record to the stream.
  if record.level.ord >= h.level.ord:
    let msg = if record.formatted.len > 0: record.formatted
              else: h.formatter.format(record)
    h.stream.writeLine(msg)
    h.stream.flushFile()

method emit*(h: FileHandler, record: LogRecord) {.gcsafe.} =
  ## Emit a log record to the file with optional rotation.
  if record.level.ord >= h.level.ord:
    let msg = if record.formatted.len > 0: record.formatted
              else: h.formatter.format(record)

    # Check if rotation is needed
    if h.maxBytes > 0 and h.currentBytes + msg.len > h.maxBytes:
      h.file.close()

      # Rotate files
      for i in countdown(h.backupCount - 1, 1):
        let src = fmt"{h.filename}.{i}"
        let dst = fmt"{h.filename}.{i + 1}"
        if fileExists(src):
          moveFile(src, dst)

      if fileExists(h.filename):
        moveFile(h.filename, fmt"{h.filename}.1")

      h.file = open(h.filename, fmWrite)
      h.currentBytes = 0

    h.file.writeLine(msg)
    h.file.flushFile()
    h.currentBytes += msg.len + 1

method emit*(h: QueueHandler, record: LogRecord) {.gcsafe.} =
  ## Emit a log record to the queue.
  if record.level.ord >= h.level.ord:
    withLock(h.lock):
      if h.queue.len >= h.maxSize:
        h.recordsLost += 1
      else:
        var r = record
        r.formatted = h.formatter.format(record)
        h.queue.addLast(r)

proc tryReportLostRecords*(h: QueueHandler) =
  ## Report lost records if any.
  withLock(h.lock):
    if h.recordsLost > 0:
      let msg = fmt"QueueHandler has lost {h.recordsLost} log records"
      var record = LogRecord(
        level: LogLevel.Warning,
        name: "patroni.log",
        message: msg,
        timestamp: now().utc
      )
      record.formatted = h.formatter.format(record)
      h.queue.addLast(record)
      h.recordsLost = 0

proc popRecord*(h: QueueHandler): Option[LogRecord] =
  ## Pop a record from the queue if available.
  withLock(h.lock):
    if h.queue.len > 0:
      result = some(h.queue.popFirst())
    else:
      result = none(LogRecord)

# Logger implementation

proc newLogger*(name: string): Logger =
  ## Create a new Logger.
  new(result)
  result.name = name
  result.level = LogLevel.Debug
  result.handlers = @[]
  result.propagate = true

proc setLevel*(logger: Logger, level: LogLevel) =
  ## Set the logger's level.
  logger.level = level

proc addHandler*(logger: Logger, handler: LogHandler) =
  ## Add a handler to the logger.
  logger.handlers.add(handler)

proc removeHandler*(logger: Logger, handler: LogHandler) =
  ## Remove a handler from the logger.
  let idx = logger.handlers.find(handler)
  if idx >= 0:
    logger.handlers.delete(idx)

proc makeRecord(logger: Logger, level: LogLevel, msg: string,
                filename: string = "", lineno: int = 0, funcName: string = ""): LogRecord =
  ## Create a log record.
  result = LogRecord(
    level: level,
    name: logger.name,
    message: msg,
    timestamp: now().utc,
    filename: filename,
    lineno: lineno,
    funcName: funcName
  )

proc handle(logger: Logger, record: LogRecord) =
  ## Handle a log record by passing it to all handlers.
  for handler in logger.handlers:
    handler.emit(record)

  if logger.propagate and logger.parent != nil:
    logger.parent.handle(record)

proc isEnabledFor*(logger: Logger, level: LogLevel): bool =
  ## Check if the logger is enabled for the given level.
  result = level.ord >= logger.level.ord

proc log*(logger: Logger, level: LogLevel, msg: string) =
  ## Log a message at the given level.
  if logger.isEnabledFor(level):
    let record = makeRecord(logger, level, msg)
    logger.handle(record)

proc debug*(logger: Logger, msg: string) =
  ## Log a debug message.
  logger.log(LogLevel.Debug, msg)

proc info*(logger: Logger, msg: string) =
  ## Log an info message.
  logger.log(LogLevel.Info, msg)

proc warning*(logger: Logger, msg: string) =
  ## Log a warning message.
  logger.log(LogLevel.Warning, msg)

proc error*(logger: Logger, msg: string) =
  ## Log an error message.
  logger.log(LogLevel.Error, msg)

proc critical*(logger: Logger, msg: string) =
  ## Log a critical message.
  logger.log(LogLevel.Critical, msg)

proc exception*(logger: Logger, msg: string, e: ref Exception = nil) =
  ## Log an exception with optional exception object.
  var fullMsg = msg
  if e != nil:
    fullMsg = fmt"{msg}, DETAIL: '{e.msg}'"
  logger.log(LogLevel.Error, fullMsg)

proc debugException*(logger: Logger, msg: string, e: ref Exception = nil) =
  ## Add full stack trace info to debug log messages and partial to others.
  ##
  ## If logger level is DEBUG, issue a DEBUG message with complete stack trace.
  ## If logger level is INFO or higher, issue an ERROR message with only the last line.
  if logger.isEnabledFor(LogLevel.Debug):
    var fullMsg = msg
    if e != nil:
      fullMsg = fmt"{msg}\n{e.msg}\n{e.getStackTrace()}"
    logger.debug(fullMsg)
  else:
    var fullMsg = msg
    if e != nil:
      fullMsg = fmt"{msg}, DETAIL: '{e.msg}'"
    logger.error(fullMsg)

proc getLogger*(name: string = ""): Logger =
  ## Get a logger by name, creating it if necessary.
  withLock(loggersLock):
    if name == "":
      if rootLogger == nil:
        rootLogger = newLogger("root")
        rootLogger.addHandler(newStreamHandler())
      return rootLogger

    if name in loggers:
      return loggers[name]

    let logger = newLogger(name)
    if rootLogger == nil:
      rootLogger = newLogger("root")
      rootLogger.addHandler(newStreamHandler())
    logger.parent = rootLogger
    loggers[name] = logger
    return logger

proc basicConfig*(level: LogLevel = LogLevel.Info,
                  format: string = "%(asctime)s - %(levelname)s - %(name)s - %(message)s",
                  datefmt: string = "yyyy-MM-dd HH:mm:ss",
                  filename: string = "",
                  stream: File = stderr) =
  ## Configure the root logger.
  if rootLogger == nil:
    rootLogger = newLogger("root")

  rootLogger.level = level
  rootLogger.handlers = @[]

  let formatter = newLogFormatter(format, datefmt)

  if filename != "":
    let handler = newFileHandler(filename, level)
    handler.formatter = formatter
    rootLogger.addHandler(handler)
  else:
    let handler = newStreamHandler(stream, level)
    handler.formatter = formatter
    rootLogger.addHandler(handler)

# PatroniLogger with async queue

proc flushLoop(pl: PatroniLogger) {.thread.} =
  ## Background thread that flushes log records from the queue.
  while pl.running:
    if pl.queueHandler != nil:
      pl.queueHandler.tryReportLostRecords()

      while true:
        let recordOpt = pl.queueHandler.popRecord()
        if recordOpt.isNone:
          break

        let record = recordOpt.get()
        for handler in pl.logger.handlers:
          if handler != pl.queueHandler:
            handler.emit(record)

    sleep(100)  # 100ms between flushes

proc newPatroniLogger*(name: string = "patroni", useQueue: bool = true): PatroniLogger =
  ## Create a new PatroniLogger with optional async queue.
  new(result)
  result.logger = getLogger(name)
  result.running = false
  initLock(result.lock)

  if useQueue:
    result.queueHandler = newQueueHandler()
    result.logger.addHandler(result.queueHandler)

proc start*(pl: PatroniLogger) =
  ## Start the background flush thread.
  pl.running = true
  createThread(pl.flushThread, flushLoop, pl)

proc stop*(pl: PatroniLogger) =
  ## Stop the background flush thread.
  pl.running = false
  joinThread(pl.flushThread)

proc updateLoggers*(loggerNames: seq[string], level: LogLevel) =
  ## Update log levels for multiple loggers.
  for name in loggerNames:
    let logger = getLogger(name)
    logger.setLevel(level)

proc parseLogLevel*(s: string): LogLevel =
  ## Parse a log level from string.
  case s.toUpperAscii()
  of "DEBUG": result = LogLevel.Debug
  of "INFO": result = LogLevel.Info
  of "WARNING", "WARN": result = LogLevel.Warning
  of "ERROR": result = LogLevel.Error
  of "CRITICAL", "FATAL": result = LogLevel.Critical
  else: result = LogLevel.Info
