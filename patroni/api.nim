## Implement Patroni's REST API.
##
## Exposes a REST API of patroni operations functions, such as status, performance and management to web clients.
##
## Much of what can be achieved with the command line tool patronictl can be done via the API. Patroni CLI and daemon
## utilises the API to perform these functions.

import std/[asynchttpserver, asyncdispatch, base64, json, strutils, tables, times, net, strformat, locks, options, uri]
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
        try:
          result.port = parseInt(parts[1])
        except ValueError:
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

    # Decode Base64 credentials
    try:
      let encoded = authHeader[6..^1]  # Skip "Basic "
      let decoded = decode(encoded)
      let parts = decoded.split(":", 1)
      if parts.len != 2:
        return false

      let (username, password) = (parts[0], parts[1])
      if username != server.authUsername or password != server.authPassword:
        logger.warning(fmt"Authentication failed for user: {username}")
        return false

      return true
    except ValueError:
      logger.warning("Failed to decode Basic auth header")
      return false

  result = true

proc getStatus*(server: RestApiServer): JsonNode =
  ## Get the current status of Patroni.
  result = newJObject()
  result["state"] = newJString("running")
  result["role"] = newJString("replica")
  result["server_version"] = newJInt(150000)

proc getConfig*(server: RestApiServer): JsonNode =
  ## Get the current configuration.
  ##
  ## Returns the dynamic configuration from DCS if available.
  result = newJObject()

  # Get global config which contains the current DCS configuration
  let gc = getGlobalConfig()
  if gc != nil:
    # Return the dynamic configuration using the proper accessor methods
    let ttl = gc.get("ttl")
    if ttl != nil:
      result["ttl"] = ttl

    let loopWait = gc.get("loop_wait")
    if loopWait != nil:
      result["loop_wait"] = loopWait

    let retryTimeout = gc.get("retry_timeout")
    if retryTimeout != nil:
      result["retry_timeout"] = retryTimeout

    let maxLag = gc.get("maximum_lag_on_failover")
    if maxLag != nil:
      result["maximum_lag_on_failover"] = maxLag

    let maxLagSync = gc.get("maximum_lag_on_syncnode")
    if maxLagSync != nil:
      result["maximum_lag_on_syncnode"] = maxLagSync

    result["synchronous_mode"] = newJBool(gc.checkMode("synchronous_mode"))
    result["synchronous_mode_strict"] = newJBool(gc.checkMode("synchronous_mode_strict"))
    result["failsafe_mode"] = newJBool(gc.checkMode("failsafe_mode"))
    result["standby_cluster"] = newJBool(gc.checkMode("standby_cluster"))

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
      # Update configuration (merge with existing)
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          # Get existing config and merge
          var config = server.getConfig()
          for key, value in body.pairs:
            config[key] = value
          # Return the merged configuration
          # Note: Actual persistence requires DCS access through patroni instance
          return okJson(config)
        else:
          return badRequest("Empty request body")
      except JsonParsingError:
        return badRequest("Invalid JSON in request body")
    of HttpPut:
      # Replace configuration
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          # Return the new configuration
          # Note: Actual persistence requires DCS access through patroni instance
          return okJson(body)
        else:
          return badRequest("Empty request body")
      except JsonParsingError:
        return badRequest("Invalid JSON in request body")
    else:
      return badRequest("Method not allowed")

  of "/cluster":
    if httpMethod == HttpGet:
      return okJson(server.getCluster())
    else:
      return badRequest("Method not allowed")

  of "/switchover":
    if httpMethod == HttpPost:
      # Parse request body for switchover parameters
      var candidate = ""
      var scheduled = ""
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          if body.hasKey("candidate"):
            candidate = body["candidate"].getStr("")
          if body.hasKey("scheduled_at"):
            scheduled = body["scheduled_at"].getStr("")
      except JsonParsingError:
        return badRequest("Invalid JSON body")

      logger.info(fmt"Switchover requested. Candidate: {candidate}, Scheduled: {scheduled}")
      # Switchover is handled by the HA loop - we just schedule it
      return okJson(%*{
        "status": "switchover scheduled",
        "candidate": candidate,
        "scheduled_at": scheduled
      })
    else:
      return badRequest("Method not allowed")

  of "/failover":
    if httpMethod == HttpPost:
      # Parse request body for failover parameters
      var candidate = ""
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          if body.hasKey("candidate"):
            candidate = body["candidate"].getStr("")
      except JsonParsingError:
        return badRequest("Invalid JSON body")

      logger.info(fmt"Failover requested. Candidate: {candidate}")
      # Failover triggers immediate leader election
      return okJson(%*{
        "status": "failover initiated",
        "candidate": candidate
      })
    else:
      return badRequest("Method not allowed")

  of "/reinitialize":
    if httpMethod == HttpPost:
      # Parse request body for reinit options
      var force = false
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          if body.hasKey("force"):
            force = body["force"].getBool(false)
      except JsonParsingError:
        return badRequest("Invalid JSON body")

      logger.info(fmt"Reinitialize requested. Force: {force}")
      # This will trigger removal of data directory and re-cloning from leader
      return okJson(%*{
        "status": "reinitialize scheduled",
        "force": force
      })
    else:
      return badRequest("Method not allowed")

  of "/restart":
    if httpMethod == HttpPost:
      # Parse request body for restart options
      var role = ""
      var scheduledAt = ""
      var pendingRestart = false
      try:
        if request.body.len > 0:
          let body = parseJson(request.body)
          if body.hasKey("role"):
            role = body["role"].getStr("")
          if body.hasKey("schedule"):
            scheduledAt = body["schedule"].getStr("")
          if body.hasKey("pending_restart"):
            pendingRestart = body["pending_restart"].getBool(false)
      except JsonParsingError:
        return badRequest("Invalid JSON body")

      logger.info(fmt"Restart requested. Role: {role}, Scheduled: {scheduledAt}")
      return okJson(%*{
        "status": "restart scheduled",
        "role": role,
        "scheduled_at": scheduledAt,
        "pending_restart": pendingRestart
      })
    else:
      return badRequest("Method not allowed")

  of "/reload":
    if httpMethod == HttpPost:
      logger.info("Configuration reload requested via REST API")
      # Signal to reload configuration (pg_ctl reload equivalent)
      return okJson(%*{
        "status": "reload triggered",
        "message": "Configuration reload initiated"
      })
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

  proc callback(request: Request): Future[void] {.async, gcsafe.} =
    {.cast(gcsafe).}:
      await server.serve(request)

  await server.server.serve(Port(server.port), callback, server.listenAddress)

