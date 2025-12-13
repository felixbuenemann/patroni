## Raft controller for standalone Raft DCS.
##
## This module provides a controller for running a standalone Raft-based
## distributed configuration store.

import std/[json, logging, os, strformat, tables]
import ./config
import ./daemon
import ./dcs/raft
import ./log

let logger = getLogger("patroni.raft_controller")

type
  RaftController* = ref object of AbstractPatroniDaemon
    ## Controller for running a standalone Raft node.
    raft: KVStoreTTL

proc newRaftController*(config: Config): RaftController =
  ## Create a new RaftController.
  ##
  ## :param config: Patroni configuration.
  ## :returns: New RaftController instance.
  new(result)
  initAbstractPatroniDaemon(result, config)

  let kvstoreConfig = config.get("raft")
  if kvstoreConfig == nil:
    raise newException(ValueError, "Raft configuration is required")

  let selfAddr = kvstoreConfig.getOrDefault("self_addr").getStr("")
  if selfAddr.len == 0:
    raise newException(ValueError, "self_addr is required in raft configuration")

  # Initialize the Raft KV store
  result.raft = newKVStoreTTL(kvstoreConfig)

proc runCycle*(self: RaftController) =
  ## Run one iteration of the raft tick loop.
  try:
    self.raft.doTick()
  except Exception as e:
    logger.error(fmt"doTick error: {e.msg}")

proc shutdown*(self: RaftController) =
  ## Shutdown the raft controller.
  if self.raft != nil:
    self.raft.destroy()

proc run*(self: RaftController) =
  ## Run the raft controller main loop.
  logger.info("Starting Raft controller")

  while not self.isReceivedSigterm():
    self.runCycle()
    # Sleep for tick period
    sleep(100)

  self.shutdown()

proc main*() =
  ## Main entry point for the raft controller.
  let args = getBaseArgParser()

  if args.showVersion:
    showVersion()
    quit(0)

  if args.showHelp:
    echo "Usage: patroni_raft_controller [OPTIONS] <config-file>"
    echo ""
    echo "Options:"
    echo "  --version, -v    Show version and exit"
    echo "  --help, -h       Show this help and exit"
    quit(0)

  if args.configFile.len == 0:
    echo "Usage: patroni_raft_controller <config-file>"
    quit(1)

  let config = newConfig(args.configFile)
  let controller = newRaftController(config)

  controller.run()

when isMainModule:
  main()

