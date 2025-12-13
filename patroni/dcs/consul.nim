## Consul DCS backend for Patroni.
##
## This module provides a distributed configuration store backend using Consul.

import std/[base64, httpclient, json, locks, net, options, os, random, re, sequtils,
            strformat, strutils, tables, times, uri]
import ../exceptions
import ../log
import ../request
import ../utils
import ../dcs as base

export base

let logger = getLogger("patroni.dcs.consul")

type
  ConsulError* = object of DCSError
    ## General Consul error.

  ConsulInternalError* = object of ConsulError
    ## An internal Consul server error occurred.

  InvalidSessionTTL* = object of ConsulError
    ## Session TTL is too small or too big.

  InvalidSession* = object of ConsulError
    ## Invalid session.

  ConsulKeyNotFound* = object of ConsulError
    ## Key not found in Consul.

  ConsulResponse* = object
    ## Response from Consul API.
    code*: int
    headers*: HttpHeaders
    body*: string
    content*: string

  ConsulHTTPClient* = ref object
    ## HTTP client for Consul API.
    token: string
    readTimeout: float
    baseUri: string
    httpClient: HttpClient
    ttl: int
    verify: bool
    cert: string
    caCert: string

  ConsulKV* = ref object
    ## Consul Key/Value client.
    client: ConsulHTTPClient

  ConsulSession* = ref object
    ## Consul session client.
    client: ConsulHTTPClient

  Consul* = ref object of AbstractDCS
    ## Consul DCS implementation.
    client: ConsulHTTPClient
    kv: ConsulKV
    session: ConsulSession
    sessionId: string
    ttl: int
    doNotWatch: bool
    hasFailed: bool
    index: int64

# HTTP Client implementation

proc newConsulHTTPClient*(host: string = "127.0.0.1", port: int = 8500,
                          token: string = "", scheme: string = "http",
                          verify: bool = true, cert: string = "",
                          caCert: string = ""): ConsulHTTPClient =
  ## Create a new Consul HTTP client.
  new(result)
  result.token = token
  result.readTimeout = 10.0
  result.baseUri = fmt"{scheme}://{host}:{port}"
  result.httpClient = newHttpClient(timeout = 30000)
  result.ttl = 30
  result.verify = verify
  result.cert = cert
  result.caCert = caCert

proc setReadTimeout*(self: ConsulHTTPClient, timeout: float) =
  ## Set the read timeout.
  self.readTimeout = timeout / 3.0
  self.httpClient.timeout = int(self.readTimeout * 1000)

proc getTtl*(self: ConsulHTTPClient): int =
  ## Get the TTL.
  result = self.ttl

proc setTtl*(self: ConsulHTTPClient, ttl: int): bool =
  ## Set the TTL.
  result = self.ttl != ttl
  self.ttl = ttl

proc doRequest(self: ConsulHTTPClient, httpMethod: HttpMethod, path: string,
               body: string = "", params: Table[string, string] = initTable[string, string](),
               timeout: float = 0): ConsulResponse =
  ## Execute an HTTP request to Consul.
  var url = self.baseUri & path

  if params.len > 0:
    var queryParams: seq[string] = @[]
    for key, value in params:
      queryParams.add(encodeUrl(key) & "=" & encodeUrl(value))
    url = url & "?" & queryParams.join("&")

  var headers = newHttpHeaders()
  headers["Content-Type"] = "application/json"
  headers["User-Agent"] = USER_AGENT

  if self.token.len > 0:
    headers["X-Consul-Token"] = self.token

  let reqTimeout = if timeout > 0: int(timeout * 1000) else: self.httpClient.timeout
  let oldTimeout = self.httpClient.timeout
  self.httpClient.timeout = reqTimeout

  try:
    let response = self.httpClient.request(url, httpMethod = httpMethod, body = body, headers = headers)
    let bodyStr = response.body

    result.code = response.code.int
    result.headers = response.headers
    result.body = bodyStr
    result.content = bodyStr

    if response.code.int == 500:
      if bodyStr.startsWith("Invalid Session TTL"):
        raise newException(InvalidSessionTTL, fmt"500 {bodyStr}")
      elif bodyStr.startsWith("invalid session"):
        raise newException(InvalidSession, fmt"500 {bodyStr}")
      else:
        raise newException(ConsulInternalError, fmt"500 {bodyStr}")
  except OSError as e:
    raise newException(ConsulError, fmt"Connection failed: {e.msg}")
  except TimeoutError as e:
    raise newException(ConsulError, fmt"Connection timeout: {e.msg}")
  finally:
    self.httpClient.timeout = oldTimeout

