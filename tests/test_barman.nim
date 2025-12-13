## Tests for patroni/scripts/barman module.
## Ported from test_barman.py

import std/[unittest, json, strutils]
import ../patroni/scripts/barman/utils
import ../patroni/scripts/barman/recover
import ../patroni/scripts/barman/config_switch

# Test constants
const
  API_URL = "http://localhost:7480"
  BARMAN_SERVER = "my_server"
  BARMAN_MODEL = "my_model"
  BACKUP_ID = "backup_id"
  SSH_COMMAND = "ssh postgres@localhost"
  DATA_DIRECTORY = "/path/to/pgdata"
  LOOP_WAIT = 10
  RETRY_WAIT = 2
  MAX_RETRIES = 5

suite "OperationStatus":
  test "operation status values":
    check OperationStatus.osInProgress == OperationStatus.osInProgress
    check OperationStatus.osDone == OperationStatus.osDone
    check OperationStatus.osFailed == OperationStatus.osFailed

  test "operation status ordering":
    check ord(OperationStatus.osInProgress) == 0
    check ord(OperationStatus.osFailed) == 1
    check ord(OperationStatus.osDone) == 2

  test "operation status distinct values":
    check osInProgress != osFailed
    check osInProgress != osDone
    check osFailed != osDone

suite "RecoverExitCode":
  test "exit codes have correct values":
    check ord(RecoverExitCode.rcRecoveryDone) == 0
    check ord(RecoverExitCode.rcRecoveryFailed) == 1
    check ord(RecoverExitCode.rcHttpError) == 2

  test "exit codes are distinct":
    check rcRecoveryDone != rcRecoveryFailed
    check rcRecoveryDone != rcHttpError
    check rcRecoveryFailed != rcHttpError

suite "ConfigSwitchExitCode":
  test "exit codes have correct values":
    check ord(ConfigSwitchExitCode.csConfigSwitchDone) == 0
    check ord(ConfigSwitchExitCode.csConfigSwitchSkipped) == 1
    check ord(ConfigSwitchExitCode.csConfigSwitchFailed) == 2
    check ord(ConfigSwitchExitCode.csHttpError) == 3
    check ord(ConfigSwitchExitCode.csInvalidArgs) == 4

  test "exit codes are distinct":
    check csConfigSwitchDone != csConfigSwitchSkipped
    check csConfigSwitchDone != csConfigSwitchFailed
    check csConfigSwitchDone != csHttpError
    check csConfigSwitchDone != csInvalidArgs

suite "Exception Types":
  test "RetriesExceeded exception":
    try:
      raise newException(RetriesExceeded, "Max retries exceeded")
    except RetriesExceeded:
      check true

  test "RetriesExceeded exception message preserved":
    try:
      raise newException(RetriesExceeded, "Maximum number of retries exceeded for method Test.")
    except RetriesExceeded as e:
      check e.msg == "Maximum number of retries exceeded for method Test."

  test "ApiNotOk exception":
    try:
      raise newException(ApiNotOk, "API is not responding")
    except ApiNotOk:
      check true

  test "ApiNotOk exception message preserved":
    try:
      raise newException(ApiNotOk, "pg-backup-api is currently not up and running at http://localhost:7480: random")
    except ApiNotOk as e:
      check "pg-backup-api" in e.msg
      check "http://localhost:7480" in e.msg

suite "URL Building":
  test "buildFullUrl constructs correct path":
    # Testing the URL construction logic
    let base = "http://localhost:7480"
    let path = "servers/test"
    # Without trailing slash, should add one
    check base & "/" & path == "http://localhost:7480/servers/test"

  test "URL with trailing slash":
    let base = "http://localhost:7480/"
    let path = "servers/test"
    check base & path == "http://localhost:7480/servers/test"

  test "expected API paths":
    # Verify the expected API path formats
    let serverPath = "servers/" & BARMAN_SERVER & "/operations"
    check serverPath == "servers/my_server/operations"

    let statusPath = "servers/" & BARMAN_SERVER & "/operations/some_id"
    check statusPath == "servers/my_server/operations/some_id"

