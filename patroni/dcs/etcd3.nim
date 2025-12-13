## Etcd v3 API DCS backend for Patroni.
##
## This module provides a distributed configuration store backend using etcd v3 gRPC API.

import std/[asyncdispatch, base64, httpclient, json, locks, net, options, os, random,
            sequtils, strformat, strutils, tables, times, uri]
import ../exceptions
import ../log
import ../request
import ../utils
import ../dcs as base
import ./etcd

export base, etcd

let logger = getLogger("patroni.dcs.etcd3")

type
  Etcd3Error* = object of DCSError
    ## General etcd3 error.

  Etcd3ConnectionFailed* = object of Etcd3Error
    ## Connection to etcd3 failed.

  Etcd3KeyNotFound* = object of Etcd3Error
    ## Key not found in etcd3.

  Etcd3TransactionFailed* = object of Etcd3Error
    ## Transaction failed.

  Etcd3LeaseNotFound* = object of Etcd3Error
    ## Lease not found.

  Etcd3LeaseExpired* = object of Etcd3Error
    ## Lease expired.

  Etcd3WatchFailed* = object of Etcd3Error
    ## Watch operation failed.

  Etcd3Lease* = ref object
    ## Etcd3 lease object.
    id*: int64
    ttl*: int
    grantedTtl*: int

  Etcd3KeyValue* = ref object
    ## Etcd3 key-value pair.
    key*: string
    value*: string
    createRevision*: int64
    modRevision*: int64
    version*: int64
    lease*: int64

  Etcd3Response* = ref object
    ## Response from etcd3 API.
    header*: JsonNode
    kvs*: seq[Etcd3KeyValue]
    count*: int64
    revision*: int64
    succeeded*: bool

  Etcd3Client* = ref object
    ## Client for etcd v3 API.
    baseUri: string
    httpClient: HttpClient
    readTimeout: float
    username: string
    password: string
    token: string
    tokenExpiry: float
    lease: Option[Etcd3Lease]
    dnsResolver: DnsCachingResolver
    staleGuard: StaleEtcdNodeGuard
    machinesCache: seq[string]
    machinesCacheTtl: int
    machinesCacheUpdated: float

  Etcd3* = ref object of AbstractDCS
    ## Etcd3 DCS implementation.
    client: Etcd3Client
    ttl: int
    leaseId: int64
    doNotWatch: bool
    hasFailed: bool
    clusterRevision: int64

# Etcd3Client implementation

proc newEtcd3Client*(hosts: seq[string], protocol: string = "http",
                     username: string = "", password: string = ""): Etcd3Client =
  ## Create a new etcd3 client.
  new(result)
  result.httpClient = newHttpClient(timeout = 30000)
  result.readTimeout = 10.0
  result.username = username
  result.password = password
  result.token = ""
  result.tokenExpiry = 0
  result.lease = none(Etcd3Lease)
  result.dnsResolver = newDnsCachingResolver()
  result.staleGuard = newStaleEtcdNodeGuard()
  result.machinesCache = @[]
  result.machinesCacheTtl = 300
  result.machinesCacheUpdated = 0

  if hosts.len > 0:
    result.baseUri = fmt"{protocol}://{hosts[0]}"
    for host in hosts:
      result.machinesCache.add(fmt"{protocol}://{host}")

proc setReadTimeout*(self: Etcd3Client, timeout: float) =
  ## Set the read timeout.
  self.readTimeout = timeout
  self.httpClient.timeout = int(timeout * 1000)

proc authenticate(self: Etcd3Client): bool =
  ## Authenticate with etcd3 using username/password.
  if self.username.len == 0:
    return true

  if self.token.len > 0 and epochTime() < self.tokenExpiry:
    return true

  try:
    let body = %*{
      "name": self.username,
      "password": self.password
    }

    var headers = newHttpHeaders()
    headers["Content-Type"] = "application/json"

    let response = self.httpClient.request(
      self.baseUri & "/v3/auth/authenticate",
      httpMethod = HttpPost,
      body = $body,
      headers = headers
    )

    if response.code.int == 200:
      let jsonResp = parseJson(response.body)
      if jsonResp.hasKey("token"):
        self.token = jsonResp["token"].getStr()
        self.tokenExpiry = epochTime() + 3600  # Token valid for 1 hour
        return true
  except CatchableError as e:
    logger.error(fmt"Authentication failed: {e.msg}")

  result = false