proc get(self: ConsulHTTPClient, path: string, params: Table[string, string] = initTable[string, string](),
         timeout: float = 0): ConsulResponse =
  ## Execute a GET request.
  result = self.doRequest(HttpGet, path, params = params, timeout = timeout)

proc put(self: ConsulHTTPClient, path: string, body: string = "",
         params: Table[string, string] = initTable[string, string]()): ConsulResponse =
  ## Execute a PUT request.
  result = self.doRequest(HttpPut, path, body = body, params = params)

proc delete(self: ConsulHTTPClient, path: string,
            params: Table[string, string] = initTable[string, string]()): ConsulResponse =
  ## Execute a DELETE request.
  result = self.doRequest(HttpDelete, path, params = params)

# KV Store implementation

proc newConsulKV*(client: ConsulHTTPClient): ConsulKV =
  ## Create a new KV client.
  new(result)
  result.client = client

proc get*(self: ConsulKV, key: string, index: int64 = 0, wait: string = "",
          recurse: bool = false): tuple[index: int64, data: seq[JsonNode]] =
  ## Get a value from the KV store.
  var params = initTable[string, string]()
  if index > 0:
    params["index"] = $index
  if wait.len > 0:
    params["wait"] = wait
  if recurse:
    params["recurse"] = "1"

  let timeout = if wait.len > 0: 120.0 else: 0.0

  try:
    let response = self.client.get("/v1/kv/" & key, params, timeout)

    if response.headers.hasKey("X-Consul-Index"):
      result.index = parseInt(response.headers["X-Consul-Index"])

    if response.code == 200 and response.body.len > 0:
      let jsonData = parseJson(response.body)
      if jsonData.kind == JArray:
        for item in jsonData:
          result.data.add(item)
    elif response.code == 404:
      result.data = @[]
  except ConsulError:
    raise
  except CatchableError as e:
    raise newException(ConsulError, fmt"KV get failed: {e.msg}")

proc put*(self: ConsulKV, key: string, value: string, cas: int64 = 0,
          acquire: string = "", release: string = ""): bool =
  ## Put a value into the KV store.
  var params = initTable[string, string]()
  if cas > 0:
    params["cas"] = $cas
  if acquire.len > 0:
    params["acquire"] = acquire
  if release.len > 0:
    params["release"] = release

  try:
    let response = self.client.put("/v1/kv/" & key, value, params)
    result = response.body.strip().toLowerAscii() == "true"
  except ConsulError:
    raise
  except CatchableError as e:
    raise newException(ConsulError, fmt"KV put failed: {e.msg}")

proc delete*(self: ConsulKV, key: string, cas: int64 = 0, recurse: bool = false): bool =
  ## Delete a key from the KV store.
  var params = initTable[string, string]()
  if cas > 0:
    params["cas"] = $cas
  if recurse:
    params["recurse"] = "1"

  try:
    let response = self.client.delete("/v1/kv/" & key, params)
    result = response.code == 200
  except ConsulError:
    raise
  except CatchableError as e:
    raise newException(ConsulError, fmt"KV delete failed: {e.msg}")

# Session implementation

proc newConsulSession*(client: ConsulHTTPClient): ConsulSession =
  ## Create a new session client.
  new(result)
  result.client = client

