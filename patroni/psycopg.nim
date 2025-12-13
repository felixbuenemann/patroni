## Abstraction layer for PostgreSQL database access.
##
## This module uses Nim's std/db_postgres which wraps libpq, providing similar functionality
## to Python's psycopg2/psycopg modules.

import std/[db_postgres, strutils, tables, options, strformat]
import ./exceptions

export db_postgres

type
  PgConnection* = ref object
    ## PostgreSQL connection wrapper with additional metadata.
    conn*: DbConn
    serverVersion*: int
    autocommit*: bool
    connString*: string

  PgCursor* = ref object
    ## Cursor for iterating through query results.
    conn*: PgConnection
    rows*: seq[Row]
    currentRow*: int
    description*: seq[tuple[name: string, typeOid: int]]

  PgError* = object of PostgresException
    ## Base PostgreSQL error.

  DatabaseError* = object of PgError
    ## Database error.

  OperationalError* = object of PgError
    ## Operational error (connection issues, etc).

  ProgrammingError* = object of PgError
    ## Programming error (SQL syntax errors, etc).

proc getParameterStatus*(conn: PgConnection, paramName: string): string =
  ## Get connection parameter status.
  ##
  ## :param paramName: the name of the connection parameter.
  ## :returns: the value for the paramName or empty string.
  # In db_postgres, we can use pqParameterStatus from the underlying PGconn
  when defined(postgres):
    let pq = conn.conn
    # Access libpq directly for parameter status
    result = ""
  else:
    result = ""

proc parseConninfo*(conninfo: string): Table[string, string] =
  ## Parse a PostgreSQL connection string into a dictionary.
  ##
  ## Handles both key=value format and URI format.
  ##
  ## :param conninfo: connection string to parse.
  ## :returns: dictionary of connection parameters.
  result = initTable[string, string]()

  if conninfo.len == 0:
    return

  # Handle URI format: postgresql://user:password@host:port/database
  if conninfo.startsWith("postgresql://") or conninfo.startsWith("postgres://"):
    var uri = conninfo
    if uri.startsWith("postgresql://"):
      uri = uri[13..^1]
    else:
      uri = uri[11..^1]

    # Extract user:password@host:port/database?params
    var userPass = ""
    var hostPart = ""
    var dbPart = ""
    var paramsPart = ""

    # Split by ?
    let qmarkIdx = uri.find('?')
    if qmarkIdx >= 0:
      paramsPart = uri[qmarkIdx + 1..^1]
      uri = uri[0..<qmarkIdx]

    # Split by /
    let slashIdx = uri.find('/')
    if slashIdx >= 0:
      dbPart = uri[slashIdx + 1..^1]
      uri = uri[0..<slashIdx]

    # Split by @
    let atIdx = uri.find('@')
    if atIdx >= 0:
      userPass = uri[0..<atIdx]
      hostPart = uri[atIdx + 1..^1]
    else:
      hostPart = uri

    # Parse user:password
    if userPass.len > 0:
      let colonIdx = userPass.find(':')
      if colonIdx >= 0:
        result["user"] = userPass[0..<colonIdx]
        result["password"] = userPass[colonIdx + 1..^1]
      else:
        result["user"] = userPass

    # Parse host:port
    if hostPart.len > 0:
      let colonIdx = hostPart.rfind(':')
      if colonIdx >= 0:
        result["host"] = hostPart[0..<colonIdx]
        result["port"] = hostPart[colonIdx + 1..^1]
      else:
        result["host"] = hostPart

    # Database
    if dbPart.len > 0:
      result["dbname"] = dbPart

    # Parse query parameters
    if paramsPart.len > 0:
      for pair in paramsPart.split('&'):
        let eqIdx = pair.find('=')
        if eqIdx >= 0:
          result[pair[0..<eqIdx]] = pair[eqIdx + 1..^1]

  else:
    # Handle key=value format
    var current = ""
    var inQuote = false
    var key = ""
    var value = ""
    var parsingKey = true

    for i, c in conninfo:
      if c == '\'' and (i == 0 or conninfo[i - 1] != '\\'):
        inQuote = not inQuote
      elif c == '=' and not inQuote and parsingKey:
        key = current.strip()
        current = ""
        parsingKey = false
      elif c == ' ' and not inQuote and not parsingKey:
        value = current.strip().strip(chars = {'\''})
        if key.len > 0:
          result[key] = value
        key = ""
        current = ""
        parsingKey = true
      else:
        current.add(c)

    # Handle last pair
    if not parsingKey and key.len > 0:
      value = current.strip().strip(chars = {'\''})
      result[key] = value

