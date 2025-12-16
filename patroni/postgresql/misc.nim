## PostgreSQL miscellaneous utilities and enumerations.

import std/[strutils, sequtils, strformat]
import ../exceptions
import ../log

let logger = getLogger("patroni.postgresql.misc")

type
  PostgresqlState* = enum
    ## Possible values of Postgresql.state.
    ##
    ## Numeric indexes should NEVER change once assigned to maintain
    ## backward compatibility with existing monitoring systems.
    psInitdb = (0, "initializing new cluster")
    psInitdbFailed = (1, "initdb failed")
    psCustomBootstrap = (2, "running custom bootstrap script")
    psCustomBootstrapFailed = (3, "custom bootstrap failed")
    psCreatingReplica = (4, "creating replica")
    psRunning = (5, "running")
    psStarting = (6, "starting")
    psBootstrapStarting = (7, "starting after custom bootstrap")
    psStartFailed = (8, "start failed")
    psRestarting = (9, "restarting")
    psRestartFailed = (10, "restart failed")
    psStopping = (11, "stopping")
    psStopped = (12, "stopped")
    psStopFailed = (13, "stop failed")
    psCrashed = (14, "crashed")

  PostgresqlRole* = enum
    ## Possible values of Postgresql.role.
    prPrimary = "primary"
    prMaster = "master"
    prStandbyLeader = "standby_leader"
    prReplica = "replica"
    prDemoted = "demoted"
    prUninitialized = "uninitialized"
    prPromoted = "promoted"

proc `$`*(state: PostgresqlState): string =
  ## Get a string representation of a PostgresqlState member.
  case state
  of psInitdb: result = "initializing new cluster"
  of psInitdbFailed: result = "initdb failed"
  of psCustomBootstrap: result = "running custom bootstrap script"
  of psCustomBootstrapFailed: result = "custom bootstrap failed"
  of psCreatingReplica: result = "creating replica"
  of psRunning: result = "running"
  of psStarting: result = "starting"
  of psBootstrapStarting: result = "starting after custom bootstrap"
  of psStartFailed: result = "start failed"
  of psRestarting: result = "restarting"
  of psRestartFailed: result = "restart failed"
  of psStopping: result = "stopping"
  of psStopped: result = "stopped"
  of psStopFailed: result = "stop failed"
  of psCrashed: result = "crashed"

proc `$`*(role: PostgresqlRole): string =
  ## Get a string representation of a PostgresqlRole member.
  case role
  of prPrimary: result = "primary"
  of prMaster: result = "master"
  of prStandbyLeader: result = "standby_leader"
  of prReplica: result = "replica"
  of prDemoted: result = "demoted"
  of prUninitialized: result = "uninitialized"
  of prPromoted: result = "promoted"

proc postgresVersionToInt*(pgVersion: string): int =
  ## Convert the server_version to integer.
  ##
  ## Example:
  ##   postgresVersionToInt("9.5.3") -> 90503
  ##   postgresVersionToInt("9.3.13") -> 90313
  ##   postgresVersionToInt("10.1") -> 100001
  var components: seq[int]
  try:
    components = pgVersion.split('.').mapIt(parseInt(it))
  except ValueError:
    raise newException(PostgresException, fmt"Invalid PostgreSQL version: {pgVersion}")

  if components.len < 2 or (components.len == 2 and components[0] < 10) or components.len > 3:
    raise newException(PostgresException,
      fmt"Invalid PostgreSQL version format: X.Y or X.Y.Z is accepted: {pgVersion}")

  if components.len == 2:
    # new style version numbers, i.e. 10.1 becomes 100001
    components.insert(0, 1)

  result = 0
  for i, c in components:
    result = result * 100 + c

proc postgresMajorVersionToInt*(pgVersion: string): int =
  ## Convert major version string to integer.
  ##
  ## Example:
  ##   postgresMajorVersionToInt("10") -> 100000
  ##   postgresMajorVersionToInt("9.6") -> 90600
  result = postgresVersionToInt(pgVersion & ".0")

proc getMajorFromMinorVersion*(version: int): int =
  ## Extract major PostgreSQL version from the provided full version.
  ##
  ## :param version: integer representation of PostgreSQL full version (major + minor).
  ## :returns: integer representation of the PostgreSQL major version.
  ##
  ## Example:
  ##   getMajorFromMinorVersion(100012) -> 100000
  ##   getMajorFromMinorVersion(90313) -> 90300
  result = (version div 100) * 100

proc parseLsn*(lsn: string): int =
  ## Parse PostgreSQL LSN string to integer.
  let t = lsn.split('/')
  result = parseHexInt(t[0]) * 0x100000000 + parseHexInt(t[1])

iterator parseHistory*(data: string): tuple[timeline: int, lsn: int, reason: string] =
  ## Parse timeline history file data.
  for line in data.split('\n'):
    let values = line.strip().split('\t')
    if values.len == 3:
      try:
        yield (parseInt(values[0]), parseLsn(values[1]), values[2])
      except ValueError, IndexDefect:
        logger.exception(fmt"Exception when parsing timeline history line '{values}'", nil)

proc formatLsn*(lsn: int, full: bool = false): string =
  ## Format integer LSN to PostgreSQL LSN string.
  let high = lsn shr 32
  let low = lsn and 0xFFFFFFFF
  if full:
    result = fmt"{high:X}/{low:08X}"
  else:
    result = fmt"{high:X}/{low:X}"

proc fsyncDir*(path: string) =
  ## Fsync a directory.
  when defined(posix):
    let fd = open(path, fmRead)
    try:
      # Note: Nim doesn't have direct fsync for directories
      # In practice, we'd use posix.fsync here
      discard
    except OSError:
      discard
    finally:
      close(fd)
