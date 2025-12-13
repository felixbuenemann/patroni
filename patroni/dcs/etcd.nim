## Etcd DCS backend for Patroni.
##
## This module provides a distributed configuration store backend using etcd (v2 API).

import std/[asyncdispatch, base64, httpclient, json, locks, net, options, os, random, sequtils,
            strformat, strutils, tables, times, uri]
import ../exceptions
import ../log
import ../request
import ../utils
import ../dcs as base

export base

let logger = getLogger("patroni.dcs.etcd")

type
  EtcdRaftInternal* = object of DCSError
    ## Raft Internal Error.

  StaleEtcdNode* = object of CatchableError
    ## Node is stale (raft term is older than previous known).

  EtcdError* = object of DCSError
    ## General etcd error.

  EtcdKeyNotFound* = object of EtcdError
    ## Key not found in etcd.

  EtcdAlreadyExist* = object of EtcdError
    ## Key already exists in etcd.

  EtcdConnectionFailed* = object of EtcdError
    ## Connection to etcd failed.

  EtcdWatchTimedOut* = object of EtcdError
    ## Watch operation timed out.

  EtcdResult* = ref object
    ## Result from an etcd operation.
    action*: string
    key*: string
    value*: string
    ttl*: int
    modifiedIndex*: int64
    createdIndex*: int64
    etcdIndex*: int64
    dir*: bool
    leaves*: seq[EtcdResult]

  DnsCachingResolver* = ref object
    ## DNS caching resolver for etcd hosts.
    cache: Table[tuple[host: string, port: int], tuple[time: float, addresses: seq[string]]]
    cacheTime: float
    cacheFailTime: float
    lock: Lock

  StaleEtcdNodeGuard* = ref object
    ## Guard to detect stale etcd nodes based on raft term.
    clusterId: string
    raftTerm: int64

  EtcdClient* = ref object
    ## Client for communicating with etcd cluster.
    baseUri: string
    machinesCache: seq[string]
    machinesCacheUpdated: float
    machinesCacheTtl: int
    readTimeout: float
    username: string
    password: string
    protocol: string
    useProxies: bool
    allowRedirect: bool
    versionPrefix: string
    updateMachinesCache: bool
    dnsResolver: DnsCachingResolver
    staleGuard: StaleEtcdNodeGuard
    config: Table[string, string]
    httpClient: HttpClient

  Etcd* = ref object of AbstractDCS
    ## Etcd DCS implementation.
    client: EtcdClient
    ttl: int
    doNotWatch: bool
    hasFailed: bool

# DNS Caching Resolver

proc newDnsCachingResolver*(cacheTime: float = 600.0, cacheFailTime: float = 30.0): DnsCachingResolver =
  ## Create a new DNS caching resolver.
  new(result)
  result.cache = initTable[tuple[host: string, port: int], tuple[time: float, addresses: seq[string]]]()
  result.cacheTime = cacheTime
  result.cacheFailTime = cacheFailTime
  initLock(result.lock)

proc doResolve(host: string, port: int): seq[string] =
  ## Perform DNS resolution.
  result = @[]
  try:
    # Simple resolution - return host as-is (could be IP or hostname)
    # In a production system, proper DNS resolution would be done here
    result.add(host)
  except OSError as e:
    logger.warning(fmt"Failed to resolve host {host}: {e.msg}")

proc resolve*(self: DnsCachingResolver, host: string, port: int): seq[string] =
  ## Resolve a host with caching.
  let currentTime = epochTime()
  withLock(self.lock):
    let key = (host, port)
    if key in self.cache:
      let (cachedTime, addresses) = self.cache[key]
      let timePassed = currentTime - cachedTime
      if timePassed <= self.cacheTime and addresses.len > 0:
        return addresses
      if timePassed <= self.cacheFailTime and addresses.len == 0:
        return @[]

    let newAddresses = doResolve(host, port)
    self.cache[key] = (currentTime, newAddresses)
    return newAddresses

