## Implements *global_config* facilities.
##
## The GlobalConfig object provides convenient methods to access/check configuration values.

import std/[tables, json, options, strutils]
import ./collections
import ./utils

type
  # Forward declaration for Cluster type
  ClusterConfig* = ref object
    modifyVersion*: int
    data*: Table[string, JsonNode]

  Cluster* = ref object
    config*: ClusterConfig

  GlobalConfig* = ref object
    ## A class that wraps global configuration and provides convenient methods to access/check values.
    config: Table[string, JsonNode]

# Global singleton instance
var globalConfigInstance*: GlobalConfig

proc newGlobalConfig*(): GlobalConfig =
  ## Initialize GlobalConfig object.
  new(result)
  result.config = initTable[string, JsonNode]()

proc clusterHasValidConfig(cluster: Cluster): bool =
  ## Check if provided cluster object has a valid global configuration.
  ##
  ## :param cluster: the currently known cluster state from DCS.
  ## :returns: true if provided cluster object has a valid global configuration, otherwise false.
  result = cluster != nil and cluster.config != nil and cluster.config.modifyVersion != 0

proc update*(self: GlobalConfig, cluster: Cluster, default: Table[string, JsonNode] = initTable[string, JsonNode]()) =
  ## Update with the new global configuration from the Cluster object view.
  ##
  ## .. note::
  ##     Update happens in-place and is executed only from the main heartbeat thread.
  ##
  ## :param cluster: the currently known cluster state from DCS.
  ## :param default: default configuration, which will be used if there is no valid cluster.config.

  # Try to protect from the case when DCS was wiped out
  if clusterHasValidConfig(cluster):
    self.config = cluster.config.data
  elif default.len > 0:
    self.config = default

proc fromCluster*(self: GlobalConfig, cluster: Cluster): GlobalConfig =
  ## Return GlobalConfig instance from the provided Cluster object view.
  ##
  ## .. note::
  ##     If the provided cluster object doesn't have a valid global configuration we return
  ##     the last known valid state of the GlobalConfig object.
  ##
  ##     This method is used when we need to have the most up-to-date values in the global configuration,
  ##     but we don't want to update the global object.
  ##
  ## :param cluster: the currently known cluster state from DCS.
  ## :returns: GlobalConfig object.

  if not clusterHasValidConfig(cluster):
    return self

  result = newGlobalConfig()
  result.update(cluster)

proc get*(self: GlobalConfig, name: string): JsonNode =
  ## Gets global configuration value by name.
  ##
  ## :param name: parameter name.
  ## :returns: configuration value or nil if it is missing.
  if name in self.config:
    result = self.config[name]
  else:
    result = nil

proc getStr*(self: GlobalConfig, name: string, default: string = ""): string =
  ## Gets global configuration value by name as string.
  let val = self.get(name)
  if val != nil and val.kind == JString:
    result = val.getStr()
  else:
    result = default

proc checkMode*(self: GlobalConfig, mode: string): bool =
  ## Checks whether the certain parameter is enabled.
  ##
  ## :param mode: parameter name, e.g. synchronous_mode, failsafe_mode, pause, check_timeline, and so on.
  ## :returns: true if parameter mode is enabled in the global configuration.
  let val = self.get(mode)
  if val == nil:
    return false
  case val.kind
  of JBool:
    result = val.getBool()
  of JString:
    result = parseBool(val.getStr())
  of JInt:
    result = val.getInt() != 0
  else:
    result = false

proc isPaused*(self: GlobalConfig): bool =
  ## true if cluster is in maintenance mode.
  result = self.checkMode("pause")

proc isQuorumCommitMode*(self: GlobalConfig): bool =
  ## Returns true if quorum commit replication is requested.
  let val = self.get("synchronous_mode")
  if val != nil and val.kind == JString:
    result = val.getStr().toLowerAscii() == "quorum"
  else:
    result = false

proc isStandbyCluster*(self: GlobalConfig): bool =
  ## true if global configuration has a valid standby_cluster section.
  let config = self.get("standby_cluster")
  if config == nil or config.kind != JObject:
    return false
  result = config.hasKey("host") or config.hasKey("port") or config.hasKey("restore_command")

proc isSynchronousMode*(self: GlobalConfig): bool =
  ## true if synchronous replication is requested and it is not a standby cluster config.
  result = (self.checkMode("synchronous_mode") or self.isQuorumCommitMode()) and not self.isStandbyCluster()

proc isSynchronousModeStrict*(self: GlobalConfig): bool =
  ## true if at least one synchronous node is required.
  result = self.checkMode("synchronous_mode_strict")

proc getStandbyClusterConfig*(self: GlobalConfig): JsonNode =
  ## Get standby_cluster configuration.
  ##
  ## :returns: a copy of standby_cluster configuration.
  result = self.get("standby_cluster")
  if result == nil:
    result = newJObject()

