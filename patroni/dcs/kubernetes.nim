## Kubernetes DCS backend for Patroni.
##
## This module provides a distributed configuration store backend using Kubernetes API.

import std/[base64, httpclient, json, locks, net, options, os, random, sequtils,
            strformat, strutils, tables, tempfiles, times, uri]
import ../collections
import ../exceptions
import ../log
import ../request
import ../utils
import ../dcs as base

export base

let logger = getLogger("patroni.dcs.kubernetes")

const
  KUBE_CONFIG_DEFAULT_LOCATION* = "~/.kube/config"
  SERVICE_HOST_ENV_NAME* = "KUBERNETES_SERVICE_HOST"
  SERVICE_PORT_ENV_NAME* = "KUBERNETES_SERVICE_PORT"
  SERVICE_TOKEN_FILENAME* = "/var/run/secrets/kubernetes.io/serviceaccount/token"
  SERVICE_CERT_FILENAME* = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

type
  KubernetesError* = object of DCSError
    ## General Kubernetes error.

  K8sConfigException* = object of KubernetesError
    ## Configuration exception.

  K8sConnectionFailed* = object of KubernetesError
    ## Connection to Kubernetes API failed.

  K8sResourceNotFound* = object of KubernetesError
    ## Kubernetes resource not found.

  K8sConflict* = object of KubernetesError
    ## Kubernetes resource conflict.

  K8sConfig* = ref object
    ## Kubernetes configuration.
    baseUri*: string
    token*: string
    tokenExpiresAt*: DateTime
    headers*: HttpHeaders
    caCert*: string
    clientCert*: string
    clientKey*: string
    namespace*: string
    verify*: bool

  K8sObject* = ref object
    ## Kubernetes object representation.
    kind*: string
    apiVersion*: string
    metadata*: K8sMetadata
    data*: Table[string, string]
    subsets*: seq[JsonNode]

  K8sMetadata* = ref object
    ## Kubernetes object metadata.
    name*: string
    namespace*: string
    uid*: string
    resourceVersion*: string
    annotations*: Table[string, string]
    labels*: Table[string, string]

  K8sClient* = ref object
    ## Kubernetes API client.
    config: K8sConfig
    httpClient: HttpClient
    readTimeout: float

  Kubernetes* = ref object of AbstractDCS
    ## Kubernetes DCS implementation.
    client: K8sClient
    namespace: string
    labels: Table[string, string]
    labelSelector: string
    fieldSelector: string
    roleLabel: string
    podIp: string
    useEndpoints: bool
    retriesCount: int
    ttl: int
    leaderObject: Option[K8sObject]
    clusterObject: Option[K8sObject]
    doNotWatch: bool
    hasFailed: bool

# Helper functions

proc toCamelCase*(value: string): string =
  ## Convert snake_case to camelCase.
  const reserved = ["api", "apiv3", "cidr", "cpu", "csi", "id", "io", "ip", "ipc", "pid", "tls", "uri", "url", "uuid"]
  let words = value.split('_')
  if words.len == 0:
    return value
  result = words[0]
  for i in 1..<words.len:
    let w = words[i]
    if w in reserved:
      result.add(w.toUpperAscii())
    else:
      result.add(w.capitalizeAscii())

proc expandPath(path: string): string =
  ## Expand ~ in path.
  if path.startsWith("~"):
    result = getHomeDir() & path[1..^1]
  else:
    result = path

# K8sConfig implementation

proc newK8sConfig*(): K8sConfig =
  ## Create a new Kubernetes configuration.
  new(result)
  result.headers = newHttpHeaders()
  result.headers["User-Agent"] = USER_AGENT
  result.tokenExpiresAt = dateTime(9999, mDec, 31)
  result.namespace = "default"
  result.verify = true

proc setToken*(self: K8sConfig, token: string) =
  ## Set the bearer token.
  self.token = token
  self.headers["Authorization"] = "Bearer " & token

proc readTokenFile*(self: K8sConfig): string =
  ## Read token from service account file.
  if not fileExists(SERVICE_TOKEN_FILENAME):
    raise newException(K8sConfigException, "Service token file does not exist")
  result = readFile(SERVICE_TOKEN_FILENAME).strip()

proc refreshToken*(self: K8sConfig) =
  ## Refresh the token if needed.
  if now() < self.tokenExpiresAt:
    return
  try:
    let token = self.readTokenFile()
    self.setToken(token)
    # Token refresh happens every hour typically
    self.tokenExpiresAt = now() + initDuration(minutes = 50)
  except IOError as e:
    logger.warning(fmt"Failed to refresh token: {e.msg}")

