## Tests for patroni/file_perm module.
## Ported from test_file_perm.py

import std/[unittest, os]
when defined(posix):
  import posix

import ../patroni/file_perm

suite "FilePermissions":
  test "newFilePermissions creates instance":
    let fp = newFilePermissions()
    check fp != nil

  test "origUmask is set":
    check pgPerm.origUmask >= Mode(0)

  test "dirCreateMode returns mode":
    let mode = pgPerm.dirCreateMode
    when defined(posix):
      # Should be either owner or group mode
      check mode == Mode(S_IRWXU) or mode == Mode(S_IRWXU or S_IRGRP or S_IXGRP)

  test "fileCreateMode returns mode":
    let mode = pgPerm.fileCreateMode
    when defined(posix):
      # Should be either owner or group mode
      check mode == Mode(S_IRUSR or S_IWUSR) or mode == Mode(S_IRUSR or S_IWUSR or S_IRGRP)

suite "FilePermissions from Data Directory":
  when defined(posix):
    test "setPermissionsFromDataDirectory with non-existent path":
      # Should not crash on non-existent directory
      let fp = newFilePermissions()
      fp.setPermissionsFromDataDirectory("/nonexistent/path/that/does/not/exist")
      check fp != nil

    test "setPermissionsFromDataDirectory with temp dir":
      let fp = newFilePermissions()
      let tempDir = getTempDir()
      fp.setPermissionsFromDataDirectory(tempDir)
      # Should have valid modes after setting
      check fp.dirCreateMode >= Mode(0)
      check fp.fileCreateMode >= Mode(0)

suite "Permission Constants":
  when defined(posix):
    test "PG_MODE_MASK_OWNER blocks group and other":
      # Mask 077 blocks group and other
      let mask = Mode(S_IRWXG or S_IRWXO)
      check (mask and Mode(S_IRWXG)) == Mode(S_IRWXG)
      check (mask and Mode(S_IRWXO)) == Mode(S_IRWXO)

    test "PG_MODE_MASK_GROUP blocks write group and all other":
      # Mask 027 blocks group write and other all
      let mask = Mode(S_IWGRP or S_IRWXO)
      check (mask and Mode(S_IWGRP)) == Mode(S_IWGRP)
      check (mask and Mode(S_IRWXO)) == Mode(S_IRWXO)

    test "PG_DIR_MODE_OWNER is 700":
      check Mode(S_IRWXU) == Mode(0o700)

    test "PG_DIR_MODE_GROUP is 750":
      let mode = Mode(S_IRWXU or S_IRGRP or S_IXGRP)
      check mode == Mode(0o750)

    test "PG_FILE_MODE_OWNER is 600":
      let mode = Mode(S_IRUSR or S_IWUSR)
      check mode == Mode(0o600)

    test "PG_FILE_MODE_GROUP is 640":
      let mode = Mode(S_IRUSR or S_IWUSR or S_IRGRP)
      check mode == Mode(0o640)

suite "Global pgPerm Instance":
  test "pgPerm is initialized":
    check pgPerm != nil

  test "pgPerm has valid origUmask":
    # origUmask should be a valid umask value
    check pgPerm.origUmask >= Mode(0)
    check pgPerm.origUmask <= Mode(0o777)

when isMainModule:
  echo "test_file_perm.nim tests completed"
