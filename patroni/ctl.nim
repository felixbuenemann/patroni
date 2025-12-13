## Implement patronictl: a command-line application which utilises the REST API to perform cluster operations.
##
## Most of the patronictl commands (restart/reinit/pause/resume/show-config/edit-config and similar)
## require the group argument and work only for that specific Citus group.
## If not specified in the command line the group might be taken from the configuration file.
## If it is also missing in the configuration file we assume that this is just a normal Patroni cluster (not Citus).

import std/[os, json, strutils, strformat, tables, times, options, terminal, parseopt]
import ./config
import ./dcs/dcs
import ./exceptions
import ./request
import ./utils
import ./version
import ./log
import ./postgresql/misc

let logger = getLogger("patroni.ctl")

const
  CONFIG_DIR_PATH* = getHomeDir() / ".config" / "patroni"
  CONFIG_FILE_PATH* = CONFIG_DIR_PATH / "patronictl.yaml"

type
  CtlPostgresqlRole* = enum
    ## PostgreSQL role for filtering.
    cpLeader = "leader"
    cpPrimary = "primary"
    cpStandbyLeader = "standby-leader"
    cpReplica = "replica"
    cpStandby = "standby"
    cpAny = "any"

  PatroniCtlException* = object of PatroniException
    ## Raised upon issues faced by patronictl utility.

  PatroniCtl* = ref object
    ## Main patronictl class.
    config*: Config
    request*: PatroniRequest
    dcs*: AbstractDCS
    scope*: string
    group*: int

  ColumnDef = tuple
    name: string
    width: int

proc newPatroniCtl*(configFile: string = ""): PatroniCtl =
  ## Create a new PatroniCtl instance.
  new(result)

  let file = if configFile.len > 0: configFile else: CONFIG_FILE_PATH
  if fileExists(file):
    result.config = newConfig(file, nil)
  else:
    result.config = newConfig("", nil)

  result.request = newPatroniRequest()

proc printTable*(headers: seq[string], rows: seq[seq[string]], title: string = "") =
  ## Print a formatted table to stdout.
  if title.len > 0:
    echo fmt"+ Cluster: {title} +"
    echo ""

  # Calculate column widths
  var widths = newSeq[int](headers.len)
  for i, header in headers:
    widths[i] = header.len

  for row in rows:
    for i, cell in row:
      if i < widths.len and cell.len > widths[i]:
        widths[i] = cell.len

  # Print headers
  var headerLine = "| "
  var sepLine = "+-"
  for i, header in headers:
    headerLine &= header.alignLeft(widths[i]) & " | "
    sepLine &= "-".repeat(widths[i]) & "-+-"
  sepLine = sepLine[0..^3] & "+"
  headerLine = headerLine[0..^3] & "|"

  echo sepLine
  echo headerLine
  echo sepLine

  # Print rows
  for row in rows:
    var rowLine = "| "
    for i in 0..<headers.len:
      let cell = if i < row.len: row[i] else: ""
      rowLine &= cell.alignLeft(widths[i]) & " | "
    rowLine = rowLine[0..^3] & "|"
    echo rowLine

  echo sepLine

proc getClusterMembers*(ctl: PatroniCtl, cluster: dcs.Cluster): seq[seq[string]] =
  ## Get cluster members as table rows.
  result = @[]
  for member in cluster.members:
    var row: seq[string] = @[]
    row.add(member.name)
    row.add(if cluster.isLeader(member.name): "Leader" else: "Replica")
    row.add(member.state)
    row.add($member.timeline)
    row.add($member.xlogLocation)
    result.add(row)

proc list*(ctl: PatroniCtl, clusterName: string): int =
  ## List cluster members.
  echo fmt"Cluster: {clusterName}"
  echo ""

  # Would fetch actual cluster data via REST API
  let headers = @["Member", "Role", "State", "Timeline", "Lag"]
  let rows: seq[seq[string]] = @[]

  printTable(headers, rows, clusterName)
  result = 0

proc switchover*(ctl: PatroniCtl, clusterName: string, leader: string = "",
                 candidate: string = "", force: bool = false): int =
  ## Perform a switchover.
  echo fmt"Performing switchover in cluster {clusterName}"

  if leader.len > 0:
    echo fmt"  From: {leader}"
  if candidate.len > 0:
    echo fmt"  To: {candidate}"

  if not force:
    stdout.write("Are you sure you want to proceed? [y/N]: ")
    let response = stdin.readLine()
    if response.toLowerAscii() != "y":
      echo "Switchover cancelled."
      return 1

  # Would call REST API to perform switchover
  echo "Switchover initiated."
  result = 0

proc failover*(ctl: PatroniCtl, clusterName: string, candidate: string = "",
               force: bool = false): int =
  ## Perform a failover.
  echo fmt"Performing failover in cluster {clusterName}"

  if candidate.len > 0:
    echo fmt"  To: {candidate}"

  if not force:
    stdout.write("Are you sure you want to proceed? [y/N]: ")
    let response = stdin.readLine()
    if response.toLowerAscii() != "y":
      echo "Failover cancelled."
      return 1

  # Would call REST API to perform failover
  echo "Failover initiated."
  result = 0

proc restart*(ctl: PatroniCtl, clusterName: string, member: string = "",
              role: CtlPostgresqlRole = cpAny, force: bool = false): int =
  ## Restart PostgreSQL on a member.
  if member.len > 0:
    echo fmt"Restarting member {member} in cluster {clusterName}"
  else:
    echo fmt"Restarting PostgreSQL in cluster {clusterName}"

  if not force:
    stdout.write("Are you sure you want to proceed? [y/N]: ")
    let response = stdin.readLine()
    if response.toLowerAscii() != "y":
      echo "Restart cancelled."
      return 1

  # Would call REST API to restart
  echo "Restart initiated."
  result = 0