proc loadInClusterConfig*(self: K8sConfig) =
  ## Load configuration from in-cluster environment.
  let host = getEnv(SERVICE_HOST_ENV_NAME)
  let port = getEnv(SERVICE_PORT_ENV_NAME, "443")

  if host.len == 0:
    raise newException(K8sConfigException, "Service host environment variable is not set")

  self.baseUri = fmt"https://{host}:{port}"

  if fileExists(SERVICE_CERT_FILENAME):
    self.caCert = SERVICE_CERT_FILENAME
  else:
    logger.warning("CA certificate not found, SSL verification may fail")

  try:
    let token = self.readTokenFile()
    self.setToken(token)
  except K8sConfigException:
    raise

  # Read namespace from file
  let namespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
  if fileExists(namespaceFile):
    self.namespace = readFile(namespaceFile).strip()

proc loadKubeConfig*(self: K8sConfig, configPath: string = "") =
  ## Load configuration from kubeconfig file.
  var path = configPath
  if path.len == 0:
    path = getEnv("KUBECONFIG", KUBE_CONFIG_DEFAULT_LOCATION)
  path = expandPath(path)

  if not fileExists(path):
    raise newException(K8sConfigException, fmt"Kubeconfig file not found: {path}")

  # Parse YAML kubeconfig (simplified - would need a YAML parser)
  # For now, just set defaults for local development
  self.baseUri = "https://127.0.0.1:6443"
  logger.warning("Kubeconfig parsing not fully implemented, using defaults")

# K8sClient implementation

proc newK8sClient*(config: K8sConfig): K8sClient =
  ## Create a new Kubernetes API client.
  new(result)
  result.config = config
  result.httpClient = newHttpClient(timeout = 30000)
  result.readTimeout = 10.0

proc setReadTimeout*(self: K8sClient, timeout: float) =
  ## Set the read timeout.
  self.readTimeout = timeout
  self.httpClient.timeout = int(timeout * 1000)

proc doRequest(self: K8sClient, httpMethod: HttpMethod, path: string,
               body: string = "", params: Table[string, string] = initTable[string, string]()): JsonNode =
  ## Execute an HTTP request to Kubernetes API.
  self.config.refreshToken()

  var url = self.config.baseUri & path

  if params.len > 0:
    var queryParams: seq[string] = @[]
    for key, value in params:
      queryParams.add(encodeUrl(key) & "=" & encodeUrl(value))
    url = url & "?" & queryParams.join("&")

  var headers = self.config.headers
  headers["Content-Type"] = "application/json"
  headers["Accept"] = "application/json"

  try:
    let response = self.httpClient.request(url, httpMethod = httpMethod, body = body, headers = headers)
    let bodyStr = response.body

    if response.code.int >= 400:
      if response.code.int == 404:
        raise newException(K8sResourceNotFound, bodyStr)
      elif response.code.int == 409:
        raise newException(K8sConflict, bodyStr)
      else:
        raise newException(KubernetesError, fmt"HTTP {response.code}: {bodyStr}")

    if bodyStr.len > 0:
      result = parseJson(bodyStr)
    else:
      result = newJObject()
  except OSError as e:
    raise newException(K8sConnectionFailed, fmt"Connection failed: {e.msg}")
  except TimeoutError as e:
    raise newException(K8sConnectionFailed, fmt"Connection timeout: {e.msg}")

proc get*(self: K8sClient, path: string, params: Table[string, string] = initTable[string, string]()): JsonNode =
  ## GET request.
  result = self.doRequest(HttpGet, path, params = params)

proc post*(self: K8sClient, path: string, body: JsonNode): JsonNode =
  ## POST request.
  result = self.doRequest(HttpPost, path, body = $body)

proc put*(self: K8sClient, path: string, body: JsonNode): JsonNode =
  ## PUT request.
  result = self.doRequest(HttpPut, path, body = $body)

proc patch*(self: K8sClient, path: string, body: JsonNode): JsonNode =
  ## PATCH request.
  var headers = self.config.headers
  headers["Content-Type"] = "application/strategic-merge-patch+json"
  self.config.refreshToken()

  let url = self.config.baseUri & path
  let response = self.httpClient.request(url, httpMethod = HttpPatch, body = $body, headers = headers)

  if response.code.int >= 400:
    if response.code.int == 404:
      raise newException(K8sResourceNotFound, response.body)
    elif response.code.int == 409:
      raise newException(K8sConflict, response.body)
    else:
      raise newException(KubernetesError, fmt"HTTP {response.code}: {response.body}")

  if response.body.len > 0:
    result = parseJson(response.body)
  else:
    result = newJObject()

