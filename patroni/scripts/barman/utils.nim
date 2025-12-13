## Utility functions for Barman-related scripts.
##
## This module provides utilities for communicating with the pg-backup-api.

import std/[httpclient, json, logging, os, strformat, strutils, times, uri]

type
  RetriesExceeded* = object of CatchableError
    ## Maximum number of retries exceeded.

  ApiNotOk* = object of CatchableError
    ## The pg-backup-api is not currently up and running.

  OperationStatus* = enum
    ## Possible status of pg-backup-api operations.
    osInProgress = 0  ## The operation is still ongoing
    osFailed = 1      ## The operation failed
    osDone = 2        ## The operation finished successfully

  PgBackupApi* = ref object
    ## Facilities for communicating with the pg-backup-api.
    apiUrl*: string
    certFile*: string
    keyFile*: string
    retryWait*: int
    maxRetries*: int
    http: HttpClient

proc retry*[T](fn: proc(): T, maxRetries: int, retryWait: int, methodName: string): T =
  ## Retry an operation n times if expected exceptions are faced.
  ##
  ## :param fn: Function to retry.
  ## :param maxRetries: Maximum retry attempts before failing.
  ## :param retryWait: How long in seconds to wait before retrying.
  ## :param methodName: Name of the method for logging.
  ## :returns: Result of the function.
  ## :raises RetriesExceeded: If the maximum number of attempts has been exhausted.
  var attempt = 1

  while attempt <= maxRetries:
    try:
      return fn()
    except KeyError as exc:
      logging.warn(fmt"Attempt {attempt} of {maxRetries} on method {methodName} failed with {exc.msg}")
      inc attempt
      sleep(retryWait * 1000)

  raise newException(RetriesExceeded, fmt"Maximum number of retries exceeded for method {methodName}.")

proc setUpLogging*(logFile: string = "") =
  ## Set up logging to file, if logFile is given, otherwise to console.
  ##
  ## :param logFile: File where to log messages, if any.
  if logFile.len > 0:
    let fh = open(logFile, fmAppend)
    let handler = newFileLogger(fh, fmtStr = "$datetime $levelname: ")
    addHandler(handler)
  else:
    addHandler(newConsoleLogger(fmtStr = "$datetime $levelname: "))

proc buildFullUrl(self: PgBackupApi, urlPath: string): string =
  ## Build the full URL by concatenating urlPath with the base URL.
  if self.apiUrl.endsWith("/"):
    return self.apiUrl & urlPath
  else:
    return self.apiUrl & "/" & urlPath

proc deserializeResponse(response: Response): JsonNode =
  ## Retrieve body from response as a deserialized JSON object.
  return parseJson(response.body)

proc serializeRequest(body: JsonNode): string =
  ## Serialize a request body.
  return $body

proc getRequest(self: PgBackupApi, urlPath: string): JsonNode =
  ## Perform a GET request to urlPath.
  ##
  ## :param urlPath: URL to perform the GET request against.
  ## :returns: The deserialized response body.
  ## :raises RetriesExceeded: Raised from the corresponding HTTP exception.
  let url = self.buildFullUrl(urlPath)

  try:
    let response = self.http.get(url)
    return deserializeResponse(response)
  except HttpRequestError as exc:
    let msg = fmt"Failed to perform a GET request to {url}"
    raise newException(RetriesExceeded, msg)

proc postRequest(self: PgBackupApi, urlPath: string, body: JsonNode): JsonNode =
  ## Perform a POST request to urlPath serializing body as JSON.
  ##
  ## :param urlPath: URL to perform the POST request against.
  ## :param body: The body to be serialized as JSON and sent in the request.
  ## :returns: The deserialized response body.
  ## :raises RetriesExceeded: Raised from the corresponding HTTP exception.
  let url = self.buildFullUrl(urlPath)
  let serializedBody = serializeRequest(body)

  try:
    self.http.headers = newHttpHeaders({"Content-Type": "application/json"})
    let response = self.http.post(url, serializedBody)
    return deserializeResponse(response)
  except HttpRequestError as exc:
    let msg = fmt"Failed to perform a POST request to {url} with {serializedBody}"
    raise newException(RetriesExceeded, msg)

