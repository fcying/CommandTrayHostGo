//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"os"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
)

const (
	wmHotkey       = 0x0312
	wmResumeHotkey = wmApp + 5
	modAlt         = 0x0001
	modControl     = 0x0002
	modShift       = 0x0004
	modWin         = 0x0008
	modNoRepeat    = 0x4000
	maxHotkeyID    = 0xbfff
)

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

type registeredHotkey struct {
	id      int32
	binding domain.HotkeyBinding
}

type hotkeyRuntime struct {
	active        map[int32]registeredHotkey
	staged        map[int32]registeredHotkey
	nextID        int32
	noRepeat      bool
	suspended     bool
	resumePending bool
}

type pendingHotkeys struct {
	desired  map[int32]registeredHotkey
	newIDs   []int32
	noRepeat bool
}

func (a *TrayApp) installInitialHotkeys() error {
	a.hotkeys.active = nil
	a.hotkeys.staged = make(map[int32]registeredHotkey)
	stage, err := a.stageHotkeys(a.config)
	if err != nil {
		return err
	}
	a.commitStagedHotkeys(stage)
	return nil
}

func (a *TrayApp) stageHotkeys(cfg config.Config) (*pendingHotkeys, error) {
	plan, err := domain.PlanHotkeys(cfg)
	if err != nil {
		return nil, err
	}
	if len(a.hotkeys.active) != 0 && len(plan.Bindings) != 0 && plan.NoRepeat != a.hotkeys.noRepeat {
		return nil, errors.New("repeat_mod_hotkey cannot change during hot reload while hotkeys are active; restart CommandTrayHost")
	}
	stage := &pendingHotkeys{
		desired:  make(map[int32]registeredHotkey, len(plan.Bindings)),
		noRepeat: plan.NoRepeat,
	}
	activeByChord := make(map[config.HotkeyChord]int32, len(a.hotkeys.active))
	for id, registered := range a.hotkeys.active {
		activeByChord[registered.binding.Key.Chord()] = id
	}
	for _, binding := range plan.Bindings {
		if id, ok := activeByChord[binding.Key.Chord()]; ok {
			stage.desired[id] = registeredHotkey{id: id, binding: binding}
			continue
		}
		id, err := a.allocateHotkeyID(stage.desired)
		if err != nil {
			a.discardStagedHotkeys(stage)
			return nil, err
		}
		registered := registeredHotkey{id: id, binding: binding}
		if err := a.registerHotkey(registered, plan.NoRepeat); err != nil {
			a.discardStagedHotkeys(stage)
			return nil, err
		}
		stage.desired[id] = registered
		stage.newIDs = append(stage.newIDs, id)
		a.hotkeys.staged[id] = registered
	}
	return stage, nil
}

func (a *TrayApp) allocateHotkeyID(pending map[int32]registeredHotkey) (int32, error) {
	for range maxHotkeyID {
		a.hotkeys.nextID++
		if a.hotkeys.nextID <= 0 || a.hotkeys.nextID > maxHotkeyID {
			a.hotkeys.nextID = 1
		}
		id := a.hotkeys.nextID
		if _, ok := a.hotkeys.active[id]; ok {
			continue
		}
		if _, ok := a.hotkeys.staged[id]; ok {
			continue
		}
		if _, ok := pending[id]; ok {
			continue
		}
		return id, nil
	}
	return 0, errors.New("no Win32 hotkey IDs are available")
}

func (a *TrayApp) registerHotkey(registered registeredHotkey, noRepeat bool) error {
	modifiers := win32HotkeyModifiers(registered.binding.Key.Modifiers)
	if noRepeat {
		modifiers |= modNoRepeat
	}
	ret, _, err := procRegisterHotKey.Call(
		a.hwnd,
		uintptr(registered.id),
		uintptr(modifiers),
		uintptr(registered.binding.Key.VirtualKey),
	)
	if ret == 0 {
		return fmt.Errorf("register %s (%s): %w", registered.binding.Source, registered.binding.Key.Text, err)
	}
	return nil
}

func win32HotkeyModifiers(modifiers uint16) uint32 {
	var result uint32
	if modifiers&config.HotkeyAlt != 0 {
		result |= modAlt
	}
	if modifiers&config.HotkeyControl != 0 {
		result |= modControl
	}
	if modifiers&config.HotkeyShift != 0 {
		result |= modShift
	}
	if modifiers&config.HotkeyWin != 0 {
		result |= modWin
	}
	return result
}

func (a *TrayApp) commitStagedHotkeys(stage *pendingHotkeys) {
	if stage == nil {
		return
	}
	a.hotkeys.suspended = true
	for id := range a.hotkeys.active {
		if _, ok := stage.desired[id]; !ok {
			procUnregisterHotKey.Call(a.hwnd, uintptr(id))
		}
	}
	a.hotkeys.active = stage.desired
	a.hotkeys.noRepeat = stage.noRepeat
	a.hotkeys.staged = make(map[int32]registeredHotkey)
	a.hotkeys.resumePending = true
	procPostMessageW.Call(a.hwnd, wmResumeHotkey, 0, 0)
}

func (a *TrayApp) discardStagedHotkeys(stage *pendingHotkeys) {
	if stage == nil {
		return
	}
	for _, id := range stage.newIDs {
		procUnregisterHotKey.Call(a.hwnd, uintptr(id))
		delete(a.hotkeys.staged, id)
	}
}