proc delete*(self: K8sClient, path: string): bool =
  ## DELETE request.
  try:
    discard self.doRequest(HttpDelete, path)
    result = true
  except K8sResourceNotFound:
    result = true
  except KubernetesError:
    result = false

# K8sObject helpers

proc parseK8sObject*(data: JsonNode): K8sObject =
  ## Parse a Kubernetes object from JSON.
  new(result)
  result.kind = data.getOrDefault("kind").getStr("")
  result.apiVersion = data.getOrDefault("apiVersion").getStr("")

  result.metadata = K8sMetadata()
  if data.hasKey("metadata"):
    let meta = data["metadata"]
    result.metadata.name = meta.getOrDefault("name").getStr("")
    result.metadata.namespace = meta.getOrDefault("namespace").getStr("")
    result.metadata.uid = meta.getOrDefault("uid").getStr("")
    result.metadata.resourceVersion = meta.getOrDefault("resourceVersion").getStr("")

    result.metadata.annotations = initTable[string, string]()
    if meta.hasKey("annotations") and meta["annotations"].kind == JObject:
      for key, val in meta["annotations"].pairs:
        result.metadata.annotations[key] = val.getStr("")

    result.metadata.labels = initTable[string, string]()
    if meta.hasKey("labels") and meta["labels"].kind == JObject:
      for key, val in meta["labels"].pairs:
        result.metadata.labels[key] = val.getStr("")

  result.data = initTable[string, string]()
  if data.hasKey("data") and data["data"].kind == JObject:
    for key, val in data["data"].pairs:
      result.data[key] = val.getStr("")

  result.subsets = @[]
  if data.hasKey("subsets") and data["subsets"].kind == JArray:
    for item in data["subsets"]:
      result.subsets.add(item)

proc toJson*(self: K8sObject): JsonNode =
  ## Convert K8sObject to JSON.
  result = newJObject()
  result["kind"] = newJString(self.kind)
  result["apiVersion"] = newJString(self.apiVersion)

  var meta = newJObject()
  meta["name"] = newJString(self.metadata.name)
  if self.metadata.namespace.len > 0:
    meta["namespace"] = newJString(self.metadata.namespace)
  if self.metadata.resourceVersion.len > 0:
    meta["resourceVersion"] = newJString(self.metadata.resourceVersion)

  if self.metadata.annotations.len > 0:
    var annotations = newJObject()
    for key, val in self.metadata.annotations:
      annotations[key] = newJString(val)
    meta["annotations"] = annotations

  if self.metadata.labels.len > 0:
    var labels = newJObject()
    for key, val in self.metadata.labels:
      labels[key] = newJString(val)
    meta["labels"] = labels

  result["metadata"] = meta

  if self.data.len > 0:
    var data = newJObject()
    for key, val in self.data:
      data[key] = newJString(val)
    result["data"] = data

# Kubernetes DCS Implementation

proc newKubernetes*(config: JsonNode): Kubernetes =
  ## Create a new Kubernetes DCS instance.
  new(result)
  initAbstractDCS(result, config)

  let k8sConfig = newK8sConfig()

  # Try in-cluster config first, then kubeconfig
  try:
    k8sConfig.loadInClusterConfig()
  except K8sConfigException:
    try:
      k8sConfig.loadKubeConfig()
    except K8sConfigException as e:
      logger.error(fmt"Failed to load Kubernetes config: {e.msg}")

  result.client = newK8sClient(k8sConfig)

  let k8sSection = if config.hasKey("kubernetes"): config["kubernetes"] else: newJObject()

  result.namespace = k8sSection.getOrDefault("namespace").getStr(k8sConfig.namespace)
  result.useEndpoints = k8sSection.getOrDefault("use_endpoints").getBool(false)
  result.roleLabel = k8sSection.getOrDefault("role_label").getStr("role")
  result.podIp = getEnv("PATRONI_KUBERNETES_POD_IP", "")

  result.labels = initTable[string, string]()
  if k8sSection.hasKey("labels") and k8sSection["labels"].kind == JObject:
    for key, val in k8sSection["labels"].pairs:
      result.labels[key] = val.getStr("")

  # Build label selector
  var selectors: seq[string] = @[]
  for key, val in result.labels:
    selectors.add(fmt"{key}={val}")
  result.labelSelector = selectors.join(",")

  result.fieldSelector = ""
  result.retriesCount = 0
  result.ttl = config["ttl"].getInt(30)
  result.leaderObject = none(K8sObject)
  result.clusterObject = none(K8sObject)
  result.doNotWatch = false
  result.hasFailed = false

