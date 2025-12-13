## PostgreSQL parameter validation.
##
## This module provides types and procedures for validating PostgreSQL
## configuration parameters across different versions.

import std/[json, options, os, sequtils, strformat, strutils, tables]
import ../collections
import ../exceptions
import ../log
import ../utils

export utils

let logger = getLogger("patroni.postgresql.validator")

type
  TransformableKind* = enum
    ## Kind of transformable validator.
    tkBool
    tkInteger
    tkReal
    tkEnum
    tkEnumBool
    tkString

  Transformable* = ref object
    ## Base class for parameter validators.
    versionFrom*: int
    versionTill*: Option[int]
    case kind*: TransformableKind
    of tkBool:
      discard
    of tkInteger:
      intMinVal*: int
      intMaxVal*: int
      intUnit*: Option[string]
    of tkReal:
      realMinVal*: float
      realMaxVal*: float
      realUnit*: Option[string]
    of tkEnum, tkEnumBool:
      possibleValues*: seq[string]
    of tkString:
      discard

  ValidatorFactoryError* = object of PatroniException
    ## Error raised when validator factory fails.

proc newBoolValidator*(versionFrom: int, versionTill: Option[int] = none(int)): Transformable =
  ## Create a boolean parameter validator.
  new(result)
  result.kind = tkBool
  result.versionFrom = versionFrom
  result.versionTill = versionTill

proc newIntegerValidator*(versionFrom: int, versionTill: Option[int] = none(int),
                          minVal: int, maxVal: int,
                          unit: Option[string] = none(string)): Transformable =
  ## Create an integer parameter validator.
  new(result)
  result.kind = tkInteger
  result.versionFrom = versionFrom
  result.versionTill = versionTill
  result.intMinVal = minVal
  result.intMaxVal = maxVal
  result.intUnit = unit

proc newRealValidator*(versionFrom: int, versionTill: Option[int] = none(int),
                       minVal: float, maxVal: float,
                       unit: Option[string] = none(string)): Transformable =
  ## Create a real/float parameter validator.
  new(result)
  result.kind = tkReal
  result.versionFrom = versionFrom
  result.versionTill = versionTill
  result.realMinVal = minVal
  result.realMaxVal = maxVal
  result.realUnit = unit

proc newEnumValidator*(versionFrom: int, versionTill: Option[int] = none(int),
                       possibleValues: seq[string]): Transformable =
  ## Create an enum parameter validator.
  new(result)
  result.kind = tkEnum
  result.versionFrom = versionFrom
  result.versionTill = versionTill
  result.possibleValues = possibleValues

proc newEnumBoolValidator*(versionFrom: int, versionTill: Option[int] = none(int),
                           possibleValues: seq[string]): Transformable =
  ## Create an enum-or-boolean parameter validator.
  new(result)
  result.kind = tkEnumBool
  result.versionFrom = versionFrom
  result.versionTill = versionTill
  result.possibleValues = possibleValues

proc newStringValidator*(versionFrom: int, versionTill: Option[int] = none(int)): Transformable =
  ## Create a string parameter validator.
  new(result)
  result.kind = tkString
  result.versionFrom = versionFrom
  result.versionTill = versionTill

proc transform*(self: Transformable, name: string, value: string): Option[string] =
  ## Validate and transform a parameter value.
  ##
  ## :param name: Parameter name.
  ## :param value: Parameter value to validate.
  ## :returns: The validated value or none if invalid.
  case self.kind
  of tkBool:
    let parsed = parseBool(value)
    if parsed.isSome:
      return some(value)
    logger.warning(fmt"Removing bool parameter={name} from the config due to invalid value={value}")
    return none(string)

  of tkInteger:
    let unit = if self.intUnit.isSome: self.intUnit.get else: ""
    let parsed = parseInt(value, unit)
    if parsed.isSome:
      let numValue = parsed.get
      if numValue < self.intMinVal:
        logger.warning(fmt"Value={value} of parameter={name} is too low, increasing to {self.intMinVal}{unit}")
        return some($self.intMinVal)
      if numValue > self.intMaxVal:
        logger.warning(fmt"Value={value} of parameter={name} is too big, decreasing to {self.intMaxVal}{unit}")
        return some($self.intMaxVal)
      return some(value)
    logger.warning(fmt"Removing integer parameter={name} from the config due to invalid value={value}")
    return none(string)

  of tkReal:
    let unit = if self.realUnit.isSome: self.realUnit.get else: ""
    let parsed = parseReal(value, unit)
    if parsed.isSome:
      let numValue = parsed.get
      if numValue < self.realMinVal:
        logger.warning(fmt"Value={value} of parameter={name} is too low, increasing to {self.realMinVal}{unit}")
        return some($self.realMinVal)
      if numValue > self.realMaxVal:
        logger.warning(fmt"Value={value} of parameter={name} is too big, decreasing to {self.realMaxVal}{unit}")
        return some($self.realMaxVal)
      return some(value)
    logger.warning(fmt"Removing real parameter={name} from the config due to invalid value={value}")
    return none(string)

  of tkEnum:
    if value.toLowerAscii() in self.possibleValues:
      return some(value)
    logger.warning(fmt"Removing enum parameter={name} from the config due to invalid value={value}")
    return none(string)

  of tkEnumBool:
    let parsed = parseBool(value)
    if parsed.isSome:
      return some(value)
    if value.toLowerAscii() in self.possibleValues:
      return some(value)
    logger.warning(fmt"Removing enum parameter={name} from the config due to invalid value={value}")
    return none(string)

  of tkString:
    return some(value)

