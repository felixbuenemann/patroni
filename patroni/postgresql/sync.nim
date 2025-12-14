## Synchronous replication handling.
##
## This module provides types and procedures for managing PostgreSQL
## synchronous_standby_names and related synchronous replication features.

import std/[algorithm, json, locks, options, re, sequtils, strformat, strutils, tables, times]
import ../collections
import ../dcs
import ../global_config
import ../log
import ../psycopg
import ./misc

export misc

let logger = getLogger("patroni.postgresql.sync")

# Regular expressions for parsing synchronous_standby_names
let SYNC_STANDBY_NAME_RE = re"^[A-Za-z_][A-Za-z_0-9\$]*$"

type
  SyncType* = enum
    ## Type of synchronous replication.
    stOff = "off"
    stPriority = "priority"
    stQuorum = "quorum"

  SSN* = object
    ## Parsed synchronous_standby_names value.
    ##
    ## :param syncType: possible values: 'off', 'priority', 'quorum'
    ## :param hasStar: is set to true if synchronous_standby_names contains '*'
    ## :param num: how many nodes are required to be synchronous
    ## :param members: collection of standby names listed in synchronous_standby_names
    syncType*: SyncType
    hasStar*: bool
    num*: int
    members*: CaseInsensitiveSet

  SyncState* = object
    ## Current synchronous state.
    ##
    ## :param syncType: possible values: off, priority, quorum
    ## :param numsync: how many nodes are required to be synchronous
    ## :param sync: collection of synchronous node names
    ## :param syncConfirmed: collection of confirmed synchronous node names
    ## :param active: collection of active node names
    syncType*: SyncType
    numsync*: int
    sync*: CaseInsensitiveSet
    syncConfirmed*: CaseInsensitiveSet
    active*: CaseInsensitiveSet

  Replica* = object
    ## A single replica eligible to be synchronous.
    ##
    ## :param pid: PID of walsender process
    ## :param applicationName: matches with the Member.name
    ## :param syncState: possible values: async, potential, quorum, sync
    ## :param lsn: write_lsn, flush_lsn, or replay_lsn
    ## :param nofailover: whether the member has nofailover tag
    ## :param syncPriority: sync priority from member tags
    pid*: int
    applicationName*: string
    syncState*: string
    lsn*: int64
    nofailover*: bool
    syncPriority*: int

  ReplicaList* = ref object
    ## Collection of Replica objects.
    replicas*: seq[Replica]
    maxLsn*: int64

  SyncHandler* = ref object
    ## Handler for synchronous_standby_names.
    ##
    ## Sync standbys are chosen based on their state in pg_stat_replication.
    postgresql: pointer  # Postgresql - forward declaration
    synchronousStandbyNames: string
    ssnData: SSN
    primaryFlushLsn: int64
    readyReplicas: CaseInsensitiveDict[int]

proc emptySSN*(): SSN =
  ## Create an empty SSN.
  result.syncType = stOff
  result.hasStar = false
  result.num = 0
  result.members = newCaseInsensitiveSet()

proc quoteStandbyName*(value: string): string =
  ## Quote provided value if necessary.
  ##
  ## :param value: name of a synchronous standby.
  ## :returns: a quoted value if required or the original one.
  let lower = value.toLowerAscii()
  if value.match(SYNC_STANDBY_NAME_RE) and lower notin ["first", "any"]:
    result = value
  else:
    result = quoteIdent(value)

type
  TokenType = enum
    ttFirst, ttAny, ttSpace, ttIdent, ttDquot, ttStar, ttNum,
    ttComma, ttParenStart, ttParenEnd, ttJunk

  Token = tuple
    tokenType: TokenType
    value: string
    pos: int