proc getInt*(self: GlobalConfig, name: string, default: int = 0, baseUnit: string = ""): int =
  ## Gets current value of name from the global configuration and try to return it as int.
  ##
  ## :param name: name of the parameter.
  ## :param default: default value if name is not in the configuration or invalid.
  ## :param baseUnit: an optional base unit to convert value of name parameter to.
  ##
  ## :returns: currently configured value of name from the global configuration or default if it is not set or invalid.
  let val = self.get(name)
  if val == nil:
    return default

  var strVal: string
  case val.kind
  of JInt:
    return val.getInt()
  of JString:
    strVal = val.getStr()
  of JFloat:
    return int(val.getFloat())
  else:
    return default

  let parsed = parseInt(strVal, baseUnit)
  if parsed.isSome:
    result = parsed.get()
  else:
    result = default

proc minSynchronousNodes*(self: GlobalConfig): int =
  ## The minimum number of synchronous nodes based on whether synchronous_mode_strict is enabled or not.
  if self.isSynchronousModeStrict():
    result = 1
  else:
    result = 0

proc synchronousNodeCount*(self: GlobalConfig): int =
  ## Currently configured value of synchronous_node_count from the global configuration.
  ##
  ## Assume 1 if it is not set or invalid.
  result = max(self.getInt("synchronous_node_count", 1), self.minSynchronousNodes())

proc maximumLagOnFailover*(self: GlobalConfig): int =
  ## Currently configured value of maximum_lag_on_failover from the global configuration.
  ##
  ## Assume 1048576 if it is not set or invalid.
  result = self.getInt("maximum_lag_on_failover", 1048576)

proc maximumLagOnSyncnode*(self: GlobalConfig): int =
  ## Currently configured value of maximum_lag_on_syncnode from the global configuration.
  ##
  ## Assume -1 if it is not set or invalid.
  result = self.getInt("maximum_lag_on_syncnode", -1)

proc primaryStartTimeout*(self: GlobalConfig): int =
  ## Currently configured value of primary_start_timeout from the global configuration.
  ##
  ## Assume 300 if it is not set or invalid.
  ##
  ## .. note::
  ##     master_start_timeout is still supported to keep backward compatibility.
  let default = 300
  if "primary_start_timeout" in self.config:
    result = self.getInt("primary_start_timeout", default)
  else:
    result = self.getInt("master_start_timeout", default)

proc primaryStopTimeout*(self: GlobalConfig): int =
  ## Currently configured value of primary_stop_timeout from the global configuration.
  ##
  ## Assume 0 if it is not set or invalid.
  ##
  ## .. note::
  ##     master_stop_timeout is still supported to keep backward compatibility.
  let default = 0
  if "primary_stop_timeout" in self.config:
    result = self.getInt("primary_stop_timeout", default)
  else:
    result = self.getInt("master_stop_timeout", default)

proc ignoreSlotsMatchers*(self: GlobalConfig): seq[JsonNode] =
  ## Currently configured value of ignore_slots from the global configuration.
  ##
  ## Assume an empty list if not set.
  let val = self.get("ignore_slots")
  if val != nil and val.kind == JArray:
    result = @[]
    for item in val.items:
      result.add(item)
  else:
    result = @[]

proc maxTimelinesHistory*(self: GlobalConfig): int =
  ## Currently configured value of max_timelines_history from the global configuration.
  ##
  ## Assume 0 if not set or invalid.
  result = self.getInt("max_timelines_history", 0)

proc useSlots*(self: GlobalConfig): bool =
  ## true if cluster is configured to use replication slots.
  let pgConfig = self.get("postgresql")
  if pgConfig != nil and pgConfig.kind == JObject:
    if pgConfig.hasKey("use_slots"):
      let val = pgConfig["use_slots"]
      case val.kind
      of JBool:
        return val.getBool()
      of JString:
        return parseBool(val.getStr())
      else:
        return true
  result = true

proc permanentSlots*(self: GlobalConfig): JsonNode =
  ## Dictionary of permanent slots information from the global configuration.
  result = self.get("permanent_replication_slots")
  if result == nil:
    result = self.get("permanent_slots")
  if result == nil:
    result = self.get("slots")
  if result == nil:
    result = newJObject()

proc memberSlotsTtl*(self: GlobalConfig): int =
  ## Currently configured value of member_slots_ttl from the global configuration converted to seconds.
  ##
  ## Assume 1800 if it is not set or invalid.
  result = self.getInt("member_slots_ttl", 1800, baseUnit = "s")

# Initialize the global singleton
globalConfigInstance = newGlobalConfig()

# Convenience proc to get the global config instance
proc getGlobalConfig*(): GlobalConfig =
  if globalConfigInstance == nil:
    globalConfigInstance = newGlobalConfig()
  result = globalConfigInstance