# Parameter validators storage
var parameters* = newCaseInsensitiveDict[seq[Transformable]]()
var recoveryParameters* = newCaseInsensitiveDict[seq[Transformable]]()

proc createValidatorFromSpec(spec: JsonNode): Transformable =
  ## Create a validator from a JSON specification.
  let typeStr = spec.getOrDefault("type").getStr("")
  let versionFrom = spec.getOrDefault("version_from").getInt(90100)
  let versionTillNode = spec.getOrDefault("version_till")
  let versionTill = if versionTillNode != nil and versionTillNode.kind == JInt:
    some(versionTillNode.getInt())
  else:
    none(int)

  case typeStr
  of "Bool":
    result = newBoolValidator(versionFrom, versionTill)
  of "Integer":
    let minVal = spec.getOrDefault("min_val").getInt(0)
    let maxVal = spec.getOrDefault("max_val").getInt(int.high)
    let unitNode = spec.getOrDefault("unit")
    let unit = if unitNode != nil and unitNode.kind == JString:
      some(unitNode.getStr())
    else:
      none(string)
    result = newIntegerValidator(versionFrom, versionTill, minVal, maxVal, unit)
  of "Real":
    let minVal = spec.getOrDefault("min_val").getFloat(0.0)
    let maxVal = spec.getOrDefault("max_val").getFloat(float.high)
    let unitNode = spec.getOrDefault("unit")
    let unit = if unitNode != nil and unitNode.kind == JString:
      some(unitNode.getStr())
    else:
      none(string)
    result = newRealValidator(versionFrom, versionTill, minVal, maxVal, unit)
  of "Enum":
    var possibleValues: seq[string] = @[]
    let pvNode = spec.getOrDefault("possible_values")
    if pvNode != nil and pvNode.kind == JArray:
      for v in pvNode:
        possibleValues.add(v.getStr())
    result = newEnumValidator(versionFrom, versionTill, possibleValues)
  of "EnumBool":
    var possibleValues: seq[string] = @[]
    let pvNode = spec.getOrDefault("possible_values")
    if pvNode != nil and pvNode.kind == JArray:
      for v in pvNode:
        possibleValues.add(v.getStr())
    result = newEnumBoolValidator(versionFrom, versionTill, possibleValues)
  of "String":
    result = newStringValidator(versionFrom, versionTill)
  else:
    raise newException(ValidatorFactoryError, fmt"Unknown validator type: {typeStr}")

proc loadValidatorsFromJson(config: JsonNode) =
  ## Load validators from a JSON configuration.
  let paramsNode = config.getOrDefault("parameters")
  if paramsNode != nil and paramsNode.kind == JObject:
    for name, validators in paramsNode.pairs:
      var validatorList: seq[Transformable] = @[]
      if validators.kind == JArray:
        for spec in validators:
          try:
            validatorList.add(createValidatorFromSpec(spec))
          except ValidatorFactoryError as e:
            logger.warning(fmt"Failed to parse validator for {name}: {e.msg}")
      parameters[name] = validatorList

  let recoveryNode = config.getOrDefault("recovery_parameters")
  if recoveryNode != nil and recoveryNode.kind == JObject:
    for name, validators in recoveryNode.pairs:
      var validatorList: seq[Transformable] = @[]
      if validators.kind == JArray:
        for spec in validators:
          try:
            validatorList.add(createValidatorFromSpec(spec))
          except ValidatorFactoryError as e:
            logger.warning(fmt"Failed to parse recovery validator for {name}: {e.msg}")
      recoveryParameters[name] = validatorList

