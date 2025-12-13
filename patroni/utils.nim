## Utilitary objects and functions that can be used throughout Patroni code.
##
## :var tzutc: UTC time zone info object.
## :var logger: logger of this module.
## :var USER_AGENT: identifies the Patroni version, Nim version, and the underlying platform.
## :var OCT_RE: regular expression to match octal numbers, signed or unsigned.
## :var DEC_RE: regular expression to match decimal numbers, signed or unsigned.
## :var HEX_RE: regular expression to match hex strings, signed or unsigned.
## :var DBL_RE: regular expression to match double precision numbers, signed or unsigned. Matches scientific notation too.
## :var WHITESPACE_RE: regular expression to match whitespace characters

import std/[strutils, strformat, tables, options, times, os, re, random, json, algorithm, net, parseutils, math, httpclient]
when defined(posix):
  import posix

import ./version

# Timezone UTC (Nim doesn't have timezone objects like Python, we use UTC functions)
proc tzutc*(): Timezone =
  result = utc()

# Logger placeholder - using echo for now
var logger* = "patroni.utils"

# User agent string
const USER_AGENT* = "Patroni/" & patroniVersion & " Nim/" & NimVersion & " " & hostOS

# Regular expressions
let
  OCT_RE* = re"^[-+]?0[0-7]*"
  DEC_RE* = re"^[-+]?(0|[1-9][0-9]*)"
  HEX_RE* = re"^[-+]?0x[0-9a-fA-F]+"
  DBL_RE* = re"^[-+]?[0-9]*\.?[0-9]+([eE][-+]?[0-9]+)?"
  WHITESPACE_RE* = re"[ \t\n\r]*"

# Unit conversion tables
proc getConversionTable*(baseUnit: string): OrderedTable[string, Table[string, float]] =
  ## Get conversion table for the specified base unit.
  ##
  ## If no conversion table exists for the passed unit, return an empty OrderedTable.
  ##
  ## :param baseUnit: unit to choose the conversion table for.
  ## :returns: OrderedTable object.

  var memoryUnitConversionTable = initOrderedTable[string, Table[string, float]]()
  memoryUnitConversionTable["TB"] = {"B": float(1024*1024*1024*1024), "kB": float(1024*1024*1024), "MB": float(1024*1024)}.toTable
  memoryUnitConversionTable["GB"] = {"B": float(1024*1024*1024), "kB": float(1024*1024), "MB": float(1024)}.toTable
  memoryUnitConversionTable["MB"] = {"B": float(1024*1024), "kB": float(1024), "MB": 1.0}.toTable
  memoryUnitConversionTable["kB"] = {"B": 1024.0, "kB": 1.0, "MB": 1.0/1024.0}.toTable
  memoryUnitConversionTable["B"] = {"B": 1.0, "kB": 1.0/1024.0, "MB": 1.0/(1024.0*1024.0)}.toTable

  var timeUnitConversionTable = initOrderedTable[string, Table[string, float]]()
  timeUnitConversionTable["d"] = {"ms": 1000.0 * 3600.0 * 24.0, "s": 3600.0 * 24.0, "min": 60.0 * 24.0}.toTable
  timeUnitConversionTable["h"] = {"ms": 1000.0 * 3600.0, "s": 3600.0, "min": 60.0}.toTable
  timeUnitConversionTable["min"] = {"ms": 1000.0 * 60.0, "s": 60.0, "min": 1.0}.toTable
  timeUnitConversionTable["s"] = {"ms": 1000.0, "s": 1.0, "min": 1.0/60.0}.toTable
  timeUnitConversionTable["ms"] = {"ms": 1.0, "s": 0.001, "min": 1.0/(1000.0*60.0)}.toTable
  timeUnitConversionTable["us"] = {"ms": 0.001, "s": 0.000001, "min": 1.0/(1000000.0*60.0)}.toTable

  if baseUnit in ["B", "kB", "MB"]:
    result = memoryUnitConversionTable
  elif baseUnit in ["ms", "s", "min"]:
    result = timeUnitConversionTable
  else:
    result = initOrderedTable[string, Table[string, float]]()

