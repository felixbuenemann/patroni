## Tests for patroni/collections module.
## Tests CaseInsensitiveSet, CaseInsensitiveDict, and FrozenDict types.

import std/[unittest, strutils, tables]
import ../patroni/collections

suite "CaseInsensitiveSet Creation":
  test "create empty set":
    let s = newCaseInsensitiveSet()
    check s.len == 0

  test "create set from array":
    let s = newCaseInsensitiveSet(@["a", "b", "c"])
    check s.len == 3
    check "a" in s
    check "b" in s
    check "c" in s

  test "create set ignores case duplicates":
    let s = newCaseInsensitiveSet(@["abc", "ABC", "Abc"])
    check s.len == 1
    check "abc" in s

suite "CaseInsensitiveSet Contains":
  test "case insensitive contains":
    let s = newCaseInsensitiveSet(@["Hello", "World"])
    check "hello" in s
    check "HELLO" in s
    check "Hello" in s
    check "world" in s
    check "WORLD" in s
    check "World" in s

  test "not in set":
    let s = newCaseInsensitiveSet(@["a", "b"])
    check "c" notin s
    check "C" notin s

suite "CaseInsensitiveSet Add and Remove":
  test "add element":
    var s = newCaseInsensitiveSet()
    s.add("test")
    check "test" in s
    check s.len == 1

  test "add preserves case":
    var s = newCaseInsensitiveSet()
    s.add("Test")
    # Last added case is preserved
    var found = ""
    for v in s:
      found = v
    check found == "Test"

  test "incl is alias for add":
    var s = newCaseInsensitiveSet()
    s.incl("item")
    check "item" in s

  test "remove element":
    var s = newCaseInsensitiveSet(@["a", "b", "c"])
    s.remove("b")
    check "b" notin s
    check s.len == 2

  test "remove case insensitive":
    var s = newCaseInsensitiveSet(@["ABC"])
    s.remove("abc")
    check "ABC" notin s
    check s.len == 0

  test "excl is alias for remove":
    var s = newCaseInsensitiveSet(@["test"])
    s.excl("TEST")
    check s.len == 0

suite "CaseInsensitiveSet Iteration":
  test "iterate over set":
    let s = newCaseInsensitiveSet(@["a", "b", "c"])
    var count = 0
    for item in s:
      check item in ["a", "b", "c"]
      inc count
    check count == 3

suite "CaseInsensitiveSet Subset Operations":
  test "issubset returns true for subset":
    let s1 = newCaseInsensitiveSet(@["a", "b"])
    let s2 = newCaseInsensitiveSet(@["a", "b", "c"])
    check s1.issubset(s2)
    check s1 <= s2

  test "issubset returns false for non-subset":
    let s1 = newCaseInsensitiveSet(@["a", "b", "d"])
    let s2 = newCaseInsensitiveSet(@["a", "b", "c"])
    check not s1.issubset(s2)

  test "isSubsetOf alias works":
    let s1 = newCaseInsensitiveSet(@["a", "b"])
    let s2 = newCaseInsensitiveSet(@["a", "b", "c"])
    check s1.isSubsetOf(s2)

  test "isProperSubsetOf":
    let s1 = newCaseInsensitiveSet(@["a", "b"])
    let s2 = newCaseInsensitiveSet(@["a", "b", "c"])
    let s3 = newCaseInsensitiveSet(@["a", "b"])
    check s1.isProperSubsetOf(s2)
    check not s1.isProperSubsetOf(s3)  # Equal sets are not proper subsets

suite "CaseInsensitiveSet Set Operations":
  test "union":
    let s1 = newCaseInsensitiveSet(@["a", "b"])
    let s2 = newCaseInsensitiveSet(@["b", "c"])
    let u = s1.union(s2)
    check u.len == 3
    check "a" in u
    check "b" in u
    check "c" in u

  test "intersection":
    let s1 = newCaseInsensitiveSet(@["a", "b", "c"])
    let s2 = newCaseInsensitiveSet(@["b", "c", "d"])
    let i = s1.intersection(s2)
    check i.len == 2
    check "b" in i
    check "c" in i
    check "a" notin i
    check "d" notin i

  test "difference":
    let s1 = newCaseInsensitiveSet(@["a", "b", "c"])
    let s2 = newCaseInsensitiveSet(@["b", "c", "d"])
    let d = s1.difference(s2)
    check d.len == 1
    check "a" in d
    check "b" notin d

  test "equality":
    let s1 = newCaseInsensitiveSet(@["a", "b"])
    let s2 = newCaseInsensitiveSet(@["b", "a"])
    let s3 = newCaseInsensitiveSet(@["a", "b", "c"])
    check s1 == s2
    check not (s1 == s3)

  test "toSeq":
    let s = newCaseInsensitiveSet(@["x", "y", "z"])
    let seq1 = s.toSeq()
    check seq1.len == 3

