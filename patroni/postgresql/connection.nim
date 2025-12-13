## PostgreSQL connection pool management.

import std/[locks, tables, strformat, options]
import ../psycopg
import ../exceptions
import ../log

let logger = getLogger("patroni.postgresql.connection")

type
  NamedConnection* = ref object
    ## Helper class to manage psycopg connections from Patroni to PostgreSQL.
    ##
    ## :ivar serverVersion: PostgreSQL version in integer format where we are connected to.
    pool: ConnectionPool
    name: string
    kwargsOverride: Table[string, string]
    lock: Lock
    connection: PgConnection
    serverVersion*: int

  ConnectionPool* = ref object
    ## Helper class to manage named connections from Patroni to PostgreSQL.
    ##
    ## The instance keeps named NamedConnection objects and parameters that must be used for new connections.
    lock: Lock
    connections: Table[string, NamedConnection]
    connKwargsInternal: Table[string, string]

proc connKwargs*(pool: ConnectionPool): Table[string, string]
  ## Forward declaration

proc close*(nc: NamedConnection, silent: bool = false): bool
  ## Forward declaration

proc newNamedConnection*(pool: ConnectionPool, name: string,
                         kwargsOverride: Table[string, string] = initTable[string, string]()): NamedConnection =
  ## Create an instance of NamedConnection class.
  ##
  ## :param pool: reference to a ConnectionPool object.
  ## :param name: name of the connection.
  ## :param kwargsOverride: dict object with connection parameters that should be
  ##                        different from default values provided by connection pool.
  new(result)
  result.pool = pool
  result.name = name
  result.kwargsOverride = kwargsOverride
  initLock(result.lock)
  result.connection = nil
  result.serverVersion = 0

proc connKwargs(nc: NamedConnection): Table[string, string] =
  ## Connection parameters for this NamedConnection.
  result = nc.pool.connKwargs
  for key, value in nc.kwargsOverride.pairs:
    result[key] = value
  result["application_name"] = fmt"Patroni {nc.name}"

proc get*(nc: NamedConnection): PgConnection =
  ## Get psycopg connection object.
  ##
  ## .. note::
  ##     Opens a new connection if necessary.
  ##
  ## :returns: PgConnection object.
  withLock(nc.lock):
    if nc.connection == nil:
      logger.info(fmt"establishing a new patroni {nc.name} connection to postgres")
      let kwargs = nc.connKwargs()
      nc.connection = connect(
        host = kwargs.getOrDefault("host", ""),
        port = kwargs.getOrDefault("port", "5432"),
        user = kwargs.getOrDefault("user", ""),
        password = kwargs.getOrDefault("password", ""),
        database = kwargs.getOrDefault("database", "postgres")
      )
      nc.serverVersion = nc.connection.serverVersion
  result = nc.connection

proc query*(nc: NamedConnection, sql: string, args: varargs[string, `$`]): seq[Row] =
  ## Execute a query with parameters and optionally returns a response.
  ##
  ## :param sql: SQL statement to execute.
  ## :param args: parameters to pass.
  ##
  ## :returns: a query response as a list of tuples if there is any.
  ## :raises:
  ##     PgError if had issues while executing sql.
  ##     PostgresConnectionException: if had issues while connecting to the database.
  try:
    let conn = nc.get()
    result = conn.query(sql, args)
  except PgError as exc:
    # When connected via unix socket, psycopg can't recognize 'connection lost' and leaves
    # connection open, but the generic exception is raised. It doesn't make
    # sense to continue with existing connection and we will close it, to avoid its reuse.
    discard nc.close(true)
    raise newException(PostgresConnectionException, "connection problems: " & exc.msg)

proc close*(nc: NamedConnection, silent: bool = false): bool =
  ## Close the psycopg connection to postgres.
  ##
  ## :param silent: whether the method should not write logs.
  ##
  ## :returns: true if psycopg connection was closed, false otherwise.
  result = false
  if nc.connection != nil:
    nc.connection.close()
    if not silent:
      logger.info(fmt"closed patroni {nc.name} connection to postgres")
    result = true
  nc.connection = nil

proc newConnectionPool*(): ConnectionPool =
  ## Create an instance of ConnectionPool class.
  new(result)
  initLock(result.lock)
  result.connections = initTable[string, NamedConnection]()
  result.connKwargsInternal = initTable[string, string]()

proc connKwargs*(pool: ConnectionPool): Table[string, string] =
  ## Connection parameters that must be used for new psycopg connections.
  withLock(pool.lock):
    result = pool.connKwargsInternal

proc `connKwargs=`*(pool: ConnectionPool, value: Table[string, string]) =
  ## Set new connection parameters.
  ##
  ## :param value: dict object with connection parameters.
  withLock(pool.lock):
    pool.connKwargsInternal = value

proc get*(pool: ConnectionPool, name: string,
          kwargsOverride: Table[string, string] = initTable[string, string]()): NamedConnection =
  ## Get a new named NamedConnection object from the pool.
  ##
  ## .. note::
  ##     Creates a new NamedConnection object if it doesn't yet exist in the pool.
  ##
  ## :param name: name of the connection.
  ## :param kwargsOverride: dict object with connection parameters that should be
  ##                        different from default values provided by connKwargs.
  ##
  ## :returns: NamedConnection object.
  withLock(pool.lock):
    if name notin pool.connections:
      pool.connections[name] = newNamedConnection(pool, name, kwargsOverride)
  result = pool.connections[name]

proc close*(pool: ConnectionPool) =
  ## Close all named connections from Patroni to PostgreSQL registered in the pool.
  withLock(pool.lock):
    var closedAny = false
    for name, conn in pool.connections.pairs:
      if conn.close(true):
        closedAny = true
    if closedAny:
      logger.info("closed patroni connections to postgres")

template withConnectionCursor*(kwargs: Table[string, string], body: untyped) =
  ## Context manager for database connection cursor.
  let conn = connect(
    host = kwargs.getOrDefault("host", ""),
    port = kwargs.getOrDefault("port", "5432"),
    user = kwargs.getOrDefault("user", ""),
    password = kwargs.getOrDefault("password", ""),
    database = kwargs.getOrDefault("database", "postgres")
  )
  try:
    let cursor {.inject.} = conn.cursor()
    body
  finally:
    conn.close()
