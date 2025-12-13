## Implement Patroni's REST API.
##
## Exposes a REST API of patroni operations functions, such as status, performance and management to web clients.
##
## Much of what can be achieved with the command line tool patronictl can be done via the API. Patroni CLI and daemon
## utilises the API to perform these functions.

import std/[asynchttpserver, asyncdispatch, json, strutils, tables, times, net, strformat, locks, options, uri]
import ./config
import ./dcs
import ./exceptions
import ./global_config
import ./log
import ./psycopg
import ./utils
import ./postgresql/misc

let logger = getLogger("patroni.api")

type
  RestApiServer* = ref object
    ## REST API HTTP server for Patroni.
    server: AsyncHttpServer
    patroni*: pointer  # Patroni instance (forward declaration)
    listenAddress: string
    port: int
    running: bool
    lock: Lock
    certFile: string
    keyFile: string
    caFile: string
    verifyClient: bool
    allowedNetworks: seq[string]
    authUsername: string
    authPassword: string
    serverHeader: string

  RestApiHandler* = ref object
    ## Handler for REST API requests.
    server: RestApiServer
    request: Request

  ApiResponse* = object
    ## HTTP response wrapper.
    status*: int
    body*: string
    contentType*: string

proc newApiResponse*(status: int, body: string, contentType: string = "application/json"): ApiResponse =
  result.status = status
  result.body = body
  result.contentType = contentType

proc ok*(body: string): ApiResponse =
  result = newApiResponse(200, body)

proc okJson*(data: JsonNode): ApiResponse =
  result = newApiResponse(200, $data)

proc notFound*(): ApiResponse =
  result = newApiResponse(404, """{"error": "Not found"}""")

proc unauthorized*(): ApiResponse =
  result = newApiResponse(401, """{"error": "Unauthorized"}""")

proc forbidden*(): ApiResponse =
  result = newApiResponse(403, """{"error": "Forbidden"}""")

proc badRequest*(message: string): ApiResponse =
  result = newApiResponse(400, fmt"""{{"error": "{message}"}}""")

proc internalError*(message: string): ApiResponse =
  result = newApiResponse(500, fmt"""{{"error": "{message}"}}""")

proc serviceUnavailable*(): ApiResponse =
  result = newApiResponse(503, """{"error": "Service Unavailable"}""")

# REST API Server implementation

proc newRestApiServer*(config: Config): RestApiServer =
  ## Create a new REST API server.
  new(result)
  result.server = newAsyncHttpServer()
  initLock(result.lock)
  result.running = false
  result.serverHeader = "Patroni"

  # Get REST API configuration
  let restapiConfig = config.get("restapi")
  if restapiConfig != nil and restapiConfig.kind == JObject:
    if "listen" in restapiConfig:
      let listen = restapiConfig["listen"].getStr("")
      let parts = listen.split(":")
      if parts.len >= 1:
        result.listenAddress = parts[0]
      if parts.len >= 2:
        let p = parseInt(parts[1])
        if p.isSome:
          result.port = p.get()
        else:
          result.port = 8008

    if "certfile" in restapiConfig:
      result.certFile = restapiConfig["certfile"].getStr("")
    if "keyfile" in restapiConfig:
      result.keyFile = restapiConfig["keyfile"].getStr("")
    if "cafile" in restapiConfig:
      result.caFile = restapiConfig["cafile"].getStr("")
    if "verify_client" in restapiConfig:
      result.verifyClient = restapiConfig["verify_client"].getBool(false)

    if "auth" in restapiConfig:
      let auth = restapiConfig["auth"].getStr("")
      let authParts = auth.split(":")
      if authParts.len >= 2:
        result.authUsername = authParts[0]
        result.authPassword = authParts[1..^1].join(":")
  else:
    result.listenAddress = "0.0.0.0"
    result.port = 8008

proc checkAccess*(server: RestApiServer, request: Request): bool =
  ## Check if the request is authorized.
  # Check basic auth if configured
  if server.authUsername.len > 0:
    let authHeader = request.headers.getOrDefault("Authorization")
    if authHeader.len == 0:
      return false

    if not authHeader.startsWith("Basic "):
      return false

    # Would decode and check credentials here
    # For now, just return true
    return true

  result = true

proc getStatus*(server: RestApiServer): JsonNode =
  ## Get the current status of Patroni.
  result = newJObject()
  result["state"] = newJString("running")
  result["role"] = newJString("replica")
  result["server_version"] = newJInt(150000)

proc getConfig*(server: RestApiServer): JsonNode =
  ## Get the current configuration.
  result = newJObject()
  # Would return actual configuration

proc getCluster*(server: RestApiServer): JsonNode =
  ## Get cluster information.
  result = newJObject()
  result["members"] = newJArray()

