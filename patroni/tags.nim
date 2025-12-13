## Tags handling.

import std/[tables, options, strutils]
import ./utils

type
  Tags* = ref object of RootObj
    ## An abstract class that encapsulates all the ``tags`` logic.
    ##
    ## Child classes that want to use provided facilities must implement ``tags`` abstract property.
    ##
    ## .. note::
    ##     Due to backward-compatibility reasons, old tags may have a less strict type conversion than new ones.

# Forward declaration for abstract method
method tags*(self: Tags): Table[string, string] {.base.} =
  ## Configured tags.
  ##
  ## Must be implemented in a child class.
  raise newException(CatchableError, "Not implemented")

proc filterTags*(tags: Table[string, string]): Table[string, string] =
  ## Get tags configured for this node, if any.
  ##
  ## Handle both predefined Patroni tags and custom defined tags.
  ##
  ## .. note::
  ##     A custom tag is any tag added to the configuration ``tags`` section that is not one of ``clonefrom``,
  ##     ``nofailover``, ``noloadbalance``,``nosync`` or ``nostream``.
  ##
  ##     For most of the Patroni predefined tags, the returning object will only contain them if they are enabled as
  ##     they all are boolean values that default to disabled.
  ##     However ``nofailover`` tag is always returned if ``failover_priority`` tag is defined. In this case, we need
  ##     both values to see if they are contradictory and the ``nofailover`` value should be used.
  ##     The same rule applies for ``nosync`` and ``sync_priority`` tags.
  ##
  ## :returns: a dictionary of tags set for this node. The key is the tag name, and the value is the corresponding
  ##     tag value.
  result = initTable[string, string]()
  for tag, value in tags.pairs:
    let shouldInclude = (tag notin ["clonefrom", "nofailover", "noloadbalance", "nosync", "nostream"]) or
                        (value.len > 0 and value != "false" and value != "0") or
                        (tag == "nofailover" and "failover_priority" in tags) or
                        (tag == "nosync" and "sync_priority" in tags)
    if shouldInclude:
      result[tag] = value

proc clonefrom*(self: Tags): bool =
  ## ``True`` if ``clonefrom`` tag is ``True``, else ``False``.
  let tagsTable = self.tags
  if "clonefrom" in tagsTable:
    result = parseBool(tagsTable["clonefrom"])
  else:
    result = false

proc priorityTag(self: Tags, boolName: string, priorityName: string): int =
  ## Common logic for obtaining the value of a priority tag from ``tags`` if defined.
  ##
  ## If boolean tag is defined as ``True``, this will return ``0``. Otherwise, it will return the value of
  ## the respective priority tag, defaulting to ``1`` if it's not defined or invalid.
  ##
  ## :param boolName: name of the boolean tag (``nofailover``. ``nosync``).
  ## :param priorityName: name of the priority tag (``failover_priority``, ``sync_priority``).
  ##
  ## :returns: integer value based on the defined tags.
  let tagsTable = self.tags
  var fromTags: Option[string] = none(string)
  if boolName in tagsTable:
    fromTags = some(tagsTable[boolName])

  var priority = 1
  if priorityName in tagsTable:
    let parsed = parseInt(tagsTable[priorityName])
    if parsed.isSome:
      priority = parsed.get()

  if fromTags.isSome and parseBool(fromTags.get()):
    result = 0
  else:
    result = priority

proc boolTag(self: Tags, boolName: string, priorityName: string): bool =
  ## Common logic for obtaining the value of a boolean tag from ``tags`` if defined.
  ##
  ## If boolean tag is not defined, this methods returns ``True`` if priority tag is non-positive,
  ## ``False`` otherwise.
  ##
  ## :param boolName: name of the boolean tag (``nofailover``. ``nosync``).
  ## :param priorityName: name of the priority tag (``failover_priority``, ``sync_priority``).
  ##
  ## :returns: boolean value based on the defined tags.
  let tagsTable = self.tags
  if boolName in tagsTable:
    # Value of bool tag takes precedence over priority tag
    return parseBool(tagsTable[boolName])

  if priorityName in tagsTable:
    let priority = parseInt(tagsTable[priorityName])
    if priority.isSome:
      return priority.get() <= 0

  result = false

proc nofailover*(self: Tags): bool =
  ## ``True`` if node configuration doesn't allow it to become primary, ``False`` otherwise.
  result = self.boolTag("nofailover", "failover_priority")

proc failoverPriority*(self: Tags): int =
  ## Value of ``failover_priority`` from ``tags`` if defined, otherwise derived from ``nofailover``.
  result = self.priorityTag("nofailover", "failover_priority")

proc noloadbalance*(self: Tags): bool =
  ## ``True`` if ``noloadbalance`` is ``True``, else ``False``.
  let tagsTable = self.tags
  if "noloadbalance" in tagsTable:
    result = parseBool(tagsTable["noloadbalance"])
  else:
    result = false

proc nosync*(self: Tags): bool =
  ## ``True`` if node configuration doesn't allow it to become synchronous, ``False`` otherwise.
  result = self.boolTag("nosync", "sync_priority")

proc syncPriority*(self: Tags): int =
  ## Value of ``sync_priority`` from ``tags`` if defined, otherwise derived from ``nosync``.
  result = self.priorityTag("nosync", "sync_priority")

proc replicatefrom*(self: Tags): Option[string] =
  ## Value of ``replicatefrom`` tag, if any.
  let tagsTable = self.tags
  if "replicatefrom" in tagsTable:
    result = some(tagsTable["replicatefrom"])
  else:
    result = none(string)

proc nostream*(self: Tags): bool =
  ## ``True`` if ``nostream`` is ``True``, else ``False``.
  let tagsTable = self.tags
  if "nostream" in tagsTable:
    result = parseBool(tagsTable["nostream"])
  else:
    result = false