proc resolveAsync*(self: DnsCachingResolver, host: string, port: int) =
  ## Queue async resolution (simplified - just does sync resolve).
  discard self.resolve(host, port)

proc remove*(self: DnsCachingResolver, host: string, port: int) =
  ## Remove an entry from the cache.
  withLock(self.lock):
    self.cache.del((host, port))

# Stale Etcd Node Guard

proc newStaleEtcdNodeGuard*(): StaleEtcdNodeGuard =
  ## Create a new stale node guard.
  new(result)
  result.clusterId = ""
  result.raftTerm = 0

proc resetClusterRaftTerm*(self: StaleEtcdNodeGuard) =
  ## Reset the cluster raft term.
  self.clusterId = ""
  self.raftTerm = 0

proc checkClusterRaftTerm*(self: StaleEtcdNodeGuard, clusterId: string, value: string) =
  ## Check that observed Raft Term in Etcd cluster is increasing.
  ##
  ## :param clusterId: last observed Etcd Cluster ID
  ## :param value: last observed Raft Term as string
  ##
  ## :raises: StaleEtcdNode if last observed raft_term is smaller than previously known.
  if clusterId.len == 0 or value.len == 0:
    return

  # We need to reset the memorized value when we notice that Cluster ID changed.
  if self.clusterId.len > 0 and self.clusterId != clusterId:
    logger.warning(fmt"Etcd Cluster ID changed from {self.clusterId} to {clusterId}")
    self.raftTerm = 0
  self.clusterId = clusterId

  var raftTerm: int64
  try:
    raftTerm = parseInt(value)
  except ValueError:
    return

  if raftTerm < self.raftTerm:
    logger.warning(fmt"Connected to Etcd node with term {raftTerm}. Old known term {self.raftTerm}. Switching to another node.")
    raise newException(StaleEtcdNode, "Stale etcd node detected")
  self.raftTerm = raftTerm

# Etcd Result helpers

proc newEtcdResult*(): EtcdResult =
  new(result)
  result.leaves = @[]

proc parseEtcdResult*(data: JsonNode): EtcdResult =
  ## Parse etcd API response into EtcdResult.
  result = newEtcdResult()

  if data.hasKey("action"):
    result.action = data["action"].getStr()

  let node = if data.hasKey("node"): data["node"] else: data

  if node.hasKey("key"):
    result.key = node["key"].getStr()
  if node.hasKey("value"):
    result.value = node["value"].getStr()
  if node.hasKey("ttl"):
    result.ttl = node["ttl"].getInt()
  if node.hasKey("modifiedIndex"):
    result.modifiedIndex = node["modifiedIndex"].getInt()
  if node.hasKey("createdIndex"):
    result.createdIndex = node["createdIndex"].getInt()
  if node.hasKey("dir"):
    result.dir = node["dir"].getBool()

  if node.hasKey("nodes") and node["nodes"].kind == JArray:
    for child in node["nodes"]:
      result.leaves.add(parseEtcdResult(child))
  else:
    result.leaves.add(result)

# Etcd Client

proc newEtcdClient*(config: Table[string, string], dnsResolver: DnsCachingResolver, cacheTtl: int = 300): EtcdClient =
  ## Create a new etcd client.
  new(result)
  result.dnsResolver = dnsResolver
  result.staleGuard = newStaleEtcdNodeGuard()
  result.machinesCacheTtl = cacheTtl
  result.machinesCacheUpdated = 0
  result.machinesCache = @[]
  result.updateMachinesCache = true
  result.allowRedirect = true
  result.useProxies = false
  result.versionPrefix = "/v2/keys"
  result.config = config
  result.httpClient = newHttpClient(timeout = 30000)

  if "protocol" in config:
    result.protocol = config["protocol"]
  else:
    result.protocol = "http"

  if "host" in config:
    let port = if "port" in config: config["port"] else: "2379"
    let host = config["host"]
    result.baseUri = fmt"{result.protocol}://{host}:{port}"
    result.machinesCache.add(result.baseUri)

  if "username" in config:
    result.username = config["username"]
  if "password" in config:
    result.password = config["password"]

  if "retry_timeout" in config:
    result.readTimeout = parseFloat(config["retry_timeout"])
  else:
    result.readTimeout = 10.0

