// Package version holds the PhantomOps version.
package version

// Version is set at release build time:
//
//	-ldflags "-X 'PhantomOps/internal/version.Version=v0.1.0'"
//
// Builds made without it (wails dev, local wails build) report "dev". The
// updater never replaces a dev build.
var Version = "dev"

// IsRelease reports whether this binary came from a release build.
func IsRelease() bool { return Version != "" && Version != "dev" }