proc apiPath(self: Kubernetes, resource: string, name: string = ""): string =
  ## Build API path for a resource.
  let resourcePath = if self.useEndpoints: "endpoints" else: resource
  result = fmt"/api/v1/namespaces/{self.namespace}/{resourcePath}"
  if name.len > 0:
    result = result & "/" & name

proc configmapPath(self: Kubernetes, name: string = ""): string =
  ## Build API path for ConfigMaps.
  result = fmt"/api/v1/namespaces/{self.namespace}/configmaps"
  if name.len > 0:
    result = result & "/" & name

method setTtl*(self: Kubernetes, ttl: int): bool =
  ## Set the TTL for keys.
  let changed = self.ttl != ttl
  self.ttl = ttl
  if changed:
    self.doNotWatch = true
  result = changed

method getTtl*(self: Kubernetes): int =
  ## Get the current TTL.
  result = self.ttl

method setRetryTimeout*(self: Kubernetes, retryTimeout: int) =
  ## Set the retry timeout.
  self.client.setReadTimeout(float(retryTimeout))

proc getMemberData(annotations: Table[string, string]): Table[string, JsonNode] =
  ## Extract member data from annotations.
  result = initTable[string, JsonNode]()
  for key, val in annotations:
    if key.startsWith("patroni.") or key in ["status", "initialize", "config", "leader", "failover", "sync", "history"]:
      try:
        result[key] = parseJson(val)
      except JsonParsingError:
        result[key] = newJString(val)

proc memberFromPod(name: string, annotations: Table[string, string]): Member =
  ## Create a Member from pod annotations.
  let data = getMemberData(annotations)
  result = fromNode(0, name, "", $(%data))

proc clusterFromK8s(self: Kubernetes, obj: K8sObject): base.Cluster =
  ## Build a Cluster from Kubernetes object.
  result = newCluster()

  let annotations = obj.metadata.annotations

  # Get initialize flag
  if "initialize" in annotations:
    result.initialize = annotations["initialize"]

  # Get global dynamic configuration
  if "config" in annotations:
    result.config = clusterConfigFromNode(0, annotations["config"])

  # Get timeline history
  if "history" in annotations:
    result.history = timelineHistoryFromNode(0, annotations["history"])

  # Get status
  if "status" in annotations:
    result.status = statusFromNode(annotations["status"])

  # Get leader
  if "leader" in annotations:
    let leaderName = annotations["leader"]
    let leaderMember = newRemoteMember(leaderName, newMemberData())
    result.leader = newLeader(0, "", leaderMember)

  # Get failover key
  if "failover" in annotations:
    result.failover = failoverFromNode(0, annotations["failover"])

  # Get synchronization state
  if "sync" in annotations:
    result.sync = syncStateFromNode(0, annotations["sync"])

  # Members would be loaded separately from pods/endpoints

method loadCluster*(self: Kubernetes, path: string): base.Cluster =
  ## Load cluster from Kubernetes.
  try:
    # Get the config ConfigMap
    let configPath = self.configmapPath(self.scope & "-config")
    let configData = self.client.get(configPath)
    let configObj = parseK8sObject(configData)
    self.clusterObject = some(configObj)
    result = self.clusterFromK8s(configObj)
    self.hasFailed = false
  except K8sResourceNotFound:
    result = emptyCluster()
  except KubernetesError as e:
    if not self.hasFailed:
      logger.exception("get_cluster", e)
    self.hasFailed = true
    raise newException(KubernetesError, "Kubernetes is not responding properly")

method touchMember*(self: Kubernetes, data: JsonNode): bool =
  ## Update member data in Kubernetes.
  try:
    let annotations = newJObject()
    for key, val in data.pairs:
      annotations["patroni." & key] = newJString($val)

    let patch = %*{
      "metadata": {
        "annotations": annotations
      }
    }

    let podPath = fmt"/api/v1/namespaces/{self.namespace}/pods/{self.name}"
    discard self.client.patch(podPath, patch)
    self.hasFailed = false
    result = true
  except KubernetesError as e:
    if not self.hasFailed:
      logger.exception("touch_member", e)
    self.hasFailed = true
    result = false