proc initializeDefaultValidators*() =
  ## Initialize default PostgreSQL parameter validators.
  # Add some common validators that are always needed
  parameters["archive_command"] = @[newStringValidator(90300)]
  parameters["archive_mode"] = @[
    newBoolValidator(90300, some(90500)),
    newEnumBoolValidator(90500, none(int), @["always"])
  ]
  parameters["archive_timeout"] = @[
    newIntegerValidator(90300, none(int), 0, 1073741823, some("s"))
  ]
  parameters["autovacuum"] = @[newBoolValidator(90300)]
  parameters["checkpoint_completion_target"] = @[
    newRealValidator(90300, none(int), 0.0, 1.0)
  ]
  parameters["checkpoint_timeout"] = @[
    newIntegerValidator(90300, none(int), 30, 86400, some("s"))
  ]
  parameters["client_min_messages"] = @[
    newEnumValidator(90300, none(int), @[
      "debug5", "debug4", "debug3", "debug2", "debug1",
      "log", "notice", "warning", "error"
    ])
  ]
  parameters["default_statistics_target"] = @[
    newIntegerValidator(90300, none(int), 1, 10000)
  ]
  parameters["effective_cache_size"] = @[
    newIntegerValidator(90300, none(int), 1, int.high, some("8kB"))
  ]
  parameters["hot_standby"] = @[newBoolValidator(90300)]
  parameters["hot_standby_feedback"] = @[newBoolValidator(90300)]
  parameters["listen_addresses"] = @[newStringValidator(90300)]
  parameters["log_autovacuum_min_duration"] = @[
    newIntegerValidator(90300, none(int), -1, 2147483647, some("ms"))
  ]
  parameters["log_checkpoints"] = @[newBoolValidator(90300)]
  parameters["log_connections"] = @[newBoolValidator(90300)]
  parameters["log_destination"] = @[newStringValidator(90300)]
  parameters["log_disconnections"] = @[newBoolValidator(90300)]
  parameters["log_line_prefix"] = @[newStringValidator(90300)]
  parameters["log_lock_waits"] = @[newBoolValidator(90300)]
  parameters["log_min_duration_statement"] = @[
    newIntegerValidator(90300, none(int), -1, 2147483647, some("ms"))
  ]
  parameters["log_statement"] = @[
    newEnumValidator(90300, none(int), @["none", "ddl", "mod", "all"])
  ]
  parameters["log_temp_files"] = @[
    newIntegerValidator(90300, none(int), -1, 2147483647, some("kB"))
  ]
  parameters["logging_collector"] = @[newBoolValidator(90300)]
  parameters["maintenance_work_mem"] = @[
    newIntegerValidator(90300, none(int), 1024, 2147483647, some("kB"))
  ]
  parameters["max_connections"] = @[
    newIntegerValidator(90300, none(int), 1, 262143)
  ]
  parameters["max_locks_per_transaction"] = @[
    newIntegerValidator(90300, none(int), 10, int.high)
  ]
  parameters["max_prepared_transactions"] = @[
    newIntegerValidator(90300, none(int), 0, 262143)
  ]
  parameters["max_replication_slots"] = @[
    newIntegerValidator(90400, none(int), 0, 262143)
  ]
  parameters["max_wal_senders"] = @[
    newIntegerValidator(90300, none(int), 0, 262143)
  ]
  parameters["max_worker_processes"] = @[
    newIntegerValidator(90400, none(int), 0, 262143)
  ]
  parameters["password_encryption"] = @[
    newBoolValidator(90300, some(100000)),
    newEnumValidator(100000, none(int), @["md5", "scram-sha-256"])
  ]
  parameters["port"] = @[
    newIntegerValidator(90300, none(int), 1, 65535)
  ]
  parameters["random_page_cost"] = @[
    newRealValidator(90300, none(int), 0.0, float.high)
  ]
  parameters["shared_buffers"] = @[
    newIntegerValidator(90300, none(int), 16, 1073741823, some("8kB"))
  ]
  parameters["shared_preload_libraries"] = @[newStringValidator(90300)]
  parameters["synchronous_commit"] = @[
    newEnumValidator(90300, some(90600), @["on", "off", "local", "remote_write"]),
    newEnumValidator(90600, none(int), @["on", "off", "local", "remote_write", "remote_apply"])
  ]
  parameters["synchronous_standby_names"] = @[newStringValidator(90300)]
  parameters["track_commit_timestamp"] = @[newBoolValidator(90500)]
  parameters["unix_socket_directories"] = @[newStringValidator(90300)]
  parameters["wal_buffers"] = @[
    newIntegerValidator(90300, none(int), -1, 2147483647, some("8kB"))
  ]
  parameters["wal_keep_segments"] = @[
    newIntegerValidator(90300, some(130000), 0, 2147483647)
  ]
  parameters["wal_keep_size"] = @[
    newIntegerValidator(130000, none(int), 0, 2147483647, some("MB"))
  ]
  parameters["wal_level"] = @[
    newEnumValidator(90300, some(90600), @["minimal", "archive", "hot_standby", "logical"]),
    newEnumValidator(90600, none(int), @["minimal", "replica", "logical"])
  ]
  parameters["wal_log_hints"] = @[newBoolValidator(90400)]
  parameters["work_mem"] = @[
    newIntegerValidator(90300, none(int), 64, 2147483647, some("kB"))
  ]

  # Recovery parameters
  recoveryParameters["archive_cleanup_command"] = @[newStringValidator(90300)]
  recoveryParameters["primary_conninfo"] = @[newStringValidator(90300)]
  recoveryParameters["primary_slot_name"] = @[newStringValidator(90400)]
  recoveryParameters["promote_trigger_file"] = @[newStringValidator(120000)]
  recoveryParameters["recovery_end_command"] = @[newStringValidator(90300)]
  recoveryParameters["recovery_min_apply_delay"] = @[
    newIntegerValidator(90400, none(int), 0, 2147483647, some("ms"))
  ]
  recoveryParameters["recovery_target"] = @[
    newEnumValidator(90400, none(int), @["immediate"])
  ]
  recoveryParameters["recovery_target_action"] = @[
    newEnumValidator(90500, none(int), @["pause", "promote", "shutdown"])
  ]
  recoveryParameters["recovery_target_lsn"] = @[newStringValidator(100000)]
  recoveryParameters["recovery_target_name"] = @[newStringValidator(90400)]
  recoveryParameters["recovery_target_time"] = @[newStringValidator(90300)]
  recoveryParameters["recovery_target_timeline"] = @[newStringValidator(90300)]
  recoveryParameters["recovery_target_xid"] = @[newStringValidator(90300)]
  recoveryParameters["restore_command"] = @[newStringValidator(90300)]
  recoveryParameters["standby_mode"] = @[newBoolValidator(90300, some(120000))]
  recoveryParameters["trigger_file"] = @[newStringValidator(90300, some(120000))]