proc deepCompare*(obj1, obj2: Table[string, string]): bool =
  ## Recursively compare two dictionaries to check if they are equal in terms of keys and values.
  ##
  ## .. note::
  ##     Values are compared based on their string representation.
  ##
  ## :param obj1: dictionary to be compared with obj2.
  ## :param obj2: dictionary to be compared with obj1.
  ## :returns: true if all keys and values match between the two dictionaries.

  if obj1.len != obj2.len:
    return false

  var keys1 = newSeq[string]()
  var keys2 = newSeq[string]()
  for k in obj1.keys:
    keys1.add(k)
  for k in obj2.keys:
    keys2.add(k)

  keys1.sort()
  keys2.sort()

  if keys1 != keys2:
    return false

  for key, value in obj1.pairs:
    if key notin obj2:
      return false
    if value != obj2[key]:
      return false

  result = true

proc deepCompare*(obj1, obj2: JsonNode): bool =
  ## Compare two JSON nodes for equality.
  if obj1.isNil and obj2.isNil:
    return true
  if obj1.isNil or obj2.isNil:
    return false
  if obj1.kind != obj2.kind:
    return false

  case obj1.kind
  of JNull:
    return true
  of JBool:
    return obj1.getBool() == obj2.getBool()
  of JInt:
    return obj1.getInt() == obj2.getInt()
  of JFloat:
    return obj1.getFloat() == obj2.getFloat()
  of JString:
    return obj1.getStr() == obj2.getStr()
  of JArray:
    if obj1.len != obj2.len:
      return false
    for i in 0..<obj1.len:
      if not deepCompare(obj1[i], obj2[i]):
        return false
    return true
  of JObject:
    if obj1.len != obj2.len:
      return false
    for key, val in obj1.pairs:
      if not obj2.hasKey(key):
        return false
      if not deepCompare(val, obj2[key]):
        return false
    return true

proc patchConfig*(config: var JsonNode, data: JsonNode): bool =
  ## Update and append to dictionary config from overrides in data.
  ##
  ## :param config: configuration to be patched.
  ## :param data: new configuration values to patch config with.
  ## :returns: true if config was changed.

  result = false
  if data.kind != JObject or config.kind != JObject:
    return

  for name, value in data.pairs:
    if value.kind == JNull:
      if config.hasKey(name):
        config.delete(name)
        result = true
    elif config.hasKey(name):
      if value.kind == JObject:
        if config[name].kind == JObject:
          var subConfig = config[name]
          if patchConfig(subConfig, value):
            config[name] = subConfig
            result = true
        else:
          config[name] = value
          result = true
      elif $config[name] != $value:
        config[name] = value
        result = true
    else:
      config[name] = value
      result = true

proc parseBoolValue*(value: string): bool =
  ## Parse a given value to a bool.
  ##
  ## .. note::
  ##     The parsing is case-insensitive, and takes into consideration these values:
  ##         * on, true, yes, and 1 as True.
  ##         * off, false, no, and 0 as False.
  ##
  ## :param value: value to be parsed to bool.
  ## :returns: the parsed value. Returns false if not able to parse.

  let v = value.toLowerAscii()
  if v in ["on", "true", "yes", "1"]:
    result = true
  elif v in ["off", "false", "no", "0"]:
    result = false
  else:
    result = false

proc parseBoolOpt*(value: string): Option[bool] =
  ## Parse a given value to a bool, returning None if unable to parse.
  let v = value.toLowerAscii()
  if v in ["on", "true", "yes", "1"]:
    result = some(true)
  elif v in ["off", "false", "no", "0"]:
    result = some(false)
  else:
    result = none(bool)

