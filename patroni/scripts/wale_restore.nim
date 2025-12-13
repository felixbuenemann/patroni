## WAL-E restore script for cloning replicas.
##
## This script clones new replicas using WAL-E restore, falling back
## to pg_basebackup if WAL-E restore fails or if WAL-E backup is too
## far behind.
##
## Note: pg_basebackup still expects to use restore from WAL-E for
## transaction logs.
##
## Theoretically should work with SWIFT, but not tested on it.
##
## Arguments:
##   --scope: cluster scope
##   --role: cluster role
##   --datadir: PostgreSQL data directory
##   --connstring: leader connection string
##   --retries: number of retries
##   --envdir: envdir for the WALE env
##   --threshold_megabytes: if WAL amount is above that - use pg_basebackup
##   --threshold_backup_size_percentage: if WAL size exceeds this percentage
##
## This script depends on an envdir defining the S3 bucket (or SWIFT dir),
## and login credentials per WAL-E documentation.

import std/[os, osproc, parseopt, strformat, strutils, tables, options, math]
import ../psycopg
import ../log

let logger = getLogger("patroni.scripts.wale_restore")

const
  RETRY_SLEEP_INTERVAL = 1  # seconds
  SI_PREFIXES = ['K', 'M', 'G', 'T', 'P', 'E', 'Z', 'Y']

type
  ExitCode* = enum
    ## Exit codes for WALERestore.
    ecSuccess = 0       ## Succeeded
    ecRetryLater = 1    ## External issue, retry later
    ecFail = 2          ## Don't try again unless configuration changes

  WALEConfig* = object
    ## WAL-E configuration.
    envDir*: string
    thresholdMb*: int
    thresholdPct*: int
    cmd*: seq[string]

  WALERestore* = ref object
    ## WAL-E restore handler.
    scope*: string
    leaderConnection*: string
    dataDir*: string
    noLeader*: bool
    walE*: WALEConfig
    initError*: bool
    retries*: int

proc getMajorVersion*(dataDir: string): float =
  ## Get PostgreSQL major version from PG_VERSION file.
  ##
  ## :param dataDir: PostgreSQL data directory.
  ## :returns: Major version as float, or 0.0 if not found.
  let versionFile = dataDir / "PG_VERSION"
  if fileExists(versionFile):
    try:
      let content = readFile(versionFile).strip()
      return parseFloat(content)
    except:
      logger.error(fmt"Failed to read PG_VERSION from {dataDir}")
  return 0.0

proc reprSize*(nBytes: float): string =
  ## Format byte size in human-readable format.
  ##
  ## Examples:
  ##   reprSize(1000) -> "1000 Bytes"
  ##   reprSize(8257332324597) -> "7.5 TiB"
  if nBytes < 1024:
    return fmt"{nBytes:.0f} Bytes"

  var n = nBytes
  var i = -1
  while n > 1023:
    n = n / 1024.0
    inc i

  return fmt"{n:.1f} {SI_PREFIXES[i]}iB"

proc sizeAsBytes*(size: float, prefix: char): int64 =
  ## Convert size with prefix to bytes.
  ##
  ## Example:
  ##   sizeAsBytes(7.5, 'T') -> 8246337208320
  let prefixUpper = prefix.toUpperAscii()
  var idx = -1
  for i, p in SI_PREFIXES:
    if p == prefixUpper:
      idx = i
      break

  if idx < 0:
    raise newException(ValueError, fmt"Unknown prefix: {prefix}")

  let exponent = idx + 1
  return int64(size * pow(1024.0, float(exponent)))

proc newWALERestore*(scope, datadir, connstring, envDir: string,
                    thresholdMb, thresholdPct, useIam: int,
                    noLeader: bool, retries: int): WALERestore =
  ## Create a new WALERestore instance.
  new(result)
  result.scope = scope
  result.leaderConnection = connstring
  result.dataDir = datadir
  result.noLeader = noLeader
  result.retries = retries

  var waleCmd = @["envdir", envDir, "wal-e"]
  if useIam == 1:
    waleCmd.add("--aws-instance-profile")

  result.walE = WALEConfig(
    envDir: envDir,
    thresholdMb: thresholdMb,
    thresholdPct: thresholdPct,
    cmd: waleCmd
  )

  result.initError = not dirExists(result.walE.envDir)

