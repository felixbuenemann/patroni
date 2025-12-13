## Available parameters module.
##
## Provides utilities for finding and loading PostgreSQL parameter
## validation YAML files from the package directory.

import std/[algorithm, logging, os, sequtils, strutils]
import ../../log

let logger = getLogger("patroni.postgresql.available_parameters")

proc getValidatorFiles*(): seq[string] =
  ## Recursively find YAML files from the current package directory.
  ##
  ## :returns: A sequence of file paths representing validator files.
  result = @[]

  # Get the directory containing this module
  let confDir = getAppDir() / "patroni" / "postgresql" / "available_parameters"

  if not dirExists(confDir):
    return

  proc walkDir(dir: string): seq[string] =
    var files: seq[string] = @[]
    for kind, path in walkDir(dir):
      case kind
      of pcFile:
        let name = extractFilename(path).toLowerAscii()
        if name.endsWith(".yml") or name.endsWith(".yaml"):
          files.add(path)
        elif not name.endsWith(".py") and not name.endsWith(".pyc") and not name.endsWith(".nim"):
          logger.log(lvlInfo, "Ignored a non-YAML file found under available_parameters directory: " & path)
      of pcDir:
        try:
          files.add(walkDir(path))
        except OSError:
          logger.log(lvlDebug, "Can't list directory " & path)
      else:
        discard
    return files.sorted()

  result = walkDir(confDir)

iterator validatorFiles*(): string =
  ## Iterator version of getValidatorFiles.
  ##
  ## :yields: File paths representing validator files.
  for f in getValidatorFiles():
    yield f
