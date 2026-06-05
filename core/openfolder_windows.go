//go:build windows

package core

import "os/exec"

// OpenFolder opens the download directory in Windows Explorer.
func OpenFolder(dir string) {
	exec.Command("explorer", dir).Start()
}
