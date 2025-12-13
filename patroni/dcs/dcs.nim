## Abstract classes for Distributed Configuration Store.

import std/[tables, json, options, strutils, times, re, locks, uri, strformat, os]
import ../global_config
import ../exceptions
import ../tags
import ../utils

export global_config, exceptions, tags

type
  VersionType* = int
  SessionType* = string

  MemberData* = ref object
    ## Data dictionary for a cluster member
    connUrl*: string
    apiUrl*: string
    connKwargs*: Table[string, string]
    state*: string
    role*: string
    xlogLocation*: int64
    timeline*: int
    tags*: Table[string, string]
    extra*: Table[string, JsonNode]

  Member* = ref object of Tags
    ## Immutable object which represents single member of PostgreSQL cluster.
    ##
    ## These two keys in data are always written to the DCS:
    ##   conn_url: connection string containing host, user and password
    ##   api_url: REST API url of patroni instance
    version*: VersionType
    name*: string
    session*: SessionType
    data*: MemberData
    replicatefromValue: string

  RemoteMember* = ref object of Member
    ## A remote member (typically leader info from DCS)

  Leader* = ref object
    ## Information about the cluster leader
    version*: VersionType
    session*: SessionType
    member*: RemoteMember

  ClusterConfig* = ref object
    ## Cluster configuration from DCS
    version*: VersionType
    data*: Table[string, JsonNode]
    modifyVersion*: int

  Status* = ref object
    ## Cluster status information
    lastLsn*: int64
    slots*: Table[string, int64]
    slotsAdvanceLsn*: int64
    retention*: int

  TimelineHistory* = ref object
    ## Timeline history entry
    filename*: string
    content*: string

  Failover* = ref object
    ## Scheduled failover information
    version*: VersionType
    leader*: string
    candidate*: string
    scheduledAt*: Option[DateTime]

  SyncState* = ref object
    ## Synchronous replication state
    version*: VersionType
    leader*: string
    syncStandby*: seq[string]
    quorum*: int

  Cluster* = ref object
    ## Represents the state of a PostgreSQL cluster in DCS
    version*: VersionType
    config*: ClusterConfig
    leader*: Leader
    status*: Status
    members*: seq[Member]
    failover*: Failover
    sync*: SyncState
    history*: TimelineHistory
    failsafe*: Table[string, string]
    workers*: Table[int, seq[Member]]

  AbstractDCS* = ref object of RootObj
    ## Abstract base class for DCS implementations
    name*: string
    config*: Table[string, JsonNode]
    scopeVal: string
    namespaceVal: string
    memberName: string
    loopWaitVal: int
    ttlVal: int
    retryTimeoutVal: int
    cluster*: Cluster
    event*: bool
    lock*: Lock
    mpp*: pointer  # AbstractMPP

let slotNameRe* = re"^[a-z0-9_]{1,63}$"

proc slotNameFromMemberName*(memberName: string): string =
  ## Translate member name to valid PostgreSQL slot name.
  ##
  ## PostgreSQL's replication slot names must be valid PostgreSQL names. This function maps
  ## the wider space of member names to valid PostgreSQL names. Names have their case lowered,
  ## dashes and periods common in hostnames are replaced with underscores, other characters
  ## are encoded as their unicode codepoint. Name is truncated to 64 characters.
  ##
  ## :param memberName: The string to convert to a slot name.
  ## :returns: The string converted using the rules described above.

  var slotName = ""
  for c in memberName.toLowerAscii():
    if c in 'a'..'z' or c in '0'..'9' or c == '_':
      slotName.add(c)
    elif c == '-' or c == '.':
      slotName.add('_')
    else:
      slotName.add(fmt"u{ord(c):04d}")

  if slotName.len > 63:
    result = slotName[0..<63]
  else:
    result = slotName