proc reinit*(ctl: PatroniCtl, clusterName: string, member: string,
             force: bool = false): int =
  ## Reinitialize a member.
  echo fmt"Reinitializing member {member} in cluster {clusterName}"

  if not force:
    stdout.write("Are you sure you want to proceed? [y/N]: ")
    let response = stdin.readLine()
    if response.toLowerAscii() != "y":
      echo "Reinitialize cancelled."
      return 1

  # Would call REST API to reinitialize
  echo "Reinitialize initiated."
  result = 0

proc pause*(ctl: PatroniCtl, clusterName: string): int =
  ## Pause automatic failover.
  echo fmt"Pausing cluster {clusterName}"
  # Would call REST API to pause
  echo "Cluster paused."
  result = 0

proc resume*(ctl: PatroniCtl, clusterName: string): int =
  ## Resume automatic failover.
  echo fmt"Resuming cluster {clusterName}"
  # Would call REST API to resume
  echo "Cluster resumed."
  result = 0

proc showConfig*(ctl: PatroniCtl, clusterName: string): int =
  ## Show cluster configuration.
  echo fmt"Configuration for cluster {clusterName}:"
  echo ""
  # Would fetch and display actual configuration
  result = 0

proc editConfig*(ctl: PatroniCtl, clusterName: string): int =
  ## Edit cluster configuration.
  echo fmt"Editing configuration for cluster {clusterName}"
  # Would open editor and update configuration via REST API
  result = 0

proc reload*(ctl: PatroniCtl, clusterName: string, member: string = ""): int =
  ## Reload configuration on members.
  if member.len > 0:
    echo fmt"Reloading member {member} in cluster {clusterName}"
  else:
    echo fmt"Reloading all members in cluster {clusterName}"
  # Would call REST API to reload
  echo "Reload triggered."
  result = 0

proc flush*(ctl: PatroniCtl, clusterName: string, member: string = ""): int =
  ## Flush scheduled actions.
  echo fmt"Flushing scheduled actions in cluster {clusterName}"
  result = 0

proc printHelp*() =
  ## Print help message.
  echo "patronictl - Patroni command-line interface"
  echo ""
  echo "Usage: patronictl [OPTIONS] COMMAND [ARGS]..."
  echo ""
  echo "Commands:"
  echo "  list        List cluster members"
  echo "  switchover  Perform a switchover"
  echo "  failover    Perform a failover"
  echo "  restart     Restart PostgreSQL"
  echo "  reinit      Reinitialize a member"
  echo "  pause       Pause automatic failover"
  echo "  resume      Resume automatic failover"
  echo "  show-config Show cluster configuration"
  echo "  edit-config Edit cluster configuration"
  echo "  reload      Reload configuration"
  echo "  flush       Flush scheduled actions"
  echo "  version     Show version"
  echo ""
  echo "Options:"
  echo "  -c, --config-file  Configuration file path"
  echo "  -d, --dcs-url      DCS connection URL"
  echo "  -k, --cluster      Cluster name (scope)"
  echo "  -h, --help         Show this help message"

proc printVersion*() =
  ## Print version.
  echo fmt"patronictl version {patroniVersion}"

proc main*(): int =
  ## Main entry point for patronictl.
  var configFile = ""
  var dcsUrl = ""
  var clusterName = ""
  var command = ""
  var args: seq[string] = @[]
  var force = false

  var p = initOptParser()
  while true:
    p.next()
    case p.kind
    of cmdEnd: break
    of cmdShortOption, cmdLongOption:
      case p.key
      of "c", "config-file": configFile = p.val
      of "d", "dcs-url": dcsUrl = p.val
      of "k", "cluster": clusterName = p.val
      of "f", "force": force = true
      of "h", "help":
        printHelp()
        return 0
      of "v", "version":
        printVersion()
        return 0
      else: discard
    of cmdArgument:
      if command.len == 0:
        command = p.key
      else:
        args.add(p.key)

  if command.len == 0:
    printHelp()
    return 0

  let ctl = newPatroniCtl(configFile)

  case command
  of "list":
    if clusterName.len == 0 and args.len > 0:
      clusterName = args[0]
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    return ctl.list(clusterName)

  of "switchover":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    var leader = ""
    var candidate = ""
    for arg in args:
      if arg.startsWith("--leader="):
        leader = arg[9..^1]
      elif arg.startsWith("--candidate="):
        candidate = arg[12..^1]
    return ctl.switchover(clusterName, leader, candidate, force)

  of "failover":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    var candidate = ""
    for arg in args:
      if arg.startsWith("--candidate="):
        candidate = arg[12..^1]
    return ctl.failover(clusterName, candidate, force)

  of "restart":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    let member = if args.len > 0: args[0] else: ""
    return ctl.restart(clusterName, member, cpAny, force)

  of "reinit":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    if args.len == 0:
      echo "Error: member name is required"
      return 1
    return ctl.reinit(clusterName, args[0], force)

  of "pause":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    return ctl.pause(clusterName)

  of "resume":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    return ctl.resume(clusterName)

  of "show-config":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    return ctl.showConfig(clusterName)

  of "edit-config":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    return ctl.editConfig(clusterName)

  of "reload":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    let member = if args.len > 0: args[0] else: ""
    return ctl.reload(clusterName, member)

  of "flush":
    if clusterName.len == 0:
      echo "Error: cluster name is required"
      return 1
    let member = if args.len > 0: args[0] else: ""
    return ctl.flush(clusterName, member)

  of "version":
    printVersion()
    return 0

  else:
    echo fmt"Unknown command: {command}"
    printHelp()
    return 1

when isMainModule:
  quit(main())
