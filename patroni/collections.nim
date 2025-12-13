## Patroni custom object types somewhat like collections module.
##
## Provides a case insensitive dict and set object types, and EMPTY_DICT frozen dictionary object.

import std/[tables, hashes, strutils]

type
  CaseInsensitiveSet* = ref object
    ## A case-insensitive set-like object.
    ##
    ## Implements all methods and operations of MutableSet. All values are expected to be strings.
    ## The structure remembers the case of the last value set, however, contains testing is case insensitive.
    values: Table[string, string]

  CaseInsensitiveDict*[V] = ref object
    ## A case-insensitive dict-like object.
    ##
    ## Implements all methods and operations of MutableMapping as well as dict's copy.
    ## All keys are expected to be strings. The structure remembers the case of the last key to be set,
    ## and iter, keys, items will contain case-sensitive keys.
    ## However, querying and contains testing is case insensitive.
    values: OrderedTable[string, tuple[key: string, value: V]]

  FrozenDict* = ref object
    ## Frozen dictionary object.
    values: Table[string, string]

# CaseInsensitiveSet implementation

proc newCaseInsensitiveSet*(values: openArray[string] = []): CaseInsensitiveSet =
  ## Create a new instance of CaseInsensitiveSet with the given values.
  ##
  ## :param values: values to be added to the set.
  new(result)
  result.values = initTable[string, string]()
  for v in values:
    result.values[v.toLowerAscii()] = v

proc `$`*(self: CaseInsensitiveSet): string =
  ## Get set values for printing.
  ##
  ## :returns: set of values in string format.
  var vals: seq[string] = @[]
  for v in self.values.values:
    vals.add(v)
  result = "{" & vals.join(", ") & "}"

proc repr*(self: CaseInsensitiveSet): string =
  ## Get a string representation of the set.
  ##
  ## Provide a helpful way of recreating the set.
  ##
  ## :returns: representation of the set, showing its values.
  var vals: seq[string] = @[]
  for v in self.values.values:
    vals.add("'" & v & "'")
  result = "<CaseInsensitiveSet(" & vals.join(", ") & ") at " & $cast[int](self) & ">"

proc contains*(self: CaseInsensitiveSet, value: string): bool =
  ## Check if set contains value.
  ##
  ## The check is performed case-insensitively.
  ##
  ## :param value: value to be checked.
  ## :returns: true if value is already in the set, false otherwise.
  result = value.toLowerAscii() in self.values

proc `in`*(value: string, self: CaseInsensitiveSet): bool =
  result = self.contains(value)

iterator items*(self: CaseInsensitiveSet): string =
  ## Iterate over the values in this set.
  ##
  ## :yields: values from set.
  for v in self.values.values:
    yield v

proc len*(self: CaseInsensitiveSet): int =
  ## Get the length of this set.
  ##
  ## :returns: number of values in the set.
  result = self.values.len

proc add*(self: CaseInsensitiveSet, value: string) =
  ## Add value to this set.
  ##
  ## Search is performed case-insensitively. If value is already in the set, overwrite it with value,
  ## so we "remember" the last case of value.
  ##
  ## :param value: value to be added to the set.
  self.values[value.toLowerAscii()] = value

proc incl*(self: CaseInsensitiveSet, value: string) =
  ## Alias for add.
  self.add(value)

proc remove*(self: CaseInsensitiveSet, value: string) =
  ## Remove value from this set.
  ##
  ## Search is performed case-insensitively. If value is not present in the set, no exception is raised.
  ##
  ## :param value: value to be removed from the set.
  self.values.del(value.toLowerAscii())

proc excl*(self: CaseInsensitiveSet, value: string) =
  ## Alias for remove.
  self.remove(value)

proc issubset*(self, other: CaseInsensitiveSet): bool =
  ## Check if this set is a subset of other.
  ##
  ## :param other: another set to be compared with this set.
  ## :returns: true if this set is a subset of other, else false.
  for key in self.values.keys:
    if key notin other.values:
      return false
  result = true

