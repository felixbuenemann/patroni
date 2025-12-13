## Watchdog base implementation.
##
## Provides hardware or software watchdog support for ensuring node failover
## if Patroni becomes unresponsive.

import std/[json, locks, options, strutils, tables, strformat]
import ../config
import ../exceptions
import ../log

export exceptions

let logger = getLogger("patroni.watchdog.base")

const
  MODE_REQUIRED* = "required"   # Will not run if a watchdog is not available
  MODE_AUTOMATIC* = "automatic" # Will use a watchdog if one is available
  MODE_OFF* = "off"             # Will not try to use a watchdog

type
  WatchdogBase* = ref object of RootObj
    ## Abstract base class for watchdog implementations.

  NullWatchdog* = ref object of WatchdogBase
    ## Null implementation that does nothing.

  WatchdogConfig* = ref object
    ## Helper to contain a snapshot of configuration.
    mode*: string
    ttl*: int
    loopWait*: int
    safetyMargin*: int
    driver*: string
    driverConfig*: Table[string, JsonNode]

  Watchdog* = ref object
    ## Facade to dynamically manage watchdog implementations and handle config changes.
    ##
    ## When activation fails underlying implementation will be switched to a Null implementation.
    ## To avoid log spam activation will only be retried when watchdog configuration is changed.
    config*: WatchdogConfig
    activeConfig*: WatchdogConfig
    lock*: Lock
    active*: bool
    impl*: WatchdogBase

proc parseMode*(mode: string): string =
  ## Parse watchdog mode string.
  let m = mode.toLowerAscii()
  case m
  of "require", "required":
    result = MODE_REQUIRED
  of "auto", "automatic":
    result = MODE_AUTOMATIC
  of "off", "disable", "disabled":
    result = MODE_OFF
  else:
    logger.warning(fmt"Watchdog mode {mode} not recognized, disabling watchdog")
    result = MODE_OFF

proc parseModeFromBool*(mode: bool): string =
  ## Parse watchdog mode from boolean.
  if mode:
    result = MODE_AUTOMATIC
  else:
    result = MODE_OFF

# WatchdogBase implementation

method open*(self: WatchdogBase) {.base.} =
  ## Open the watchdog device.
  discard

method close*(self: WatchdogBase) {.base.} =
  ## Close the watchdog device.
  discard

method keepalive*(self: WatchdogBase) {.base.} =
  ## Send keepalive to the watchdog.
  discard

method isRunning*(self: WatchdogBase): bool {.base.} =
  ## Check if the watchdog is running.
  result = false

method isNull*(self: WatchdogBase): bool {.base.} =
  ## Check if this is a null implementation.
  result = false

method canBeDisabled*(self: WatchdogBase): bool {.base.} =
  ## Check if the watchdog can be disabled.
  result = true

method hasSetTimeout*(self: WatchdogBase): bool {.base.} =
  ## Check if set_timeout is supported.
  result = false

method setTimeout*(self: WatchdogBase, timeout: int) {.base.} =
  ## Set the watchdog timeout.
  discard

method getTimeout*(self: WatchdogBase): Option[int] {.base.} =
  ## Get the current timeout.
  result = none(int)

method describe*(self: WatchdogBase): string {.base.} =
  ## Get a description of the watchdog.
  result = "WatchdogBase"

# NullWatchdog implementation

proc newNullWatchdog*(): NullWatchdog =
  ## Create a new NullWatchdog instance.
  new(result)

method isNull*(self: NullWatchdog): bool =
  result = true

method isRunning*(self: NullWatchdog): bool =
  result = false

method describe*(self: NullWatchdog): string =
  result = "NullWatchdog"

# WatchdogConfig implementation

proc newWatchdogConfig*(config: Config): WatchdogConfig =
  ## Create a WatchdogConfig from Config.
  new(result)

  let watchdogConfig = config.get("watchdog")

  if watchdogConfig != nil and watchdogConfig.kind == JObject:
    if "mode" in watchdogConfig:
      let modeVal = watchdogConfig["mode"]
      case modeVal.kind
      of JBool:
        result.mode = parseModeFromBool(modeVal.getBool())
      of JString:
        result.mode = parseMode(modeVal.getStr())
      else:
        result.mode = MODE_AUTOMATIC
    else:
      result.mode = MODE_AUTOMATIC

    if "safety_margin" in watchdogConfig:
      result.safetyMargin = watchdogConfig["safety_margin"].getInt(5)
    else:
      result.safetyMargin = 5

    if "driver" in watchdogConfig:
      result.driver = watchdogConfig["driver"].getStr("default")
    else:
      result.driver = "default"

    result.driverConfig = initTable[string, JsonNode]()
    for key, value in watchdogConfig.pairs:
      if key notin ["mode", "safety_margin", "driver"]:
        result.driverConfig[key] = value
  else:
    result.mode = MODE_AUTOMATIC
    result.safetyMargin = 5
    result.driver = "default"
    result.driverConfig = initTable[string, JsonNode]()

  result.ttl = config.getInt("ttl", 30)
  result.loopWait = config.getInt("loop_wait", 10)

proc `==`*(a, b: WatchdogConfig): bool =
  ## Compare two WatchdogConfig instances.
  result = a.mode == b.mode and
           a.ttl == b.ttl and
           a.loopWait == b.loopWait and
           a.safetyMargin == b.safetyMargin and
           a.driver == b.driver

proc timeout*(self: WatchdogConfig): int =
  ## Calculate the watchdog timeout.
  if self.safetyMargin == -1:
    result = self.ttl div 2
  else:
    result = self.ttl - self.safetyMargin