proc tokenize(value: string): seq[Token] =
  ## Tokenize synchronous_standby_names value.
  result = @[]
  var i = 0
  let length = value.len

  while i < length:
    let c = value[i]

    if c in Whitespace:
      var j = i
      while j < length and value[j] in Whitespace:
        inc j
      # Skip spaces
      i = j
      continue

    elif c == '(':
      result.add((ttParenStart, "(", i))
      inc i

    elif c == ')':
      result.add((ttParenEnd, ")", i))
      inc i

    elif c == ',':
      result.add((ttComma, ",", i))
      inc i

    elif c == '*':
      result.add((ttStar, "*", i))
      inc i

    elif c == '"':
      # Double-quoted identifier
      var j = i + 1
      var ident = ""
      while j < length:
        if value[j] == '"':
          if j + 1 < length and value[j + 1] == '"':
            ident.add('"')
            j += 2
          else:
            inc j
            break
        else:
          ident.add(value[j])
          inc j
      result.add((ttDquot, value[i..<j], i))
      i = j

    elif c.isDigit():
      var j = i
      while j < length and value[j].isDigit():
        inc j
      result.add((ttNum, value[i..<j], i))
      i = j

    elif c.isAlphaAscii() or c == '_':
      var j = i
      while j < length and (value[j].isAlphaNumeric() or value[j] in {'_', '$'}):
        inc j
      let ident = value[i..<j]
      let lower = ident.toLowerAscii()
      if lower == "first":
        result.add((ttFirst, ident, i))
      elif lower == "any":
        result.add((ttAny, ident, i))
      else:
        result.add((ttIdent, ident, i))
      i = j

    else:
      result.add((ttJunk, $c, i))
      inc i

proc parseSyncStandbyNames*(value: string): SSN =
  ## Parse postgresql synchronous_standby_names to constituent parts.
  ##
  ## :param value: the value of synchronous_standby_names
  ## :returns: SSN object
  ## :raises ValueError: if the configuration value cannot be parsed
  let tokens = tokenize(value)

  if tokens.len == 0:
    return emptySSN()

  var syncType: SyncType
  var num: int
  var synclist: seq[Token]

  # Check for ANY N (...) pattern
  if tokens.len >= 4 and
     tokens[0].tokenType == ttAny and
     tokens[1].tokenType == ttNum and
     tokens[2].tokenType == ttParenStart and
     tokens[^1].tokenType == ttParenEnd:
    syncType = stQuorum
    num = parseInt(tokens[1].value)
    synclist = tokens[3..^2]

  # Check for FIRST N (...) pattern
  elif tokens.len >= 4 and
       tokens[0].tokenType == ttFirst and
       tokens[1].tokenType == ttNum and
       tokens[2].tokenType == ttParenStart and
       tokens[^1].tokenType == ttParenEnd:
    syncType = stPriority
    num = parseInt(tokens[1].value)
    synclist = tokens[3..^2]

  # Check for N (...) pattern
  elif tokens.len >= 3 and
       tokens[0].tokenType == ttNum and
       tokens[1].tokenType == ttParenStart and
       tokens[^1].tokenType == ttParenEnd:
    syncType = stPriority
    num = parseInt(tokens[0].value)
    synclist = tokens[2..^2]

  # Simple list pattern
  else:
    syncType = stPriority
    num = 1
    synclist = tokens

  var hasStar = false
  var members = newCaseInsensitiveSet()

  for i, token in synclist:
    if i mod 2 == 1:
      # Odd elements should be commas
      if i == synclist.len - 1:
        raise newException(ValueError,
          fmt"Unparsable synchronous_standby_names value '{value}': Unexpected token at {token.pos}")
      if token.tokenType != ttComma:
        raise newException(ValueError,
          fmt"Unparsable synchronous_standby_names value '{value}': Expected comma at {token.pos}")
    else:
      case token.tokenType
      of ttIdent, ttFirst, ttAny:
        members.incl(token.value)
      of ttStar:
        members.incl(token.value)
        hasStar = true
      of ttDquot:
        # Remove quotes and unescape
        let inner = token.value[1..^2].replace("\"\"", "\"")
        members.incl(inner)
      else:
        raise newException(ValueError,
          fmt"Unparsable synchronous_standby_names value '{value}': Unexpected token at {token.pos}")

  result.syncType = syncType
  result.hasStar = hasStar
  result.num = num
  result.members = members

proc newSyncHandler*(postgresql: pointer): SyncHandler =
  ## Create a new SyncHandler.
  new(result)
  result.postgresql = postgresql
  result.synchronousStandbyNames = ""
  result.ssnData = emptySSN()
  result.primaryFlushLsn = 0
  result.readyReplicas = newCaseInsensitiveDict[int]()

proc shouldCascade(members: CaseInsensitiveDict[Member],
                   replication: CaseInsensitiveDict[JsonNode],
                   member: Member): bool =
  ## Check whether member should cascade from another standby node.
  if member.replicatefromValue.len == 0 or member.replicatefromValue notin members:
    return false

  let upstream = members[member.replicatefromValue]
  if upstream.replicatefromValue.len == 0:
    return upstream.name in replication

  return shouldCascade(members, replication, upstream)