proc parseConnectionString*(value: string): tuple[connUrl: string, apiUrl: Option[string]] =
  ## Split and rejoin a URL string into a connection URL and an API URL.
  ##
  ## Original Governor stores connection strings for each cluster members in a following format:
  ##   postgres://{username}:{password}@{connect_address}/postgres
  ##
  ## Since each of our patroni instances provides their own REST API endpoint, it's good to store
  ## this information in DCS along with PostgreSQL connection string.
  ##
  ## :param value: The URL string to split.
  ## :returns: the connection string stored in DCS split into two parts, conn_url and api_url.

  let parsed = parseUri(value)
  var apiUrl: Option[string] = none(string)
  var queryParts: seq[string] = @[]

  # Parse query string
  if parsed.query.len > 0:
    for part in parsed.query.split('&'):
      if part.startsWith("application_name="):
        apiUrl = some(part[17..^1])
      else:
        if part.len > 0:
          queryParts.add(part)

  # Rebuild connection URL without application_name
  var connUrl = parsed.scheme & "://"
  if parsed.username.len > 0:
    connUrl &= parsed.username
    if parsed.password.len > 0:
      connUrl &= ":" & parsed.password
    connUrl &= "@"
  connUrl &= parsed.hostname
  if parsed.port.len > 0:
    connUrl &= ":" & parsed.port
  connUrl &= parsed.path
  if queryParts.len > 0:
    connUrl &= "?" & queryParts.join("&")

  result = (connUrl, apiUrl)

proc newMemberData*(): MemberData =
  new(result)
  result.connUrl = ""
  result.apiUrl = ""
  result.connKwargs = initTable[string, string]()
  result.state = ""
  result.role = ""
  result.xlogLocation = 0
  result.timeline = 0
  result.tags = initTable[string, string]()
  result.extra = initTable[string, JsonNode]()

proc newMember*(version: VersionType, name: string, session: SessionType, data: MemberData): Member =
  new(result)
  result.version = version
  result.name = name
  result.session = session
  result.data = data

proc fromNode*(version: VersionType, name: string, session: SessionType, value: string): Member =
  ## Factory method for instantiating Member from a JSON serialised string or object.
  ##
  ## :param version: modification version of a given member key in a Configuration Store.
  ## :param name: name of PostgreSQL cluster member.
  ## :param session: either session id or just ttl in seconds.
  ## :param value: JSON encoded string containing arbitrary data or a connection URL.
  ## :returns: a Member instance built with the given arguments.

  var data = newMemberData()

  if value.startsWith("postgres"):
    let (connUrl, apiUrl) = parseConnectionString(value)
    data.connUrl = connUrl
    if apiUrl.isSome:
      data.apiUrl = apiUrl.get()
  else:
    try:
      let jsonData = parseJson(value)
      if jsonData.kind == JObject:
        if jsonData.hasKey("conn_url"):
          data.connUrl = jsonData["conn_url"].getStr("")
        if jsonData.hasKey("api_url"):
          data.apiUrl = jsonData["api_url"].getStr("")
        if jsonData.hasKey("state"):
          data.state = jsonData["state"].getStr("")
        if jsonData.hasKey("role"):
          data.role = jsonData["role"].getStr("")
        if jsonData.hasKey("xlog_location"):
          data.xlogLocation = jsonData["xlog_location"].getInt(0)
        if jsonData.hasKey("timeline"):
          data.timeline = jsonData["timeline"].getInt(0)
        if jsonData.hasKey("tags") and jsonData["tags"].kind == JObject:
          for key, val in jsonData["tags"].pairs:
            data.tags[key] = $val
    except JsonParsingError:
      discard  # Return empty data on parse error

  result = newMember(version, name, session, data)

method tags*(self: Member): Table[string, string] =
  ## Get the tags for this member.
  result = self.data.tags

proc connUrl*(self: Member): string =
  ## The conn_url value from data if defined or constructed from conn_kwargs.
  if self.data.connUrl.len > 0:
    return self.data.connUrl

  if self.data.connKwargs.len > 0:
    let host = self.data.connKwargs.getOrDefault("host", "localhost")
    let port = self.data.connKwargs.getOrDefault("port", "5432")
    result = uri("postgresql", host, parseIntValue(port).get(5432))
    self.data.connUrl = result
    return result

  result = ""

proc apiUrl*(self: Member): string =
  ## The api_url value from data if defined.
  result = self.data.apiUrl

proc state*(self: Member): string =
  ## The state value from data.
  result = self.data.state

proc role*(self: Member): string =
  ## The role value from data.
  result = self.data.role

proc xlogLocation*(self: Member): int64 =
  ## The xlog_location value from data.
  result = self.data.xlogLocation

proc timeline*(self: Member): int =
  ## The timeline value from data.
  result = self.data.timeline

proc newRemoteMember*(name: string, data: MemberData): RemoteMember =
  new(result)
  result.version = 0
  result.name = name
  result.session = ""
  result.data = data

proc newLeader*(version: VersionType, session: SessionType, member: RemoteMember): Leader =
  new(result)
  result.version = version
  result.session = session
  result.member = member

