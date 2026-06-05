//go:build !darwin && !windows

package core

import "os/exec"

// OpenFolder opens the given directory in the default file manager.
func OpenFolder(dir string) {
	exec.Command("xdg-open", dir).Start()
}
