//go:build !windows

package fileperm

import "syscall"

func doSetUmask(mask int) int {
	return syscall.Umask(mask)
}