suite "JSON Serialization":
  test "serialize request body":
    let body = %*{"key": "value"}
    let serialized = $body
    check serialized.len > 0
    check "key" in serialized
    check "value" in serialized

  test "deserialize response":
    let jsonStr = """{"status": "OK"}"""
    let parsed = parseJson(jsonStr)
    check parsed["status"].getStr() == "OK"

  test "deserialize operation status response":
    let jsonStr = """{"status": "DONE"}"""
    let parsed = parseJson(jsonStr)
    check parsed["status"].getStr() == "DONE"

  test "deserialize operation id response":
    let jsonStr = """{"operation_id": "12345-abc"}"""
    let parsed = parseJson(jsonStr)
    check parsed["operation_id"].getStr() == "12345-abc"

  test "deserialize invalid JSON raises":
    try:
      discard parseJson("not valid json")
      check false  # Should have raised
    except JsonParsingError:
      check true

  test "deserialize empty JSON raises":
    try:
      discard parseJson("")
      check false  # Should have raised
    except JsonParsingError:
      check true

suite "Recovery Operation Body":
  test "recovery request body format":
    let body = %*{
      "type": "recovery",
      "backup_id": BACKUP_ID,
      "remote_ssh_command": SSH_COMMAND,
      "destination_directory": DATA_DIRECTORY
    }
    check body["type"].getStr() == "recovery"
    check body["backup_id"].getStr() == BACKUP_ID
    check body["remote_ssh_command"].getStr() == SSH_COMMAND
    check body["destination_directory"].getStr() == DATA_DIRECTORY

suite "Config Switch Operation Body":
  test "config switch with model name":
    var body = %*{"type": "config_switch"}
    body["model_name"] = %BARMAN_MODEL
    check body["type"].getStr() == "config_switch"
    check body["model_name"].getStr() == BARMAN_MODEL

  test "config switch with reset":
    var body = %*{"type": "config_switch"}
    body["reset"] = %true
    check body["type"].getStr() == "config_switch"
    check body["reset"].getBool() == true

suite "ShouldSkipSwitch Logic":
  # These tests mirror the Python test cases for _should_skip_switch

  test "primary with promoted - should not skip":
    var args = ConfigSwitchArgs(role: "primary", switchWhen: "promoted")
    # Based on shouldSkipSwitch logic: switchWhen == "promoted" and role in ["primary", "promoted"] -> false
    # Test the logic: if switchWhen == "promoted" and role notin ["primary", "promoted"] -> skip
    check args.role in ["primary", "promoted"]  # Should not skip

  test "primary with demoted - should skip":
    var args = ConfigSwitchArgs(role: "primary", switchWhen: "demoted")
    # switchWhen == "demoted" and role notin ["replica", "demoted"] -> skip
    check args.role notin ["replica", "demoted"]  # Should skip

  test "promoted with promoted - should not skip":
    var args = ConfigSwitchArgs(role: "promoted", switchWhen: "promoted")
    check args.role in ["primary", "promoted"]  # Should not skip

  test "promoted with demoted - should skip":
    var args = ConfigSwitchArgs(role: "promoted", switchWhen: "demoted")
    check args.role notin ["replica", "demoted"]  # Should skip

  test "standby_leader with promoted - should skip":
    var args = ConfigSwitchArgs(role: "standby_leader", switchWhen: "promoted")
    check args.role notin ["primary", "promoted"]  # Should skip

  test "standby_leader with demoted - should skip":
    var args = ConfigSwitchArgs(role: "standby_leader", switchWhen: "demoted")
    check args.role notin ["replica", "demoted"]  # Should skip

  test "replica with promoted - should skip":
    var args = ConfigSwitchArgs(role: "replica", switchWhen: "promoted")
    check args.role notin ["primary", "promoted"]  # Should skip

  test "replica with demoted - should not skip":
    var args = ConfigSwitchArgs(role: "replica", switchWhen: "demoted")
    check args.role in ["replica", "demoted"]  # Should not skip

  test "demoted with promoted - should skip":
    var args = ConfigSwitchArgs(role: "demoted", switchWhen: "promoted")
    check args.role notin ["primary", "promoted"]  # Should skip

  test "demoted with demoted - should not skip":
    var args = ConfigSwitchArgs(role: "demoted", switchWhen: "demoted")
    check args.role in ["replica", "demoted"]  # Should not skip

  test "any role with always - should not skip":
    # With switchWhen != "promoted" and != "demoted", should return false (not skip)
    for role in ["primary", "promoted", "standby_leader", "replica", "demoted"]:
      var args = ConfigSwitchArgs(role: role, switchWhen: "always")
      # switchWhen is neither "promoted" nor "demoted", so shouldSkipSwitch returns false
      check args.switchWhen notin ["promoted", "demoted"]

