## Linux hardware watchdog support.
##
## This module provides types and procedures for interacting with
## Linux hardware watchdog devices via ioctl.

import std/[json, os, posix, strformat, tables]
import ./base

export base

# Linux ioctl definitions
const
  IOC_NONE = 0
  IOC_WRITE = 1
  IOC_READ = 2

  IOC_NRBITS = 8
  IOC_TYPEBITS = 8
  IOC_SIZEBITS = 14
  IOC_DIRBITS = 2

  IOC_NRSHIFT = 0
  IOC_TYPESHIFT = IOC_NRSHIFT + IOC_NRBITS
  IOC_SIZESHIFT = IOC_TYPESHIFT + IOC_TYPEBITS
  IOC_DIRSHIFT = IOC_SIZESHIFT + IOC_SIZEBITS

  WATCHDOG_IOCTL_BASE = 'W'.ord

  INT_SIZE = 4
  WATCHDOG_INFO_SIZE = 40

proc IOC(dir: int, typeChar: int, nr: int, size: int): culong =
  ## Calculate ioctl request number.
  result = culong(
    (dir shl IOC_DIRSHIFT) or
    (typeChar shl IOC_TYPESHIFT) or
    (nr shl IOC_NRSHIFT) or
    (size shl IOC_SIZESHIFT)
  )

proc IOR(typeChar: char, nr: int, size: int): culong =
  IOC(IOC_READ, typeChar.ord, nr, size)

proc IOW(typeChar: char, nr: int, size: int): culong =
  IOC(IOC_WRITE, typeChar.ord, nr, size)

proc IOWR(typeChar: char, nr: int, size: int): culong =
  IOC(IOC_READ or IOC_WRITE, typeChar.ord, nr, size)

# Watchdog ioctl commands
let
  WDIOC_GETSUPPORT = IOR('W', 0, WATCHDOG_INFO_SIZE)
  WDIOC_GETSTATUS = IOR('W', 1, INT_SIZE)
  WDIOC_GETBOOTSTATUS = IOR('W', 2, INT_SIZE)
  WDIOC_GETTEMP = IOR('W', 3, INT_SIZE)
  WDIOC_SETOPTIONS = IOR('W', 4, INT_SIZE)
  WDIOC_KEEPALIVE = IOR('W', 5, INT_SIZE)
  WDIOC_SETTIMEOUT = IOWR('W', 6, INT_SIZE)
  WDIOC_GETTIMEOUT = IOR('W', 7, INT_SIZE)
  WDIOC_SETPRETIMEOUT = IOWR('W', 8, INT_SIZE)
  WDIOC_GETPRETIMEOUT = IOR('W', 9, INT_SIZE)
  WDIOC_GETTIMELEFT = IOR('W', 10, INT_SIZE)

# Watchdog options flags
const
  WDIOF_OVERHEAT = 0x0001
  WDIOF_FANFAULT = 0x0002
  WDIOF_EXTERN1 = 0x0004
  WDIOF_EXTERN2 = 0x0008
  WDIOF_POWERUNDER = 0x0010
  WDIOF_CARDRESET = 0x0020
  WDIOF_POWEROVER = 0x0040
  WDIOF_SETTIMEOUT* = 0x0080
  WDIOF_MAGICCLOSE* = 0x0100
  WDIOF_PRETIMEOUT = 0x0200
  WDIOF_ALARMONLY = 0x0400
  WDIOF_KEEPALIVEPING = 0x8000

  WDIOS_DISABLECARD = 0x0001
  WDIOS_ENABLECARD = 0x0002
  WDIOS_TEMPPANIC = 0x0004

type
  WatchdogInfo* = object
    ## Watchdog device information.
    options*: uint32
    firmwareVersion*: uint32
    identity*: string

  LinuxWatchdogDevice* = ref object of WatchdogBase
    ## Linux hardware watchdog device.
    device: string
    fd: cint
    supportCache: Option[WatchdogInfo]

const
  DEFAULT_DEVICE* = "/dev/watchdog"

proc hasSettimeout*(self: WatchdogInfo): bool =
  ## Check if device supports setting timeout.
  result = (self.options and WDIOF_SETTIMEOUT) != 0

proc hasMagicclose*(self: WatchdogInfo): bool =
  ## Check if device supports magic close.
  result = (self.options and WDIOF_MAGICCLOSE) != 0

proc hasPretimeout*(self: WatchdogInfo): bool =
  ## Check if device supports pretimeout.
  result = (self.options and WDIOF_PRETIMEOUT) != 0

proc newLinuxWatchdogDevice*(device: string = DEFAULT_DEVICE): LinuxWatchdogDevice =
  ## Create a new Linux watchdog device.
  new(result)
  result.device = device
  result.fd = -1
  result.supportCache = none(WatchdogInfo)

proc fromConfig*(config: JsonNode): LinuxWatchdogDevice =
  ## Create a watchdog device from configuration.
  let device = config.getOrDefault("device").getStr(DEFAULT_DEVICE)
  result = newLinuxWatchdogDevice(device)

proc isRunning*(self: LinuxWatchdogDevice): bool =
  ## Check if watchdog is running.
  result = self.fd >= 0

proc isHealthy*(self: LinuxWatchdogDevice): bool =
  ## Check if watchdog device is healthy.
  result = fileExists(self.device) and access(self.device.cstring, W_OK) == 0