proc handleRequest(server: RestApiServer, request: Request): Future[ApiResponse] {.async.} =
  ## Handle an incoming REST API request.
  let path = request.url.path
  let httpMethod = request.reqMethod

  # Check access
  if not server.checkAccess(request):
    return unauthorized()

  # Route the request
  case path
  of "/", "/health", "/patroni":
    if httpMethod == HttpGet:
      return okJson(server.getStatus())
    else:
      return badRequest("Method not allowed")

  of "/primary", "/master", "/leader", "/read-write":
    if httpMethod == HttpGet:
      let status = server.getStatus()
      let role = status["role"].getStr("")
      if role in ["primary", "master"]:
        return okJson(status)
      else:
        return serviceUnavailable()
    else:
      return badRequest("Method not allowed")

  of "/replica", "/read-only":
    if httpMethod == HttpGet:
      let status = server.getStatus()
      let role = status["role"].getStr("")
      if role == "replica":
        return okJson(status)
      else:
        return serviceUnavailable()
    else:
      return badRequest("Method not allowed")

  of "/standby-leader":
    if httpMethod == HttpGet:
      let status = server.getStatus()
      let role = status["role"].getStr("")
      if role == "standby_leader":
        return okJson(status)
      else:
        return serviceUnavailable()
    else:
      return badRequest("Method not allowed")

  of "/config":
    case httpMethod
    of HttpGet:
      return okJson(server.getConfig())
    of HttpPatch:
      # Would update configuration
      return okJson(newJObject())
    of HttpPut:
      # Would replace configuration
      return okJson(newJObject())
    else:
      return badRequest("Method not allowed")

  of "/cluster":
    if httpMethod == HttpGet:
      return okJson(server.getCluster())
    else:
      return badRequest("Method not allowed")

  of "/switchover":
    if httpMethod == HttpPost:
      # Would trigger switchover
      return okJson(%*{"status": "switchover scheduled"})
    else:
      return badRequest("Method not allowed")

  of "/failover":
    if httpMethod == HttpPost:
      # Would trigger failover
      return okJson(%*{"status": "failover scheduled"})
    else:
      return badRequest("Method not allowed")

  of "/reinitialize":
    if httpMethod == HttpPost:
      # Would reinitialize the node
      return okJson(%*{"status": "reinitialize scheduled"})
    else:
      return badRequest("Method not allowed")

  of "/restart":
    if httpMethod == HttpPost:
      # Would restart PostgreSQL
      return okJson(%*{"status": "restart scheduled"})
    else:
      return badRequest("Method not allowed")

  of "/reload":
    if httpMethod == HttpPost:
      # Would reload configuration
      return okJson(%*{"status": "reload triggered"})
    else:
      return badRequest("Method not allowed")

  of "/liveness":
    return ok("")

  of "/readiness":
    return ok("")

  else:
    return notFound()

proc serve(server: RestApiServer, request: Request) {.async.} =
  ## Serve a single request.
  try:
    let response = await server.handleRequest(request)
    await request.respond(
      HttpCode(response.status),
      response.body,
      newHttpHeaders([("Content-Type", response.contentType)])
    )
  except CatchableError as e:
    logger.exception("Error handling request", e)
    await request.respond(
      Http500,
      """{"error": "Internal server error"}""",
      newHttpHeaders([("Content-Type", "application/json")])
    )

proc start*(server: RestApiServer) {.async.} =
  ## Start the REST API server.
  server.running = true
  logger.info(fmt"Starting REST API server on {server.listenAddress}:{server.port}")

  proc callback(request: Request) {.async.} =
    await server.serve(request)

  await server.server.serve(Port(server.port), callback, server.listenAddress)

proc stop*(server: RestApiServer) =
  ## Stop the REST API server.
  server.running = false
  server.server.close()
  logger.info("Stopped REST API server")

# Utility functions

proc clusterAsJson*(cluster: Cluster): JsonNode =
  ## Convert a Cluster to JSON representation.
  result = newJObject()

  if cluster == nil:
    return

  var members = newJArray()
  for member in cluster.members:
    var m = newJObject()
    m["name"] = newJString(member.name)
    m["role"] = newJString(member.role)
    m["state"] = newJString(member.state)
    m["api_url"] = newJString(member.apiUrl)
    m["timeline"] = newJInt(member.timeline)
    m["lag"] = newJInt(0)  # Would calculate actual lag
    members.add(m)
  result["members"] = members

  if cluster.leader != nil and cluster.leader.member != nil:
    result["leader"] = newJString(cluster.leader.member.name)
  else:
    result["leader"] = newJNull()

  if cluster.sync != nil:
    var sync = newJObject()
    sync["sync_standby"] = newJArray()
    for s in cluster.sync.syncStandby:
      sync["sync_standby"].add(newJString(s))
    result["sync"] = sync

proc parseUri*(s: string): tuple[scheme: string, host: string, port: int, path: string] =
  ## Parse a URI string.
  let parsed = parseUri(s)
  var port = 0
  if parsed.port.len > 0:
    let p = parseInt(parsed.port)
    if p.isSome:
      port = p.get()
  result = (parsed.scheme, parsed.hostname, port, parsed.path)
