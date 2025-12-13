## Tests for patroni/watchdog module.

import std/[json, options, tables, unittest]
import ../patroni/watchdog

# Helper to create a properly initialized WatchdogConfig
proc createWatchdogConfig(mode: string = MODE_OFF, ttl: int = 30,
                          loopWait: int = 10, safetyMargin: int = 5,
                          driver: string = "default"): WatchdogConfig =
  new(result)
  result.mode = mode
  result.ttl = ttl
  result.loopWait = loopWait
  result.safetyMargin = safetyMargin
  result.driver = driver
  result.driverConfig = initTable[string, JsonNode]()

suite "Watchdog - Mode Parsing":
  test "parseMode required":
    check parseMode("required") == MODE_REQUIRED
    check parseMode("require") == MODE_REQUIRED
    check parseMode("REQUIRED") == MODE_REQUIRED
    check parseMode("Require") == MODE_REQUIRED

  test "parseMode automatic":
    check parseMode("automatic") == MODE_AUTOMATIC
    check parseMode("auto") == MODE_AUTOMATIC
    check parseMode("AUTOMATIC") == MODE_AUTOMATIC
    check parseMode("Auto") == MODE_AUTOMATIC

  test "parseMode off":
    check parseMode("off") == MODE_OFF
    check parseMode("disable") == MODE_OFF
    check parseMode("disabled") == MODE_OFF
    check parseMode("OFF") == MODE_OFF
    check parseMode("Disabled") == MODE_OFF

  test "parseMode unknown defaults to off":
    check parseMode("unknown") == MODE_OFF
    check parseMode("invalid") == MODE_OFF
    check parseMode("") == MODE_OFF

  test "parseModeFromBool true returns automatic":
    check parseModeFromBool(true) == MODE_AUTOMATIC

  test "parseModeFromBool false returns off":
    check parseModeFromBool(false) == MODE_OFF

suite "Watchdog - Constants":
  test "MODE_REQUIRED value":
    check MODE_REQUIRED == "required"

  test "MODE_AUTOMATIC value":
    check MODE_AUTOMATIC == "automatic"

  test "MODE_OFF value":
    check MODE_OFF == "off"

suite "Watchdog - WatchdogBase":
  test "WatchdogBase isNull returns false by default":
    let base = WatchdogBase()
    check base.isNull == false

  test "WatchdogBase isRunning returns false by default":
    let base = WatchdogBase()
    check base.isRunning == false

  test "WatchdogBase canBeDisabled returns true by default":
    let base = WatchdogBase()
    check base.canBeDisabled == true

  test "WatchdogBase hasSetTimeout returns false by default":
    let base = WatchdogBase()
    check base.hasSetTimeout == false

  test "WatchdogBase getTimeout returns none by default":
    let base = WatchdogBase()
    check base.getTimeout.isNone

  test "WatchdogBase describe returns WatchdogBase":
    let base = WatchdogBase()
    check base.describe == "WatchdogBase"

  test "WatchdogBase methods don't crash":
    let base = WatchdogBase()
    base.open()
    base.keepalive()
    base.setTimeout(30)
    base.close()
    check true

suite "Watchdog - NullWatchdog":
  test "newNullWatchdog creates instance":
    let wd = newNullWatchdog()
    check wd != nil

  test "NullWatchdog isNull returns true":
    let wd = newNullWatchdog()
    check wd.isNull == true

  test "NullWatchdog isRunning returns false":
    let wd = newNullWatchdog()
    check wd.isRunning == false

  test "NullWatchdog describe returns NullWatchdog":
    let wd = newNullWatchdog()
    check wd.describe == "NullWatchdog"

  test "NullWatchdog inherits canBeDisabled as true":
    let wd = newNullWatchdog()
    check wd.canBeDisabled == true

  test "NullWatchdog methods don't crash":
    let wd = newNullWatchdog()
    wd.open()
    wd.keepalive()
    wd.setTimeout(30)
    wd.close()
    check true

suite "Watchdog - WatchdogConfig":
  test "WatchdogConfig can be created":
    let wc = createWatchdogConfig()
    check wc != nil

  test "WatchdogConfig has correct mode":
    let wc = createWatchdogConfig()
    let mode = wc.mode
    check mode == MODE_OFF

  test "WatchdogConfig has correct ttl":
    let wc = createWatchdogConfig()
    let ttl = wc.ttl
    check ttl == 30

  test "WatchdogConfig timeout with safety margin":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = 5)
    check wc.timeout == 25

  test "WatchdogConfig timeout with -1 safety margin":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = -1)
    check wc.timeout == 15  # ttl / 2

  test "WatchdogConfig timeout with zero safety margin":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = 0)
    check wc.timeout == 30

  test "WatchdogConfig timingSlack":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = 5, loopWait = 10)
    check wc.timingSlack == 15  # timeout(25) - loopWait(10)

  test "WatchdogConfig timingSlack with large loopWait":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = 5, loopWait = 20)
    check wc.timingSlack == 5

  test "WatchdogConfig timingSlack can be negative":
    let wc = createWatchdogConfig(ttl = 30, safetyMargin = 5, loopWait = 30)
    check wc.timingSlack == -5

  test "WatchdogConfig getImpl returns implementation":
    let wc = createWatchdogConfig(driver = "default")
    let impl = wc.getImpl()
    check impl != nil

  test "WatchdogConfig getImpl returns NullWatchdog":
    let wc = createWatchdogConfig(driver = "default")
    let impl = wc.getImpl()
    check impl.isNull == true

  test "WatchdogConfig driverConfig can be set":
    let wc = createWatchdogConfig()
    wc.driverConfig["path"] = newJString("/dev/watchdog")
    let path = wc.driverConfig["path"].getStr()
    check path == "/dev/watchdog"

  test "WatchdogConfig mode can be set to automatic":
    let wc = createWatchdogConfig(mode = MODE_AUTOMATIC)
    let mode = wc.mode
    check mode == MODE_AUTOMATIC

  test "WatchdogConfig mode can be set to required":
    let wc = createWatchdogConfig(mode = MODE_REQUIRED)
    let mode = wc.mode
    check mode == MODE_REQUIRED

  test "WatchdogConfig with custom ttl":
    let wc = createWatchdogConfig(ttl = 60)
    let ttl = wc.ttl
    check ttl == 60

  test "WatchdogConfig with custom loopWait":
    let wc = createWatchdogConfig(loopWait = 15)
    let loopWait = wc.loopWait
    check loopWait == 15

  test "WatchdogConfig with custom safetyMargin":
    let wc = createWatchdogConfig(safetyMargin = 10)
    let safetyMargin = wc.safetyMargin
    check safetyMargin == 10

  test "WatchdogConfig with custom driver":
    let wc = createWatchdogConfig(driver = "custom")
    let driver = wc.driver
    check driver == "custom"

when isMainModule:
  discard
