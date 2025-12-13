## Dynamic module loading helpers.
##
## Provides procedures to search for and load implementations of specific
## interfaces in a package. In Nim, this is handled at compile time rather
## than runtime like Python.

import std/[logging, os, strformat, strutils, tables]
import ./log

let logger = getLogger("patroni.dynamic_loader")

type
  ModuleInfo* = object
    ## Information about a loadable module.
    name*: string
    path*: string

proc iterModules*(package: string): seq[string] =
  ## Get names of modules from package.
  ##
  ## In Nim, modules are statically linked at compile time.
  ## This procedure returns a list of known module names for the package.
  ##
  ## :param package: a package name to search modules in, e.g. "patroni.dcs".
  ## :returns: list of known module names.
  result = @[]

  # For DCS backends
  if package == "patroni.dcs":
    result = @[
      "patroni.dcs.etcd",
      "patroni.dcs.etcd3",
      "patroni.dcs.consul",
      "patroni.dcs.zookeeper",
      "patroni.dcs.kubernetes",
      "patroni.dcs.raft",
      "patroni.dcs.exhibitor"
    ]
  # For watchdog implementations
  elif package == "patroni.watchdog":
    result = @[
      "patroni.watchdog.base",
      "patroni.watchdog.linux"
    ]
  # For scripts
  elif package == "patroni.scripts":
    result = @[
      "patroni.scripts.aws",
      "patroni.scripts.wale_restore"
    ]

proc findModule*(moduleName: string, config: Table[string, string]): bool =
  ## Check if a module name is present in the configuration.
  ##
  ## :param moduleName: Full module name (e.g., "patroni.dcs.etcd").
  ## :param config: Configuration with possible module names as keys.
  ## :returns: true if the module should be loaded based on config.
  let shortName = moduleName.split('.')[^1]
  result = shortName in config

# DCS factory - statically dispatched
type
  DCSType* = enum
    dcsEtcd = "etcd"
    dcsEtcd3 = "etcd3"
    dcsConsul = "consul"
    dcsZookeeper = "zookeeper"
    dcsKubernetes = "kubernetes"
    dcsRaft = "raft"
    dcsExhibitor = "exhibitor"

proc detectDCSType*(config: Table[string, string]): DCSType =
  ## Detect which DCS backend to use from configuration.
  ##
  ## :param config: Configuration dictionary.
  ## :returns: DCS type to use.
  if "etcd3" in config:
    return dcsEtcd3
  if "etcd" in config:
    return dcsEtcd
  if "consul" in config:
    return dcsConsul
  if "zookeeper" in config:
    return dcsZookeeper
  if "kubernetes" in config:
    return dcsKubernetes
  if "raft" in config:
    return dcsRaft
  if "exhibitor" in config:
    return dcsExhibitor

  # Default to etcd
  result = dcsEtcd

proc getModuleClasses*(package: string): seq[string] =
  ## Get class names that implement a specific interface.
  ##
  ## :param package: Package to search in.
  ## :returns: List of class names.
  case package
  of "patroni.dcs":
    result = @["Etcd", "Etcd3", "Consul", "ZooKeeper", "Kubernetes", "Raft", "Exhibitor"]
  of "patroni.watchdog":
    result = @["Watchdog", "LinuxWatchdog"]
  else:
    result = @[]

# For compatibility with Python-style iteration
iterator iterClasses*[T](package: string, config: Table[string, T]): tuple[name: string, available: bool] =
  ## Iterate through available module classes.
  ##
  ## :param package: Package to iterate.
  ## :param config: Configuration to check against.
  ## :yields: Tuples of (name, available).
  let modules = iterModules(package)
  for modName in modules:
    let shortName = modName.split('.')[^1]
    let available = shortName in config
    yield (shortName, available)