proc parseBackupList(output: string): Option[Table[string, string]] =
  ## Parse wal-e backup-list --detail output (TSV format).
  let lines = output.strip().split('\n')
  if lines.len < 2:
    return none(Table[string, string])

  let headers = lines[0].split('\t')
  let values = lines[1].split('\t')

  if headers.len != values.len:
    return none(Table[string, string])

  var result = initTable[string, string]()
  for i in 0..<headers.len:
    result[headers[i]] = values[i]

  return some(result)

proc shouldUseS3ToCreateReplica*(self: WALERestore): Option[bool] =
  ## Determine whether to use S3 (WAL-E) instead of pg_basebackup.
  let thresholdMegabytes = self.walE.thresholdMb
  let thresholdPercent = self.walE.thresholdPct

  # Get latest backup info from wal-e
  var cmd = self.walE.cmd & @["backup-list", "--detail", "LATEST"]
  logger.debug(fmt"calling {cmd}")

  let (waleOutput, exitCode) = execCmdEx(cmd.join(" "))
  if exitCode != 0:
    logger.error("could not query wal-e latest backup")
    return none(bool)

  let backupInfoOpt = parseBackupList(waleOutput)
  if backupInfoOpt.isNone:
    logger.warning("wal-e did not find any backups")
    return some(false)

  let backupInfo = backupInfoOpt.get

  var backupSize: int64
  var backupStartSegment, backupStartOffset: string

  try:
    backupSize = parseBiggestInt(backupInfo["expanded_size_bytes"])
    backupStartSegment = backupInfo["wal_segment_backup_start"]
    backupStartOffset = backupInfo["wal_segment_offset_backup_start"]
  except KeyError:
    logger.error("unable to get some of WALE backup parameters")
    return none(bool)

  # WAL filename is XXXXXXXXYYYYYYYY000000ZZ
  # X - timeline, Y - LSN logical log file, ZZ - 2 high digits of LSN offset
  let lsnSegment = backupStartSegment[8..15]
  let highOffset = parseHexInt(backupStartSegment[16..31])
  let fullOffset = (highOffset shl 24) + parseInt(backupStartOffset)
  let lsnOffset = toHex(fullOffset).toLowerAscii().strip(chars = {'0'}, leading = true)

  let backupStartLsn = fmt"{lsnSegment}/{lsnOffset}"

  var diffInBytes = backupSize
  var attemptsNo = 0

  while true:
    if self.leaderConnection.len > 0:
      var conn: Connection = nil
      try:
        conn = connect(self.leaderConnection)
        let serverVersion = conn.serverVersion

        var walName, lsnName: string
        if serverVersion >= 100000:
          walName = "wal"
          lsnName = "lsn"
        else:
          walName = "xlog"
          lsnName = "location"

        let query = fmt"""SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
          THEN GREATEST(pg_catalog.pg_{walName}_{lsnName}_diff(COALESCE(
          pg_last_{walName}_receive_{lsnName}(), '0/0'), $1)::bigint,
          pg_catalog.pg_{walName}_{lsnName}_diff(pg_catalog.pg_last_{walName}_replay_{lsnName}(), $2)::bigint)
          ELSE pg_catalog.pg_{walName}_{lsnName}_diff(pg_catalog.pg_current_{walName}_{lsnName}(), $3)::bigint
          END"""

        let rows = conn.query(query, @[backupStartLsn, backupStartLsn, backupStartLsn])
        if rows.len > 0 and rows[0].len > 0:
          diffInBytes = parseBiggestInt(rows[0][0])
        break

      except PostgresError:
        logger.error("could not determine difference with the leader location")
        if attemptsNo < self.retries:
          inc attemptsNo
          sleep(RETRY_SLEEP_INTERVAL * 1000)
          continue
        else:
          if not self.noLeader:
            return some(false)
          logger.info("continue with base backup from S3 since leader is not available")
          diffInBytes = 0
          break
      finally:
        if conn != nil:
          conn.close()
    else:
      # Always try to use WAL-E if leader connection string is not available
      diffInBytes = 0
    break

  # Check thresholds
  let isSizeThreshOk = diffInBytes < int64(thresholdMegabytes) * 1048576
  let thresholdPctBytes = float(backupSize) * float(thresholdPercent) / 100.0
  let isPercentageThreshOk = float(diffInBytes) < thresholdPctBytes
  let areThresholdsOk = isSizeThreshOk and isPercentageThreshOk

  let humanContext = fmt"threshold_size={reprSize(float(thresholdMegabytes) * 1048576)}, " &
                     fmt"threshold_percent={thresholdPercent}, " &
                     fmt"threshold_percent_size={reprSize(thresholdPctBytes)}, " &
                     fmt"backup_size={reprSize(float(backupSize))}, " &
                     fmt"backup_diff={reprSize(float(diffInBytes))}, " &
                     fmt"is_size_thresh_ok={isSizeThreshOk}, " &
                     fmt"is_percentage_thresh_ok={isPercentageThreshOk}"

  if not areThresholdsOk:
    logger.info(fmt"wal-e backup size diff is over threshold, falling back to other means of restore: {humanContext}")
  else:
    logger.info(fmt"Thresholds are OK, using wal-e basebackup: {humanContext}")

  return some(areThresholdsOk)