proc strtol*(value: string, strict: bool = true): tuple[num: Option[int], rest: string] =
  ## Extract the long integer part from the beginning of a string.
  ##
  ## As most as possible close equivalent of strtol(3) C function (with base=0).
  ##
  ## :param value: any value from which we want to extract a long integer.
  ## :param strict: dictates how the first item is set when unable to find an integer.
  ## :returns: tuple of (parsed integer or None, remaining string).

  let v = value.strip()

  # Try hex first
  if v.startsWith("0x") or v.startsWith("0X") or v.startsWith("+0x") or v.startsWith("-0x"):
    var idx = 0
    var negative = false
    if v[0] == '-':
      negative = true
      idx = 1
    elif v[0] == '+':
      idx = 1
    idx += 2  # Skip "0x"

    var hexStr = ""
    while idx < v.len and v[idx] in HexDigits:
      hexStr.add(v[idx])
      inc idx

    if hexStr.len > 0:
      try:
        var num = fromHex[int](hexStr)
        if negative:
          num = -num
        return (some(num), v[idx..^1])
      except:
        discard

  # Try octal (starts with 0 but not 0x)
  if v.len > 0 and (v[0] == '0' or (v.len > 1 and v[0] in ['+', '-'] and v[1] == '0')):
    var idx = 0
    var negative = false
    if v[0] == '-':
      negative = true
      idx = 1
    elif v[0] == '+':
      idx = 1

    if idx < v.len and v[idx] == '0':
      inc idx
      var octStr = "0"
      while idx < v.len and v[idx] in {'0'..'7'}:
        octStr.add(v[idx])
        inc idx

      if octStr.len > 0:
        try:
          var num = 0
          for c in octStr:
            num = num * 8 + (ord(c) - ord('0'))
          if negative:
            num = -num
          return (some(num), v[idx..^1])
        except:
          discard

  # Try decimal
  var idx = 0
  var negative = false
  if v.len > 0 and v[0] == '-':
    negative = true
    idx = 1
  elif v.len > 0 and v[0] == '+':
    idx = 1

  var decStr = ""
  while idx < v.len and v[idx] in Digits:
    decStr.add(v[idx])
    inc idx

  if decStr.len > 0:
    try:
      var num = parseInt(decStr)
      if negative:
        num = -num
      return (some(num), v[idx..^1])
    except:
      discard

  if strict:
    result = (none(int), v)
  else:
    result = (some(1), v)

proc strtod*(value: string): tuple[num: Option[float], rest: string] =
  ## Extract the double precision part from the beginning of a string.
  ##
  ## :param value: any value from which we want to extract a double precision.
  ## :returns: tuple of (parsed float or None, remaining string).

  let v = value.strip()
  var idx = 0

  # Optional sign
  if idx < v.len and v[idx] in ['+', '-']:
    inc idx

  # Integer part
  while idx < v.len and v[idx] in Digits:
    inc idx

  # Optional decimal part
  if idx < v.len and v[idx] == '.':
    inc idx
    while idx < v.len and v[idx] in Digits:
      inc idx

  # Optional exponent
  if idx < v.len and v[idx] in ['e', 'E']:
    inc idx
    if idx < v.len and v[idx] in ['+', '-']:
      inc idx
    while idx < v.len and v[idx] in Digits:
      inc idx

  if idx > 0:
    try:
      let numStr = v[0..<idx]
      let num = parseFloat(numStr)
      return (some(num), v[idx..^1])
    except:
      discard

  result = (none(float), v)

proc convertToBaseUnit*(value: float, unit: string, baseUnit: string): Option[float] =
  ## Convert value as a unit of compute information or time to baseUnit.
  ##
  ## :param value: value to be converted to the base unit.
  ## :param unit: unit of value.
  ## :param baseUnit: target unit in the conversion.
  ## :returns: value in unit converted to baseUnit, or None if invalid.

  let (baseValueOpt, actualBaseUnit) = strtol(baseUnit, false)
  let baseValue = baseValueOpt.get(1)

  let convertTbl = getConversionTable(actualBaseUnit)
  if unit in convertTbl and actualBaseUnit in convertTbl[unit]:
    let converted = value * convertTbl[unit][actualBaseUnit] / float(baseValue)
    return some(converted)

  result = none(float)

proc parseIntValue*(value: string, baseUnit: string = ""): Option[int] =
  ## Parse value as an int.
  ##
  ## :param value: any value that can be handled by strtol or strtod.
  ## :param baseUnit: an optional base unit to convert value.
  ## :returns: the parsed value, if able to parse. Otherwise returns None.

  let (numOpt, rest) = strtol(value)
  if numOpt.isNone:
    return none(int)

  let num = numOpt.get()
  let unit = rest.strip()

  if unit.len == 0:
    return some(num)

  if baseUnit.len > 0:
    let converted = convertToBaseUnit(float(num), unit, baseUnit)
    if converted.isSome:
      return some(int(converted.get()))

  result = none(int)

proc parseRealValue*(value: string, baseUnit: string = ""): Option[float] =
  ## Parse value as a float (real number).
  ##
  ## :param value: any value that can be handled by strtod.
  ## :param baseUnit: an optional base unit to convert value.
  ## :returns: the parsed value, if able to parse. Otherwise returns None.

  let (numOpt, rest) = strtod(value)
  if numOpt.isNone:
    return none(float)

  let num = numOpt.get()
  let unit = rest.strip()

  if unit.len == 0:
    return some(num)

  if baseUnit.len > 0:
    let converted = convertToBaseUnit(num, unit, baseUnit)
    if converted.isSome:
      return some(converted.get())

  result = none(float)