proc create*(self: ConsulSession, name: string = "", ttl: int = 0,
             lockDelay: int = 0): string =
  ## Create a new session.
  var body = newJObject()
  if name.len > 0:
    body["Name"] = newJString(name)
  if ttl > 0:
    body["TTL"] = newJString(fmt"{ttl}s")
  if lockDelay >= 0:
    body["LockDelay"] = newJString(fmt"{lockDelay}s")
  body["Behavior"] = newJString("delete")

  try:
    let response = self.client.put("/v1/session/create", $body)
    if response.code == 200:
      let data = parseJson(response.body)
      if data.hasKey("ID"):
        result = data["ID"].getStr()
  except ConsulError:
    raise
  except CatchableError as e:
    raise newException(ConsulError, fmt"Session create failed: {e.msg}")

proc renew*(self: ConsulSession, sessionId: string): bool =
  ## Renew a session.
  try:
    let response = self.client.put("/v1/session/renew/" & sessionId)
    result = response.code == 200
  except ConsulError:
    result = false
  except CatchableError:
    result = false

proc destroy*(self: ConsulSession, sessionId: string): bool =
  ## Destroy a session.
  try:
    let response = self.client.put("/v1/session/destroy/" & sessionId)
    result = response.code == 200
  except ConsulError:
    result = false
  except CatchableError:
    result = false

# Consul DCS Implementation

proc newConsul*(config: JsonNode): Consul =
  ## Create a new Consul DCS instance.
  new(result)
  initAbstractDCS(result, config)

  var host = "127.0.0.1"
  var port = 8500
  var scheme = "http"
  var token = ""
  var verify = true
  var cert = ""
  var caCert = ""

  let consulSection = if config.hasKey("consul"): config["consul"] else: newJObject()

  if consulSection.hasKey("host"):
    host = consulSection["host"].getStr()
  if consulSection.hasKey("port"):
    port = consulSection["port"].getInt(8500)
  if consulSection.hasKey("scheme"):
    scheme = consulSection["scheme"].getStr()
  if consulSection.hasKey("token"):
    token = consulSection["token"].getStr()
  if consulSection.hasKey("verify"):
    verify = consulSection["verify"].getBool(true)
  if consulSection.hasKey("cert"):
    cert = consulSection["cert"].getStr()
  if consulSection.hasKey("cacert"):
    caCert = consulSection["cacert"].getStr()

  result.client = newConsulHTTPClient(host, port, token, scheme, verify, cert, caCert)
  result.kv = newConsulKV(result.client)
  result.session = newConsulSession(result.client)
  result.sessionId = ""
  result.ttl = config["ttl"].getInt(30)
  result.doNotWatch = false
  result.hasFailed = false
  result.index = 0

proc createSession(self: Consul): bool =
  ## Create a new Consul session.
  try:
    self.sessionId = self.session.create(name = self.name, ttl = self.ttl)
    result = self.sessionId.len > 0
  except ConsulError as e:
    logger.exception("Failed to create session", e)
    result = false

proc renewSession(self: Consul): bool =
  ## Renew the current session.
  if self.sessionId.len == 0:
    return self.createSession()
  result = self.session.renew(self.sessionId)
  if not result:
    self.sessionId = ""
    result = self.createSession()

method setTtl*(self: Consul, ttl: int): bool =
  ## Set the TTL for keys.
  let changed = self.client.setTtl(ttl)
  if changed:
    self.ttl = ttl
    self.doNotWatch = true
  result = changed

method getTtl*(self: Consul): int =
  ## Get the current TTL.
  result = self.ttl

method setRetryTimeout*(self: Consul, retryTimeout: int) =
  ## Set the retry timeout.
  self.client.setReadTimeout(float(retryTimeout))

proc kvGet(self: Consul, key: string, index: int64 = 0, wait: string = "",
           recurse: bool = false): tuple[index: int64, data: seq[JsonNode]] =
  ## Get from KV store with consul path.
  result = self.kv.get(self.clientPath(key), index, wait, recurse)

proc kvPut(self: Consul, key: string, value: string, cas: int64 = 0): bool =
  ## Put to KV store with consul path.
  if self.sessionId.len == 0:
    discard self.createSession()
  result = self.kv.put(self.clientPath(key), value, cas, acquire = self.sessionId)

proc kvDelete(self: Consul, key: string, cas: int64 = 0, recurse: bool = false): bool =
  ## Delete from KV store with consul path.
  result = self.kv.delete(self.clientPath(key), cas, recurse)

