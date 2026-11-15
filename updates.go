package main

import (
	"context"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"PhantomOps/internal/updater"
	"PhantomOps/internal/version"
)

// quitAfterInstallerDelay is how long the app stays open after a Windows
// installer has started, so the user can read why it is closing.
const quitAfterInstallerDelay = 4 * time.Second

const (
	updateProgressEvent  = "update:progress"
	updateAvailableEvent = "update:available"
	firstUpdateCheck     = 20 * time.Second
	updateCheckEvery     = 12 * time.Hour
)

func (a *App) initUpdater() {
	a.updater = updater.New()
	a.updater.OnProgress = func(p updater.Progress) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, updateProgressEvent, p)
		}
	}
	// The Windows installer cannot overwrite a running PhantomOps, so close once
	// it has started. This only runs after the installer started successfully.
	a.updater.OnInstallerStarted = func() {
		time.AfterFunc(quitAfterInstallerDelay, func() {
			if a.ctx != nil {
				runtime.Quit(a.ctx)
			}
		})
	}
	go a.updateLoop()
}

// updateLoop checks for updates shortly after startup and then periodically,
// unless the user turned automatic checks off.
func (a *App) updateLoop() {
	t := time.NewTimer(firstUpdateCheck)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		off := a.cfg.Updates.AutoCheckDisabled
		a.mu.Unlock()
		if !off {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			info, err := a.updater.Check(ctx)
			cancel()
			if err == nil {
				a.rememberUpdate(info)
			}
		}
		t.Reset(updateCheckEvery)
	}
}

// GetAppVersion returns the running version ("dev" for development builds).
func (a *App) GetAppVersion() string { return version.Version }

// rememberUpdate keeps the latest check result so a UI that loads later (or
// reloads) still learns about an available update, and notifies a UI that is
// already open.
func (a *App) rememberUpdate(info updater.Info) {
	a.mu.Lock()
	if info.Available {
		a.pendingUpdate = &info
	} else {
		a.pendingUpdate = nil
	}
	a.mu.Unlock()
	if info.Available && a.ctx != nil {
		runtime.EventsEmit(a.ctx, updateAvailableEvent, info)
	}
}

// PendingUpdate returns the last check result if an update is available,
// otherwise nil.
func (a *App) PendingUpdate() *updater.Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingUpdate
}

// CheckForUpdates asks GitHub for the latest release.
func (a *App) CheckForUpdates() (updater.Info, error) {
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	info, err := a.updater.Check(ctx)
	if err == nil {
		a.rememberUpdate(info)
	}
	return info, err
}

// InstallUpdate downloads, verifies and installs an asset of the release found
// by the last CheckForUpdates. Progress is reported via update:progress.
func (a *App) InstallUpdate(assetName string) error {
	ctx, cancel := context.WithTimeout(a.context(), 15*time.Minute)
	defer cancel()
	return a.updater.Install(ctx, assetName)
}

// GetUpdateProgress returns the current install progress.
func (a *App) GetUpdateProgress() updater.Progress { return a.updater.CurrentProgress() }

// RestartApp restarts PhantomOps (after an update).
func (a *App) RestartApp() error { return a.updater.Restart() }