proc doRequest(self: Etcd3Client, path: string, body: JsonNode): JsonNode =
  ## Execute an HTTP request to etcd3 API.
  discard self.authenticate()

  var headers = newHttpHeaders()
  headers["Content-Type"] = "application/json"
  if self.token.len > 0:
    headers["Authorization"] = self.token

  try:
    let response = self.httpClient.request(
      self.baseUri & path,
      httpMethod = HttpPost,
      body = $body,
      headers = headers
    )

    if response.code.int >= 400:
      let errBody = if response.body.len > 0: response.body else: $response.code
      raise newException(Etcd3Error, errBody)

    if response.body.len > 0:
      result = parseJson(response.body)
    else:
      result = newJObject()
  except OSError as e:
    raise newException(Etcd3ConnectionFailed, fmt"Connection failed: {e.msg}")
  except TimeoutError as e:
    raise newException(Etcd3ConnectionFailed, fmt"Connection timeout: {e.msg}")

proc parseKV(kv: JsonNode): Etcd3KeyValue =
  ## Parse a key-value from JSON.
  new(result)
  if kv.hasKey("key"):
    result.key = decode(kv["key"].getStr())
  if kv.hasKey("value"):
    result.value = decode(kv["value"].getStr())
  result.createRevision = kv.getOrDefault("create_revision").getInt(0)
  result.modRevision = kv.getOrDefault("mod_revision").getInt(0)
  result.version = kv.getOrDefault("version").getInt(0)
  result.lease = kv.getOrDefault("lease").getInt(0)

proc parseResponse(resp: JsonNode): Etcd3Response =
  ## Parse an etcd3 response.
  new(result)
  result.kvs = @[]

  if resp.hasKey("header"):
    result.header = resp["header"]
    if result.header.hasKey("revision"):
      result.revision = result.header["revision"].getInt(0)

  if resp.hasKey("kvs") and resp["kvs"].kind == JArray:
    for kv in resp["kvs"]:
      result.kvs.add(parseKV(kv))

  result.count = resp.getOrDefault("count").getInt(0)
  result.succeeded = resp.getOrDefault("succeeded").getBool(true)

proc rangeRequest*(self: Etcd3Client, key: string, rangeEnd: string = "",
                   keysOnly: bool = false): Etcd3Response =
  ## Get a range of keys.
  var body = %*{
    "key": encode(key)
  }

  if rangeEnd.len > 0:
    body["range_end"] = newJString(encode(rangeEnd))
  if keysOnly:
    body["keys_only"] = newJBool(true)

  let resp = self.doRequest("/v3/kv/range", body)
  result = parseResponse(resp)

proc get*(self: Etcd3Client, key: string): Option[Etcd3KeyValue] =
  ## Get a single key.
  let resp = self.rangeRequest(key)
  if resp.kvs.len > 0:
    result = some(resp.kvs[0])
  else:
    result = none(Etcd3KeyValue)

proc getPrefix*(self: Etcd3Client, prefix: string): seq[Etcd3KeyValue] =
  ## Get all keys with a prefix.
  # Range end is the prefix with the last byte incremented
  var rangeEnd = prefix
  if rangeEnd.len > 0:
    let lastByte = ord(rangeEnd[^1])
    rangeEnd[^1] = chr(lastByte + 1)

  let resp = self.rangeRequest(prefix, rangeEnd)
  result = resp.kvs

proc put*(self: Etcd3Client, key: string, value: string, leaseId: int64 = 0): Etcd3Response =
  ## Put a key-value pair.
  var body = %*{
    "key": encode(key),
    "value": encode(value)
  }

  if leaseId != 0:
    body["lease"] = newJInt(leaseId)

  let resp = self.doRequest("/v3/kv/put", body)
  result = parseResponse(resp)

proc delete*(self: Etcd3Client, key: string, rangeEnd: string = ""): Etcd3Response =
  ## Delete a key or range of keys.
  var body = %*{
    "key": encode(key)
  }

  if rangeEnd.len > 0:
    body["range_end"] = newJString(encode(rangeEnd))

  let resp = self.doRequest("/v3/kv/deleterange", body)
  result = parseResponse(resp)

