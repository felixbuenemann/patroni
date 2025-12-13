// Package fileperm provides helpers for managing PostgreSQL file and directory permissions.
package fileperm

import (
	"os"
	"sync"

	"github.com/rs/zerolog/log"
)

const (
	// Mode mask for data directory permissions that only allows the owner - mask 077
	pgModeMaskOwner = 0o077

	// Mode mask for data directory permissions that allows group read/execute - mask 027
	pgModeMaskGroup = 0o027

	// Default mode for creating directories - mode 700
	pgDirModeOwner = 0o700

	// Mode for creating directories that allows group read/execute - mode 750
	pgDirModeGroup = 0o750

	// Default mode for creating files - mode 600
	pgFileModeOwner = 0o600

	// Mode for creating files that allows group read - mode 640
	pgFileModeGroup = 0o640
)

// FilePermissions manages permissions for directories and files under PGDATA.
type FilePermissions struct {
	mu               sync.RWMutex
	dirCreateMode    os.FileMode
	fileCreateMode   os.FileMode
	modeMask         int
	origUmask        int
}

// New creates a new FilePermissions with default owner-only permissions.
func New() *FilePermissions {
	fp := &FilePermissions{}
	fp.setOwnerPermissions()
	fp.origUmask = fp.setUmask()
	return fp
}

// setUmask sets the umask value based on current mode mask.
func (fp *FilePermissions) setUmask() int {
	oldUmask := setUmask(fp.modeMask)
	return oldUmask
}

// setOwnerPermissions makes directories/files accessible only by the owner.
func (fp *FilePermissions) setOwnerPermissions() {
	fp.dirCreateMode = pgDirModeOwner
	fp.fileCreateMode = pgFileModeOwner
	fp.modeMask = pgModeMaskOwner
}

// setGroupPermissions makes directories/files accessible by owner and readable by group.
func (fp *FilePermissions) setGroupPermissions() {
	fp.dirCreateMode = pgDirModeGroup
	fp.fileCreateMode = pgFileModeGroup
	fp.modeMask = pgModeMaskGroup
}

// SetPermissionsFromDataDirectory sets permissions based on the PGDATA directory.
func (fp *FilePermissions) SetPermissionsFromDataDirectory(dataDir string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()

	info, err := os.Stat(dataDir)
	if err != nil {
		log.Error().Err(err).Str("data_dir", dataDir).Msg("Cannot check permissions")
		return
	}

	mode := info.Mode().Perm()
	if (mode & pgDirModeGroup) == pgDirModeGroup {
		fp.setGroupPermissions()
	} else {
		fp.setOwnerPermissions()
	}

	fp.setUmask()
}

// DirCreateMode returns the mode to use when creating directories.
func (fp *FilePermissions) DirCreateMode() os.FileMode {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.dirCreateMode
}

// FileCreateMode returns the mode to use when creating files.
func (fp *FilePermissions) FileCreateMode() os.FileMode {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.fileCreateMode
}

// OrigUmask returns the original umask value before modification.
func (fp *FilePermissions) OrigUmask() int {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.origUmask
}

// IsSecure checks if a file mode is secure (no world access).
func IsSecure(mode os.FileMode) bool {
	// PostgreSQL data directory should not have world access
	return (mode.Perm() & 0o007) == 0
}

// setUmask is a platform-specific function to set umask.
// On Unix systems, this calls syscall.Umask.
func setUmask(mask int) int {
	// Note: On Windows, umask is not meaningful, return 0o022 as default
	return doSetUmask(mask)
}

// Default is the global default FilePermissions instance.
var Default = New()
