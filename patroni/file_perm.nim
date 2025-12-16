## Helper object that helps with figuring out file and directory permissions based on permissions of PGDATA.
##
## :var pg_perm: instance of the FilePermissions object.

import std/[posix, strformat]
import ./log

let logger = getLogger("patroni.file_perm")

const
  # Mode mask for data directory permissions that only allows the owner to
  # read/write directories and files -- mask 077.
  PG_MODE_MASK_OWNER = Mode(S_IRWXG or S_IRWXO)

  # Mode mask for data directory permissions that also allows group read/execute -- mask 027.
  PG_MODE_MASK_GROUP = Mode(S_IWGRP or S_IRWXO)

  # Default mode for creating directories -- mode 700.
  PG_DIR_MODE_OWNER = Mode(S_IRWXU)

  # Mode for creating directories that allows group read/execute -- mode 750.
  PG_DIR_MODE_GROUP = Mode(S_IRWXU or S_IRGRP or S_IXGRP)

  # Default mode for creating files -- mode 600.
  PG_FILE_MODE_OWNER = Mode(S_IRUSR or S_IWUSR)

  # Mode for creating files that allows group read -- mode 640.
  PG_FILE_MODE_GROUP = Mode(S_IRUSR or S_IWUSR or S_IRGRP)

type
  FilePermissions* = ref object
    ## Helper class for managing permissions of directories and files under PGDATA.
    ##
    ## Execute setPermissionsFromDataDirectory to figure out which permissions should be used for files and
    ## directories under PGDATA based on permissions of PGDATA root directory.
    pgDirCreateMode: Mode
    pgFileCreateMode: Mode
    pgModeMask: Mode
    origUmask*: Mode

proc setOwnerPermissions(self: FilePermissions) =
  ## Make directories/files accessible only by the owner.
  self.pgDirCreateMode = PG_DIR_MODE_OWNER
  self.pgFileCreateMode = PG_FILE_MODE_OWNER
  self.pgModeMask = PG_MODE_MASK_OWNER

proc setGroupPermissions(self: FilePermissions) =
  ## Make directories/files accessible by the owner and readable by group.
  self.pgDirCreateMode = PG_DIR_MODE_GROUP
  self.pgFileCreateMode = PG_FILE_MODE_GROUP
  self.pgModeMask = PG_MODE_MASK_GROUP

proc setUmaskInternal(self: FilePermissions): Mode =
  ## Set umask value based on calculations.
  ##
  ## .. note::
  ##     Should only be called once either setOwnerPermissions
  ##     or setGroupPermissions has been executed.
  ##
  ## :returns: the previous value of the umask or 0o22 if umask call failed.
  try:
    result = umask(self.pgModeMask)
  except CatchableError as e:
    logger.error(fmt"Can not set umask to {self.pgModeMask:03o}: {e.msg}")
    result = Mode(0o22)

proc newFilePermissions*(): FilePermissions =
  ## Create a FilePermissions object and set default permissions.
  new(result)
  result.setOwnerPermissions()
  result.origUmask = result.setUmaskInternal()

proc setPermissionsFromDataDirectory*(self: FilePermissions, dataDir: string) =
  ## Set new permissions based on provided dataDir.
  ##
  ## :param dataDir: reference to PGDATA to calculate permissions from.
  var st: Stat
  try:
    if stat(dataDir.cstring, st) == 0:
      if (st.st_mode and PG_DIR_MODE_GROUP) == PG_DIR_MODE_GROUP:
        self.setGroupPermissions()
      else:
        self.setOwnerPermissions()
      discard self.setUmaskInternal()
    else:
      logger.error(fmt"Can not check permissions on {dataDir}: stat failed")
  except CatchableError as e:
    logger.error(fmt"Can not check permissions on {dataDir}: {e.msg}")

proc dirCreateMode*(self: FilePermissions): Mode =
  ## Directory permissions.
  result = self.pgDirCreateMode

proc fileCreateMode*(self: FilePermissions): Mode =
  ## File permissions.
  result = self.pgFileCreateMode

# Global instance
var pgPerm* = newFilePermissions()