proc quoteIdent*(value: string, conn: PgConnection = nil): string =
  ## Quote value as a SQL identifier.
  ##
  ## :param value: value to be quoted.
  ## :param conn: connection to evaluate the returning string into (optional).
  ##
  ## :returns: value quoted as a SQL identifier.
  result = "\"" & value.replace("\"", "\"\"") & "\""

proc quoteLiteral*(value: string, conn: PgConnection = nil): string =
  ## Quote value as a SQL literal.
  ##
  ## :param value: value to be quoted.
  ## :param conn: connection to evaluate the returning string into (optional).
  ##
  ## :returns: value quoted as a SQL literal.
  result = "'" & value.replace("'", "''") & "'"

proc quoteLiteral*(value: int, conn: PgConnection = nil): string =
  ## Quote integer value as a SQL literal.
  result = $value

proc quoteLiteral*(value: float, conn: PgConnection = nil): string =
  ## Quote float value as a SQL literal.
  result = $value

proc quoteLiteral*(value: bool, conn: PgConnection = nil): string =
  ## Quote boolean value as a SQL literal.
  if value:
    result = "true"
  else:
    result = "false"

proc buildConnString(host: string = "", port: string = "", user: string = "",
                     password: string = "", database: string = "",
                     options: string = ""): string =
  ## Build a connection string from components.
  var parts: seq[string] = @[]

  if host.len > 0:
    parts.add("host=" & host)
  if port.len > 0:
    parts.add("port=" & port)
  if user.len > 0:
    parts.add("user=" & user)
  if password.len > 0:
    parts.add("password=" & password)
  if database.len > 0:
    parts.add("dbname=" & database)
  if options.len > 0:
    parts.add("options=" & options)

  result = parts.join(" ")

proc connect*(conninfo: string = "", host: string = "", port: string = "5432",
              user: string = "", password: string = "", database: string = "",
              options: string = "", replication: string = "",
              fallbackApplicationName: string = ""): PgConnection =
  ## Get a connection to the database.
  ##
  ## .. note::
  ##     The connection will have autocommit enabled.
  ##
  ##     It also enforces search_path=pg_catalog for non-replication connections to mitigate security issues as
  ##     Patroni relies on superuser connections.
  ##
  ## :param conninfo: connection string (optional).
  ## :param host: database host.
  ## :param port: database port.
  ## :param user: database user.
  ## :param password: database password.
  ## :param database: database name.
  ## :param options: additional connection options.
  ## :param replication: replication mode.
  ## :param fallbackApplicationName: application name.
  ##
  ## :returns: a connection to the database.

  new(result)

  var connStr = conninfo
  if connStr.len == 0:
    var finalOptions = options
    if replication.len == 0 and fallbackApplicationName != "Patroni ctl":
      if finalOptions.len > 0:
        finalOptions &= " "
      finalOptions &= "-c search_path=pg_catalog"

    connStr = buildConnString(host, port, user, password, database, finalOptions)

  try:
    # Parse the connection string to get individual parameters
    let params = parseConninfo(connStr)
    let h = params.getOrDefault("host", "localhost")
    let u = params.getOrDefault("user", "postgres")
    let p = params.getOrDefault("password", "")
    let d = params.getOrDefault("dbname", "postgres")

    result.conn = db_postgres.open(h, u, p, d)
    result.autocommit = true
    result.connString = connStr
    result.serverVersion = 0  # Would need libpq to get actual version
  except DbError as e:
    raise newException(OperationalError, "Failed to connect: " & e.msg)

proc close*(conn: PgConnection) =
  ## Close the database connection.
  if conn.conn != nil:
    conn.conn.close()

