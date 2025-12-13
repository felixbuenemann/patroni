## Test helpers and utilities for Patroni Nim tests.
##
## Provides mock objects, test fixtures, and common test utilities.

import std/[unittest, tables, options, os, strutils]
import ../patroni/utils
import ../patroni/exceptions
import ../patroni/dcs

type
  SleepException* = object of CatchableError
    ## Exception raised to simulate sleep interruption in tests.

  MockResponse* = ref object
    ## Mock HTTP response for testing.
    statusCode*: int
    headers*: Table[string, string]
    content*: string
    reason*: string

  MockCursor* = ref object
    ## Mock database cursor for testing.
    connection*: MockConnect
    closed*: bool
    rowcount*: int
    results*: seq[seq[string]]
    description*: seq[string]

  MockConnect* = ref object
    ## Mock database connection for testing.
    serverVersion*: int
    autocommit*: bool
    closed*: int

# Test data constants
const
  GET_PG_SETTINGS_RESULT* = @[
    ("wal_segment_size", "2048", "8kB", "integer", "internal"),
    ("wal_block_size", "8192", "", "integer", "internal"),
    ("shared_buffers", "16384", "8kB", "integer", "postmaster"),
    ("wal_buffers", "-1", "8kB", "integer", "postmaster"),
    ("max_connections", "100", "", "integer", "postmaster"),
    ("max_prepared_transactions", "200", "", "integer", "postmaster"),
    ("max_worker_processes", "8", "", "integer", "postmaster"),
    ("max_locks_per_transaction", "64", "", "integer", "postmaster"),
    ("max_wal_senders", "5", "", "integer", "postmaster"),
    ("search_path", "public", "", "string", "user"),
    ("port", "5432", "", "integer", "postmaster"),
    ("listen_addresses", "127.0.0.2, 127.0.0.3", "", "string", "postmaster"),
    ("autovacuum", "on", "", "bool", "sighup"),
    ("unix_socket_directories", "/tmp", "", "string", "postmaster"),
    ("shared_preload_libraries", "citus", "", "string", "postmaster"),
    ("wal_keep_size", "128", "MB", "integer", "sighup"),
    ("cluster_name", "batman", "", "string", "postmaster"),
    ("vacuum_cost_delay", "200", "ms", "real", "user"),
    ("vacuum_cost_limit", "-1", "", "integer", "user"),
    ("max_stack_depth", "2048", "kB", "integer", "superuser"),
    ("constraint_exclusion", "", "", "enum", "user"),
    ("force_parallel_mode", "1", "", "enum", "user"),
    ("zero_damaged_pages", "off", "", "bool", "superuser"),
    ("stats_temp_directory", "/tmp", "", "string", "sighup"),
    ("track_commit_timestamp", "off", "", "bool", "postmaster"),
    ("wal_log_hints", "on", "", "bool", "postmaster"),
    ("hot_standby", "on", "", "bool", "postmaster"),
    ("max_replication_slots", "5", "", "integer", "postmaster"),
    ("wal_level", "logical", "", "enum", "postmaster"),
  ]

  MOCK_AVAILABLE_GUCS* = [
    "cluster_name", "constraint_exclusion", "force_parallel_mode", "hot_standby",
    "listen_addresses", "max_connections", "max_locks_per_transaction",
    "max_prepared_transactions", "max_replication_slots", "max_stack_depth",
    "max_wal_senders", "max_worker_processes", "port", "search_path",
    "shared_preload_libraries", "stats_temp_directory", "synchronous_standby_names",
    "track_commit_timestamp", "unix_socket_directories", "vacuum_cost_delay",
    "vacuum_cost_limit", "wal_keep_size", "wal_level", "wal_log_hints",
    "zero_damaged_pages", "autovacuum", "wal_segment_size", "wal_block_size",
    "shared_buffers", "wal_buffers", "fork_specific_param"
  ]

proc newMockResponse*(statusCode: int = 200): MockResponse =
  ## Create a new mock HTTP response.
  new(result)
  result.statusCode = statusCode
  result.headers = {"content-type": "json", "lsn": "100"}.toTable
  result.content = "{}"
  result.reason = "Not Found"