proc setReadTimeout*(self: EtcdClient, timeout: float) =
  ## Set the read timeout.
  self.readTimeout = timeout
  self.httpClient.timeout = int(timeout * 1000)

proc setMachinesCacheTtl*(self: EtcdClient, ttl: int) =
  ## Set the machines cache TTL.
  self.machinesCacheTtl = ttl

proc setBaseUri*(self: EtcdClient, value: string) =
  ## Set the base URI for requests.
  if self.baseUri != value:
    logger.info(fmt"Selected new etcd server {value}")
    self.baseUri = value

proc reloadConfig*(self: EtcdClient, config: Table[string, string]) =
  ## Reload configuration.
  if "username" in config:
    self.username = config["username"]
  if "password" in config:
    self.password = config["password"]

proc doRequest(self: EtcdClient, httpMethod: HttpMethod, path: string,
               body: string = "", params: Table[string, string] = initTable[string, string]()): JsonNode =
  ## Execute an HTTP request to etcd.
  var url = self.baseUri & self.versionPrefix & path

  if params.len > 0:
    var queryParams: seq[string] = @[]
    for key, value in params:
      queryParams.add(encodeUrl(key) & "=" & encodeUrl(value))
    url = url & "?" & queryParams.join("&")

  var headers = newHttpHeaders()
  headers["Content-Type"] = "application/x-www-form-urlencoded"
  headers["User-Agent"] = USER_AGENT

  if self.username.len > 0 and self.password.len > 0:
    let auth = encode(self.username & ":" & self.password)
    headers["Authorization"] = "Basic " & auth

  try:
    let response = self.httpClient.request(url, httpMethod = httpMethod, body = body, headers = headers)

    # Check raft term
    if response.headers.hasKey("x-etcd-cluster-id") and response.headers.hasKey("x-raft-term"):
      self.staleGuard.checkClusterRaftTerm(
        response.headers["x-etcd-cluster-id"],
        response.headers["x-raft-term"]
      )

    if response.headers.hasKey("x-etcd-index"):
      discard  # Could store etcd index

    let bodyStr = response.body
    if bodyStr.len > 0:
      result = parseJson(bodyStr)
    else:
      result = newJObject()

    if response.code.int >= 400:
      if result.hasKey("errorCode"):
        let errorCode = result["errorCode"].getInt()
        case errorCode
        of 100:
          raise newException(EtcdKeyNotFound, result["message"].getStr("Key not found"))
        of 105:
          raise newException(EtcdAlreadyExist, result["message"].getStr("Key already exists"))
        else:
          raise newException(EtcdError, result["message"].getStr("Etcd error"))
      else:
        raise newException(EtcdError, fmt"HTTP {response.code}")
  except OSError as e:
    raise newException(EtcdConnectionFailed, fmt"Connection failed: {e.msg}")
  except TimeoutError as e:
    raise newException(EtcdConnectionFailed, fmt"Connection timeout: {e.msg}")

proc read*(self: EtcdClient, key: string, recursive: bool = false, quorum: bool = false): EtcdResult =
  ## Read a key from etcd.
  var params = initTable[string, string]()
  if recursive:
    params["recursive"] = "true"
  if quorum:
    params["quorum"] = "true"

  let response = self.doRequest(HttpGet, key, params = params)
  result = parseEtcdResult(response)
  if response.hasKey("etcd_index"):
    result.etcdIndex = response["etcd_index"].getInt()

proc write*(self: EtcdClient, key: string, value: string, ttl: int = 0,
            prevExist: Option[bool] = none(bool), prevValue: string = "",
            prevIndex: int64 = 0): EtcdResult =
  ## Write a key to etcd.
  var params: seq[string] = @[]
  params.add("value=" & encodeUrl(value))
  if ttl > 0:
    params.add("ttl=" & $ttl)
  if prevExist.isSome:
    params.add("prevExist=" & $prevExist.get())
  if prevValue.len > 0:
    params.add("prevValue=" & encodeUrl(prevValue))
  if prevIndex > 0:
    params.add("prevIndex=" & $prevIndex)

  let body = params.join("&")
  let response = self.doRequest(HttpPut, key, body = body)
  result = parseEtcdResult(response)