proc decodeValue(node: JsonNode): string =
  ## Decode base64 value from Consul KV.
  if node.hasKey("Value") and node["Value"].kind == JString:
    try:
      result = decode(node["Value"].getStr())
    except CatchableError:
      result = ""
  else:
    result = ""

proc memberFromNode(node: JsonNode): Member =
  ## Create a Member from a Consul KV node.
  let key = node["Key"].getStr("")
  let name = key.rsplit('/', 1)[^1]
  let value = decodeValue(node)
  let modifyIndex = node["ModifyIndex"].getInt(0)
  let session = if node.hasKey("Session"): node["Session"].getStr() else: ""
  result = fromNode(modifyIndex, name, session, value)

proc clusterFromKv(self: Consul, index: int64, nodes: Table[string, JsonNode]): base.Cluster =
  ## Build a Cluster from Consul KV nodes.
  result = newCluster()

  # Get initialize flag
  if "initialize" in nodes:
    result.initialize = decodeValue(nodes["initialize"])

  # Get global dynamic configuration
  if "config" in nodes:
    let configNode = nodes["config"]
    result.config = clusterConfigFromNode(configNode["ModifyIndex"].getInt(0), decodeValue(configNode))

  # Get timeline history
  if "history" in nodes:
    let historyNode = nodes["history"]
    result.history = timelineHistoryFromNode(historyNode["ModifyIndex"].getInt(0), decodeValue(historyNode))

  # Get status
  if "status" in nodes:
    result.status = statusFromNode(decodeValue(nodes["status"]))
  elif "optime/leader" in nodes:
    result.status = statusFromNode(decodeValue(nodes["optime/leader"]))

  # Get list of members
  for key, node in nodes:
    if key.startsWith("members/") and key.count('/') == 1:
      result.members.add(memberFromNode(node))

  # Get leader
  if "leader" in nodes:
    let leaderNode = nodes["leader"]
    let leaderName = decodeValue(leaderNode)
    let session = if leaderNode.hasKey("Session"): leaderNode["Session"].getStr() else: ""
    var leaderMember = newRemoteMember(leaderName, newMemberData())
    for m in result.members:
      if m.name == leaderName:
        leaderMember = newRemoteMember(m.name, m.data)
        break
    result.leader = newLeader(leaderNode["ModifyIndex"].getInt(0), session, leaderMember)

  # Get failover key
  if "failover" in nodes:
    let failoverNode = nodes["failover"]
    result.failover = failoverFromNode(failoverNode["ModifyIndex"].getInt(0), decodeValue(failoverNode))

  # Get synchronization state
  if "sync" in nodes:
    let syncNode = nodes["sync"]
    result.sync = syncStateFromNode(syncNode["ModifyIndex"].getInt(0), decodeValue(syncNode))

  # Get failsafe topology
  if "failsafe" in nodes:
    try:
      let failsafeData = parseJson(decodeValue(nodes["failsafe"]))
      if failsafeData.kind == JObject:
        result.failsafe = failsafeData
    except JsonParsingError:
      discard

method loadCluster*(self: Consul, path: string): base.Cluster =
  ## Load cluster from Consul.
  try:
    let (index, kvNodes) = self.kvGet("", recurse = true)
    self.index = index
    var nodes = initTable[string, JsonNode]()
    let basePath = self.clientPath("")
    for node in kvNodes:
      let key = node["Key"].getStr("")
      if key.startsWith(basePath):
        let relKey = key[basePath.len..^1].strip(chars = {'/'})
        nodes[relKey] = node
    result = self.clusterFromKv(index, nodes)
    self.hasFailed = false
  except ConsulError as e:
    if not self.hasFailed:
      logger.exception("get_cluster", e)
    self.hasFailed = true
    raise newException(ConsulError, "Consul is not responding properly")

method touchMember*(self: Consul, data: JsonNode): bool =
  ## Update member data in Consul.
  try:
    let value = $data
    result = self.kvPut("members/" & self.name, value)
    self.hasFailed = false
  except ConsulError as e:
    if not self.hasFailed:
      logger.exception("touch_member", e)
    self.hasFailed = true
    result = false

