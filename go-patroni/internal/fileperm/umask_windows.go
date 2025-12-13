//go:build windows

package fileperm

func doSetUmask(mask int) int {
	// Windows doesn't have umask, return default
	return 0o022
}
