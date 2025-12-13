# Package

version       = "4.1.0"
author        = "Alexander Kukushkin, Polina Bungina (Nim port)"
description   = "PostgreSQL High-Available orchestrator and CLI"
license       = "MIT"
srcDir        = "patroni"
bin           = @["main"]
binDir        = "bin"

# Dependencies

requires "nim >= 2.0.0"

# Tasks

task test, "Run tests":
  exec "nim c -r tests/test_all.nim"

task build_release, "Build release binaries":
  exec "nim c -d:release --opt:speed -o:bin/patroni patroni/main.nim"

task check, "Type check all modules":
  exec "nim check patroni/main.nim"