proc transformParameterValue*(validators: CaseInsensitiveDict[seq[Transformable]],
                              version: int, name: string, value: string,
                              availableGucs: CaseInsensitiveSet): Option[string] =
  ## Validate parameter value for a specific PostgreSQL version.
  ##
  ## :param validators: Dictionary of validators by parameter name.
  ## :param version: PostgreSQL version as integer (e.g., 140000 for 14.0).
  ## :param name: Parameter name.
  ## :param value: Parameter value.
  ## :param availableGucs: Set of available GUCs for this version.
  ## :returns: Validated value or none if invalid.
  let validatorList = validators.getOrDefault(name, @[])
  for validator in validatorList:
    if version >= validator.versionFrom and
       (validator.versionTill.isNone or version < validator.versionTill.get):
      return validator.transform(name, value)

  # No validator found, check if parameter exists in available GUCs
  if name in availableGucs:
    return some(value)

  logger.warning(fmt"Removing unexpected parameter={name} value={value} from the config")
  return none(string)

proc transformPostgresqlParameterValue*(version: int, name: string, value: string,
                                        availableGucs: CaseInsensitiveSet): Option[string] =
  ## Validate a PostgreSQL parameter value.
  ##
  ## :param version: PostgreSQL version.
  ## :param name: Parameter name.
  ## :param value: Parameter value.
  ## :param availableGucs: Available GUCs for this version.
  ## :returns: Validated value or none if invalid.

  # Extension GUCs (contain '.') are passed through unless we have a validator
  if '.' in name and name notin parameters:
    return some(value)

  # Recovery parameters are handled separately
  if name in recoveryParameters:
    return none(string)

  return transformParameterValue(parameters, version, name, value, availableGucs)

proc transformRecoveryParameterValue*(version: int, name: string, value: string,
                                      availableGucs: CaseInsensitiveSet): Option[string] =
  ## Validate a recovery parameter value.
  ##
  ## :param version: PostgreSQL version.
  ## :param name: Parameter name.
  ## :param value: Parameter value.
  ## :param availableGucs: Available GUCs for this version.
  ## :returns: Validated value or none if invalid.

  # For PG < 12, recovery parameters aren't in describe-config output
  let effectiveGucs = if version >= 120000:
    availableGucs
  else:
    var gucs = newCaseInsensitiveSet()
    for key in recoveryParameters.keys:
      gucs.incl(key)
    gucs

  return transformParameterValue(recoveryParameters, version, name, value, effectiveGucs)

# Initialize default validators on module load
initializeDefaultValidators()