proc leaseGrant*(self: Etcd3Client, ttl: int): Etcd3Lease =
  ## Grant a new lease.
  let body = %*{
    "TTL": ttl
  }

  let resp = self.doRequest("/v3/lease/grant", body)

  new(result)
  result.id = resp.getOrDefault("ID").getInt(0)
  result.ttl = resp.getOrDefault("TTL").getInt(0)
  result.grantedTtl = result.ttl
  self.lease = some(result)

proc leaseKeepAlive*(self: Etcd3Client, leaseId: int64): bool =
  ## Keep a lease alive.
  let body = %*{
    "ID": leaseId
  }

  try:
    let resp = self.doRequest("/v3/lease/keepalive", body)
    if resp.hasKey("result"):
      let res = resp["result"]
      return res.getOrDefault("TTL").getInt(0) > 0
    result = false
  except Etcd3Error:
    result = false

proc leaseRevoke*(self: Etcd3Client, leaseId: int64): bool =
  ## Revoke a lease.
  let body = %*{
    "ID": leaseId
  }

  try:
    discard self.doRequest("/v3/lease/revoke", body)
    result = true
  except Etcd3Error:
    result = false

proc transaction*(self: Etcd3Client, compare: seq[JsonNode], success: seq[JsonNode],
                  failure: seq[JsonNode] = @[]): Etcd3Response =
  ## Execute a transaction.
  var body = %*{
    "compare": compare,
    "success": success
  }

  if failure.len > 0:
    body["failure"] = %failure

  let resp = self.doRequest("/v3/kv/txn", body)
  result = parseResponse(resp)

# Etcd3 DCS Implementation

proc newEtcd3*(config: JsonNode): Etcd3 =
  ## Create a new Etcd3 DCS instance.
  new(result)
  initAbstractDCS(result, config)

  var hosts: seq[string] = @[]
  var protocol = "http"
  var username = ""
  var password = ""

  let etcdSection = if config.hasKey("etcd3"): config["etcd3"]
                    elif config.hasKey("etcd"): config["etcd"]
                    else: newJObject()

  if etcdSection.hasKey("hosts"):
    if etcdSection["hosts"].kind == JArray:
      for h in etcdSection["hosts"]:
        hosts.add(h.getStr())
    else:
      hosts.add(etcdSection["hosts"].getStr())
  elif etcdSection.hasKey("host"):
    let host = etcdSection["host"].getStr()
    let port = etcdSection.getOrDefault("port").getInt(2379)
    hosts.add(fmt"{host}:{port}")

  if hosts.len == 0:
    hosts.add("127.0.0.1:2379")

  if etcdSection.hasKey("protocol"):
    protocol = etcdSection["protocol"].getStr()
  if etcdSection.hasKey("username"):
    username = etcdSection["username"].getStr()
  if etcdSection.hasKey("password"):
    password = etcdSection["password"].getStr()

  result.client = newEtcd3Client(hosts, protocol, username, password)
  result.ttl = config["ttl"].getInt(30)
  result.leaseId = 0
  result.doNotWatch = false
  result.hasFailed = false
  result.clusterRevision = 0

proc ensureLease(self: Etcd3): bool =
  ## Ensure we have a valid lease.
  if self.leaseId != 0:
    if self.client.leaseKeepAlive(self.leaseId):
      return true
    self.leaseId = 0

  try:
    let lease = self.client.leaseGrant(self.ttl)
    self.leaseId = lease.id
    result = true
  except Etcd3Error as e:
    logger.error(fmt"Failed to grant lease: {e.msg}")
    result = false

method setTtl*(self: Etcd3, ttl: int): bool =
  ## Set the TTL for keys.
  let changed = self.ttl != ttl
  self.ttl = ttl
  if changed:
    self.doNotWatch = true
    # Force lease renewal with new TTL
    if self.leaseId != 0:
      discard self.client.leaseRevoke(self.leaseId)
      self.leaseId = 0
  result = changed

method getTtl*(self: Etcd3): int =
  ## Get the current TTL.
  result = self.ttl