proc doIoctl(self: LinuxWatchdogDevice, request: culong, arg: pointer): bool =
  ## Perform ioctl on watchdog device.
  if self.fd < 0:
    raise newException(WatchdogError, "Watchdog device is closed")

  when defined(posix):
    let ret = ioctl(self.fd, request, arg)
    return ret == 0
  else:
    return false

proc open*(self: LinuxWatchdogDevice) =
  ## Open the watchdog device.
  when defined(posix):
    let fd = posix.open(self.device.cstring, O_WRONLY)
    if fd < 0:
      raise newException(WatchdogError, fmt"Can't open watchdog device: {self.device}")
    self.fd = fd
  else:
    raise newException(WatchdogError, "Watchdog not supported on this platform")

proc close*(self: LinuxWatchdogDevice) =
  ## Close the watchdog device.
  if self.fd >= 0:
    # Write magic close character 'V'
    when defined(posix):
      discard posix.write(self.fd, "V".cstring, 1)
      discard posix.close(self.fd)
    self.fd = -1

proc getSupport*(self: LinuxWatchdogDevice): WatchdogInfo =
  ## Get watchdog support information.
  if self.supportCache.isSome:
    return self.supportCache.get

  when defined(posix):
    # watchdog_info structure: uint32 options, uint32 firmware_version, uint8[32] identity
    var info: array[40, byte]
    if not self.doIoctl(WDIOC_GETSUPPORT, addr info[0]):
      raise newException(WatchdogError, "Could not get watchdog device information")

    var options: uint32
    copyMem(addr options, addr info[0], 4)

    var firmwareVersion: uint32
    copyMem(addr firmwareVersion, addr info[4], 4)

    var identity = ""
    for i in 8..<40:
      if info[i] == 0:
        break
      identity.add(char(info[i]))

    result.options = options
    result.firmwareVersion = firmwareVersion
    result.identity = identity
    self.supportCache = some(result)
  else:
    raise newException(WatchdogError, "Not supported")

proc canBeDisabled*(self: LinuxWatchdogDevice): bool =
  ## Check if watchdog can be disabled.
  result = self.getSupport().hasMagicclose

proc describe*(self: LinuxWatchdogDevice): string =
  ## Describe the watchdog device.
  var devStr = ""
  if self.device != DEFAULT_DEVICE:
    devStr = fmt" at {self.device}"

  var verStr = ""
  var identity = "Linux watchdog device"

  if self.fd >= 0:
    try:
      let info = self.getSupport()
      if info.firmwareVersion > 0:
        verStr = fmt" (firmware {info.firmwareVersion})"
      identity = info.identity
    except WatchdogError:
      discard

  result = identity & verStr & devStr

proc keepalive*(self: LinuxWatchdogDevice) =
  ## Send keepalive to watchdog.
  if self.fd < 0:
    raise newException(WatchdogError, "Watchdog device is closed")

  when defined(posix):
    let written = posix.write(self.fd, "1".cstring, 1)
    if written != 1:
      raise newException(WatchdogError, "Could not send watchdog keepalive")
  else:
    raise newException(WatchdogError, "Not supported")

proc hasSetTimeout*(self: LinuxWatchdogDevice): bool =
  ## Check if setting timeout is supported.
  result = self.getSupport().hasSettimeout

proc setTimeout*(self: LinuxWatchdogDevice, timeout: int) =
  ## Set watchdog timeout.
  if timeout <= 0 or timeout >= 0xFFFF:
    raise newException(WatchdogError,
      fmt"Invalid timeout {timeout}. Supported values are between 1 and 65535")

  var timeoutVal: cint = cint(timeout)
  if not self.doIoctl(WDIOC_SETTIMEOUT, addr timeoutVal):
    raise newException(WatchdogError, "Could not set timeout on watchdog device")

proc getTimeout*(self: LinuxWatchdogDevice): int =
  ## Get watchdog timeout.
  var timeout: cint = 0
  if not self.doIoctl(WDIOC_GETTIMEOUT, addr timeout):
    raise newException(WatchdogError, "Could not get timeout from watchdog device")
  result = int(timeout)

# Testing watchdog device for tests

type
  TestingWatchdogDevice* = ref object of LinuxWatchdogDevice
    ## Test harness for watchdog device.
    timeout: int

proc newTestingWatchdogDevice*(device: string): TestingWatchdogDevice =
  ## Create a testing watchdog device.
  new(result)
  result.device = device
  result.fd = -1
  result.supportCache = none(WatchdogInfo)
  result.timeout = 60

method getSupport*(self: TestingWatchdogDevice): WatchdogInfo =
  ## Get fake support information.
  result.options = uint32(WDIOF_MAGICCLOSE or WDIOF_SETTIMEOUT)
  result.firmwareVersion = 0
  result.identity = "Watchdog test harness"

method setTimeout*(self: TestingWatchdogDevice, timeout: int) =
  ## Set timeout (writes to pipe for testing).
  if self.fd < 0:
    raise newException(WatchdogError, "Watchdog device is closed")

  let buf = fmt"Ctimeout={timeout}\n"
  when defined(posix):
    discard posix.write(self.fd, buf.cstring, buf.len.csize_t)
  self.timeout = timeout

method getTimeout*(self: TestingWatchdogDevice): int =
  ## Get stored timeout.
  result = self.timeout

