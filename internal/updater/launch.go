package updater

import (
	"errors"
	"fmt"
	"syscall"
)

// startInstaller launches a downloaded installer (the Windows .exe). It is a
// variable so tests can observe the call without running anything.
var startInstaller = platformStartInstaller

// Results of ShellExecute that need a specific explanation.
const (
	seErrFNF           = 2    // file not found
	seErrPNF           = 3    // path not found
	seErrAccessDenied  = 5    // the system refused to start it
	errorCancelled     = 1223 // ERROR_CANCELLED: the user answered No to the administrator prompt
	shellExecuteMinOK  = 32   // ShellExecute returns a value greater than this on success
	swShowNormal       = 1
	shellExecuteVerbUI = "runas" // ask Windows for administrator rights (shows the Yes/No prompt)
)

// shellExecuteResult turns what ShellExecuteW returned into an error. ret is
// its return value (a number above 32 means success). lastErr is the error
// the call reported, which is non-nil even on success (errno 0).
func shellExecuteResult(ret uintptr, lastErr error) error {
	if ret > shellExecuteMinOK {
		return nil
	}
	var errno syscall.Errno
	if errors.As(lastErr, &errno) && errno == errorCancelled {
		return errors.New("the installer was not started: the administrator permission prompt was declined")
	}
	switch ret {
	case seErrAccessDenied:
		return errors.New("the installer was not started: Windows denied permission to run it (the administrator permission prompt was declined, or this account may not install programs)")
	case seErrFNF, seErrPNF:
		return errors.New("the installer was not started: the downloaded installer file could not be found")
	}
	return fmt.Errorf("the installer was not started: Windows could not run it (ShellExecute code %d)", ret)
}
