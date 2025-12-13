## Facilities for handling communication with Patroni's REST API.

import std/[httpclient, json, options, strutils, tables, net, uri]
import ./utils

type
  PatroniRequest* = ref object
    ## Wrapper for performing requests to Patroni's REST API.
    ##
    ## Prepares the request manager with the configured settings before performing the request.
    insecure: Option[bool]
    pool: HttpClient
    headers: HttpHeaders
    sslContext: SslContext
    certFile: string
    keyFile: string
    keyPassword: string
    caFile: string

  PatroniResponse* = object
    ## HTTP response wrapper.
    status*: int
    body*: string
    headers*: HttpHeaders

# Forward declarations
proc reloadConfig*(req: PatroniRequest, config: Table[string, JsonNode])

proc getCtlValue(config: Table[string, JsonNode], name: string, default: JsonNode = nil): JsonNode =
  ## Get value of name setting from the ``ctl`` section of the config.
  ##
  ## :param config: Patroni YAML configuration.
  ## :param name: name of the setting value to be retrieved.
  ##
  ## :returns: value of ``ctl.name`` if present, nil otherwise.
  if "ctl" in config:
    let ctl = config["ctl"]
    if ctl.kind == JObject and name in ctl:
      return ctl[name]
  return default

proc getRestapiValue(config: Table[string, JsonNode], name: string): JsonNode =
  ## Get value of name setting from the ``restapi`` section of the config.
  ##
  ## :param config: Patroni YAML configuration.
  ## :param name: name of the setting value to be retrieved.
  ##
  ## :returns: value of ``restapi.name`` if present, nil otherwise.
  if "restapi" in config:
    let restapi = config["restapi"]
    if restapi.kind == JObject and name in restapi:
      return restapi[name]
  return nil

proc newPatroniRequest*(config: Table[string, JsonNode] = initTable[string, JsonNode](),
                        insecure: Option[bool] = none(bool)): PatroniRequest =
  ## Create a new PatroniRequest instance with given config.
  ##
  ## :param config: Patroni YAML configuration.
  ## :param insecure: how to deal with SSL certs verification:
  ##
  ##     * If true it will perform REST API requests without verifying SSL certs; or
  ##     * If false it will perform REST API requests and verify SSL certs; or
  ##     * If none it will behave according to the value of ``ctl.insecure`` configuration; or
  ##     * If none of the above applies, then it falls back to false.
  new(result)
  result.insecure = insecure
  result.headers = newHttpHeaders()
  result.certFile = ""
  result.keyFile = ""
  result.keyPassword = ""
  result.caFile = ""
  result.pool = newHttpClient()
  result.reloadConfig(config)

proc applySslContext(req: PatroniRequest) =
  ## Apply SSL context based on current configuration.
  when defined(ssl):
    var verifyMode = CVerifyPeer
    if req.insecure.isSome and req.insecure.get():
      verifyMode = CVerifyNone

    if req.certFile.len > 0:
      req.sslContext = newContext(verifyMode = verifyMode,
                                  certFile = req.certFile,
                                  keyFile = req.keyFile)
    elif req.caFile.len > 0:
      req.sslContext = newContext(verifyMode = verifyMode,
                                  caFile = req.caFile)
    else:
      req.sslContext = newContext(verifyMode = verifyMode)

    req.pool = newHttpClient(sslContext = req.sslContext)

proc applySslFileParam(req: PatroniRequest, config: Table[string, JsonNode], name: string): string =
  ## Apply a given SSL related param to the request manager.
  ##
  ## :param config: Patroni YAML configuration.
  ## :param name: prefix of the Patroni SSL related setting name. Currently, supports these:
  ##
  ##     * ``cert``: gets translated to ``certfile``
  ##     * ``key``: gets translated to ``keyfile``
  ##
  ##     Will attempt to fetch the requested key first from ``ctl`` section.
  ##
  ## :returns: value of ``ctl.{name}file`` if present, empty string otherwise.
  let value = getCtlValue(config, name & "file")
  if value != nil and value.kind == JString:
    result = value.getStr()
  else:
    result = ""

proc reloadConfig*(req: PatroniRequest, config: Table[string, JsonNode]) =
  ## Apply config to request manager.
  ##
  ## Configure these HTTP headers for requests:
  ##
  ##     * ``authorization``: based on Patroni' CTL or REST API authentication config;
  ##     * ``user-agent``: based on ``patroni.utils.USER_AGENT``.
  ##
  ## Also configure SSL related settings for requests:
  ##
  ##     * ``ca_certs`` is configured if ``ctl.cacert`` or ``restapi.cafile`` is available;
  ##     * ``cert``, ``key`` and ``key_password`` are configured if ``ctl.certfile`` is available.
  ##
  ## :param config: Patroni YAML configuration.

  # Get auth configuration
  var basicAuth = ""
  let ctlAuth = getCtlValue(config, "auth")
  let restapiAuth = getRestapiValue(config, "auth")

  if ctlAuth != nil and ctlAuth.kind == JString:
    basicAuth = ctlAuth.getStr()
  elif restapiAuth != nil and restapiAuth.kind == JString:
    basicAuth = restapiAuth.getStr()

  # Set headers
  req.headers = newHttpHeaders()
  req.headers["User-Agent"] = USER_AGENT

  if basicAuth.len > 0:
    let encoded = encode(basicAuth)
    req.headers["Authorization"] = "Basic " & encoded

  # Determine insecure mode
  var isInsecure = false
  if req.insecure.isSome:
    isInsecure = req.insecure.get()
  else:
    let insecureVal = getCtlValue(config, "insecure", newJBool(false))
    if insecureVal != nil and insecureVal.kind == JBool:
      isInsecure = insecureVal.getBool()

  # Apply SSL file parameters
  req.certFile = req.applySslFileParam(config, "cert")
  if req.certFile.len > 0:
    req.keyFile = req.applySslFileParam(config, "key")
    let passwordVal = getCtlValue(config, "keyfile_password")
    if passwordVal != nil and passwordVal.kind == JString:
      req.keyPassword = passwordVal.getStr()

  # Get CA certificate
  var cacert = ""
  let cacertVal = getCtlValue(config, "cacert")
  let cafileVal = getRestapiValue(config, "cafile")

  if cacertVal != nil and cacertVal.kind == JString:
    cacert = cacertVal.getStr()
  elif cafileVal != nil and cafileVal.kind == JString:
    cacert = cafileVal.getStr()

  req.caFile = cacert

  # Apply SSL context
  if isInsecure:
    req.insecure = some(true)
  req.applySslContext()

