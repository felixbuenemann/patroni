## Define general variables and functions for patroni.
##
## :var PATRONI_ENV_PREFIX: prefix for Patroni related configuration environment variables.
## :var KUBERNETES_ENV_PREFIX: prefix for Kubernetes related configuration environment variables.
## :var MIN_PSYCOPG2: minimum version of psycopg2 required by Patroni to work.
## :var MIN_PSYCOPG3: minimum version of psycopg required by Patroni to work.

import std/[strutils, sequtils]

const
  PATRONI_ENV_PREFIX* = "PATRONI_"
  KUBERNETES_ENV_PREFIX* = "KUBERNETES_"
  MIN_PSYCOPG2*: tuple[major, minor, patch: int] = (2, 5, 4)
  MIN_PSYCOPG3*: tuple[major, minor, patch: int] = (3, 0, 0)

# Re-export global_config for convenience
import ./global_config
export global_config

proc parseVersion*(version: string): seq[int] =
  ## Convert *version* from human-readable format to sequence of integers.
  ##
  ## .. note::
  ##     Designed for easy comparison of software versions.
  ##
  ## :param version: human-readable software version, e.g. ``2.5.4.dev1 (dt dec pq3 ext lo64)``.
  ##
  ## :returns: sequence of *version* parts, each part as an integer.
  ##
  ## :Example:
  ##
  ##     parseVersion("2.5.4.dev1 (dt dec pq3 ext lo64)")
  ##     # => @[2, 5, 4]
  result = @[]
  let versionPart = version.split(' ')[0]
  for e in versionPart.split('.'):
    try:
      result.add(parseInt(e))
    except ValueError:
      break