proc fixSubdirectoryPathIfBroken*(self: WALERestore, dirname: string): bool =
  ## Fix broken symlinks for pg_xlog/pg_wal directory.
  let path = self.dataDir / dirname

  if not dirExists(path) and not fileExists(path):
    if symlinkExists(path):
      # Broken symlink, remove it
      try:
        let target = expandSymlink(path)
        removeFile(path)
      except OSError:
        logger.error(fmt"could not remove broken {dirname} symlink")
        return false

    try:
      createDir(path)
    except OSError:
      logger.error(fmt"could not create missing {dirname} directory path")
      return false

  return true

proc createReplicaWithS3*(self: WALERestore): int =
  ## Restore replica using wal-e backup-fetch.
  var cmd = self.walE.cmd & @["backup-fetch", self.dataDir, "LATEST"]
  logger.debug(fmt"calling: {cmd}")

  try:
    let exitCode = execCmd(cmd.join(" "))
    if exitCode == 0:
      let walDir = if getMajorVersion(self.dataDir) < 10: "pg_xlog" else: "pg_wal"
      if not self.fixSubdirectoryPathIfBroken(walDir):
        return int(ecFail)
    return exitCode
  except:
    logger.error("Error when fetching backup with WAL-E")
    return int(ecRetryLater)

proc run*(self: WALERestore): int =
  ## Create a new replica using WAL-E.
  ##
  ## Returns:
  ##   0 = Success
  ##   1 = Error, try again
  ##   2 = Error, don't try again
  if self.initError:
    logger.error(fmt"init error: {self.walE.envDir} did not exist at initialization time")
    return int(ecFail)

  try:
    let shouldUseS3 = self.shouldUseS3ToCreateReplica()
    if shouldUseS3.isNone:
      return int(ecRetryLater)
    elif shouldUseS3.get:
      return self.createReplicaWithS3()
    else:
      return int(ecFail)
  except:
    logger.error("Unhandled exception when running WAL-E restore")
  return int(ecFail)

proc main*(): int =
  ## Entry point for WAL-E restore script.
  var p = initOptParser()
  var
    scope = ""
    role = ""
    datadir = ""
    connstring = ""
    retries = 1
    envdir = ""
    thresholdMegabytes = 10240
    thresholdBackupSizePercentage = 30
    useIam = 0
    noLeader = 0

  while true:
    p.next()
    case p.kind
    of cmdEnd: break
    of cmdShortOption, cmdLongOption:
      case p.key
      of "scope": scope = p.val
      of "role": role = p.val
      of "datadir": datadir = p.val
      of "connstring": connstring = p.val
      of "retries": retries = parseInt(p.val)
      of "envdir": envdir = p.val
      of "threshold_megabytes": thresholdMegabytes = parseInt(p.val)
      of "threshold_backup_size_percentage": thresholdBackupSizePercentage = parseInt(p.val)
      of "use_iam": useIam = parseInt(p.val)
      of "no_leader": noLeader = parseInt(p.val)
      else: discard
    of cmdArgument: discard

  if scope.len == 0 or datadir.len == 0 or connstring.len == 0 or envdir.len == 0:
    echo "Usage: wale_restore --scope=<scope> --datadir=<dir> --connstring=<conn> --envdir=<dir> [options]"
    return 1

  assert retries >= 0

  var exitCode = int(ecFail)

  # Retry cloning in a loop
  for _ in 0..retries:
    let restore = newWALERestore(
      scope = scope,
      datadir = datadir,
      connstring = connstring,
      envDir = envdir,
      thresholdMb = thresholdMegabytes,
      thresholdPct = thresholdBackupSizePercentage,
      useIam = useIam,
      noLeader = noLeader != 0,
      retries = retries
    )
    exitCode = restore.run()
    if exitCode != int(ecRetryLater):
      logger.debug(fmt"exit_code is {exitCode}, not retrying")
      break
    sleep(RETRY_SLEEP_INTERVAL * 1000)

  return exitCode

when isMainModule:
  quit(main())
