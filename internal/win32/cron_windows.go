//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"strings"
	"time"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"github.com/fcying/CommandTrayHostGo/internal/cronexpr"
)

type cronEntryRuntime struct {
	expression *cronexpr.Expression
	counter    domain.CronCounter
	next       time.Time
	phase      cronPhase
	token      uint64
}

type cronRuntime struct {
	entries    []cronEntryRuntime
	serial     uint64
	suspended  bool
	retryAt    time.Time
	timerIndex int
	timerRenew bool
}

func newCronRuntime(cfg config.Config, now time.Time) (*cronRuntime, error) {
	runtime := &cronRuntime{entries: make([]cronEntryRuntime, len(cfg.Configs))}
	var errs []error
	for i := range cfg.Configs {
		cron := cfg.Configs[i].Cron
		if cron == nil || !cron.IsEnabled() {
			continue
		}
		expression := cron.Parsed
		if expression == nil {
			var err error
			expression, err = cronexpr.Parse(cron.Expression)
			if err != nil {
				errs = append(errs, fmt.Errorf("initialize cron for %s: %w", cfg.Configs[i].Name, err))
				continue
			}
		}
		next, err := expression.Next(now)
		if err != nil {
			errs = append(errs, fmt.Errorf("schedule cron for %s: %w", cfg.Configs[i].Name, err))
			continue
		}
		runtime.entries[i] = cronEntryRuntime{
			expression: expression,
			counter:    domain.NewCronCounter(cron.Count),
			next:       next,
			phase:      cronWaiting,
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (a *TrayApp) installInitialCron(now time.Time) error {
	runtime, err := newCronRuntime(a.config, now)
	if err != nil {
		return err
	}
	a.cron = runtime
	var errs []error
	for i := range runtime.entries {
		entry := &runtime.entries[i]
		if entry.phase == cronWaiting {
			errs = append(errs, a.writeCronScheduleLog(i, "schedule next", entry.next))
		}
	}
	errs = append(errs, a.updateCronTimer(now))
	return errors.Join(errs...)
}

func (a *TrayApp) resetCronSchedule(now time.Time) error {
	if a.cron == nil {
		return nil
	}
	var errs []error
	for i := range a.cron.entries {
		entry := &a.cron.entries[i]
		if entry.phase != cronWaiting || entry.expression == nil {
			continue
		}
		next, err := entry.expression.Next(now)
		if err != nil {
			entry.phase = cronDisabled
			entry.next = time.Time{}
			errs = append(errs, fmt.Errorf("schedule cron for %s: %w", a.entries[i].config.Name, err))
			continue
		}
		entry.next = next
		errs = append(errs, a.writeCronScheduleLog(i, "schedule next", next))
	}
	return errors.Join(errs...)
}

func (a *TrayApp) suspendCron() {
	if a.hwnd != 0 {
		procKillTimer.Call(a.hwnd, cronTimerID)
	}
	if a.cron != nil {
		a.cron.suspended = true
	}
}

func (a *TrayApp) resumeCron(now time.Time) {
	if a.cron == nil || a.sessionEndPending || a.closing.Load() || a.closed || a.reload != nil || a.reloadChecking || a.exclusion != nil || a.menuOpen {
		return
	}
	a.cron.suspended = false
	a.cron.retryAt = time.Time{}
	a.skipMissedRenewTargets(now)
	a.reportCronError(a.updateCronTimer(now))
}

func (a *TrayApp) updateCronTimer(now time.Time) error {
	if a == nil || a.hwnd == 0 || a.cron == nil {
		return nil
	}
	if a.cron.suspended || a.sessionEndPending || a.closing.Load() || a.closed || a.reload != nil || a.reloadChecking || a.exclusion != nil || a.menuOpen {
		procKillTimer.Call(a.hwnd, cronTimerID)
		return nil
	}
	if a.cronActionInFlight() || a.hasBusyEntries() {
		procKillTimer.Call(a.hwnd, cronTimerID)
		return nil
	}
	var next time.Time
	nextIndex := -1
	for i := range a.cron.entries {
		entry := &a.cron.entries[i]
		if (entry.phase != cronWaiting && entry.phase != cronFinalStopRetryWaiting) || entry.next.IsZero() {
			continue
		}
		candidate := entry.next
		if !candidate.After(now) && a.entries[i].busy && a.cron.retryAt.After(candidate) {
			candidate = a.cron.retryAt
		}
		if next.IsZero() || candidate.Before(next) {
			next = candidate
			nextIndex = i
		}
	}
	if next.IsZero() {
		a.cron.timerIndex = -1
		a.cron.timerRenew = false
		procKillTimer.Call(a.hwnd, cronTimerID)
		return nil
	}
	delay, renew := domain.CronTimerDelay(now, next)
	a.cron.timerIndex = nextIndex
	a.cron.timerRenew = renew
	milliseconds := (delay + time.Millisecond - 1) / time.Millisecond
	if milliseconds < 1 {
		milliseconds = 1
	}
	ret, _, err := procSetTimer.Call(a.hwnd, cronTimerID, uintptr(milliseconds), 0)
	if ret == 0 {
		return fmt.Errorf("cron timer could not be started: %w", err)
	}
	return nil
}

func (a *TrayApp) handleCronTimer(now time.Time) {
	procKillTimer.Call(a.hwnd, cronTimerID)
	if a.cron == nil || a.sessionEndPending || a.closing.Load() || a.closed {
		return
	}
	if a.cron.timerRenew {
		a.cron.timerRenew = false
		a.cron.timerIndex = -1
		var errs []error
		for index := range a.cron.entries {
			runtime := &a.cron.entries[index]
			if runtime.phase != cronWaiting || runtime.expression == nil || runtime.next.After(now) {
				continue
			}
			next, err := runtime.expression.Next(now)
			if err != nil {
				runtime.phase = cronDisabled
				runtime.next = time.Time{}
				errs = append(errs, fmt.Errorf("renew cron for %s: %w", a.entries[index].config.Name, err))
			} else {
				runtime.next = next
				errs = append(errs, a.writeCronScheduleLog(index, "renew", next))
			}
		}
		errs = append(errs, a.updateCronTimer(now))
		a.reportCronError(errors.Join(errs...))
		return
	}
	if a.menuOpen || a.reloadChecking || a.reload != nil || a.exclusion != nil {
		a.cron.retryAt = now.Add(cronRetryDelay)
		a.reportCronError(a.updateCronTimer(now))
		return
	}
	if a.hasBusyEntries() {
		a.cron.retryAt = now.Add(cronRetryDelay)
		return
	}
	var errs []error
	blocked := false
	for i := range a.cron.entries {
		runtime := &a.cron.entries[i]
		if (runtime.phase != cronWaiting && runtime.phase != cronFinalStopRetryWaiting) || runtime.next.After(now) {
			continue
		}
		if a.entries[i].busy || a.exclusion != nil {
			blocked = true
			continue
		}
		phase := runtime.phase
		a.cron.serial++
		runtime.token = a.cron.serial
		if phase == cronFinalStopRetryWaiting {
			runtime.phase = cronFinalStopPending
			errs = append(errs, a.dispatchCronFinalStop(i, runtime.token))
		} else {
			runtime.phase = cronActionPending
			if err := a.dispatchCronAction(i, runtime.token); err != nil {
				errs = append(errs, err)
			}
		}
		break
	}
	if blocked {
		a.cron.retryAt = now.Add(cronRetryDelay)
	}
	if a.exclusion != nil {
		return
	}
	errs = append(errs, a.updateCronTimer(time.Now()))
	if err := errors.Join(errs...); err != nil {
		a.reportCronError(err)
	}
}

func (a *TrayApp) dispatchCronAction(index int, token uint64) error {
	if !a.validCronToken(index, token, cronActionPending) {
		return nil
	}
	entry := &a.entries[index]
	cron := entry.config.Cron
	if entry.state.Running && entry.ownership == domain.ManagedAndJobOwned {
		if entry.process == nil {
			return a.completeCronAction(index, token, errors.New("managed cron entry has no process handle"))
		}
		running, err := entry.process.running()
		if err != nil {
			return a.completeCronAction(index, token, fmt.Errorf("query %s before cron action: %w", entry.config.Name, err))
		}
		if !running {
			a.resetStoppedEntry(index, false)
		}
	}
	show := entry.config.EffectiveCronStartShow()
	if cron.LogLevel >= 1 && (cron.Method == config.CronStart || cron.Method == config.CronRestart ||
		cron.Method == config.CronStartCountStop || cron.Method == config.CronRestartCountStop) {
		if err := a.writeCronLog(index, 1, fmt.Sprintf("launch start_show:%t", show)); err != nil {
			a.reportCronError(err)
		}
	}
	switch cron.Method {
	case config.CronStart, config.CronStartCountStop:
		if entry.state.Running && entry.ownership == domain.ManagedAndJobOwned {
			return a.completeCronAction(index, token, nil)
		}
		pending, err := a.startEntryWithExclusionOptions(index, show, false, token)
		if !pending {
			return a.completeCronAction(index, token, err)
		}
		return nil
	case config.CronRestart, config.CronRestartCountStop:
		if entry.state.Running && entry.process != nil && entry.ownership == domain.ManagedAndJobOwned {
			if a.beginStopEntryWithOptions(index, processCronRestart, show, false, false, token) {
				return nil
			}
		}
		pending, err := a.startEntryWithExclusionOptions(index, show, false, token)
		if !pending {
			return a.completeCronAction(index, token, err)
		}
		return nil
	case config.CronStop:
		if entry.state.Running && entry.process != nil && entry.ownership == domain.ManagedAndJobOwned {
			if a.beginStopEntryWithOptions(index, processCronStop, false, false, false, token) {
				return nil
			}
		}
		entry.state.Enabled = false
		entry.state.Show = false
		return a.completeCronAction(index, token, nil)
	default:
		return a.completeCronAction(index, token, fmt.Errorf("unsupported cron method %q", cron.Method))
	}
}

func (a *TrayApp) completeCronAction(index int, token uint64, actionErr error) error {
	if !a.validCronToken(index, token, cronActionPending) {
		return actionErr
	}
	runtime := &a.cron.entries[index]
	cron := a.entries[index].config.Cron
	exhausted := runtime.counter.Consume()
	if exhausted && cron.StopsWhenExhausted() {
		runtime.phase = cronFinalStopPending
		logErr := a.writeCronActionLog(index, actionErr, true, time.Time{})
		return errors.Join(logErr, a.dispatchCronFinalStop(index, token))
	}
	if exhausted {
		runtime.phase = cronExhausted
		runtime.next = time.Time{}
		return a.writeCronActionLog(index, actionErr, true, time.Time{})
	}
	next, err := runtime.expression.Next(time.Now())
	if err != nil {
		runtime.phase = cronDisabled
		runtime.next = time.Time{}
		return errors.Join(a.writeCronActionLog(index, actionErr, false, time.Time{}), fmt.Errorf("schedule next cron for %s: %w", a.entries[index].config.Name, err))
	}
	runtime.next = next
	runtime.phase = cronWaiting
	return errors.Join(a.writeCronActionLog(index, actionErr, false, next), a.writeCronScheduleLog(index, "schedule next", next))
}

func (a *TrayApp) dispatchCronFinalStop(index int, token uint64) error {
	if !a.validCronToken(index, token, cronFinalStopPending) {
		return nil
	}
	entry := &a.entries[index]
	if entry.state.Running && entry.process != nil && entry.ownership == domain.ManagedAndJobOwned &&
		a.beginStopEntryWithOptions(index, processCronFinalStop, false, false, false, token) {
		return nil
	}
	entry.state.Enabled = false
	entry.state.Show = false
	return a.completeCronFinalStop(index, token, nil)
}

func (a *TrayApp) completeCronFinalStop(index int, token uint64, actionErr error) error {
	if !a.validCronToken(index, token, cronFinalStopPending) {
		return actionErr
	}
	runtime := &a.cron.entries[index]
	runtime.phase, runtime.next = cronFinalStopCompletion(time.Now(), actionErr)
	if actionErr != nil {
		return a.writeCronLog(index, 0, "final stop failed: "+actionErr.Error()+"; retry scheduled")
	}
	return a.writeCronLog(index, 0, "final stop completed count exhausted")
}

func (a *TrayApp) validCronToken(index int, token uint64, phase cronPhase) bool {
	return a.cron != nil && index >= 0 && index < len(a.cron.entries) && index < len(a.entries) &&
		a.cron.entries[index].token == token && a.cron.entries[index].phase == phase
}

func (a *TrayApp) cronActionInFlight() bool {
	if a.cron == nil {
		return false
	}
	for i := range a.cron.entries {
		if a.cron.entries[i].phase == cronActionPending || a.cron.entries[i].phase == cronFinalStopPending {
			return true
		}
	}
	return false
}

func (a *TrayApp) writeCronActionLog(index int, actionErr error, exhausted bool, next time.Time) error {
	runtime := &a.cron.entries[index]
	message := cronActionMessage(a.entries[index].config.Cron.Method, actionErr, exhausted)
	message += fmt.Sprintf(" left count:%d", runtime.counter.Remaining)
	if !next.IsZero() {
		message += " next:" + next.Format("2006-01-02 15:04:05")
	}
	return a.writeCronLog(index, 0, message)
}

func cronActionMessage(method config.CronMethod, actionErr error, exhausted bool) string {
	message := string(method)
	if actionErr != nil {
		message += " failed: " + actionErr.Error()
	} else {
		message += " completed"
	}
	if exhausted {
		message += " count exhausted"
	}
	return message
}

func (a *TrayApp) writeCronScheduleLog(index int, message string, next time.Time) error {
	if index < 0 || index >= len(a.entries) || a.entries[index].config.Cron == nil || a.entries[index].config.Cron.LogLevel < 2 {
		return nil
	}
	return a.writeCronLog(index, 2, fmt.Sprintf("%s [%s] %s", message, a.entries[index].config.Cron.Expression, next.Format("2006-01-02 15:04:05")))
}

func (a *TrayApp) writeCronLog(index int, minimumLevel int64, message string) error {
	if a.cronLogger == nil || index < 0 || index >= len(a.entries) {
		return nil
	}
	entry := &a.entries[index]
	if entry.config.Cron == nil || entry.config.Cron.Log == "" || entry.config.Cron.LogLevel < minimumLevel {
		return nil
	}
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("2006-01-02 15:04:05"), entry.config.Name, strings.TrimSpace(message))
	return a.cronLogger.Write(entry.config.Cron.Log, line)
}

func (a *TrayApp) reportCronError(err error) {
	if err != nil && !a.sessionEndPending && !a.closing.Load() && !a.closed {
		ShowError("CommandTrayHost", err.Error())
	}
}

func (a *TrayApp) resumeCronAfterMessage() {
	if a == nil || a.sessionEndPending || a.messageDepth != 0 || a.closePending || a.closing.Load() || a.closed {
		return
	}
	if a.cron != nil {
		a.cron.suspended = false
	}
	a.resumeCron(time.Now())
}

func (a *TrayApp) skipMissedRenewTargets(now time.Time) {
	if a.cron == nil || !a.cron.timerRenew {
		return
	}
	var errs []error
	for i := range a.cron.entries {
		runtime := &a.cron.entries[i]
		if runtime.phase != cronWaiting || runtime.expression == nil || runtime.next.After(now) {
			continue
		}
		next, err := runtime.expression.Next(now)
		if err != nil {
			runtime.phase = cronDisabled
			runtime.next = time.Time{}
			errs = append(errs, fmt.Errorf("renew cron for %s: %w", a.entries[i].config.Name, err))
			continue
		}
		runtime.next = next
		errs = append(errs, a.writeCronScheduleLog(i, "renew", next))
	}
	a.cron.timerRenew = false
	a.cron.timerIndex = -1
	a.reportCronError(errors.Join(errs...))
}