proc data*(self: MockResponse): string =
  ## Get response data as string.
  result = self.content

proc status*(self: MockResponse): int =
  ## Get response status code.
  result = self.statusCode

proc getheader*(self: MockResponse, name: string): string =
  ## Get a header value.
  result = ""

proc mockRequestsGet*(url: string, httpMethod: string = "GET", endpoint: string = "", data: string = ""): MockResponse =
  ## Mock HTTP request handler for testing.
  let members = """[{"id":14855829450254237642,"peerURLs":["http://localhost:2380","http://localhost:7001"],""" &
                """"name":"default","clientURLs":["http://localhost:2379","http://localhost:4001"]}]"""
  result = newMockResponse()

  if endpoint == "failsafe":
    result.content = "Accepted"
  elif url.startsWith("http://local"):
    raise newException(IOError, "HTTP Error")
  elif ":8011/patroni" in url:
    result.content = """{"role": "replica", "wal": {"received_location": 0}, "tags": {}}"""
  elif url.endsWith("/members"):
    result.content = if url.startsWith("http://error"): "[{}]" else: members
  elif url.startsWith("http://exhibitor"):
    result.content = """{"servers":["127.0.0.1","127.0.0.2","127.0.0.3"],"port":2181}"""
  elif url.endsWith(":8011/reinitialize"):
    if " false}" in data:
      result.statusCode = 503
      result.content = "restarting after failure already in progress"
  else:
    result.statusCode = 404

proc newMockConnect*(): MockConnect =
  ## Create a new mock database connection.
  new(result)
  result.serverVersion = 99999
  result.autocommit = false
  result.closed = 0

proc newMockCursor*(connection: MockConnect): MockCursor =
  ## Create a new mock database cursor.
  new(result)
  result.connection = connection
  result.closed = false
  result.rowcount = 0
  result.results = @[]
  result.description = @["col1"]

proc getParameterStatus*(self: MockConnect, paramName: string): string =
  ## Get a connection parameter status.
  if paramName == "is_superuser":
    return "on"
  return "0"

proc cursor*(self: MockConnect): MockCursor =
  ## Get a cursor from the connection.
  result = newMockCursor(self)

proc close*(self: MockConnect) =
  ## Close the connection.
  discard

proc psycopgConnect*(): MockConnect =
  ## Mock psycopg connect function.
  result = newMockConnect()

# Test fixture helpers
proc createTempDir*(name: string): string =
  ## Create a temporary directory for testing.
  result = getTempDir() / name
  if not dirExists(result):
    createDir(result)

proc removeTempDir*(path: string) =
  ## Remove a temporary directory.
  if dirExists(path):
    removeDir(path)

# Test member creation helpers
proc createTestMember*(name: string, connUrl: string, index: int = 0, modifyIndex: int64 = 28): Member =
  ## Create a test member.
  var data = newMemberData()
  data.connUrl = connUrl
  data.state = "running"
  result = newMember(index, name, modifyIndex, data)

proc createTestLeader*(name: string, connUrl: string): Leader =
  ## Create a test leader.
  let member = createTestMember(name, connUrl)
  result = newLeader(-1, 28, member)

# Standard test parameters
const
  TEST_PARAMETERS* = {
    "wal_level": "hot_standby",
    "max_replication_slots": "5",
    "f.oo": "bar",
    "search_path": "public",
    "hot_standby": "on",
    "max_wal_senders": "5",
    "wal_keep_segments": "8",
    "wal_log_hints": "on",
    "max_locks_per_transaction": "64",
    "max_worker_processes": "8",
    "max_connections": "100",
    "max_prepared_transactions": "200",
    "track_commit_timestamp": "off",
    "unix_socket_directories": "/tmp",
    "trigger_file": "bla",
    "stats_temp_directory": "/tmp",
    "zero_damaged_pages": "off",
    "force_parallel_mode": "1",
    "constraint_exclusion": "",
    "max_stack_depth": "2048",
    "vacuum_cost_limit": "-1",
    "vacuum_cost_delay": "200"
  }.toTable
