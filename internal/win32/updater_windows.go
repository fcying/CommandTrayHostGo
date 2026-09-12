//go:build windows && amd64

package win32

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/i18n"
	"github.com/fcying/CommandTrayHostGo/internal/updater"
	"golang.org/x/sys/windows"
)

const (
	wmUpdateComplete = wmApp + 6
	updateUITimerID  = 4
	updateUIDelay    = 250
)

type updateCheckResult struct {
	result     updater.Result
	err        error
	manual     bool
	download   bool
	updatePath string
}

type updateRuntime struct {
	checker            *updater.Checker
	checkerErr         error
	checking           bool
	downloading        bool
	canceled           bool
	currentManual      bool
	resumeAfterSession bool
	resumeManual       bool
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	results            chan updateCheckResult
	pending            *updateCheckResult
}

func newUpdateRuntime() updateRuntime {
	checker, err := updater.NewChecker(updater.Repository)
	return updateRuntime{
		checker:    checker,
		checkerErr: err,
		results:    make(chan updateCheckResult, 1),
	}
}

func (a *TrayApp) startUpdateCheck(manual bool) error {
	if a.update.checking || a.update.pending != nil {
		return errors.New(i18n.Text(a.language).UpdaterAlreadyRunning)
	}
	if !manual && (!a.config.AutoUpdateEnabled() || !updater.IsComparable(domain.Version)) {
		return nil
	}
	if a.update.checkerErr != nil {
		return a.update.checkerErr
	}
	checker := a.update.checker
	options := updater.CheckOptions{
		CurrentVersion:  domain.Version,
		SkipPrereleases: a.config.SkipPrereleases(),
	}
	a.startUpdateWorker(manual, false, func(ctx context.Context) updateCheckResult {
		result, err := checker.Check(ctx, options)
		return updateCheckResult{result: result, err: err, manual: manual}
	})
	return nil
}

