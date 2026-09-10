//go:build windows && amd64

package win32

import (
	"context"
	"errors"
	"fmt"
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
	result updater.Result
	err    error
	manual bool
}

type updateRuntime struct {
	checker            *updater.Checker
	checkerErr         error
	checking           bool
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
	ctx, cancel := context.WithCancel(context.Background())
	a.update.checking = true
	a.update.currentManual = manual
	a.update.cancel = cancel
	checker := a.update.checker
	options := updater.CheckOptions{
		CurrentVersion:  domain.Version,
		SkipPrereleases: a.config.SkipPrereleases(),
	}
	hwnd := a.hwnd
	results := a.update.results
	a.update.workers.Add(1)
	go func() {
		defer a.update.workers.Done()
		result, err := checker.Check(ctx, options)
		results <- updateCheckResult{result: result, err: err, manual: manual}
		for !a.closing.Load() {
			ret, _, _ := procPostMessageW.Call(hwnd, wmUpdateComplete, 0, 0)
			if ret != 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return nil
}

func (a *TrayApp) handleUpdateResults() {
	for {
		select {
		case result := <-a.update.results:
			a.update.checking = false
			a.update.currentManual = false
			if a.update.cancel != nil {
				a.update.cancel()
				a.update.cancel = nil
			}
			if errors.Is(result.err, context.Canceled) {
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
	if !a.update.checking {
		return
	}
	a.update.resumeAfterSession = true
	a.update.resumeManual = a.update.currentManual
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
	if pending.err != nil {
		if pending.manual {
			ShowError("CommandTrayHost", text.UpdateCheckFailed+"\n\n"+pending.err.Error())
		}
		return
	}
	result := pending.result
	switch result.Outcome {
	case updater.OutcomeCurrentVersionUnknown:
		if pending.manual && ShowConfirm("CommandTrayHost", fmt.Sprintf(text.UpdateVersionUnknown, result.CurrentVersion, result.Latest.Tag)) {
			a.openUpdatePage(result.Latest.URL)
		}
	case updater.OutcomeUpToDate:
		if pending.manual {
			ShowInfo("CommandTrayHost", fmt.Sprintf(text.NoUpdates, result.CurrentVersion, result.Latest.Tag))
		}
	case updater.OutcomeUpdateAvailable:
		if ShowConfirm("CommandTrayHost", fmt.Sprintf(text.UpdateAvailable, result.Latest.Tag, result.CurrentVersion)) {
			a.openUpdatePage(result.Latest.URL)
		}
	}
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
		ShowError("CommandTrayHost", err.Error())
	}
}

func (a *TrayApp) cancelUpdateCheck() {
	if a.update.cancel != nil {
		a.update.cancel()
	}
}

func (a *TrayApp) cleanupUpdater() {
	a.update.resumeAfterSession = false
	a.cancelUpdateCheck()
	a.update.workers.Wait()
	for {
		select {
		case <-a.update.results:
		default:
			return
		}
	}
}