method takeLeader*(self: Kubernetes): bool =
  ## Take the leader lock.
  try:
    let leaderPath = self.configmapPath(self.scope & "-leader")

    # Try to create or update the leader ConfigMap
    let leaderData = %*{
      "apiVersion": "v1",
      "kind": "ConfigMap",
      "metadata": {
        "name": self.scope & "-leader",
        "namespace": self.namespace,
        "annotations": {
          "leader": self.name
        }
      }
    }

    try:
      discard self.client.post(self.configmapPath(), leaderData)
    except K8sConflict:
      # Already exists, try to patch
      let patch = %*{
        "metadata": {
          "annotations": {
            "leader": self.name
          }
        }
      }
      discard self.client.patch(leaderPath, patch)

    self.hasFailed = false
    result = true
  except KubernetesError as e:
    if not self.hasFailed:
      logger.exception("take_leader", e)
    self.hasFailed = true
    result = false

method attemptToAcquireLeader*(self: Kubernetes): bool =
  ## Attempt to acquire the leader lock.
  result = self.takeLeader()

method setFailoverValue*(self: Kubernetes, value: string, version: int64 = 0): bool =
  ## Set failover value.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "failover": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method setConfigValue*(self: Kubernetes, value: string, version: int64 = 0): bool =
  ## Set config value.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "config": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method writeLeaderOptime*(self: Kubernetes, lastLsn: string): bool =
  ## Write leader optime.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "optime": lastLsn
        }
      }
    }
    let leaderPath = self.configmapPath(self.scope & "-leader")
    discard self.client.patch(leaderPath, patch)
    result = true
  except KubernetesError:
    result = false

method writeStatus*(self: Kubernetes, value: string): bool =
  ## Write status.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "status": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method writeFailsafe*(self: Kubernetes, value: string): bool =
  ## Write failsafe value.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "failsafe": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method updateLeader*(self: Kubernetes, leader: Leader): bool =
  ## Update the leader lock.
  result = self.takeLeader()

method initialize*(self: Kubernetes, createNew: bool = true, sysid: string = ""): bool =
  ## Initialize the cluster.
  try:
    if createNew:
      let configData = %*{
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {
          "name": self.scope & "-config",
          "namespace": self.namespace,
          "annotations": {
            "initialize": sysid
          }
        }
      }
      discard self.client.post(self.configmapPath(), configData)
    result = true
  except KubernetesError:
    result = false

method deleteLeader*(self: Kubernetes, leader: Leader): bool =
  ## Delete the leader lock.
  try:
    let leaderPath = self.configmapPath(self.scope & "-leader")
    result = self.client.delete(leaderPath)
  except KubernetesError:
    result = false

method cancelInitialization*(self: Kubernetes): bool =
  ## Cancel initialization.
  try:
    let configPath = self.configmapPath(self.scope & "-config")
    result = self.client.delete(configPath)
  except KubernetesError:
    result = false

method deleteCluster*(self: Kubernetes): bool =
  ## Delete the entire cluster data.
  try:
    discard self.client.delete(self.configmapPath(self.scope & "-config"))
    discard self.client.delete(self.configmapPath(self.scope & "-leader"))
    result = true
  except KubernetesError:
    result = false

method setHistoryValue*(self: Kubernetes, value: string): bool =
  ## Set history value.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "history": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method setSyncStateValue*(self: Kubernetes, value: string, version: int64 = 0): int64 =
  ## Set sync state value.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "sync": value
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    let response = self.client.patch(configPath, patch)
    if response.hasKey("metadata") and response["metadata"].hasKey("resourceVersion"):
      result = parseInt(response["metadata"]["resourceVersion"].getStr("0"))
    else:
      result = 0
  except KubernetesError:
    result = -1

method deleteSyncState*(self: Kubernetes, version: int64 = 0): bool =
  ## Delete sync state.
  try:
    let patch = %*{
      "metadata": {
        "annotations": {
          "sync": newJNull()
        }
      }
    }
    let configPath = self.configmapPath(self.scope & "-config")
    discard self.client.patch(configPath, patch)
    result = true
  except KubernetesError:
    result = false

method watch*(self: Kubernetes, leaderVersion: int64, timeout: float): bool =
  ## Watch for changes.
  if self.doNotWatch:
    self.doNotWatch = false
    return true

  # Kubernetes watches are complex - simplified to polling for now
  sleep(int(timeout * 1000))
  result = false
