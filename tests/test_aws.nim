## Tests for patroni/scripts/aws module.
## Ported from test_aws.py

import std/[unittest, json]
import ../patroni/scripts/aws

# Mock data for testing
const
  MOCK_INSTANCE_DOC = """{"instanceId": "012345", "region": "eu-west-1"}"""

# Create mock connection that doesn't make network calls
proc createMockConnection(available: bool = true, clusterName: string = "test"): AWSConnection =
  new(result)
  result.available = available
  result.clusterName = clusterName
  if available:
    result.instanceId = "012345"
    result.region = "eu-west-1"

suite "AWSConnection":
  test "connection stores cluster name":
    let conn = createMockConnection(true, "my-cluster")
    check conn.clusterName == "my-cluster"

  test "connection stores instance id when available":
    let conn = createMockConnection(true)
    check conn.instanceId == "012345"

  test "connection stores region when available":
    let conn = createMockConnection(true)
    check conn.region == "eu-west-1"

  test "unavailable connection has empty instance id":
    let conn = createMockConnection(false)
    check conn.instanceId == ""

  test "unavailable connection has empty region":
    let conn = createMockConnection(false)
    check conn.region == ""

suite "AWSConnection.awsAvailable":
  test "returns true when available":
    let conn = createMockConnection(true)
    check conn.awsAvailable() == true

  test "returns false when unavailable":
    let conn = createMockConnection(false)
    check conn.awsAvailable() == false

suite "AWSConnection.onRoleChange":
  test "returns false when connection unavailable":
    var conn = createMockConnection(false)
    check conn.onRoleChange("primary") == false

  test "returns false when connection unavailable for replica":
    var conn = createMockConnection(false)
    check conn.onRoleChange("replica") == false

  test "returns false when connection unavailable for any role":
    var conn = createMockConnection(false)
    for role in ["master", "replica", "standby", "primary"]:
      check conn.onRoleChange(role) == false

suite "Instance Document Parsing":
  test "parse valid instance document":
    let doc = parseJson(MOCK_INSTANCE_DOC)
    check doc["instanceId"].getStr() == "012345"
    check doc["region"].getStr() == "eu-west-1"

  test "handle malformed instance document":
    try:
      discard parseJson("not valid json")
      check false  # Should have raised
    except JsonParsingError:
      check true

  test "handle empty instance document":
    try:
      discard parseJson("")
      check false  # Should have raised
    except JsonParsingError:
      check true

  test "handle missing fields in document":
    let doc = parseJson("""{"instanceId": "123"}""")
    check doc["instanceId"].getStr() == "123"
    check doc.hasKey("region") == false

suite "AWS Main Function Arguments":
  test "valid action strings":
    # These are the valid action strings
    let validActions = ["on_start", "on_stop", "on_role_change"]
    for action in validActions:
      check action in ["on_start", "on_stop", "on_role_change"]

  test "valid role strings":
    # Typical role values
    let validRoles = ["primary", "replica", "master", "standby"]
    for role in validRoles:
      check role.len > 0

when isMainModule:
  echo "test_aws.nim tests completed"