proc ensureApiOk(self: PgBackupApi) =
  ## Ensure pg-backup-api is reachable and OK.
  ##
  ## :raises ApiNotOk: If pg-backup-api status is not OK.
  let response = self.getRequest("status")

  if response.getStr() != "OK":
    let msg = fmt"pg-backup-api is currently not up and running at {self.apiUrl}: {response}"
    raise newException(ApiNotOk, msg)

proc newPgBackupApi*(apiUrl: string, certFile: string = "",
                     keyFile: string = "", retryWait: int = 2,
                     maxRetries: int = 5): PgBackupApi =
  ## Create a new instance of PgBackupApi.
  ##
  ## Make sure the pg-backup-api is reachable and running fine.
  ##
  ## :param apiUrl: Base URL to reach the pg-backup-api.
  ## :param certFile: Certificate to authenticate against the API, if required.
  ## :param keyFile: Certificate key to authenticate against the API, if required.
  ## :param retryWait: How long in seconds to wait before retrying a failed request.
  ## :param maxRetries: Maximum number of retries when pg-backup-api returns malformed responses.
  ## :raises ApiNotOk: If the API is down or returns a bogus status.
  new(result)
  result.apiUrl = apiUrl
  result.certFile = certFile
  result.keyFile = keyFile
  result.retryWait = retryWait
  result.maxRetries = maxRetries

  # Create HTTP client with SSL context if certificates provided
  if certFile.len > 0 and keyFile.len > 0:
    let ctx = newContext(certFile = certFile, keyFile = keyFile)
    result.http = newHttpClient(sslContext = ctx)
  else:
    result.http = newHttpClient()

  result.ensureApiOk()

proc getOperationStatus*(self: PgBackupApi, barmanServer: string,
                         operationId: string): OperationStatus =
  ## Get status of the operation which ID is operationId.
  ##
  ## :param barmanServer: Name of the Barman server related with the operation.
  ## :param operationId: ID of the operation to be checked.
  ## :returns: The status of the operation.
  proc doGetStatus(): OperationStatus =
    let response = self.getRequest(fmt"servers/{barmanServer}/operations/{operationId}")
    let status = response["status"].getStr()
    case status
    of "IN_PROGRESS": return osInProgress
    of "FAILED": return osFailed
    of "DONE": return osDone
    else: raise newException(KeyError, fmt"Unknown status: {status}")

  return retry(doGetStatus, self.maxRetries, self.retryWait, "PgBackupApi.getOperationStatus")

proc createRecoveryOperation*(self: PgBackupApi, barmanServer: string,
                              backupId: string, sshCommand: string,
                              dataDirectory: string): string =
  ## Create a recovery operation on the pg-backup-api.
  ##
  ## :param barmanServer: Name of the Barman server which backup is to be restored.
  ## :param backupId: ID of the backup from the Barman server.
  ## :param sshCommand: SSH command to connect from the Barman host to the target host.
  ## :param dataDirectory: Path to the Postgres data directory where to restore the backup.
  ## :returns: The ID of the recovery operation that has been created.
  proc doCreateRecovery(): string =
    let body = %*{
      "type": "recovery",
      "backup_id": backupId,
      "remote_ssh_command": sshCommand,
      "destination_directory": dataDirectory
    }
    let response = self.postRequest(fmt"servers/{barmanServer}/operations", body)
    return response["operation_id"].getStr()

  return retry(doCreateRecovery, self.maxRetries, self.retryWait, "PgBackupApi.createRecoveryOperation")

proc createConfigSwitchOperation*(self: PgBackupApi, barmanServer: string,
                                  barmanModel: string = "",
                                  reset: bool = false): string =
  ## Create a config switch operation on the pg-backup-api.
  ##
  ## :param barmanServer: Name of the Barman server which config is to be switched.
  ## :param barmanModel: Name of the Barman model to be applied to the server, if any.
  ## :param reset: True if you would like to unapply the currently active model.
  ## :returns: The ID of the config switch operation that has been created.
  proc doCreateConfigSwitch(): string =
    var body = %*{"type": "config_switch"}

    if barmanModel.len > 0:
      body["model_name"] = %barmanModel
    elif reset:
      body["reset"] = %reset

    let response = self.postRequest(fmt"servers/{barmanServer}/operations", body)
    return response["operation_id"].getStr()

  return retry(doCreateConfigSwitch, self.maxRetries, self.retryWait, "PgBackupApi.createConfigSwitchOperation")

proc close*(self: PgBackupApi) =
  ## Close the HTTP client.
  self.http.close()