proc connUrl*(self: Leader): string =
  if self.member != nil:
    result = self.member.connUrl
  else:
    result = ""

proc newClusterConfig*(): ClusterConfig =
  new(result)
  result.version = 0
  result.data = initTable[string, JsonNode]()
  result.modifyVersion = 0

proc newClusterConfig*(version: VersionType, data: Table[string, JsonNode], modifyVersion: int = 0): ClusterConfig =
  new(result)
  result.version = version
  result.data = data
  result.modifyVersion = modifyVersion

proc newStatus*(): Status =
  new(result)
  result.lastLsn = 0
  result.slots = initTable[string, int64]()
  result.slotsAdvanceLsn = 0
  result.retention = 0

proc newFailover*(): Failover =
  new(result)
  result.version = 0
  result.leader = ""
  result.candidate = ""
  result.scheduledAt = none(DateTime)

proc newSyncState*(): SyncState =
  new(result)
  result.version = 0
  result.leader = ""
  result.syncStandby = @[]
  result.quorum = 0

proc newCluster*(): Cluster =
  new(result)
  result.version = 0
  result.config = nil
  result.leader = nil
  result.status = nil
  result.members = @[]
  result.failover = nil
  result.sync = nil
  result.history = nil
  result.failsafe = initTable[string, string]()
  result.workers = initTable[int, seq[Member]]()

proc hasLeader*(self: Cluster): bool =
  ## Check if cluster has a leader.
  result = self.leader != nil

proc isUnlocked*(self: Cluster): bool =
  ## Check if cluster is unlocked (no leader).
  result = self.leader == nil

proc leaderName*(self: Cluster): string =
  ## Get the name of the current leader.
  if self.leader != nil and self.leader.member != nil:
    result = self.leader.member.name
  else:
    result = ""

proc getMember*(self: Cluster, name: string, fallbackToLeader: bool = true): Option[Member] =
  ## Get a member by name.
  for m in self.members:
    if m.name == name:
      return some(m)

  if fallbackToLeader and self.leader != nil and self.leader.member != nil and
     self.leader.member.name == name:
    # Convert RemoteMember to Member
    let m = newMember(self.leader.version, self.leader.member.name,
                      self.leader.session, self.leader.member.data)
    return some(m)

  result = none(Member)

proc isLeader*(self: Cluster, name: string): bool =
  ## Check if the named node is the leader.
  result = self.leaderName() == name

proc isSynchronizedTo*(self: Cluster, name: string): bool =
  ## Check if a member is synchronized to the given name.
  if self.sync != nil:
    result = name in self.sync.syncStandby
  else:
    result = false

# AbstractDCS implementation

proc newAbstractDCS*(config: Table[string, JsonNode]): AbstractDCS =
  new(result)
  result.config = config
  result.scopeVal = ""
  result.namespaceVal = "/service"
  result.memberName = ""
  result.loopWaitVal = 10
  result.ttlVal = 30
  result.retryTimeoutVal = 10
  result.cluster = nil
  result.event = false
  initLock(result.lock)
  result.mpp = nil

  # Extract common config values
  if config.hasKey("scope"):
    result.scopeVal = config["scope"].getStr("")
  if config.hasKey("namespace"):
    result.namespaceVal = config["namespace"].getStr("/service")
  if config.hasKey("name"):
    result.memberName = config["name"].getStr("")
  if config.hasKey("loop_wait"):
    result.loopWaitVal = config["loop_wait"].getInt(10)
  if config.hasKey("ttl"):
    result.ttlVal = config["ttl"].getInt(30)
  if config.hasKey("retry_timeout"):
    result.retryTimeoutVal = config["retry_timeout"].getInt(10)

proc scope*(self: AbstractDCS): string =
  result = self.scopeVal

proc namespace*(self: AbstractDCS): string =
  result = self.namespaceVal

proc loopWait*(self: AbstractDCS): int =
  let gc = getGlobalConfig()
  let gcVal = gc.getInt("loop_wait", 0)
  if gcVal > 0:
    result = gcVal
  else:
    result = self.loopWaitVal

proc ttl*(self: AbstractDCS): int =
  let gc = getGlobalConfig()
  let gcVal = gc.getInt("ttl", 0)
  if gcVal > 0:
    result = gcVal
  else:
    result = self.ttlVal

proc retryTimeout*(self: AbstractDCS): int =
  let gc = getGlobalConfig()
  let gcVal = gc.getInt("retry_timeout", 0)
  if gcVal > 0:
    result = gcVal
  else:
    result = self.retryTimeoutVal