proc set*(self: EtcdClient, key: string, value: string, ttl: int = 0): EtcdResult =
  ## Set a key in etcd (alias for write without conditions).
  result = self.write(key, value, ttl)

proc delete*(self: EtcdClient, key: string, recursive: bool = false,
             prevValue: string = "", prevIndex: int64 = 0): EtcdResult =
  ## Delete a key from etcd.
  var params = initTable[string, string]()
  if recursive:
    params["recursive"] = "true"
  if prevValue.len > 0:
    params["prevValue"] = prevValue
  if prevIndex > 0:
    params["prevIndex"] = $prevIndex

  let response = self.doRequest(HttpDelete, key, params = params)
  result = parseEtcdResult(response)

proc watch*(self: EtcdClient, key: string, index: int64 = 0, timeout: float = 0): EtcdResult =
  ## Watch a key for changes.
  var params = initTable[string, string]()
  params["wait"] = "true"
  if index > 0:
    params["waitIndex"] = $index

  let oldTimeout = self.httpClient.timeout
  if timeout > 0:
    self.httpClient.timeout = int(timeout * 1000)

  try:
    let response = self.doRequest(HttpGet, key, params = params)
    result = parseEtcdResult(response)
  except TimeoutError:
    raise newException(EtcdWatchTimedOut, "Watch timed out")
  finally:
    self.httpClient.timeout = oldTimeout

# Etcd DCS Implementation

proc newEtcd*(config: JsonNode): Etcd =
  ## Create a new Etcd DCS instance.
  new(result)
  initAbstractDCS(result, config)

  var etcdConfig = initTable[string, string]()

  let etcdSection = if config.hasKey("etcd"): config["etcd"] else: newJObject()

  if etcdSection.hasKey("host"):
    etcdConfig["host"] = etcdSection["host"].getStr()
  if etcdSection.hasKey("port"):
    etcdConfig["port"] = $etcdSection["port"].getInt(2379)
  else:
    etcdConfig["port"] = "2379"
  if etcdSection.hasKey("protocol"):
    etcdConfig["protocol"] = etcdSection["protocol"].getStr()
  if etcdSection.hasKey("username"):
    etcdConfig["username"] = etcdSection["username"].getStr()
  if etcdSection.hasKey("password"):
    etcdConfig["password"] = etcdSection["password"].getStr()
  if config.hasKey("retry_timeout"):
    etcdConfig["retry_timeout"] = $config["retry_timeout"].getInt(10)

  let dnsResolver = newDnsCachingResolver()
  result.client = newEtcdClient(etcdConfig, dnsResolver)
  result.ttl = config["ttl"].getInt(30)
  result.doNotWatch = false
  result.hasFailed = false

method setTtl*(self: Etcd, ttl: int): bool =
  ## Set the TTL for keys.
  let changed = self.ttl != ttl
  self.ttl = ttl
  self.client.setMachinesCacheTtl(ttl * 10)
  if changed:
    self.doNotWatch = true
  result = changed

method getTtl*(self: Etcd): int =
  ## Get the current TTL.
  result = self.ttl

method setRetryTimeout*(self: Etcd, retryTimeout: int) =
  ## Set the retry timeout.
  self.client.setReadTimeout(float(retryTimeout))

proc memberFromNode(node: EtcdResult): Member =
  ## Create a Member from an etcd node.
  result = fromNode(node.modifiedIndex, os.extractFilename(node.key), $node.ttl, node.value)

