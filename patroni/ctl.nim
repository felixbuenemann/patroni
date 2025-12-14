## Implement patronictl: a command-line application which utilises the REST API to perform cluster operations.
##
## Most of the patronictl commands (restart/reinit/pause/resume/show-config/edit-config and similar)
## require the group argument and work only for that specific Citus group.
## If not specified in the command line the group might be taken from the configuration file.
## If it is also missing in the configuration file we assume that this is just a normal Patroni cluster (not Citus).

import std/[os, json, strutils, strformat, tables, times, options, terminal, parseopt]
import ./config
import ./dcs
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

proc getMemberApiUrl(ctl: PatroniCtl, member: string): string =
  ## Get the API URL for a specific member from DCS.
  if ctl.dcs != nil:
    let cluster = ctl.dcs.getCluster()
    for m in cluster.members:
      if m.name == member:
        return m.apiUrl
  result = ""

proc getLeaderApiUrl(ctl: PatroniCtl): string =
  ## Get the API URL for the current leader from DCS.
  if ctl.dcs != nil:
    let cluster = ctl.dcs.getCluster()
    if cluster.leader != nil and cluster.leader.member != nil:
      return cluster.leader.member.apiUrl
  result = ""

proc list*(ctl: PatroniCtl, clusterName: string): int =
  ## List cluster members.
  echo fmt"Cluster: {clusterName}"
  echo ""

  let headers = @["Member", "Role", "State", "Timeline", "Lag"]
  var rows: seq[seq[string]] = @[]

  # Try to fetch cluster data via DCS
  if ctl.dcs != nil:
    try:
      let cluster = ctl.dcs.getCluster()
      rows = ctl.getClusterMembers(cluster)
    except DCSError as e:
      logger.error(fmt"Failed to get cluster info from DCS: {e.msg}")
  else:
    logger.warning("No DCS configured, cannot list cluster members")

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

  # Get leader API URL and call switchover endpoint
  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  let body = %*{
    "leader": leader,
    "candidate": candidate
  }

  try:
    let response = ctl.request.call(leaderUrl, "POST", "switchover", $body)
    if response.status in [200, 202]:
      echo "Switchover initiated successfully."
      try:
        let respJson = parseJson(response.body)
        if respJson.hasKey("message"):
          echo "  " & respJson["message"].getStr()
      except JsonParsingError:
        discard
      return 0
    else:
      echo fmt"Switchover failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error performing switchover: {e.msg}"
    return 1

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

  # Get leader API URL and call failover endpoint
  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  let body = %*{"candidate": candidate}

  try:
    let response = ctl.request.call(leaderUrl, "POST", "failover", $body)
    if response.status in [200, 202]:
      echo "Failover initiated successfully."
      return 0
    else:
      echo fmt"Failover failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error performing failover: {e.msg}"
    return 1

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

  # Get member or leader API URL
  var apiUrl = ""
  if member.len > 0:
    apiUrl = ctl.getMemberApiUrl(member)
  else:
    apiUrl = ctl.getLeaderApiUrl()

  if apiUrl.len == 0:
    echo "Error: Could not determine API URL"
    return 1

  let body = %*{"role": $role}

  try:
    let response = ctl.request.call(apiUrl, "POST", "restart", $body)
    if response.status in [200, 202]:
      echo "Restart initiated successfully."
      return 0
    else:
      echo fmt"Restart failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error performing restart: {e.msg}"
    return 1

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

  let apiUrl = ctl.getMemberApiUrl(member)
  if apiUrl.len == 0:
    echo fmt"Error: Could not find API URL for member {member}"
    return 1

  let body = %*{"force": force}

  try:
    let response = ctl.request.call(apiUrl, "POST", "reinitialize", $body)
    if response.status in [200, 202]:
      echo "Reinitialize initiated successfully."
      return 0
    else:
      echo fmt"Reinitialize failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error performing reinitialize: {e.msg}"
    return 1

proc pause*(ctl: PatroniCtl, clusterName: string): int =
  ## Pause automatic failover.
  echo fmt"Pausing cluster {clusterName}"

  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  let body = %*{"pause": true}

  try:
    let response = ctl.request.call(leaderUrl, "PATCH", "config", $body)
    if response.status == 200:
      echo "Cluster paused successfully."
      return 0
    else:
      echo fmt"Pause failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error pausing cluster: {e.msg}"
    return 1