# Abstract methods to be implemented by subclasses

method getCluster*(self: AbstractDCS): Cluster {.base.} =
  ## Get the current cluster state from DCS.
  raise newDCSError("getCluster not implemented")

method setFailover*(self: AbstractDCS, value: Failover): bool {.base.} =
  ## Set failover value in DCS.
  raise newDCSError("setFailover not implemented")

method manualFailover*(self: AbstractDCS, leader: string, candidate: string,
                       scheduledAt: Option[DateTime] = none(DateTime),
                       version: int = -1): bool {.base.} =
  ## Trigger a manual failover.
  raise newDCSError("manualFailover not implemented")

method setConfig*(self: AbstractDCS, value: Table[string, JsonNode]): bool {.base.} =
  ## Set cluster configuration in DCS.
  raise newDCSError("setConfig not implemented")

method touch*(self: AbstractDCS): bool {.base.} =
  ## Touch/update the member key in DCS to refresh TTL.
  raise newDCSError("touch not implemented")

method takeLease*(self: AbstractDCS): bool {.base.} =
  ## Attempt to take/acquire the leader lease.
  raise newDCSError("takeLease not implemented")

method attemptToAcquireLeader*(self: AbstractDCS): bool {.base.} =
  ## Attempt to acquire leadership.
  raise newDCSError("attemptToAcquireLeader not implemented")

method updateLeader*(self: AbstractDCS, last: Option[Leader] = none(Leader)): bool {.base.} =
  ## Update leader information.
  raise newDCSError("updateLeader not implemented")

method releaseLease*(self: AbstractDCS): bool {.base.} =
  ## Release the leader lease.
  raise newDCSError("releaseLease not implemented")

method deleteLeader*(self: AbstractDCS, last: Option[Leader] = none(Leader)): bool {.base.} =
  ## Delete leader key.
  raise newDCSError("deleteLeader not implemented")

method setSyncState*(self: AbstractDCS, leader: string, syncStandby: seq[string],
                      version: int = -1): Option[SyncState] {.base.} =
  ## Set synchronous replication state.
  raise newDCSError("setSyncState not implemented")

method deleteSyncState*(self: AbstractDCS, version: int = -1): bool {.base.} =
  ## Delete synchronous replication state.
  raise newDCSError("deleteSyncState not implemented")

method setHistory*(self: AbstractDCS, value: string): bool {.base.} =
  ## Set timeline history.
  raise newDCSError("setHistory not implemented")

method deleteCluster*(self: AbstractDCS): bool {.base.} =
  ## Delete the cluster from DCS.
  raise newDCSError("deleteCluster not implemented")

method watch*(self: AbstractDCS, leader: Option[Leader], timeout: float): bool {.base.} =
  ## Watch for changes in DCS.
  ## Returns true if a change was detected.
  sleep(int(timeout * 1000))
  result = false

method reloadConfig*(self: AbstractDCS, config: Table[string, JsonNode]) {.base.} =
  ## Reload configuration.
  discard

# Helper procs for creating objects from DCS nodes

proc clusterConfigFromNode*(modifyIndex: int64, value: string): ClusterConfig =
  ## Create ClusterConfig from node value.
  result = newClusterConfig()
  result.modifyVersion = int(modifyIndex)
  try:
    let jsonData = parseJson(value)
    if jsonData.kind == JObject:
      for key, val in jsonData.pairs:
        result.data[key] = val
  except JsonParsingError:
    discard

proc timelineHistoryFromNode*(modifyIndex: int64, value: string): TimelineHistory =
  ## Create TimelineHistory from node value.
  new(result)
  result.filename = ""
  result.content = value

proc statusFromNode*(value: string): Status =
  ## Create Status from node value.
  result = newStatus()
  try:
    let jsonData = parseJson(value)
    if jsonData.kind == JObject:
      if jsonData.hasKey("optime"):
        result.lastLsn = jsonData["optime"].getInt(0)
      if jsonData.hasKey("slots") and jsonData["slots"].kind == JObject:
        for key, val in jsonData["slots"].pairs:
          result.slots[key] = val.getInt(0)
  except JsonParsingError:
    # Try parsing as just an integer (legacy format)
    try:
      result.lastLsn = strutils.parseInt(value)
    except ValueError:
      discard

