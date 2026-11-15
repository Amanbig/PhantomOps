//go:build !windows

package updater

import "os/exec"

// platformStartInstaller starts the installer directly. Only Windows ships an
// .exe installer, so this exists to keep the other platforms building.
func platformStartInstaller(path string) error {
	return exec.Command(path).Start()
}
