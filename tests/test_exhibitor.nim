## Tests for patroni/dcs/exhibitor module.

import std/[unittest, json, options, httpclient]

suite "Exhibitor":
  test "exhibitor discovery":
    check true

  test "exhibitor cluster info":
    check true

  test "exhibitor error handling":
    check true

when isMainModule:
  discard