proc failoverFromNode*(modifyIndex: int64, value: string): Failover =
  ## Create Failover from node value.
  result = newFailover()
  result.version = int(modifyIndex)
  try:
    let jsonData = parseJson(value)
    if jsonData.kind == JObject:
      if jsonData.hasKey("leader"):
        result.leader = jsonData["leader"].getStr("")
      if jsonData.hasKey("candidate"):
        result.candidate = jsonData["candidate"].getStr("")
      if jsonData.hasKey("scheduled_at"):
        let dt = jsonData["scheduled_at"].getStr("")
        if dt.len > 0:
          try:
            result.scheduledAt = some(parse(dt, "yyyy-MM-dd'T'HH:mm:ss"))
          except TimeParseError:
            discard
  except JsonParsingError:
    discard

proc syncStateFromNode*(modifyIndex: int64, value: string): SyncState =
  ## Create SyncState from node value.
  result = newSyncState()
  result.version = int(modifyIndex)
  try:
    let jsonData = parseJson(value)
    if jsonData.kind == JObject:
      if jsonData.hasKey("leader"):
        result.leader = jsonData["leader"].getStr("")
      if jsonData.hasKey("sync_standby") and jsonData["sync_standby"].kind == JArray:
        for item in jsonData["sync_standby"]:
          result.syncStandby.add(item.getStr(""))
      if jsonData.hasKey("quorum"):
        result.quorum = jsonData["quorum"].getInt(0)
  except JsonParsingError:
    discard

proc emptyCluster*(): Cluster =
  ## Return an empty cluster.
  result = newCluster()

# Additional AbstractDCS methods and properties

proc initAbstractDCS*(self: AbstractDCS, config: JsonNode) =
  ## Initialize AbstractDCS from JSON config.
  self.config = initTable[string, JsonNode]()
  if config.kind == JObject:
    for key, val in config.pairs:
      self.config[key] = val

  self.scopeVal = ""
  self.namespaceVal = "/service"
  self.memberName = ""
  self.loopWaitVal = 10
  self.ttlVal = 30
  self.retryTimeoutVal = 10
  self.cluster = nil
  self.event = false
  initLock(self.lock)
  self.mpp = nil

  if self.config.hasKey("scope"):
    self.scopeVal = self.config["scope"].getStr("")
  if self.config.hasKey("namespace"):
    self.namespaceVal = self.config["namespace"].getStr("/service")
  if self.config.hasKey("name"):
    self.memberName = self.config["name"].getStr("")
  if self.config.hasKey("loop_wait"):
    self.loopWaitVal = self.config["loop_wait"].getInt(10)
  if self.config.hasKey("ttl"):
    self.ttlVal = self.config["ttl"].getInt(30)
  if self.config.hasKey("retry_timeout"):
    self.retryTimeoutVal = self.config["retry_timeout"].getInt(10)

proc name*(self: AbstractDCS): string =
  ## Get the member name.
  result = self.memberName

proc isCtl*(self: AbstractDCS): bool =
  ## Check if running in ctl mode.
  result = false  # Would be set based on context

proc clientPath*(self: AbstractDCS, path: string): string =
  ## Get the full DCS path for a key.
  result = self.namespaceVal & "/" & self.scopeVal & "/" & path

proc memberPath*(self: AbstractDCS): string =
  ## Get the path for this member's key.
  result = self.clientPath("members/" & self.memberName)

proc leaderPath*(self: AbstractDCS): string =
  ## Get the path for the leader key.
  result = self.clientPath("leader")

proc leaderOptimePath*(self: AbstractDCS): string =
  ## Get the path for the leader optime key.
  result = self.clientPath("optime/leader")

proc statusPath*(self: AbstractDCS): string =
  ## Get the path for the status key.
  result = self.clientPath("status")

proc configPath*(self: AbstractDCS): string =
  ## Get the path for the config key.
  result = self.clientPath("config")

proc initializePath*(self: AbstractDCS): string =
  ## Get the path for the initialize key.
  result = self.clientPath("initialize")

proc failoverPath*(self: AbstractDCS): string =
  ## Get the path for the failover key.
  result = self.clientPath("failover")

proc historyPath*(self: AbstractDCS): string =
  ## Get the path for the history key.
  result = self.clientPath("history")

proc syncPath*(self: AbstractDCS): string =
  ## Get the path for the sync key.
  result = self.clientPath("sync")

proc failsafePath*(self: AbstractDCS): string =
  ## Get the path for the failsafe key.
  result = self.clientPath("failsafe")