proc newReplicaList*(postgresql: pointer, cluster: dcs.Cluster): ReplicaList =
  ## Create a ReplicaList from cluster state.
  new(result)
  result.replicas = @[]
  result.maxLsn = 0

  # This would need access to Postgresql methods:
  # - synchronousCommit()
  # - pg_stat_replication()
  # - lastOperation()
  # - name

  # For now, create an empty list
  # The actual implementation would query pg_stat_replication

proc handleSynchronousStandbyNamesChange(self: SyncHandler) =
  ## Handle changes of synchronous_standby_names GUC.
  # This would need access to Postgresql methods
  discard

proc processReplicaReadiness(self: SyncHandler, cluster: dcs.Cluster,
                             replicaList: ReplicaList) =
  ## Flag replicas as truly synchronous when they have caught up.
  for replica in replicaList.replicas:
    if replica.applicationName notin self.readyReplicas and
       replica.applicationName in self.ssnData.members:
      # Check if replica has caught up
      if replica.lsn >= self.primaryFlushLsn:
        if replica.syncState in ["sync", "quorum", "potential"]:
          self.readyReplicas[replica.applicationName] = replica.pid

proc currentState*(self: SyncHandler, cluster: dcs.Cluster): SyncState =
  ## Find the best candidates to be synchronous standbys.
  ##
  ## :param cluster: current cluster topology from DCS
  ## :returns: current synchronous replication state
  self.handleSynchronousStandbyNamesChange()

  let replicaList = newReplicaList(self.postgresql, cluster)
  self.processReplicaReadiness(cluster, replicaList)

  var active = newCaseInsensitiveSet()
  var syncConfirmed = newCaseInsensitiveSet()

  let globalConf = getGlobalConfig()
  let syncNodeCount = globalConf.synchronousNodeCount()
  let syncNodeMaxlag = globalConf.maximumLagOnSyncnode()

  # Sort replicas preferring those without nofailover
  var sortedReplicas = replicaList.replicas.sorted(proc(a, b: Replica): int =
    if a.nofailover != b.nofailover:
      return if a.nofailover: 1 else: -1
    if a.syncPriority != b.syncPriority:
      return b.syncPriority - a.syncPriority
    if a.syncState != b.syncState:
      return if a.syncState == "sync": -1 else: 1
    return int(b.lsn - a.lsn)
  )

  for replica in sortedReplicas:
    if syncNodeMaxlag <= 0 or replicaList.maxLsn - replica.lsn <= syncNodeMaxlag:
      active.incl(replica.applicationName)
      if replica.syncState == "sync" and replica.applicationName in self.readyReplicas:
        syncConfirmed.incl(replica.applicationName)
      if active.len >= syncNodeCount:
        break

  result.syncType = self.ssnData.syncType
  result.numsync = self.ssnData.num
  result.sync = if self.ssnData.hasStar: newCaseInsensitiveSet() else: self.ssnData.members
  result.syncConfirmed = syncConfirmed
  result.active = active

proc setSynchronousStandbyNames*(self: SyncHandler, sync: seq[string],
                                  num: Option[int] = none(int)) =
  ## Construct and set synchronous_standby_names GUC value.
  ##
  ## :param sync: set of nodes to sync to
  ## :param num: specifies number of nodes to sync to (for quorum commit)
  var syncList = sync
  var hasAsterisk = "*" in sync

  # Special case: if sync nodes set is empty but num >= 1, use '*'
  if num.isSome and num.get >= 1 and sync.len == 0:
    hasAsterisk = true

  if hasAsterisk:
    syncList = @["*"]
  else:
    # Sort and quote names
    syncList = sync.sorted().mapIt(quoteStandbyName(it))

  var syncParam: string
  var effectiveNum = num.get(syncList.len)

  if syncList.len > 1:
    let joined = syncList.join(",")
    let globalConf = getGlobalConfig()
    let isQuorumMode = globalConf.isQuorumCommitMode()

    if isQuorumMode:
      syncParam = fmt"ANY {effectiveNum} ({joined})"
    else:
      syncParam = fmt"{effectiveNum} ({joined})"
  elif syncList.len == 1:
    syncParam = syncList[0]
  else:
    syncParam = ""

  # Store the synchronous_standby_names value
  # In a full implementation, this would update postgresql.auto.conf and trigger a reload
  self.synchronousStandbyNames = syncParam
  logger.info(fmt"Setting synchronous_standby_names to '{syncParam}'")

