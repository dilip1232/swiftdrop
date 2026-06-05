//go:build !darwin && !windows

package core

import "syscall"

// DiskFree returns available bytes on the volume containing dir.
func DiskFree(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