func (a *TrayApp) startUpdateWorker(manual, download bool, work func(context.Context) updateCheckResult) {
	ctx, cancel := context.WithCancel(context.Background())
	a.update.checking = true
	a.update.downloading = download
	a.update.canceled = false
	a.update.currentManual = manual
	a.update.cancel = cancel
	hwnd := a.hwnd
	results := a.update.results
	a.update.workers.Add(1)
	go func() {
		defer a.update.workers.Done()
		defer cancel()
		result := work(ctx)
		if ctx.Err() != nil {
			result.err = ctx.Err()
		}
		if result.err != nil && result.updatePath != "" {
			_ = os.Remove(result.updatePath)
			result.updatePath = ""
		}
		results <- result
		for !a.closing.Load() {
			ret, _, _ := procPostMessageW.Call(hwnd, wmUpdateComplete, 0, 0)
			if ret != 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

func (a *TrayApp) handleUpdateResults() {
	for {
		select {
		case result := <-a.update.results:
			canceled := a.update.canceled
			a.update.checking = false
			a.update.downloading = false
			a.update.canceled = false
			a.update.currentManual = false
			if a.update.cancel != nil {
				a.update.cancel()
				a.update.cancel = nil
			}
			if canceled || errors.Is(result.err, context.Canceled) || a.closing.Load() || a.closed {
				if result.updatePath != "" {
					_ = os.Remove(result.updatePath)
				}
				a.resumeInterruptedUpdate()
				continue
			}
			a.update.pending = &result
		default:
			a.resumeUpdateUI()
			return
		}
	}
}

func (a *TrayApp) interruptUpdateForSession() {
	if a.update.pending != nil && a.update.pending.download {
		a.cancelUpdateCheck()
	}
	if !a.update.checking {
		return
	}
	if !a.update.downloading {
		a.update.resumeAfterSession = true
		a.update.resumeManual = a.update.currentManual
	}
	a.cancelUpdateCheck()
}

func (a *TrayApp) resumeInterruptedUpdate() {
	if !a.update.resumeAfterSession || a.update.checking || a.sessionEndPending || a.closing.Load() || a.closed {
		return
	}
	manual := a.update.resumeManual
	a.update.resumeAfterSession = false
	a.update.resumeManual = false
	_ = a.startUpdateCheck(manual)
}

func (a *TrayApp) resumeUpdateUI() {
	if a == nil || a.update.pending == nil || a.hwnd == 0 || a.closing.Load() || a.closed || a.sessionEndPending {
		return
	}
	if a.messageDepth != 0 || a.menuOpen || a.reloadChecking || a.reload != nil || a.exclusion != nil || a.cronActionInFlight() || a.hasBusyEntries() {
		procSetTimer.Call(a.hwnd, updateUITimerID, updateUIDelay, 0)
		return
	}
	procKillTimer.Call(a.hwnd, updateUITimerID)
	pending := a.update.pending
	a.update.pending = nil
	text := i18n.Text(a.language)
	if pending.download {
		if pending.err != nil {
			ShowError(productName, pending.err.Error())
			return
		}
		if err := LaunchUpdateHelper(a.executablePath, pending.updatePath, a.configArgument, a.startupUserSID); err != nil {
			_ = os.Remove(pending.updatePath)
			ShowError(productName, err.Error())
			return
		}
		procPostMessageW.Call(a.hwnd, wmClose, 0, 0)
		return
	}
	if pending.err != nil {
		if pending.manual {
			ShowError(productName, text.UpdateCheckFailed+"\n\n"+pending.err.Error())
		}
		return
	}
	result := pending.result
	switch result.Outcome {
	case updater.OutcomeCurrentVersionUnknown:
		if pending.manual && ShowConfirm(productName, fmt.Sprintf(text.UpdateVersionUnknown, result.CurrentVersion, result.Latest.Tag)) {
			a.openUpdatePage(result.Latest.URL)
		}
	case updater.OutcomeUpToDate:
		if pending.manual {
			ShowInfo(productName, fmt.Sprintf(text.NoUpdates, result.CurrentVersion, result.Latest.Tag))
		}
	case updater.OutcomeUpdateAvailable:
		if ShowConfirm(productName, fmt.Sprintf(text.UpdateAvailable, result.Latest.Tag, result.CurrentVersion)) {
			if err := a.installUpdate(result.Latest.Tag); err != nil {
				ShowError(productName, err.Error())
			}
		}
	}
}

func (a *TrayApp) installUpdate(version string) error {
	if a.closing.Load() || a.closed || a.sessionEndPending {
		return nil
	}
	if a.update.checking || a.update.pending != nil {
		return errors.New(i18n.Text(a.language).UpdaterAlreadyRunning)
	}
	a.startUpdateWorker(true, true, func(ctx context.Context) updateCheckResult {
		result := updateCheckResult{manual: true, download: true}
		file, err := os.CreateTemp(a.baseDir, ".commandtrayhost-update-*.exe")
		if err != nil {
			result.err = err
			return result
		}
		result.updatePath = file.Name()
		if err := file.Close(); err != nil {
			result.err = err
			return result
		}
		if err := os.Remove(result.updatePath); err != nil {
			result.err = err
			return result
		}
		result.err = updater.DownloadVerified(ctx, updater.Repository, version, result.updatePath)
		return result
	})
	return nil
}

func (a *TrayApp) openRepositoryPage() error {
	if a.update.checkerErr != nil {
		return a.update.checkerErr
	}
	return shellOpen(a.hwnd, a.update.checker.RepositoryURL(), "", "", windows.SW_SHOWMAXIMIZED)
}

func (a *TrayApp) openUpdatePage(url string) {
	if a.closing.Load() || a.closed || a.sessionEndPending {
		return
	}
	if err := shellOpen(a.hwnd, url, "", "", windows.SW_SHOWMAXIMIZED); err != nil {
		ShowError(productName, err.Error())
	}
}

func (a *TrayApp) cancelUpdateCheck() {
	if a.update.checking {
		a.update.canceled = true
	}
	if a.update.pending != nil && a.update.pending.download {
		if a.update.pending.updatePath != "" {
			_ = os.Remove(a.update.pending.updatePath)
		}
		a.update.pending = nil
	}
	if a.update.cancel != nil {
		a.update.cancel()
	}
}

func (a *TrayApp) cleanupUpdater() {
	a.update.resumeAfterSession = false
	a.update.resumeManual = false
	if a.hwnd != 0 {
		procKillTimer.Call(a.hwnd, updateUITimerID)
	}
	a.cancelUpdateCheck()
	a.update.workers.Wait()
	a.update.pending = nil
	a.update.cancel = nil
	a.update.checking = false
	a.update.downloading = false
	a.update.canceled = false
	for {
		select {
		case result := <-a.update.results:
			if result.updatePath != "" {
				_ = os.Remove(result.updatePath)
			}
		default:
			return
		}
	}
}