proc request*(req: PatroniRequest, httpMethod: string, url: string,
              body: string = ""): PatroniResponse =
  ## Perform an HTTP request.
  ##
  ## :param httpMethod: the HTTP method to be used, e.g. ``GET``.
  ## :param url: the URL to be requested.
  ## :param body: anything to be used as the request body.
  ##
  ## :returns: the response returned upon request.

  # Set headers on the client
  for key, value in req.headers.pairs:
    req.pool.headers[key] = value

  let response = req.pool.request(url, httpMethod = parseEnum[HttpMethod]("http" & httpMethod),
                                  body = body)

  result = PatroniResponse(
    status: response.code.int,
    body: response.body,
    headers: response.headers
  )

proc request*(req: PatroniRequest, httpMethod: string, url: string,
              body: JsonNode): PatroniResponse =
  ## Perform an HTTP request with JSON body.
  ##
  ## :param httpMethod: the HTTP method to be used, e.g. ``GET``.
  ## :param url: the URL to be requested.
  ## :param body: JSON to be used as the request body.
  ##
  ## :returns: the response returned upon request.
  req.request(httpMethod, url, $body)

proc call*(req: PatroniRequest, memberApiUrl: string, httpMethod: string = "GET",
           endpoint: string = "", data: string = ""): PatroniResponse =
  ## Perform a request to a cluster member.
  ##
  ## :param memberApiUrl: base URL for the REST API.
  ## :param httpMethod: HTTP method to be used, e.g. ``GET``.
  ## :param endpoint: URL path of this request, e.g. ``switchover``.
  ## :param data: anything to be used as the request body.
  ##
  ## :returns: the response returned upon request.
  var url = memberApiUrl
  if not url.endsWith("/"):
    url &= "/"
  if endpoint.len > 0:
    url &= endpoint

  req.request(httpMethod, url, data)

proc get*(url: string, verify: bool = true): PatroniResponse =
  ## Perform an HTTP GET request.
  ##
  ## .. note::
  ##     It uses PatroniRequest so all relevant configuration is applied before processing the request.
  ##
  ## :param url: full URL for this GET request.
  ## :param verify: if it should verify SSL certificates when processing the request.
  ##
  ## :returns: the response returned from the request.
  let insecure = if verify: some(false) else: some(true)
  let http = newPatroniRequest(initTable[string, JsonNode](), insecure)
  http.request("GET", url)

proc post*(url: string, body: string = "", verify: bool = true): PatroniResponse =
  ## Perform an HTTP POST request.
  ##
  ## :param url: full URL for this POST request.
  ## :param body: request body.
  ## :param verify: if it should verify SSL certificates when processing the request.
  ##
  ## :returns: the response returned from the request.
  let insecure = if verify: some(false) else: some(true)
  let http = newPatroniRequest(initTable[string, JsonNode](), insecure)
  http.request("POST", url, body)

proc patch*(url: string, body: string = "", verify: bool = true): PatroniResponse =
  ## Perform an HTTP PATCH request.
  ##
  ## :param url: full URL for this PATCH request.
  ## :param body: request body.
  ## :param verify: if it should verify SSL certificates when processing the request.
  ##
  ## :returns: the response returned from the request.
  let insecure = if verify: some(false) else: some(true)
  let http = newPatroniRequest(initTable[string, JsonNode](), insecure)
  http.request("PATCH", url, body)

proc delete*(url: string, verify: bool = true): PatroniResponse =
  ## Perform an HTTP DELETE request.
  ##
  ## :param url: full URL for this DELETE request.
  ## :param verify: if it should verify SSL certificates when processing the request.
  ##
  ## :returns: the response returned from the request.
  let insecure = if verify: some(false) else: some(true)
  let http = newPatroniRequest(initTable[string, JsonNode](), insecure)
  http.request("DELETE", url)

# Base64 encoding helper
proc encode(s: string): string =
  ## Base64 encode a string.
  const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
  result = ""
  var i = 0
  while i < s.len:
    let b0 = ord(s[i])
    let b1 = if i + 1 < s.len: ord(s[i + 1]) else: 0
    let b2 = if i + 2 < s.len: ord(s[i + 2]) else: 0

    result.add(base64Chars[(b0 shr 2) and 0x3F])
    result.add(base64Chars[((b0 shl 4) or (b1 shr 4)) and 0x3F])

    if i + 1 < s.len:
      result.add(base64Chars[((b1 shl 2) or (b2 shr 6)) and 0x3F])
    else:
      result.add('=')

    if i + 2 < s.len:
      result.add(base64Chars[b2 and 0x3F])
    else:
      result.add('=')

    i += 3