proc execute*(conn: PgConnection, query: string, args: varargs[string, `$`]): int64 =
  ## Execute a SQL query.
  ##
  ## :param query: SQL query to execute.
  ## :param args: query parameters.
  ##
  ## :returns: number of affected rows.
  try:
    result = conn.conn.execAffectedRows(sql(query), args)
  except DbError as e:
    if e.msg.contains("syntax"):
      raise newException(ProgrammingError, e.msg)
    else:
      raise newException(DatabaseError, e.msg)

proc query*(conn: PgConnection, query: string, args: varargs[string, `$`]): seq[Row] =
  ## Execute a SQL query and return results.
  ##
  ## :param query: SQL query to execute.
  ## :param args: query parameters.
  ##
  ## :returns: sequence of result rows.
  try:
    result = conn.conn.getAllRows(sql(query), args)
  except DbError as e:
    if e.msg.contains("syntax"):
      raise newException(ProgrammingError, e.msg)
    else:
      raise newException(DatabaseError, e.msg)

proc queryOne*(conn: PgConnection, query: string, args: varargs[string, `$`]): Option[Row] =
  ## Execute a SQL query and return the first row.
  ##
  ## :param query: SQL query to execute.
  ## :param args: query parameters.
  ##
  ## :returns: first result row or none.
  try:
    let row = conn.conn.getRow(sql(query), args)
    if row.len > 0 and row[0].len > 0:
      result = some(row)
    else:
      result = none(Row)
  except DbError as e:
    if e.msg.contains("syntax"):
      raise newException(ProgrammingError, e.msg)
    else:
      raise newException(DatabaseError, e.msg)

proc getValue*(conn: PgConnection, query: string, args: varargs[string, `$`]): string =
  ## Execute a SQL query and return a single value.
  ##
  ## :param query: SQL query to execute.
  ## :param args: query parameters.
  ##
  ## :returns: first column of first row.
  try:
    result = conn.conn.getValue(sql(query), args)
  except DbError as e:
    if e.msg.contains("syntax"):
      raise newException(ProgrammingError, e.msg)
    else:
      raise newException(DatabaseError, e.msg)

proc cursor*(conn: PgConnection): PgCursor =
  ## Create a cursor for the connection.
  new(result)
  result.conn = conn
  result.rows = @[]
  result.currentRow = 0
  result.description = @[]

proc execute*(cursor: PgCursor, query: string, args: varargs[string, `$`]) =
  ## Execute a query using the cursor.
  cursor.rows = cursor.conn.query(query, args)
  cursor.currentRow = 0

proc fetchone*(cursor: PgCursor): Option[Row] =
  ## Fetch the next row from the cursor.
  if cursor.currentRow < cursor.rows.len:
    result = some(cursor.rows[cursor.currentRow])
    inc cursor.currentRow
  else:
    result = none(Row)

proc fetchall*(cursor: PgCursor): seq[Row] =
  ## Fetch all remaining rows from the cursor.
  result = cursor.rows[cursor.currentRow..^1]
  cursor.currentRow = cursor.rows.len

proc fetchmany*(cursor: PgCursor, size: int): seq[Row] =
  ## Fetch up to size rows from the cursor.
  let endIdx = min(cursor.currentRow + size, cursor.rows.len)
  result = cursor.rows[cursor.currentRow..<endIdx]
  cursor.currentRow = endIdx

proc tryExec*(conn: PgConnection, query: string, args: varargs[string, `$`]): bool =
  ## Try to execute a SQL query, returning success status.
  try:
    discard conn.conn.tryExec(sql(query), args)
    result = true
  except:
    result = false

proc setAutocommit*(conn: PgConnection, value: bool) =
  ## Set autocommit mode.
  conn.autocommit = value

proc commit*(conn: PgConnection) =
  ## Commit the current transaction.
  if not conn.autocommit:
    discard conn.execute("COMMIT")

proc rollback*(conn: PgConnection) =
  ## Rollback the current transaction.
  if not conn.autocommit:
    discard conn.execute("ROLLBACK")

proc beginTransaction*(conn: PgConnection) =
  ## Begin a new transaction.
  if not conn.autocommit:
    discard conn.execute("BEGIN")