proc clusterFromNodes(self: Etcd, etcdIndex: int64, nodes: Table[string, EtcdResult]): base.Cluster =
  ## Build a Cluster from etcd nodes.
  result = newCluster()

  # Get initialize flag
  if "_initialize" in nodes:
    result.initialize = nodes["_initialize"].value

  # Get global dynamic configuration
  if "_config" in nodes:
    let configNode = nodes["_config"]
    result.config = clusterConfigFromNode(configNode.modifiedIndex, configNode.value)

  # Get timeline history
  if "_history" in nodes:
    let historyNode = nodes["_history"]
    result.history = timelineHistoryFromNode(historyNode.modifiedIndex, historyNode.value)

  # Get status
  if "_status" in nodes:
    result.status = statusFromNode(nodes["_status"].value)
  elif "_leader_optime" in nodes:
    result.status = statusFromNode(nodes["_leader_optime"].value)

  # Get list of members
  for key, node in nodes:
    if key.startsWith("_members/") and key.count('/') == 1:
      result.members.add(memberFromNode(node))

  # Get leader
  if "_leader" in nodes:
    let leaderNode = nodes["_leader"]
    var remoteMember = newRemoteMember(leaderNode.value, newMemberData())
    for m in result.members:
      if m.name == leaderNode.value:
        remoteMember.version = m.version
        remoteMember.data = m.data
        break
    let version = if etcdIndex > leaderNode.modifiedIndex: etcdIndex else: leaderNode.modifiedIndex + 1
    result.leader = newLeader(version, $leaderNode.ttl, remoteMember)

  # Get failover key
  if "_failover" in nodes:
    let failoverNode = nodes["_failover"]
    result.failover = failoverFromNode(failoverNode.modifiedIndex, failoverNode.value)

  # Get synchronization state
  if "_sync" in nodes:
    let syncNode = nodes["_sync"]
    result.sync = syncStateFromNode(syncNode.modifiedIndex, syncNode.value)

  # Get failsafe topology
  if "_failsafe" in nodes:
    try:
      result.failsafe = parseJson(nodes["_failsafe"].value)
    except JsonParsingError:
      result.failsafe = nil

method loadCluster*(self: Etcd, path: string): base.Cluster =
  ## Load cluster from etcd.
  try:
    let etcdResult = self.client.read(path, recursive = true, quorum = self.isCtl)
    var nodes = initTable[string, EtcdResult]()
    for node in etcdResult.leaves:
      let relKey = node.key[etcdResult.key.len..^1].strip(chars = {'/'})
      nodes[relKey] = node
    result = self.clusterFromNodes(etcdResult.etcdIndex, nodes)
    self.hasFailed = false
  except EtcdKeyNotFound:
    result = emptyCluster()
  except EtcdError as e:
    if not self.hasFailed:
      logger.exception("get_cluster", e)
    self.hasFailed = true
    raise newException(EtcdError, "Etcd is not responding properly")

method touchMember*(self: Etcd, data: JsonNode): bool =
  ## Update member data in etcd.
  try:
    let value = $data
    discard self.client.set(self.memberPath, value, self.ttl)
    self.hasFailed = false
    result = true
  except EtcdError as e:
    if not self.hasFailed:
      logger.exception("touch_member", e)
    self.hasFailed = true
    result = false

method takeLeader*(self: Etcd): bool =
  ## Take the leader lock.
  try:
    discard self.client.write(self.leaderPath, self.name, ttl = self.ttl)
    self.hasFailed = false
    result = true
  except EtcdError as e:
    if not self.hasFailed:
      logger.exception("take_leader", e)
    self.hasFailed = true
    result = false

method attemptToAcquireLeader*(self: Etcd): bool =
  ## Attempt to acquire the leader lock.
  try:
    discard self.client.write(self.leaderPath, self.name, ttl = self.ttl, prevExist = some(false))
    self.hasFailed = false
    result = true
  except EtcdAlreadyExist:
    logger.info("Could not take out TTL lock")
    result = false
  except EtcdError as e:
    if not self.hasFailed:
      logger.exception("attempt_to_acquire_leader", e)
    self.hasFailed = true
    result = false

