## Perform operations on Barman through pg-backup-api.
##
## The actual operations are implemented by separate modules. This module only
## builds the CLI that makes an interface with the actual commands.
##
## See ExitCode for possible exit codes of this main script.

import std/[logging, os, parseopt, strformat]
import ./config_switch
import ./recover
import ./utils

type
  CliExitCode* = enum
    ## Possible exit codes of this script.
    ecNoCommand = -1    ## No sub-command of patroni_barman application has been selected
    ecApiNotOk = -2     ## pg-backup-api status is not OK

  SubCommand = enum
    scNone
    scRecover
    scConfigSwitch

proc printUsage() =
  echo """
Usage: patroni_barman [options] <command> [command-options]

Wrapper application for pg-backup-api. Communicate with the API
running at the given URL to perform remote Barman operations.

Options:
  --api-url=URL         URL to reach the pg-backup-api (required)
  --cert-file=FILE      Certificate to authenticate against the API
  --key-file=FILE       Certificate key to authenticate against the API
  --retry-wait=SECONDS  Wait time before retrying failed requests (default: 2)
  --max-retries=NUM     Maximum number of retries (default: 5)
  --log-file=FILE       File where to log messages

Commands:
  recover               Remote 'barman recover'
  config-switch         Remote 'barman config-switch'

For command-specific help, run: patroni_barman <command> --help
"""

proc printRecoverUsage() =
  echo """
Usage: patroni_barman [options] recover [command-options]

Restore a Barman backup of a given Barman server.

Command options:
  --barman-server=NAME  Name of the Barman server (required)
  --backup-id=ID        ID of the backup to restore (default: latest)
  --ssh-command=CMD     SSH command for remote connection (required)
  --data-directory=DIR  Destination path for backup restore (required)
  --loop-wait=SECONDS   Wait time between status checks (default: 10)
"""

proc printConfigSwitchUsage() =
  echo """
Usage: patroni_barman [options] config-switch <action> <role> <cluster> [command-options]

Switch the configuration of a given Barman server.
Intended to be used as a 'on_role_change' callback.

Positional arguments:
  action                Name of the callback (on_role_change)
  role                  New role (primary, promoted, standby_leader, replica, demoted)
  cluster               Name of the Patroni cluster

Command options:
  --barman-server=NAME  Name of the Barman server (required)
  --barman-model=NAME   Name of the Barman config model to apply
  --reset               Unapply the currently active model
  --switch-when=WHEN    When to switch config (promoted, demoted, always)
"""

proc main*(): int =
  ## Entry point of patroni_barman application.
  var
    apiUrl = ""
    certFile = ""
    keyFile = ""
    retryWait = 2
    maxRetries = 5
    logFile = ""
    subCommand = scNone

    # Recover args
    recoverBarmanServer = ""
    recoverBackupId = "latest"
    recoverSshCommand = ""
    recoverDataDirectory = ""
    recoverLoopWait = 10

    # Config-switch args
    configSwitchAction = ""
    configSwitchRole = ""
    configSwitchCluster = ""
    configSwitchBarmanServer = ""
    configSwitchBarmanModel = ""
    configSwitchReset = false
    configSwitchSwitchWhen = "promoted"

  var p = initOptParser()
  var positionalArgs: seq[string] = @[]

  while true:
    p.next()
    case p.kind
    of cmdEnd: break
    of cmdShortOption, cmdLongOption:
      case p.key
      of "api-url": apiUrl = p.val
      of "cert-file": certFile = p.val
      of "key-file": keyFile = p.val
      of "retry-wait": retryWait = parseInt(p.val)
      of "max-retries": maxRetries = parseInt(p.val)
      of "log-file": logFile = p.val
      of "barman-server":
        if subCommand == scRecover:
          recoverBarmanServer = p.val
        else:
          configSwitchBarmanServer = p.val
      of "backup-id": recoverBackupId = p.val
      of "ssh-command": recoverSshCommand = p.val
      of "data-directory", "datadir": recoverDataDirectory = p.val
      of "loop-wait": recoverLoopWait = parseInt(p.val)
      of "barman-model": configSwitchBarmanModel = p.val
      of "reset": configSwitchReset = true
      of "switch-when": configSwitchSwitchWhen = p.val
      of "help", "h":
        case subCommand
        of scNone: printUsage()
        of scRecover: printRecoverUsage()
        of scConfigSwitch: printConfigSwitchUsage()
        return 0
      else: discard
    of cmdArgument:
      case p.key
      of "recover":
        subCommand = scRecover
      of "config-switch":
        subCommand = scConfigSwitch
      else:
        positionalArgs.add(p.key)

  # Handle positional args for config-switch
  if subCommand == scConfigSwitch and positionalArgs.len >= 3:
    configSwitchAction = positionalArgs[0]
    configSwitchRole = positionalArgs[1]
    configSwitchCluster = positionalArgs[2]

  # Set up logging
  setUpLogging(logFile)

  if subCommand == scNone:
    printUsage()
    return int(ecNoCommand)

  if apiUrl.len == 0:
    error("--api-url is required")
    return int(ecNoCommand)

  # Create API connection
  var api: PgBackupApi

  try:
    api = newPgBackupApi(apiUrl, certFile, keyFile, retryWait, maxRetries)
  except ApiNotOk as exc:
    error(fmt"pg-backup-api is not working: {exc.msg}")
    return int(ecApiNotOk)

  defer: api.close()

  # Execute sub-command
  case subCommand
  of scRecover:
    let args = RecoverArgs(
      barmanServer: recoverBarmanServer,
      backupId: recoverBackupId,
      sshCommand: recoverSshCommand,
      dataDirectory: recoverDataDirectory,
      loopWait: recoverLoopWait
    )
    return runBarmanRecover(api, args)

  of scConfigSwitch:
    let args = ConfigSwitchArgs(
      action: configSwitchAction,
      role: configSwitchRole,
      cluster: configSwitchCluster,
      barmanServer: configSwitchBarmanServer,
      barmanModel: configSwitchBarmanModel,
      reset: configSwitchReset,
      switchWhen: configSwitchSwitchWhen
    )
    return runBarmanConfigSwitch(api, args)

  of scNone:
    return int(ecNoCommand)

when isMainModule:
  quit(main())