proc `<=`*(self, other: CaseInsensitiveSet): bool =
  result = self.issubset(other)

proc isSubsetOf*(self, other: CaseInsensitiveSet): bool =
  ## Check if this set is a subset of other.
  ##
  ## :param other: another set to be compared with this set.
  ## :returns: true if this set is a subset of other, else false.
  result = self.issubset(other)

proc isProperSubsetOf*(self, other: CaseInsensitiveSet): bool =
  ## Check if this set is a proper subset of other (subset but not equal).
  ##
  ## :param other: another set to be compared with this set.
  ## :returns: true if this set is a proper subset of other, else false.
  result = self.len < other.len and self.issubset(other)

proc `==`*(self, other: CaseInsensitiveSet): bool =
  ## Check if two sets are equal.
  ##
  ## :param other: another set to be compared with this set.
  ## :returns: true if sets contain the same values, else false.
  if self.len != other.len:
    return false
  for key in self.values.keys:
    if key notin other.values:
      return false
  result = true

proc union*(self, other: CaseInsensitiveSet): CaseInsensitiveSet =
  ## Return a new set containing all elements from both sets.
  ##
  ## :param other: another set to union with this set.
  ## :returns: a new set containing all elements from both sets.
  result = newCaseInsensitiveSet()
  for v in self.values.values:
    result.values[v.toLowerAscii()] = v
  for v in other.values.values:
    result.values[v.toLowerAscii()] = v

proc intersection*(self, other: CaseInsensitiveSet): CaseInsensitiveSet =
  ## Return a new set containing elements common to both sets.
  ##
  ## :param other: another set to intersect with this set.
  ## :returns: a new set containing common elements.
  result = newCaseInsensitiveSet()
  for key, v in self.values.pairs:
    if key in other.values:
      result.values[key] = v

proc difference*(self, other: CaseInsensitiveSet): CaseInsensitiveSet =
  ## Return a new set containing elements in this set but not in other.
  ##
  ## :param other: another set to subtract from this set.
  ## :returns: a new set containing elements in self but not in other.
  result = newCaseInsensitiveSet()
  for key, v in self.values.pairs:
    if key notin other.values:
      result.values[key] = v

proc toSeq*(self: CaseInsensitiveSet): seq[string] =
  ## Convert set to sequence of values.
  result = @[]
  for v in self.values.values:
    result.add(v)

# CaseInsensitiveDict implementation

proc newCaseInsensitiveDict*[V](data: openArray[(string, V)] = []): CaseInsensitiveDict[V] =
  ## Create a new instance of CaseInsensitiveDict with the given data.
  ##
  ## :param data: initial dictionary to create a CaseInsensitiveDict from.
  new(result)
  result.values = initOrderedTable[string, tuple[key: string, value: V]]()
  for (k, v) in data:
    result[k] = v

proc `[]=`*[V](self: CaseInsensitiveDict[V], key: string, value: V) =
  ## Assign value to key in this dict.
  ##
  ## key is searched/stored case-insensitively in the dict.
  ##
  ## :param key: key to be created or updated in the dict.
  ## :param value: value for key.
  self.values[key.toLowerAscii()] = (key, value)

proc `[]`*[V](self: CaseInsensitiveDict[V], key: string): V =
  ## Get the value corresponding to key.
  ##
  ## key is searched case-insensitively in the dict.
  ##
  ## :param key: key to be searched in the dict.
  ## :returns: value corresponding to key.
  result = self.values[key.toLowerAscii()].value

proc del*[V](self: CaseInsensitiveDict[V], key: string) =
  ## Remove key from this dict.
  ##
  ## key is searched case-insensitively in the dict.
  ##
  ## :param key: key to be removed from the dict.
  self.values.del(key.toLowerAscii())