method takeLeader*(self: Consul): bool =
  ## Take the leader lock.
  try:
    if self.sessionId.len == 0:
      discard self.createSession()
    result = self.kv.put(self.leaderPath, self.name, acquire = self.sessionId)
    self.hasFailed = false
  except ConsulError as e:
    if not self.hasFailed:
      logger.exception("take_leader", e)
    self.hasFailed = true
    result = false

method attemptToAcquireLeader*(self: Consul): bool =
  ## Attempt to acquire the leader lock.
  result = self.takeLeader()

method setFailoverValue*(self: Consul, value: string, version: int64 = 0): bool =
  ## Set failover value.
  try:
    result = self.kvPut("failover", value, cas = version)
  except ConsulError:
    result = false

method setConfigValue*(self: Consul, value: string, version: int64 = 0): bool =
  ## Set config value.
  try:
    result = self.kvPut("config", value, cas = version)
  except ConsulError:
    result = false

method writeLeaderOptime*(self: Consul, lastLsn: string): bool =
  ## Write leader optime.
  try:
    result = self.kvPut("optime/leader", lastLsn)
  except ConsulError:
    result = false

method writeStatus*(self: Consul, value: string): bool =
  ## Write status.
  try:
    result = self.kvPut("status", value)
  except ConsulError:
    result = false

method writeFailsafe*(self: Consul, value: string): bool =
  ## Write failsafe value.
  try:
    result = self.kvPut("failsafe", value)
  except ConsulError:
    result = false

method updateLeader*(self: Consul, leader: Leader): bool =
  ## Update the leader lock.
  if not self.renewSession():
    return false
  result = self.takeLeader()

method initialize*(self: Consul, createNew: bool = true, sysid: string = ""): bool =
  ## Initialize the cluster.
  try:
    if createNew:
      result = self.kvPut("initialize", sysid)
    else:
      let (_, data) = self.kvGet("initialize")
      result = data.len > 0
  except ConsulError:
    result = false

method deleteLeader*(self: Consul, leader: Leader): bool =
  ## Delete the leader lock.
  try:
    if leader != nil:
      result = self.kvDelete("leader", cas = leader.version)
    else:
      result = self.kvDelete("leader")
  except ConsulError:
    result = false

method cancelInitialization*(self: Consul): bool =
  ## Cancel initialization.
  try:
    result = self.kvDelete("initialize")
  except ConsulError:
    result = false

method deleteCluster*(self: Consul): bool =
  ## Delete the entire cluster data.
  try:
    result = self.kvDelete("", recurse = true)
  except ConsulError:
    result = false

method setHistoryValue*(self: Consul, value: string): bool =
  ## Set history value.
  try:
    result = self.kvPut("history", value)
  except ConsulError:
    result = false

method setSyncStateValue*(self: Consul, value: string, version: int64 = 0): int64 =
  ## Set sync state value.
  try:
    if self.kvPut("sync", value, cas = version):
      let (_, data) = self.kvGet("sync")
      if data.len > 0:
        result = data[0]["ModifyIndex"].getInt(0)
      else:
        result = -1
    else:
      result = -1
  except ConsulError:
    result = -1

method deleteSyncState*(self: Consul, version: int64 = 0): bool =
  ## Delete sync state.
  try:
    result = self.kvDelete("sync", cas = version)
  except ConsulError:
    result = false

method watch*(self: Consul, leaderVersion: int64, timeout: float): bool =
  ## Watch for changes.
  if self.doNotWatch:
    self.doNotWatch = false
    return true

  if self.index > 0:
    let waitTime = fmt"{int(timeout)}s"
    try:
      let (newIndex, _) = self.kvGet("", index = self.index, wait = waitTime, recurse = true)
      self.hasFailed = false
      if newIndex != self.index:
        self.index = newIndex
        return true
    except ConsulError as e:
      if not self.hasFailed:
        logger.error(fmt"watch: {e.msg}")
      self.hasFailed = true
      sleep(1000)

  # Fall back to sleep-based wait
  sleep(int(timeout * 1000))
  result = false
