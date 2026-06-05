//go:build darwin

package core

import "os/exec"

// OpenFolder opens the download directory in the platform file manager.
func OpenFolder(dir string) {
	exec.Command("open", dir).Start()
}