method setFailoverValue*(self: Etcd, value: string, version: int64 = 0): bool =
  ## Set failover value.
  try:
    discard self.client.write(self.failoverPath, value, prevIndex = version)
    result = true
  except EtcdError:
    result = false

method setConfigValue*(self: Etcd, value: string, version: int64 = 0): bool =
  ## Set config value.
  try:
    discard self.client.write(self.configPath, value, prevIndex = version)
    result = true
  except EtcdError:
    result = false

method writeLeaderOptime*(self: Etcd, lastLsn: string): bool =
  ## Write leader optime.
  try:
    discard self.client.set(self.leaderOptimePath, lastLsn)
    result = true
  except EtcdError:
    result = false

method writeStatus*(self: Etcd, value: string): bool =
  ## Write status.
  try:
    discard self.client.set(self.statusPath, value)
    result = true
  except EtcdError:
    result = false

method writeFailsafe*(self: Etcd, value: string): bool =
  ## Write failsafe value.
  try:
    discard self.client.set(self.failsafePath, value)
    result = true
  except EtcdError:
    result = false

method updateLeader*(self: Etcd, leader: Leader): bool =
  ## Update the leader lock.
  try:
    discard self.client.write(self.leaderPath, self.name, ttl = self.ttl, prevValue = self.name)
    self.hasFailed = false
    result = true
  except EtcdKeyNotFound:
    result = self.attemptToAcquireLeader()
  except EtcdError as e:
    if not self.hasFailed:
      logger.exception("update_leader", e)
    self.hasFailed = true
    result = false

method initialize*(self: Etcd, createNew: bool = true, sysid: string = ""): bool =
  ## Initialize the cluster.
  try:
    discard self.client.write(self.initializePath, sysid, prevExist = some(not createNew))
    result = true
  except EtcdError:
    result = false

method deleteLeader*(self: Etcd, leader: Leader): bool =
  ## Delete the leader lock.
  try:
    discard self.client.delete(self.leaderPath, prevValue = self.name)
    result = true
  except EtcdError:
    result = false

method cancelInitialization*(self: Etcd): bool =
  ## Cancel initialization.
  try:
    discard self.client.delete(self.initializePath)
    result = true
  except EtcdError:
    result = false

method deleteCluster*(self: Etcd): bool =
  ## Delete the entire cluster data.
  try:
    discard self.client.delete(self.clientPath(""), recursive = true)
    result = true
  except EtcdError:
    result = false

method setHistoryValue*(self: Etcd, value: string): bool =
  ## Set history value.
  try:
    discard self.client.write(self.historyPath, value)
    result = true
  except EtcdError:
    result = false

method setSyncStateValue*(self: Etcd, value: string, version: int64 = 0): int64 =
  ## Set sync state value.
  try:
    let res = self.client.write(self.syncPath, value, prevIndex = version)
    result = res.modifiedIndex
  except EtcdError:
    result = -1

method deleteSyncState*(self: Etcd, version: int64 = 0): bool =
  ## Delete sync state.
  try:
    discard self.client.delete(self.syncPath, prevIndex = version)
    result = true
  except EtcdError:
    result = false

method watch*(self: Etcd, leaderVersion: int64, timeout: float): bool =
  ## Watch for changes.
  if self.doNotWatch:
    self.doNotWatch = false
    return true

  if leaderVersion > 0:
    var endTime = epochTime() + timeout
    var remainingTimeout = timeout

    while remainingTimeout >= 1.0:
      try:
        let watchResult = self.client.watch(self.leaderPath, index = leaderVersion, timeout = remainingTimeout + 0.5)
        self.hasFailed = false
        if watchResult.action == "compareAndSwap":
          sleep(10)
        return true
      except EtcdWatchTimedOut:
        self.hasFailed = false
        return false
      except EtcdError as e:
        if not self.hasFailed:
          logger.error(fmt"watch: {e.msg}")
        self.hasFailed = true
        sleep(1000)

      remainingTimeout = endTime - epochTime()

  # Fall back to sleep-based wait
  sleep(int(timeout * 1000))
  result = false