func (a *TrayApp) rollbackStagedHotkeys(stage *pendingHotkeys) {
	a.discardStagedHotkeys(stage)
	a.hotkeys.suspended = true
	a.hotkeys.resumePending = true
	procPostMessageW.Call(a.hwnd, wmResumeHotkey, 0, 0)
}

func (a *TrayApp) unregisterAllHotkeys() {
	for id := range a.hotkeys.staged {
		procUnregisterHotKey.Call(a.hwnd, uintptr(id))
	}
	for id := range a.hotkeys.active {
		procUnregisterHotKey.Call(a.hwnd, uintptr(id))
	}
	a.hotkeys.active = nil
	a.hotkeys.staged = nil
	a.hotkeys.resumePending = false
}

func (a *TrayApp) handleHotkey(id int32, lparam uintptr) {
	if a.hotkeys.suspended || a.sessionEndPending || a.menuOpen || a.closePending || a.closing.Load() || a.closed {
		return
	}
	registered, ok := a.hotkeys.active[id]
	if !ok {
		return
	}
	expectedModifiers := win32HotkeyModifiers(registered.binding.Key.Modifiers)
	actualModifiers := uint32(lparam & 0xffff)
	actualKey := uint16((lparam >> 16) & 0xffff)
	if expectedModifiers != actualModifiers || registered.binding.Key.VirtualKey != actualKey {
		return
	}
	a.executeHotkey(registered.binding.Action)
}

func (a *TrayApp) executeHotkey(action domain.HotkeyAction) {
	if action.Kind == domain.HotkeyExit {
		procPostMessageW.Call(a.hwnd, wmClose, 0, 0)
		return
	}
	if a.reload != nil || a.reloadChecking || a.exclusion != nil || a.cronActionInFlight() {
		return
	}
	var err error
	switch action.Kind {
	case domain.HotkeyDisableAll:
		a.disableAll()
	case domain.HotkeyEnableAll:
		err = a.enableAll()
	case domain.HotkeyHideAll:
		a.hideAll()
	case domain.HotkeyShowAll:
		a.showAll()
	case domain.HotkeyRestartAll:
		a.restartAll()
	case domain.HotkeyElevate:
		err = a.elevateHost()
	case domain.HotkeyLeftClick:
		a.handleLeftClick()
	case domain.HotkeyRightClick:
		a.showMenu()
	case domain.HotkeyAddAlpha:
		err = adjustForegroundWindowAlpha(int(a.config.EffectiveGlobalHotkeyAlphaStep()))
	case domain.HotkeyMinusAlpha:
		err = adjustForegroundWindowAlpha(-int(a.config.EffectiveGlobalHotkeyAlphaStep()))
	case domain.HotkeyTopmost:
		err = toggleForegroundWindowTopmost()
	case domain.HotkeyHideCurrent:
		err = a.dockForegroundWindow()
	case domain.HotkeyShowAllDocked:
		a.restoreAllDockedWindows()
	case domain.HotkeyEntryHideShow:
		if action.EntryIndex >= 0 && action.EntryIndex < len(a.entries) {
			err = a.toggleEntryWindow(action.EntryIndex)
		}
	case domain.HotkeyEntryDisableEnable:
		if action.EntryIndex >= 0 && action.EntryIndex < len(a.entries) {
			err = a.toggleEntry(action.EntryIndex)
		}
	case domain.HotkeyEntryRestart:
		if action.EntryIndex >= 0 && action.EntryIndex < len(a.entries) {
			entry := &a.entries[action.EntryIndex]
			if !entry.busy && entry.state.Running && entry.ownership == domain.ManagedAndJobOwned {
				a.beginStopEntry(action.EntryIndex, processRestart, false)
			}
		}
	case domain.HotkeyEntryElevate:
		err = a.elevateEntry(action.EntryIndex)
	}
	if err != nil {
		ShowError("CommandTrayHost", err.Error())
	}
}

func (a *TrayApp) elevateHost() error {
	if IsElevated() {
		return nil
	}
	if a.hasBusyEntries() {
		return errors.New("wait for current process operations before elevating CommandTrayHost")
	}
	for i := range a.entries {
		if a.entries[i].state.Running && a.entries[i].ownership == domain.ManagedAndJobOwned {
			return errors.New("disable managed entries before elevating CommandTrayHost")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if err := RelaunchElevated(executable, a.baseDir, a.startupUserSID, a.configArgument); err != nil {
		return err
	}
	procPostMessageW.Call(a.hwnd, wmClose, 0, 0)
	return nil
}

func (a *TrayApp) elevateEntry(index int) error {
	if index < 0 || index >= len(a.entries) {
		return nil
	}
	if !IsElevated() {
		return errors.New("elevate CommandTrayHost before running a managed entry as administrator")
	}
	entry := &a.entries[index]
	if entry.busy {
		return nil
	}
	if entry.state.Running && entry.ownership == domain.ManagedAndJobOwned {
		a.beginStopEntry(index, processRestart, false)
		return nil
	}
	entry.state.Enabled = true
	entry.state.Show = entry.config.EffectiveStartShow()
	return a.startEntryWithExclusion(index)
}

func (a *TrayApp) hotkeyText(source string) string {
	if !a.config.ShowHotkeysInMenu() {
		return ""
	}
	for _, registered := range a.hotkeys.active {
		if registered.binding.Source == source {
			return " (" + registered.binding.Key.Text + ")"
		}
	}
	return ""
}