method setRetryTimeout*(self: Etcd3, retryTimeout: int) =
  ## Set the retry timeout.
  self.client.setReadTimeout(float(retryTimeout))

proc memberFromKV(kv: Etcd3KeyValue): Member =
  ## Create a Member from an etcd3 key-value.
  let name = kv.key.rsplit('/', 1)[^1]
  result = fromNode(kv.modRevision, name, "", kv.value)

proc clusterFromKVs(self: Etcd3, kvs: seq[Etcd3KeyValue], revision: int64): base.Cluster =
  ## Build a Cluster from etcd3 key-values.
  result = newCluster()
  self.clusterRevision = revision

  var kvMap = initTable[string, Etcd3KeyValue]()
  let basePath = self.clientPath("")

  for kv in kvs:
    if kv.key.startsWith(basePath):
      let relKey = kv.key[basePath.len..^1].strip(chars = {'/'})
      kvMap[relKey] = kv

  # Get initialize flag
  if "initialize" in kvMap:
    result.initialize = kvMap["initialize"].value

  # Get global dynamic configuration
  if "config" in kvMap:
    let configKv = kvMap["config"]
    result.config = clusterConfigFromNode(configKv.modRevision, configKv.value)

  # Get timeline history
  if "history" in kvMap:
    let historyKv = kvMap["history"]
    result.history = timelineHistoryFromNode(historyKv.modRevision, historyKv.value)

  # Get status
  if "status" in kvMap:
    result.status = statusFromNode(kvMap["status"].value)
  elif "optime/leader" in kvMap:
    result.status = statusFromNode(kvMap["optime/leader"].value)

  # Get list of members
  for key, kv in kvMap:
    if key.startsWith("members/") and key.count('/') == 1:
      result.members.add(memberFromKV(kv))

  # Get leader
  if "leader" in kvMap:
    let leaderKv = kvMap["leader"]
    var leaderMember = newRemoteMember(leaderKv.value, newMemberData())
    for m in result.members:
      if m.name == leaderKv.value:
        leaderMember = newRemoteMember(m.name, m.data)
        break
    result.leader = newLeader(leaderKv.modRevision, "", leaderMember)

  # Get failover key
  if "failover" in kvMap:
    let failoverKv = kvMap["failover"]
    result.failover = failoverFromNode(failoverKv.modRevision, failoverKv.value)

  # Get synchronization state
  if "sync" in kvMap:
    let syncKv = kvMap["sync"]
    result.sync = syncStateFromNode(syncKv.modRevision, syncKv.value)

method loadCluster*(self: Etcd3, path: string): base.Cluster =
  ## Load cluster from etcd3.
  try:
    let kvs = self.client.getPrefix(self.clientPath(""))
    result = self.clusterFromKVs(kvs, 0)
    self.hasFailed = false
  except Etcd3Error as e:
    if not self.hasFailed:
      logger.exception("get_cluster", e)
    self.hasFailed = true
    raise newException(Etcd3Error, "Etcd3 is not responding properly")

method touchMember*(self: Etcd3, data: JsonNode): bool =
  ## Update member data in etcd3.
  if not self.ensureLease():
    return false

  try:
    let path = self.clientPath("members/" & self.name)
    discard self.client.put(path, $data, self.leaseId)
    self.hasFailed = false
    result = true
  except Etcd3Error as e:
    if not self.hasFailed:
      logger.exception("touch_member", e)
    self.hasFailed = true
    result = false

method takeLeader*(self: Etcd3): bool =
  ## Take the leader lock.
  if not self.ensureLease():
    return false

  try:
    let path = self.clientPath("leader")
    discard self.client.put(path, self.name, self.leaseId)
    self.hasFailed = false
    result = true
  except Etcd3Error as e:
    if not self.hasFailed:
      logger.exception("take_leader", e)
    self.hasFailed = true
    result = false

method attemptToAcquireLeader*(self: Etcd3): bool =
  ## Attempt to acquire the leader lock.
  if not self.ensureLease():
    return false

  try:
    let path = self.clientPath("leader")

    # Use transaction to create only if not exists
    let compare = @[%*{
      "key": encode(path),
      "target": "CREATE",
      "create_revision": 0
    }]
    let success = @[%*{
      "request_put": {
        "key": encode(path),
        "value": encode(self.name),
        "lease": self.leaseId
      }
    }]

    let resp = self.client.transaction(compare, success)
    result = resp.succeeded
  except Etcd3Error:
    result = false