proc timingSlack*(self: WatchdogConfig): int =
  ## Calculate the timing slack.
  result = self.timeout - self.loopWait

proc getImpl*(self: WatchdogConfig): WatchdogBase =
  ## Get the appropriate watchdog implementation.
  when defined(linux):
    if self.driver == "default":
      # Would use LinuxWatchdogDevice here
      result = newNullWatchdog()
    else:
      result = newNullWatchdog()
  else:
    result = newNullWatchdog()

# Watchdog implementation

proc newWatchdog*(config: Config): Watchdog =
  ## Create a new Watchdog instance.
  new(result)
  result.config = newWatchdogConfig(config)
  result.activeConfig = result.config
  initLock(result.lock)
  result.active = false

  if result.config.mode == MODE_OFF:
    result.impl = newNullWatchdog()
  else:
    result.impl = result.config.getImpl()
    if result.config.mode == MODE_REQUIRED and result.impl.isNull:
      logger.error("Configuration requires a watchdog, but watchdog is not supported on this platform.")
      quit(1)

proc setTimeoutInternal(self: Watchdog): Option[int] =
  ## Set timeout and return actual timeout.
  if self.impl.hasSetTimeout():
    self.impl.setTimeout(self.config.timeout)

  let actualTimeout = self.impl.getTimeout()
  if self.impl.isRunning and actualTimeout.isSome and actualTimeout.get() < self.config.loopWait:
    logger.error(fmt"loop_wait of {self.config.loopWait} seconds is too long for watchdog {actualTimeout.get()} second timeout")
    if self.impl.canBeDisabled:
      logger.info("Disabling watchdog due to unsafe timeout.")
      self.impl.close()
      self.impl = newNullWatchdog()
      return none(int)
  result = actualTimeout

proc activateInternal(self: Watchdog): bool =
  ## Internal activation logic.
  self.activeConfig = self.config

  if self.config.timingSlack < 0:
    logger.warning(fmt"Watchdog not supported because leader TTL {self.config.ttl} is less than 2x loop_wait {self.config.loopWait}")
    self.impl = newNullWatchdog()

  var actualTimeout: Option[int]
  try:
    self.impl.open()
    actualTimeout = self.setTimeoutInternal()
  except WatchdogError as e:
    if self.config.mode == MODE_REQUIRED:
      logger.warning(fmt"Could not activate {self.impl.describe()}: {e.msg}")
    else:
      logger.debug(fmt"Could not activate {self.impl.describe()}: {e.msg}")
    self.impl = newNullWatchdog()
    actualTimeout = self.impl.getTimeout()

  if self.impl.isRunning and not self.impl.canBeDisabled:
    logger.warning("Watchdog implementation can't be disabled. Watchdog will trigger after Patroni loses leader key.")

  if not self.impl.isRunning or (actualTimeout.isSome and actualTimeout.get() > self.config.timeout):
    if self.config.mode == MODE_REQUIRED:
      if self.impl.isNull:
        logger.error("Configuration requires watchdog, but watchdog could not be configured.")
      else:
        logger.error(fmt"Configuration requires watchdog, but a safe watchdog timeout {self.config.timeout} could not be configured. Watchdog timeout is {actualTimeout}.")
      return false
    else:
      if not self.impl.isNull and actualTimeout.isSome:
        logger.warning(fmt"Watchdog timeout {actualTimeout.get()} seconds does not ensure safe termination within {self.config.timeout} seconds")

  if self.impl.isRunning:
    if actualTimeout.isSome:
      logger.info(fmt"{self.impl.describe()} activated with {actualTimeout.get()} second timeout, timing slack {self.config.timingSlack} seconds")
  else:
    if self.config.mode == MODE_REQUIRED:
      logger.error("Configuration requires watchdog, but watchdog could not be activated")
      return false

  result = true

proc reloadConfig*(self: Watchdog, config: Config) =
  ## Reload watchdog configuration.
  withLock(self.lock):
    self.config = newWatchdogConfig(config)
    # Turning a watchdog off can always be done immediately
    if self.config.mode == MODE_OFF:
      if self.active:
        self.impl.close()
      self.activeConfig = self.config
      self.impl = newNullWatchdog()
    # If watchdog is not active we can apply config immediately
    if not self.active:
      if self.config.driver != self.activeConfig.driver:
        self.impl = self.config.getImpl()
      self.activeConfig = self.config

proc activate*(self: Watchdog): bool =
  ## Activate the watchdog.
  ##
  ## :returns: false if a safe watchdog could not be configured, but is required.
  withLock(self.lock):
    self.active = true
    result = self.activateInternal()

proc disable*(self: Watchdog) =
  ## Disable the watchdog.
  withLock(self.lock):
    try:
      if self.impl.isRunning and not self.impl.canBeDisabled:
        self.impl.keepalive()
      self.impl.close()
    except WatchdogError as e:
      logger.warning(fmt"Error disabling watchdog: {e.msg}")
    self.active = false

proc keepalive*(self: Watchdog) =
  ## Send keepalive to the watchdog.
  withLock(self.lock):
    if not self.active:
      return

    # Apply any pending config changes
    if self.config != self.activeConfig:
      discard self.activateInternal()

    try:
      self.impl.keepalive()
    except WatchdogError as e:
      logger.error(fmt"Watchdog keepalive failed: {e.msg}")

proc isRunning*(self: Watchdog): bool =
  ## Check if the watchdog is running.
  withLock(self.lock):
    result = self.impl.isRunning

proc isHealthy*(self: Watchdog): bool =
  ## Check if the watchdog is healthy.
  withLock(self.lock):
    result = self.config.mode != MODE_REQUIRED or self.impl.isRunning
