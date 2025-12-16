## Factory module for MPP implementations.
##
## This module exists to break the circular dependency between mpp.nim and citus.nim.
## It imports both modules and provides the getMpp factory function.

import std/[json, tables]
import ./mpp
import ./mpp/citus

export mpp

proc getMpp*(config: JsonNode): AbstractMPP =
  ## Get the appropriate MPP implementation based on configuration.
  ##
  ## :param config: Patroni configuration.
  ## :returns: MPP implementation (Citus) or NullMPP if not configured.
  if "citus" in config:
    let citusConfig = config["citus"]
    if citusConfig.kind == JObject:
      var citusConfigTable = initTable[string, JsonNode]()
      for key, value in citusConfig.pairs:
        citusConfigTable[key] = value
      return newCitus(citusConfigTable)

  return newNullMPP()

proc getHandler*(mpp: AbstractMPP, postgresql: pointer): AbstractMPPHandler =
  ## Get handler implementation for this MPP.
  ##
  ## :param mpp: The MPP configuration object.
  ## :param postgresql: Reference to Postgresql object.
  ## :returns: Handler implementation.
  if mpp of Citus:
    return newCitusHandler(postgresql, mpp.config)
  return newNullMPPHandler(postgresql, mpp.config)