proc resume*(ctl: PatroniCtl, clusterName: string): int =
  ## Resume automatic failover.
  echo fmt"Resuming cluster {clusterName}"

  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  let body = %*{"pause": false}

  try:
    let response = ctl.request.call(leaderUrl, "PATCH", "config", $body)
    if response.status == 200:
      echo "Cluster resumed successfully."
      return 0
    else:
      echo fmt"Resume failed with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error resuming cluster: {e.msg}"
    return 1

proc showConfig*(ctl: PatroniCtl, clusterName: string): int =
  ## Show cluster configuration.
  echo fmt"Configuration for cluster {clusterName}:"
  echo ""

  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  try:
    let response = ctl.request.call(leaderUrl, "GET", "config")
    if response.status == 200:
      try:
        let config = parseJson(response.body)
        echo pretty(config)
      except JsonParsingError:
        echo response.body
      return 0
    else:
      echo fmt"Failed to get config with status {response.status}"
      echo "  " & response.body
      return 1
  except CatchableError as e:
    echo fmt"Error getting config: {e.msg}"
    return 1

proc editConfig*(ctl: PatroniCtl, clusterName: string): int =
  ## Edit cluster configuration.
  echo fmt"Editing configuration for cluster {clusterName}"

  # First, get the current config
  let leaderUrl = ctl.getLeaderApiUrl()
  if leaderUrl.len == 0:
    echo "Error: Could not determine leader API URL"
    return 1

  try:
    let response = ctl.request.call(leaderUrl, "GET", "config")
    if response.status != 200:
      echo fmt"Failed to get config with status {response.status}"
      return 1

    # Write config to temp file
    let tempFile = getTempDir() / "patroni_config_edit.json"
    try:
      let config = parseJson(response.body)
      writeFile(tempFile, pretty(config))
    except JsonParsingError:
      writeFile(tempFile, response.body)

    # Get editor from EDITOR env var or use default
    let editor = getEnv("EDITOR", "vi")

    # Open editor
    let exitCode = execShellCmd(fmt"{editor} {tempFile}")
    if exitCode != 0:
      echo "Editor exited with error"
      return 1

    # Read modified config
    let newConfig = readFile(tempFile)

    # Update config via API
    let updateResponse = ctl.request.call(leaderUrl, "PATCH", "config", newConfig)
    if updateResponse.status == 200:
      echo "Configuration updated successfully."
      return 0
    else:
      echo fmt"Failed to update config with status {updateResponse.status}"
      echo "  " & updateResponse.body
      return 1
  except CatchableError as e:
    echo fmt"Error editing config: {e.msg}"
    return 1

proc reload*(ctl: PatroniCtl, clusterName: string, member: string = ""): int =
  ## Reload configuration on members.
  if member.len > 0:
    echo fmt"Reloading member {member} in cluster {clusterName}"
    let apiUrl = ctl.getMemberApiUrl(member)
    if apiUrl.len == 0:
      echo fmt"Error: Could not find API URL for member {member}"
      return 1

    try:
      let response = ctl.request.call(apiUrl, "POST", "reload")
      if response.status in [200, 202]:
        echo fmt"Reload triggered on {member}."
        return 0
      else:
        echo fmt"Reload failed on {member} with status {response.status}"
        return 1
    except CatchableError as e:
      echo fmt"Error reloading {member}: {e.msg}"
      return 1
  else:
    echo fmt"Reloading all members in cluster {clusterName}"
    var failed = 0

    if ctl.dcs != nil:
      try:
        let cluster = ctl.dcs.getCluster()
        for m in cluster.members:
          try:
            let response = ctl.request.call(m.apiUrl, "POST", "reload")
            if response.status in [200, 202]:
              echo fmt"  Reload triggered on {m.name}."
            else:
              echo fmt"  Reload failed on {m.name} with status {response.status}"
              inc failed
          except CatchableError as e:
            echo fmt"  Error reloading {m.name}: {e.msg}"
            inc failed
      except DCSError as e:
        echo fmt"Error getting cluster members: {e.msg}"
        return 1
    else:
      echo "Error: No DCS configured"
      return 1

    if failed > 0:
      return 1
    return 0

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
