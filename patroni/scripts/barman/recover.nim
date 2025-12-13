## Implements ``patroni_barman recover`` sub-command.
##
## Restore a Barman backup to the local node through ``pg-backup-api``.
##
## This sub-command can be used both as a custom bootstrap method, and as a custom
## create replica method.
##
## It requires that you have previously configured a Barman server, and that you
## have ``pg-backup-api`` configured and running in the same host as Barman.

import std/[logging, os, strformat]
import ./utils

type
  RecoverExitCode* = enum
    ## Possible exit codes of recover sub-command.
    rcRecoveryDone = 0    ## Backup was successfully restored
    rcRecoveryFailed = 1  ## Recovery of the backup faced an issue
    rcHttpError = 2       ## Error occurred while communicating with pg-backup-api

  RecoverArgs* = object
    ## Arguments for recover command.
    barmanServer*: string
    backupId*: string
    sshCommand*: string
    dataDirectory*: string
    loopWait*: int

proc restoreBackup(api: PgBackupApi, barmanServer: string, backupId: string,
                   sshCommand: string, dataDirectory: string,
                   loopWait: int): int =
  ## Restore the configured Barman backup through pg-backup-api.
  ##
  ## :param api: A PgBackupApi instance to handle communication with the API.
  ## :param barmanServer: Name of the Barman server which backup is to be restored.
  ## :param backupId: ID of the backup from the Barman server.
  ## :param sshCommand: SSH command to connect from the Barman host to the target host.
  ## :param dataDirectory: Path to the Postgres data directory where to restore the backup.
  ## :param loopWait: How long in seconds to wait before checking again the status.
  ## :returns: The return code to be used when exiting the application.
  var operationId: string

  try:
    operationId = api.createRecoveryOperation(barmanServer, backupId,
                                               sshCommand, dataDirectory)
  except RetriesExceeded as exc:
    error(fmt"An issue was faced while trying to create a recovery operation: {exc.msg}")
    return int(rcHttpError)

  info(fmt"Created the recovery operation with ID {operationId}")

  var status: OperationStatus

  while true:
    try:
      status = api.getOperationStatus(barmanServer, operationId)
    except RetriesExceeded:
      error("Maximum number of retries exceeded, exiting.")
      return int(rcHttpError)

    if status != osInProgress:
      break

    info(fmt"Recovery operation {operationId} is still in progress")
    sleep(loopWait * 1000)

  if status == osDone:
    info("Recovery operation finished successfully.")
    return int(rcRecoveryDone)
  else:
    error("Recovery operation failed.")
    return int(rcRecoveryFailed)

proc runBarmanRecover*(api: PgBackupApi, args: RecoverArgs): int =
  ## Run a remote ``barman recover`` through the pg-backup-api.
  ##
  ## :param api: A PgBackupApi instance to handle communication with the API.
  ## :param args: Arguments received from the command-line.
  ## :returns: The return code to be used when exiting the application.
  return restoreBackup(api, args.barmanServer, args.backupId,
                       args.sshCommand, args.dataDirectory, args.loopWait)