proc uri*(scheme: string, hostPort: tuple[host: string, port: int]): string =
  ## Build a URI string from scheme, host and port.
  result = fmt"{scheme}://{hostPort.host}:{hostPort.port}"

proc uri*(scheme: string, host: string, port: int): string =
  ## Build a URI string from scheme, host and port.
  result = fmt"{scheme}://{host}:{port}"

iterator pollingLoop*(timeout: float, interval: float = 1.0): float =
  ## Iterator that yields elapsed time until timeout is reached.
  ##
  ## :param timeout: total time to poll.
  ## :param interval: time between polls.
  ## :yields: elapsed time since start.

  let startTime = epochTime()
  var elapsed = 0.0

  while elapsed < timeout:
    yield elapsed
    sleep(int(interval * 1000))
    elapsed = epochTime() - startTime

proc splitAddress*(address: string): tuple[host: string, port: int] =
  ## Split host:port string into components.
  ## Handles IPv6 addresses in brackets like [::1]:5432
  if address.startsWith("["):
    # IPv6 address in brackets
    let closeBracket = address.find(']')
    if closeBracket > 0:
      result.host = address[1 ..< closeBracket]  # Remove brackets
      if closeBracket + 1 < address.len and address[closeBracket + 1] == ':':
        try:
          result.port = strutils.parseInt(address[closeBracket + 2 .. ^1])
        except:
          result.port = 0
      else:
        result.port = 0
    else:
      # Malformed, return as-is
      result.host = address
      result.port = 0
  else:
    # IPv4 or hostname
    let parts = address.rsplit(':', maxsplit = 1)
    if parts.len == 2:
      result.host = parts[0]
      try:
        result.port = strutils.parseInt(parts[1])
      except:
        result.port = 0
    else:
      result.host = address
      result.port = 0

proc enableKeepAlive*(sock: Socket, keepaliveIdle: int = 10, keepaliveIntvl: int = 3, keepaliveCnt: int = 3) =
  ## Enable TCP keepalive on a socket.
  sock.setSockOpt(OptKeepAlive, true)
  # Note: Nim's standard library doesn't expose detailed keepalive options
  # These would need to be set via raw socket options

proc getuid*(): int =
  ## Get the current user ID (POSIX only).
  when defined(posix):
    result = int(posix.getuid())
  else:
    result = -1

proc isRunningAsRoot*(): bool =
  ## Check if the process is running as root.
  result = getuid() == 0

proc cluster_as_json*(cluster: pointer): JsonNode =
  ## Convert cluster to JSON representation.
  ## Placeholder - actual implementation depends on Cluster type.
  result = newJObject()

proc dateRangeToStr*(from_dt, to_dt: DateTime, sep: string = " - "): string =
  ## Convert a date range to a human-readable string.
  result = from_dt.format("yyyy-MM-dd HH:mm:ss") & sep & to_dt.format("yyyy-MM-dd HH:mm:ss")

proc sleepMs*(ms: int) =
  ## Sleep for the specified number of milliseconds.
  sleep(ms)

proc sleepSec*(sec: float) =
  ## Sleep for the specified number of seconds.
  sleep(int(sec * 1000))

proc getHostname*(): string =
  ## Get the hostname of the current machine.
  when defined(posix):
    var hostname: array[256, char]
    if posix.gethostname(cast[cstring](addr hostname[0]), 256) == 0:
      result = $cast[cstring](addr hostname[0])
    else:
      result = "localhost"
  else:
    # Windows fallback
    result = "localhost"

proc randomShuffle*[T](s: var seq[T]) =
  ## Shuffle a sequence in place.
  randomize()
  for i in countdown(s.high, 1):
    let j = rand(i)
    swap(s[i], s[j])

proc keepAliveSocketWrapper*(host: string, port: int, timeout: float = 30.0): Socket =
  ## Create a socket with keepalive enabled.
  result = newSocket()
  result.connect(host, Port(port), timeout = int(timeout * 1000))
  enableKeepAlive(result)

proc compareVersions*(v1, v2: seq[int]): int =
  ## Compare two version sequences.
  ## Returns -1 if v1 < v2, 0 if equal, 1 if v1 > v2.
  let maxLen = max(v1.len, v2.len)
  for i in 0..<maxLen:
    let a = if i < v1.len: v1[i] else: 0
    let b = if i < v2.len: v2[i] else: 0
    if a < b:
      return -1
    elif a > b:
      return 1
  result = 0
