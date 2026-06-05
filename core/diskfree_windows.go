//go:build windows

package core

import (
	"syscall"
	"unsafe"
)

// DiskFree returns the number of free bytes available to the calling user
// on the filesystem containing dir.
func DiskFree(dir string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	r1, _, e1 := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		0,
		0,
	)
	if r1 == 0 {
		return 0, e1
	}
	return freeBytesAvailable, nil
}