iterator keys*[V](self: CaseInsensitiveDict[V]): string =
  ## Iterate over keys of this dict.
  ##
  ## :yields: each key present in the dict. Yields each key with its last case that has been stored.
  for (k, _) in self.values.values:
    yield k

iterator values*[V](self: CaseInsensitiveDict[V]): V =
  ## Iterate over values of this dict.
  for (_, v) in self.values.values:
    yield v

iterator pairs*[V](self: CaseInsensitiveDict[V]): (string, V) =
  ## Iterate over key-value pairs of this dict.
  for (k, v) in self.values.values:
    yield (k, v)

iterator items*[V](self: CaseInsensitiveDict[V]): (string, V) =
  ## Alias for pairs.
  for item in self.pairs:
    yield item

proc len*[V](self: CaseInsensitiveDict[V]): int =
  ## Get the length of this dict.
  ##
  ## :returns: number of keys in the dict.
  result = self.values.len

proc contains*[V](self: CaseInsensitiveDict[V], key: string): bool =
  ## Check if key exists in the dict.
  result = self.values.hasKey(key.toLowerAscii())

proc hasKey*[V](self: CaseInsensitiveDict[V], key: string): bool =
  ## Check if key exists in the dict.
  result = self.contains(key)

proc copy*[V](self: CaseInsensitiveDict[V]): CaseInsensitiveDict[V] =
  ## Create a copy of this dict.
  ##
  ## :return: a new dict object with the same keys and values of this dict.
  new(result)
  result.values = initOrderedTable[string, tuple[key: string, value: V]]()
  for (k, v) in self.values.values:
    result.values[k.toLowerAscii()] = (k, v)

proc repr*[V](self: CaseInsensitiveDict[V]): string =
  ## Get a string representation of the dict.
  ##
  ## Provide a helpful way of recreating the dict.
  ##
  ## :returns: representation of the dict, showing its keys and values.
  var items: seq[string] = @[]
  for (k, v) in self.values.values:
    items.add("'" & k & "': " & $v)
  result = "<CaseInsensitiveDict{" & items.join(", ") & "} at " & $cast[int](self) & ">"

proc getOrDefault*[V](self: CaseInsensitiveDict[V], key: string, default: V): V =
  ## Get value for key or return default if not found.
  let lowerKey = key.toLowerAscii()
  if self.values.hasKey(lowerKey):
    result = self.values[lowerKey].value
  else:
    result = default

proc get*[V](self: CaseInsensitiveDict[V], key: string, default: V): V =
  ## Get value for key or return default if not found.
  result = self.getOrDefault(key, default)

# FrozenDict implementation

proc newFrozenDict*(data: openArray[(string, string)] = []): FrozenDict =
  ## Create a new instance of FrozenDict with given data.
  new(result)
  result.values = initTable[string, string]()
  for (k, v) in data:
    result.values[k] = v

iterator keys*(self: FrozenDict): string =
  ## Iterate over keys of this dict.
  for k in self.values.keys:
    yield k

iterator values*(self: FrozenDict): string =
  ## Iterate over values of this dict.
  for v in self.values.values:
    yield v

iterator pairs*(self: FrozenDict): (string, string) =
  ## Iterate over key-value pairs of this dict.
  for (k, v) in self.values.pairs:
    yield (k, v)

proc len*(self: FrozenDict): int =
  ## Get the length of this dict.
  result = self.values.len

proc `[]`*(self: FrozenDict, key: string): string =
  ## Get the value corresponding to key.
  result = self.values[key]

proc contains*(self: FrozenDict, key: string): bool =
  ## Check if key exists in the dict.
  result = key in self.values

proc copy*(self: FrozenDict): Table[string, string] =
  ## Create a copy of this dict.
  ##
  ## :return: a new dict object with the same keys and values of this dict.
  result = initTable[string, string]()
  for (k, v) in self.values.pairs:
    result[k] = v

# Empty frozen dict singleton
let EMPTY_DICT* = newFrozenDict()