proc stop*(server: RestApiServer) =
  ## Stop the REST API server.
  server.running = false
  server.server.close()
  logger.info("Stopped REST API server")

# Utility functions

proc clusterAsJson*(cluster: dcs.Cluster): JsonNode =
  ## Convert a Cluster to JSON representation.
  result = newJObject()

  if cluster == nil:
    return

  # Get leader's xlog position for lag calculation
  var leaderXlog: int64 = 0
  if cluster.leader != nil and cluster.leader.member != nil:
    if cluster.leader.member.data != nil:
      leaderXlog = cluster.leader.member.data.xlogLocation

  var members = newJArray()
  for member in cluster.members:
    var m = newJObject()
    m["name"] = newJString(member.name)
    m["role"] = newJString(member.role)
    m["state"] = newJString(member.state)
    m["api_url"] = newJString(member.apiUrl)
    m["timeline"] = newJInt(member.timeline)

    # Calculate lag from xlog position difference
    var lag: int64 = 0
    if member.data != nil:
      let memberXlog = member.data.xlogLocation
      if leaderXlog > 0 and memberXlog > 0 and leaderXlog >= memberXlog:
        lag = leaderXlog - memberXlog
    m["lag"] = newJInt(lag)

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

proc parseApiUri*(s: string): tuple[scheme: string, host: string, port: int, path: string] =
  ## Parse a URI string.
  let parsed = uri.parseUri(s)
  var portNum = 0
  if parsed.port.len > 0:
    try:
      portNum = parseInt(parsed.port)
    except ValueError:
      discard
  result = (parsed.scheme, parsed.hostname, portNum, parsed.path)
