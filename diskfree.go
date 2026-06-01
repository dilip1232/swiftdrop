package main

import "syscall"

// diskFree returns the number of free bytes available to an unprivileged user
// on the filesystem containing dir.
func diskFree(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