method setFailoverValue*(self: Etcd3, value: string, version: int64 = 0): bool =
  ## Set failover value.
  try:
    discard self.client.put(self.clientPath("failover"), value)
    result = true
  except Etcd3Error:
    result = false

method setConfigValue*(self: Etcd3, value: string, version: int64 = 0): bool =
  ## Set config value.
  try:
    discard self.client.put(self.clientPath("config"), value)
    result = true
  except Etcd3Error:
    result = false

method writeLeaderOptime*(self: Etcd3, lastLsn: string): bool =
  ## Write leader optime.
  try:
    discard self.client.put(self.clientPath("optime/leader"), lastLsn)
    result = true
  except Etcd3Error:
    result = false

method writeStatus*(self: Etcd3, value: string): bool =
  ## Write status.
  try:
    discard self.client.put(self.clientPath("status"), value)
    result = true
  except Etcd3Error:
    result = false

method writeFailsafe*(self: Etcd3, value: string): bool =
  ## Write failsafe value.
  try:
    discard self.client.put(self.clientPath("failsafe"), value)
    result = true
  except Etcd3Error:
    result = false

method updateLeader*(self: Etcd3, leader: Leader): bool =
  ## Update the leader lock.
  if not self.ensureLease():
    return self.attemptToAcquireLeader()

  try:
    let path = self.clientPath("leader")
    discard self.client.put(path, self.name, self.leaseId)
    result = true
  except Etcd3Error:
    result = self.attemptToAcquireLeader()

method initialize*(self: Etcd3, createNew: bool = true, sysid: string = ""): bool =
  ## Initialize the cluster.
  try:
    let path = self.clientPath("initialize")
    if createNew:
      # Use transaction to create only if not exists
      let compare = @[%*{
        "key": encode(path),
        "target": "CREATE",
        "create_revision": 0
      }]
      let success = @[%*{
        "request_put": {
          "key": encode(path),
          "value": encode(sysid)
        }
      }]
      let resp = self.client.transaction(compare, success)
      result = resp.succeeded
    else:
      let kv = self.client.get(path)
      result = kv.isSome
  except Etcd3Error:
    result = false

method deleteLeader*(self: Etcd3, leader: Leader): bool =
  ## Delete the leader lock.
  try:
    discard self.client.delete(self.clientPath("leader"))
    result = true
  except Etcd3Error:
    result = false

method cancelInitialization*(self: Etcd3): bool =
  ## Cancel initialization.
  try:
    discard self.client.delete(self.clientPath("initialize"))
    result = true
  except Etcd3Error:
    result = false

method deleteCluster*(self: Etcd3): bool =
  ## Delete the entire cluster data.
  try:
    let prefix = self.clientPath("")
    var rangeEnd = prefix
    if rangeEnd.len > 0:
      rangeEnd[^1] = chr(ord(rangeEnd[^1]) + 1)
    discard self.client.delete(prefix, rangeEnd)
    result = true
  except Etcd3Error:
    result = false

method setHistoryValue*(self: Etcd3, value: string): bool =
  ## Set history value.
  try:
    discard self.client.put(self.clientPath("history"), value)
    result = true
  except Etcd3Error:
    result = false

method setSyncStateValue*(self: Etcd3, value: string, version: int64 = 0): int64 =
  ## Set sync state value.
  try:
    let resp = self.client.put(self.clientPath("sync"), value)
    result = resp.revision
  except Etcd3Error:
    result = -1

method deleteSyncState*(self: Etcd3, version: int64 = 0): bool =
  ## Delete sync state.
  try:
    discard self.client.delete(self.clientPath("sync"))
    result = true
  except Etcd3Error:
    result = false

method watch*(self: Etcd3, leaderVersion: int64, timeout: float): bool =
  ## Watch for changes.
  if self.doNotWatch:
    self.doNotWatch = false
    return true

  # Etcd3 watches would use streaming - simplified to polling
  sleep(int(timeout * 1000))
  result = false