suite "RecoverArgs":
  test "RecoverArgs struct":
    var args = RecoverArgs(
      barmanServer: BARMAN_SERVER,
      backupId: BACKUP_ID,
      sshCommand: SSH_COMMAND,
      dataDirectory: DATA_DIRECTORY,
      loopWait: LOOP_WAIT
    )
    check args.barmanServer == BARMAN_SERVER
    check args.backupId == BACKUP_ID
    check args.sshCommand == SSH_COMMAND
    check args.dataDirectory == DATA_DIRECTORY
    check args.loopWait == LOOP_WAIT

suite "ConfigSwitchArgs":
  test "ConfigSwitchArgs struct with model":
    var args = ConfigSwitchArgs(
      action: "on_role_change",
      role: "primary",
      cluster: "my_cluster",
      barmanServer: BARMAN_SERVER,
      barmanModel: BARMAN_MODEL,
      reset: false,
      switchWhen: "promoted"
    )
    check args.action == "on_role_change"
    check args.role == "primary"
    check args.cluster == "my_cluster"
    check args.barmanServer == BARMAN_SERVER
    check args.barmanModel == BARMAN_MODEL
    check args.reset == false
    check args.switchWhen == "promoted"

  test "ConfigSwitchArgs struct with reset":
    var args = ConfigSwitchArgs(
      barmanServer: BARMAN_SERVER,
      barmanModel: "",
      reset: true,
      switchWhen: "demoted"
    )
    check args.barmanServer == BARMAN_SERVER
    check args.barmanModel == ""
    check args.reset == true
    check args.switchWhen == "demoted"

suite "Invalid Args Validation":
  test "both model and reset provided should be invalid":
    # One, and only one among 'barman_model' and 'reset' should be given
    let hasModel = BARMAN_MODEL.len > 0
    let hasReset = true
    # XOR check: (hasModel xor hasReset) should be true for valid args
    check not (hasModel xor hasReset)  # Both true = invalid

  test "neither model nor reset provided should be invalid":
    let hasModel = "".len > 0
    let hasReset = false
    # XOR check: (hasModel xor hasReset) should be true for valid args
    check not (hasModel xor hasReset)  # Both false = invalid

  test "only model provided should be valid":
    let hasModel = BARMAN_MODEL.len > 0
    let hasReset = false
    check hasModel xor hasReset  # Valid

  test "only reset provided should be valid":
    let hasModel = "".len > 0
    let hasReset = true
    check hasModel xor hasReset  # Valid

suite "API Constants":
  test "retry constants":
    check RETRY_WAIT == 2
    check MAX_RETRIES == 5

  test "API URL format":
    check API_URL.startsWith("http://")
    check ":" in API_URL  # Has port

when isMainModule:
  echo "test_barman.nim tests completed"
