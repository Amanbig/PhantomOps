//go:build windows

package updater

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var procShellExecute = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

// platformStartInstaller runs the installer through ShellExecute with the
// "runas" verb. The NSIS installer asks for administrator rights (it installs
// into Program Files). CreateProcess, which os/exec uses, cannot show the
// Windows permission prompt and fails with "the requested operation requires
// elevation"; ShellExecute shows the Yes/No prompt instead.
func platformStartInstaller(path string) error {
	verb, err := syscall.UTF16PtrFromString(shellExecuteVerbUI)
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	dir, err := syscall.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return err
	}
	ret, _, callErr := procShellExecute.Call(
		0, // no parent window
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, // no arguments
		uintptr(unsafe.Pointer(dir)),
		swShowNormal,
	)
	return shellExecuteResult(ret, callErr)
}