suite "CaseInsensitiveSet String Representation":
  test "string representation":
    let s = newCaseInsensitiveSet(@["a"])
    let str = $s
    check "a" in str
    check "{" in str
    check "}" in str

  test "repr":
    let s = newCaseInsensitiveSet(@["a", "b"])
    let r = repr(s)
    check "<CaseInsensitiveSet" in r

suite "CaseInsensitiveDict Creation":
  test "create empty dict":
    let d = newCaseInsensitiveDict[string]()
    check d.len == 0

  test "create dict from array":
    let d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    check d.len == 2
    check d["a"] == 1
    check d["b"] == 2

suite "CaseInsensitiveDict Access":
  test "case insensitive key access":
    let d = newCaseInsensitiveDict[string](@[("Hello", "world")])
    check d["hello"] == "world"
    check d["HELLO"] == "world"
    check d["Hello"] == "world"

  test "set and get":
    var d = newCaseInsensitiveDict[int]()
    d["Key"] = 42
    check d["key"] == 42
    check d["KEY"] == 42

  test "overwrite with different case":
    var d = newCaseInsensitiveDict[string]()
    d["key"] = "first"
    d["KEY"] = "second"
    check d["key"] == "second"
    check d.len == 1

suite "CaseInsensitiveDict Delete":
  test "delete key":
    var d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    d.del("a")
    check d.len == 1
    check not d.contains("a")

  test "delete case insensitive":
    var d = newCaseInsensitiveDict[int](@[("ABC", 1)])
    d.del("abc")
    check d.len == 0

suite "CaseInsensitiveDict Contains":
  test "contains":
    let d = newCaseInsensitiveDict[int](@[("test", 1)])
    check d.contains("test")
    check d.contains("TEST")
    check not d.contains("other")

  test "hasKey":
    let d = newCaseInsensitiveDict[int](@[("test", 1)])
    check d.hasKey("test")
    check d.hasKey("TEST")

suite "CaseInsensitiveDict Iteration":
  test "iterate keys":
    let d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    var count = 0
    for k in d.keys:
      check k in ["a", "b"]
      inc count
    check count == 2

  test "iterate values":
    let d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    var sum = 0
    for v in d.values:
      sum += v
    check sum == 3

  test "iterate pairs":
    let d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    var count = 0
    for (k, v) in d.pairs:
      check k in ["a", "b"]
      check v in [1, 2]
      inc count
    check count == 2

suite "CaseInsensitiveDict Copy and Get":
  test "copy dict":
    let d = newCaseInsensitiveDict[int](@[("a", 1), ("b", 2)])
    let c = d.copy()
    check c.len == 2
    check c["a"] == 1

  test "getOrDefault":
    let d = newCaseInsensitiveDict[int](@[("a", 1)])
    check d.getOrDefault("a", 0) == 1
    check d.getOrDefault("b", 99) == 99

  test "get alias":
    let d = newCaseInsensitiveDict[int](@[("a", 1)])
    check d.get("a", 0) == 1
    check d.get("b", 99) == 99

suite "CaseInsensitiveDict String Representation":
  test "repr":
    let d = newCaseInsensitiveDict[int](@[("a", 1)])
    let r = repr(d)
    check "<CaseInsensitiveDict" in r

suite "FrozenDict Creation":
  test "create empty frozen dict":
    let d = newFrozenDict()
    check d.len == 0

  test "create frozen dict from array":
    let d = newFrozenDict(@[("a", "1"), ("b", "2")])
    check d.len == 2
    check d["a"] == "1"
    check d["b"] == "2"

suite "FrozenDict Access":
  test "get value":
    let d = newFrozenDict(@[("key", "value")])
    check d["key"] == "value"

  test "contains":
    let d = newFrozenDict(@[("test", "value")])
    check d.contains("test")
    check "test" in d
    check not d.contains("other")

suite "FrozenDict Iteration":
  test "iterate keys":
    let d = newFrozenDict(@[("a", "1"), ("b", "2")])
    var count = 0
    for k in d.keys:
      inc count
    check count == 2

  test "iterate values":
    let d = newFrozenDict(@[("a", "1"), ("b", "2")])
    var count = 0
    for v in d.values:
      inc count
    check count == 2

  test "iterate pairs":
    let d = newFrozenDict(@[("a", "1")])
    for (k, v) in d.pairs:
      check k == "a"
      check v == "1"

suite "FrozenDict Copy":
  test "copy returns regular table":
    let d = newFrozenDict(@[("a", "1"), ("b", "2")])
    let c = d.copy()
    check len(c) == 2
    check c["a"] == "1"

suite "EMPTY_DICT":
  test "EMPTY_DICT is empty":
    check EMPTY_DICT.len == 0

  test "EMPTY_DICT iteration":
    var count = 0
    for _ in EMPTY_DICT.keys:
      inc count
    check count == 0

when isMainModule:
  echo "test_collections.nim tests completed"
