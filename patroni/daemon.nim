## Daemon processes abstraction module.
##
## This module implements abstraction classes and functions for creating and managing daemon processes in Patroni.
## Currently it is only used for the main "Thread" of ``patroni`` and ``patroni_raft_controller`` commands.

import std/[os, locks, strformat, parseopt]
import ./config
import ./exceptions
import ./log
import ./version

let logger = getLogger("patroni.daemon")

# Systemd notification support
when defined(linux):
  proc sd_notify(unset_environment: cint, state: cstring): cint {.importc, header: "<systemd/sd-daemon.h>", dynlib: "libsystemd.so".}

  proc notifySystemd*(msg: string) =
    ## Notify systemd of daemon state.
    discard sd_notify(0, msg.cstring)
else:
  proc notifySystemd*(msg: string) =
    ## Stub for non-Linux systems.
    discard

# Signal handling
var
  receivedSighup* {.threadvar.}: bool
  receivedSigterm* {.threadvar.}: bool
  sigtermLock*: Lock

proc sighupHandler(sig: cint) {.noconv.} =
  ## Handle SIGHUP signals.
  receivedSighup = true
  notifySystemd("RELOADING=1")

proc sigtermHandler(sig: cint) {.noconv.} =
  ## Handle SIGTERM signals.
  withLock(sigtermLock):
    if not receivedSigterm:
      receivedSigterm = true
      quit(0)

proc setupSignalHandlers*() =
  ## Set up daemon signal handlers.
  ##
  ## Set up SIGHUP and SIGTERM signal handlers.
  ##
  ## .. note::
  ##     SIGHUP is only handled in non-Windows environments.
  receivedSighup = false
  receivedSigterm = false
  initLock(sigtermLock)

  when defined(posix):
    import std/posix
    var sa: Sigaction
    sa.sa_handler = sighupHandler
    discard sigemptyset(sa.sa_mask)
    sa.sa_flags = 0
    discard sigaction(SIGHUP, sa, nil)

    sa.sa_handler = sigtermHandler
    discard sigaction(SIGTERM, sa, nil)

proc getBaseArgParser*(): tuple[configFile: string, showVersion: bool, showHelp: bool] =
  ## Create a basic argument parser with the arguments used for both patroni and raft controller daemon.
  ##
  ## :returns: parsed arguments tuple
  var p = initOptParser()
  result.configFile = ""
  result.showVersion = false
  result.showHelp = false

  while true:
    p.next()
    case p.kind
    of cmdEnd: break
    of cmdShortOption, cmdLongOption:
      case p.key
      of "version", "v":
        result.showVersion = true
      of "help", "h":
        result.showHelp = true
      else:
        discard
    of cmdArgument:
      result.configFile = p.key

type
  AbstractPatroniDaemon* = ref object of RootObj
    ## A Patroni daemon process.
    ##
    ## .. note::
    ##     When inheriting from AbstractPatroniDaemon you are expected to define the methods runCycle
    ##     to determine what it should do in each execution cycle, and shutdown to determine what it should do
    ##     when shutting down.
    ##
    ## :ivar patroniLogger: log handler used by this daemon.
    ## :ivar config: configuration options for this daemon.
    patroniLogger*: PatroniLogger
    config*: Config
    receivedSighup: bool
    receivedSigterm: bool
    sigtermLock: Lock

proc apiSigterm*(self: AbstractPatroniDaemon): bool =
  ## Guarantee only a single SIGTERM is being processed.
  ##
  ## Flag the daemon as "SIGTERM received" with a lock-based approach.
  ##
  ## :returns: true if the daemon was flagged as "SIGTERM received".
  result = false
  withLock(self.sigtermLock):
    if not self.receivedSigterm:
      self.receivedSigterm = true
      result = true

proc isReceivedSigterm*(self: AbstractPatroniDaemon): bool =
  ## If daemon was signaled with SIGTERM.
  withLock(self.sigtermLock):
    result = self.receivedSigterm

method runCycle*(self: AbstractPatroniDaemon) {.base.} =
  ## Define what the daemon should do in each execution cycle.
  ##
  ## Keep being called in the daemon's main loop until the daemon is eventually terminated.
  raise newException(NotImplementedError, "runCycle must be implemented")

method shutdownInternal*(self: AbstractPatroniDaemon) {.base.} =
  ## Define what the daemon should do when shutting down.
  raise newException(NotImplementedError, "shutdownInternal must be implemented")

method reloadConfig*(self: AbstractPatroniDaemon, sighup: bool = false, local: bool = false) {.base.} =
  ## Reload configuration.
  ##
  ## :param sighup: if it is related to a SIGHUP signal.
  ##                The sighup parameter could be used in the method overridden in a child class.
  ## :param local: will be true if there are changes in the local configuration file.
  if local:
    let logConfig = self.config.get("log")
    self.patroniLogger.reloadConfig(logConfig)

proc initAbstractPatroniDaemon*(self: AbstractPatroniDaemon, config: Config) =
  ## Set up signal handlers, logging handler and configuration.
  ##
  ## :param config: configuration options for this daemon.
  self.receivedSighup = false
  self.receivedSigterm = false
  initLock(self.sigtermLock)

  setupSignalHandlers()

  self.patroniLogger = newPatroniLogger()
  self.config = config
  self.reloadConfig(local = true)

proc run*(self: AbstractPatroniDaemon) =
  ## Run the daemon process.
  ##
  ## Start the logger thread and keep running execution cycles until a SIGTERM is eventually received. Also reload
  ## configuration upon receiving SIGHUP.
  notifySystemd("READY=1")
  self.patroniLogger.start()

  while not self.isReceivedSigterm():
    if self.receivedSighup:
      self.receivedSighup = false
      let localChanged = self.config.reloadLocalConfiguration()
      self.reloadConfig(sighup = true, local = localChanged)
      notifySystemd("READY=1")

    self.runCycle()

proc shutdown*(self: AbstractPatroniDaemon) =
  ## Shut the daemon down when a SIGTERM is received.
  ##
  ## Shut down the daemon process and the logger thread.
  withLock(self.sigtermLock):
    self.receivedSigterm = true
  self.shutdownInternal()
  self.patroniLogger.shutdown()

proc abstractMain*[T: AbstractPatroniDaemon](createDaemon: proc(config: Config): T, configFile: string) =
  ## Create the main entry point of a given daemon process.
  ##
  ## :param createDaemon: a proc that creates a daemon instance from config.
  ## :param configFile: path to configuration file.
  var config: Config
  try:
    config = newConfig(configFile)
  except ConfigParseError as e:
    echo fmt"Configuration error: {e.msg}"
    quit(1)

  let controller = createDaemon(config)
  try:
    controller.run()
  except CatchableError:
    discard
  finally:
    controller.shutdown()

proc showVersion*() =
  ## Display version information.
  echo fmt"patroni {VERSION}"

proc showHelp*() =
  ## Display help information.
  echo "Usage: patroni [OPTIONS] [CONFIGFILE]"
  echo ""
  echo "Arguments:"
  echo "  CONFIGFILE    Path to Patroni configuration file"
  echo "                (may also use PATRONI_CONFIGURATION env variable)"
  echo ""
  echo "Options:"
  echo "  --version, -v    Show version and exit"
  echo "  --help, -h       Show this help and exit"
