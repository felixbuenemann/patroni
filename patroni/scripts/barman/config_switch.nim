## Implements ``patroni_barman config-switch`` sub-command.
##
## Apply a Barman configuration model through ``pg-backup-api``.
##
## This sub-command is specially useful as a ``on_role_change`` callback to change
## Barman configuration in response to failovers and switchovers.
##
## It requires that you have previously configured a Barman server and Barman
## config models, and that you have ``pg-backup-api`` configured and running in
## the same host as Barman.

import std/[logging, os, strformat, tables]
import ./utils

type
  ConfigSwitchExitCode* = enum
    ## Possible exit codes of config-switch sub-command.
    csConfigSwitchDone = 0      ## Config switch was successfully performed
    csConfigSwitchSkipped = 1   ## Execution was skipped because of not matching user expectations
    csConfigSwitchFailed = 2    ## Config switch faced an issue
    csHttpError = 3             ## Error occurred while communicating with pg-backup-api
    csInvalidArgs = 4           ## Invalid set of arguments given to the operation

  ConfigSwitchArgs* = object
    ## Arguments for config-switch command.
    action*: string
    role*: string
    cluster*: string
    barmanServer*: string
    barmanModel*: string
    reset*: bool
    switchWhen*: string

proc shouldSkipSwitch(args: ConfigSwitchArgs): bool =
  ## Check if we should skip the config switch operation.
  ##
  ## :param args: Arguments received from the command-line.
  ## :returns: If the operation should be skipped.
  if args.switchWhen == "promoted":
    return args.role notin ["primary", "promoted"]
  if args.switchWhen == "demoted":
    return args.role notin ["replica", "demoted"]
  return false

proc switchConfig(api: PgBackupApi, barmanServer: string,
                  barmanModel: string, reset: bool): int =
  ## Switch configuration of Barman server through pg-backup-api.
  ##
  ## :param api: A PgBackupApi instance to handle communication with the API.
  ## :param barmanServer: Name of the Barman server which config is to be switched.
  ## :param barmanModel: Name of the Barman model to be applied to the server, if any.
  ## :param reset: True if you would like to unapply the currently active model.
  ## :returns: The return code to be used when exiting the application.
  var operationId: string

  try:
    operationId = api.createConfigSwitchOperation(barmanServer, barmanModel, reset)
  except RetriesExceeded as exc:
    error(fmt"An issue was faced while trying to create a config switch operation: {exc.msg}")
    return int(csHttpError)

  info(fmt"Created the config switch operation with ID {operationId}")

  var status: OperationStatus

  while true:
    try:
      status = api.getOperationStatus(barmanServer, operationId)
    except RetriesExceeded:
      error("Maximum number of retries exceeded, exiting.")
      return int(csHttpError)

    if status != osInProgress:
      break

    info(fmt"Config switch operation {operationId} is still in progress")
    sleep(5000)

  if status == osDone:
    info("Config switch operation finished successfully.")
    return int(csConfigSwitchDone)
  else:
    error("Config switch operation failed.")
    return int(csConfigSwitchFailed)

proc runBarmanConfigSwitch*(api: PgBackupApi, args: ConfigSwitchArgs): int =
  ## Run a remote ``barman config-switch`` through the pg-backup-api.
  ##
  ## :param api: A PgBackupApi instance to handle communication with the API.
  ## :param args: Arguments received from the command-line.
  ## :returns: The return code to be used when exiting the application.
  if shouldSkipSwitch(args):
    info(fmt"Config switch operation was skipped (role={args.role}, switch_when={args.switchWhen}).")
    return int(csConfigSwitchSkipped)

  if not (args.barmanModel.len > 0 xor args.reset):
    error(fmt"One, and only one among 'barman_model' ('{args.barmanModel}') and 'reset' ('{args.reset}') should be given")
    return int(csInvalidArgs)

  return switchConfig(api, args.barmanServer, args.barmanModel, args.reset)
