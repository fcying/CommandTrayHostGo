//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"github.com/fcying/CommandTrayHostGo/internal/cronlog"
	"github.com/fcying/CommandTrayHostGo/internal/i18n"
	"github.com/fcying/CommandTrayHostGo/internal/statecache"
	"golang.org/x/sys/windows"
)

const productName = "CommandTrayHostGo"

const (
	wmDestroy                = 0x0002
	wmClose                  = 0x0010
	wmQueryEndSession        = 0x0011
	wmEndSession             = 0x0016
	wmCommand                = 0x0111
	wmTimer                  = 0x0113
	wmNull                   = 0x0000
	wmApp                    = 0x8000
	wmTray                   = wmApp + 1
	wmProcessComplete        = wmApp + 2
	wmConfigDirectoryChanged = wmApp + 3
	wmConfigWatcherFailed    = wmApp + 4
	wmLButtonUp              = 0x0202
	wmRButtonUp              = 0x0205
	cwUseDefault             = 0x80000000
	nimAdd                   = 0x00000000
	nimModify                = 0x00000001
	nimDelete                = 0x00000002
	nifMessage               = 0x00000001
	nifIcon                  = 0x00000002
	nifTip                   = 0x00000004
	mfString                 = 0x00000000
	mfGray                   = 0x00000001
	mfDisabled               = 0x00000002
	mfChecked                = 0x00000008
	mfPopup                  = 0x00000010
	mfSeparator              = 0x00000800
	tpmRightButton           = 0x0002
	tpmBottomAlign           = 0x0020
	idiApplication           = 32512
	idcArrow                 = 32512
	commandAbout             = 1001
	commandExit              = 1002
	commandHome              = 1003
	commandStartOnBoot       = 1004
	commandCheckForUpdates   = 1005
	commandHideAll           = 1010
	commandDisableAll        = 1011
	commandEnableAll         = 1012
	commandShowAll           = 1013
	commandRestartAll        = 1014
	commandShowAllDocked     = 1015
	commandElevate           = 1016
	commandDockedBase        = 0x1200
	commandEntryBase         = 0x2000
	commandEntryStep         = 0x10
	commandOpenPath          = 0
	commandSelectExecutable  = 1
	commandShowHide          = 2
	commandToggle            = 3
	commandRestart           = 4
	commandElevateEntry      = 5
	windowTimerID            = 1
	configReloadTimerID      = 2
	cronTimerID              = 3
	trayIconRetryTimerID     = 5
	windowTimerDelay         = 200
	configReloadDelay        = 250
	windowRetryDelay         = 5000
	trayIconRetryDelay       = 250
	windowFindLimit          = 100
	sessionCleanupTimeout    = 4 * time.Second
	trayIconID               = 666
	trayIconResourceID       = 1
	smallIconResourceID      = 2
	messageBoxOK             = 0x00000000
	messageBoxYesNo          = 0x00000004
	messageBoxYesNoCancel    = 0x00000003
	messageBoxError          = 0x00000010
	messageBoxQuestion       = 0x00000020
	shutdownNoRetry          = 0x00000001
	shutdownPriority         = 0x03ff
	idYes                    = 6
	idNo                     = 7
	idCancel                 = 2
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	user32                   = windows.NewLazySystemDLL("user32.dll")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procRegisterWindowMsgW   = user32.NewProc("RegisterWindowMessageW")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWnd     = user32.NewProc("SetForegroundWindow")
	procSetTimer             = user32.NewProc("SetTimer")
	procKillTimer            = user32.NewProc("KillTimer")
	procIsWindow             = user32.NewProc("IsWindow")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procShowWindowAsync      = user32.NewProc("ShowWindowAsync")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procShutdownBlockCreate  = user32.NewProc("ShutdownBlockReasonCreate")
	procShutdownBlockDestroy = user32.NewProc("ShutdownBlockReasonDestroy")
	procShellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	procSetShutdownParams    = kernel32.NewProc("SetProcessShutdownParameters")

	activeApp *TrayApp
)

type TrayApp struct {
	className           string
	tooltip             string
	baseDir             string
	executablePath      string
	configPath          string
	cachePath           string
	configArgument      string
	config              config.Config
	language            i18n.Language
	configStamp         config.FileStamp
	observedConfigStamp config.FileStamp
	cache               *statecache.Store
	entries             []trayEntry
	processes           *processController
	hwnd                uintptr
	defaultIcon         uintptr
	classIcon           uintptr
	icons               *iconResources
	taskbarCreated      uint32
	trayIconRecovery    trayIconRecovery
	showingWindowError  bool
	windowEnumErrShown  bool
	processResults      chan processResult
	processWorkers      sync.WaitGroup
	watcher             *directoryWatcher
	menuOpen            bool
	reloadChecking      bool
	reloadPending       bool
	reloadSerial        uint64
	reload              *pendingReload
	exclusion           *pendingExclusionStart
	hotkeys             hotkeyRuntime
	cron                *cronRuntime
	cronLogger          *cronlog.Writer
	startup             startupRegistration
	startupUserSID      string
	docked              []dockedWindow
	console             consoleFallback
	closing             atomic.Bool
	closed              bool
	messageDepth        int
	closePending        bool
	sessionEndPending   bool
	update              updateRuntime
}

type consoleFallback struct {
	process           *childProcess
	hwnd              uintptr
	pendingToggle     bool
	targetVisible     bool
	foregroundPending bool
	iconPending       bool
	iconErr           error
	retainedIcons     windowIconPair
	retainedIconHWND  uintptr
	findCount         uint16
	findTimedOut      bool
	retryWindowAt     time.Time
}

type trayEntry struct {
	config            config.EntryConfig
	ownership         domain.Ownership
	state             domain.EntryState
	process           *childProcess
	hwnd              uintptr
	needsWindow       bool
	findCount         uint16
	findTimedOut      bool
	retryWindowAt     time.Time
	appearanceErr     error
	appearancePending bool
	iconPending       bool
	retainedIcons     windowIconPair
	retainedIconHWND  uintptr
	showPending       bool
	foregroundPending bool
	busy              bool
	launched          bool
	cronTransient     bool
}

type processOperation uint8

const (
	processStop processOperation = iota
	processRestart
	processReloadStop
	processExclusionStop
	processCronStop
	processCronRestart
	processCronFinalStop
)

type processResult struct {
	index       int
	generation  uint64
	operation   processOperation
	process     *childProcess
	show        bool
	restartShow bool
	cacheShow   bool
	updateCache bool
	err         error
	reloadID    uint64
	cronToken   uint64
	hwnd        uintptr
}

type reloadStopState struct {
	index         int
	enabled       bool
	show          bool
	cronTransient bool
}

type pendingReload struct {
	id               uint64
	config           *config.Config
	cache            *statecache.Store
	stamp            config.FileStamp
	keep             bool
	matches          []int
	preserve         []bool
	stops            []reloadStopState
	pending          int
	failed           bool
	errs             []error
	preserveLaunched []bool
	hotkeys          *pendingHotkeys
	cron             *cronRuntime
	icons            *iconResources
	language         i18n.Language
}

type dockedWindow struct {
	hwnd            uintptr
	pid             uint32
	caption         string
	untitledCaption bool
	process         windows.Handle
}

type pendingExclusionStart struct {
	target      int
	waiting     map[int]uint64
	peers       []exclusionPeerState
	failed      bool
	startShow   bool
	updateCache bool
	cronToken   uint64
	priorErr    error
}

type exclusionPeerState struct {
	index         int
	enabled       bool
	show          bool
	cronTransient bool
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  uintptr
}

type point struct {
	X int32
	Y int32
}

type message struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	HWND            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            windows.GUID
	BalloonIcon     uintptr
}

func NewTrayApp(className, tooltip, executablePath, startupUserSID, baseDir, configPath, cachePath, configArgument string, cfg config.Config, configStamp config.FileStamp, cache *statecache.Store, language i18n.Language) *TrayApp {
	if startupUserSID == "" {
		startupUserSID = currentUserSID()
	}
	entries := make([]trayEntry, len(cfg.Configs))
	for i, entry := range cfg.Configs {
		entries[i] = trayEntry{
			config:    entry,
			ownership: domain.OwnershipFor(entry),
			state: domain.EntryState{
				Enabled: entry.Enabled,
				Show:    entry.EffectiveStartShow(),
			},
		}
	}
	return &TrayApp{
		className:           className + ".Window",
		tooltip:             tooltip,
		executablePath:      executablePath,
		baseDir:             baseDir,
		configPath:          configPath,
		cachePath:           cachePath,
		configArgument:      configArgument,
		config:              cfg,
		language:            language,
		configStamp:         configStamp,
		observedConfigStamp: configStamp,
		cache:               cache,
		entries:             entries,
		processResults:      make(chan processResult, len(entries)),
		cronLogger:          cronlog.New(baseDir),
		startup:             newStartupRegistration(executablePath, startupUserSID, configArgument),
		startupUserSID:      startupUserSID,
		update:              newUpdateRuntime(),
	}
}

func (a *TrayApp) Run() error {
	if activeApp != nil {
		return fmt.Errorf("tray application is already running")
	}
	activeApp = a
	defer func() { activeApp = nil }()

	processes, err := newProcessController(a.baseDir, a.executablePath)
	if err != nil {
		return err
	}
	a.processes = processes
	defer a.cleanup()

	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("GetModuleHandleW: %w", err)
	}
	a.classIcon, _, _ = procLoadIconW.Call(instance, trayIconResourceID)
	a.defaultIcon, _, _ = procLoadIconW.Call(instance, smallIconResourceID)
	if a.defaultIcon == 0 {
		a.defaultIcon, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	if a.classIcon == 0 {
		a.classIcon = a.defaultIcon
	}
	a.icons, err = loadIconResources(a.baseDir, a.config)
	if err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return nil
		}
		if a.icons == nil {
			a.icons = &iconResources{entries: make([]*windowIconPair, len(a.entries))}
		}
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	className, err := windows.UTF16PtrFromString(a.className)
	if err != nil {
		return err
	}

	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   windows.NewCallback(windowProc),
		Instance:  instance,
		Icon:      a.classIcon,
		Cursor:    cursor,
		ClassName: className,
		IconSmall: a.defaultIcon,
	}
	registered, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if registered == 0 {
		return fmt.Errorf("RegisterClassExW: %w", err)
	}

	a.hwnd, _, err = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		cwUseDefault,
		cwUseDefault,
		cwUseDefault,
		cwUseDefault,
		0,
		0,
		instance,
		0,
	)
	if a.hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %w", err)
	}
	if ret, _, callErr := procSetShutdownParams.Call(shutdownPriority, shutdownNoRetry); ret == 0 {
		ShowError(productName, fmt.Sprintf("SetProcessShutdownParameters: %v", callErr))
		if a.closing.Load() || a.closed {
			return nil
		}
	}
	if err := a.installInitialHotkeys(); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return nil
		}
	}
	taskbarMessage, _ := windows.UTF16PtrFromString("TaskbarCreated")
	msgID, _, callErr := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(taskbarMessage)))
	if msgID == 0 {
		return fmt.Errorf("RegisterWindowMessageW(TaskbarCreated): %w", callErr)
	}
	a.taskbarCreated = uint32(msgID)
	if err := a.addTrayIcon(); err != nil {
		procDestroyWindow.Call(a.hwnd)
		return err
	}
	if err := a.updateConsoleFallback(); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return nil
		}
	}
	a.startConfiguredEntries()
	if a.closing.Load() || a.closed {
		return nil
	}
	a.reportWindowTimerError(a.updateWindowTimer())
	if a.closing.Load() || a.closed {
		return nil
	}
	if err := a.updateConfigWatcher(); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return nil
		}
	}
	if err := a.installInitialCron(time.Now()); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return nil
		}
	}
	if err := a.startUpdateCheck(false); err != nil {
		ShowError(productName, err.Error())
	}

	var msg message
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("GetMessageW: %w", err)
		}
		if ret == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (a *TrayApp) addTrayIcon() error {
	nid := a.notifyData()
	ret, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ret == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %w", err)
	}
	return nil
}

func (a *TrayApp) restoreTrayIcon() {
	procKillTimer.Call(a.hwnd, trayIconRetryTimerID)
	if a.sessionEndPending || a.closing.Load() || a.closed {
		a.trayIconRecovery.reset()
		return
	}

	retry, err := a.trayIconRecovery.attempt(
		func() bool {
			nid := a.notifyData()
			ret, _, _ := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
			return ret != 0
		},
		a.addTrayIcon,
	)
	if err == nil {
		return
	}
	if retry {
		if timer, _, _ := procSetTimer.Call(a.hwnd, trayIconRetryTimerID, trayIconRetryDelay, 0); timer != 0 {
			return
		}
		a.trayIconRecovery.reset()
		err = fmt.Errorf("%w; tray icon retry timer could not be started", err)
	}
	ShowError(productName, err.Error())
}

func (a *TrayApp) modifyTrayIcon(icon uintptr) error {
	nid := a.notifyData()
	nid.Flags = nifIcon
	nid.Icon = icon
	ret, _, err := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	if ret == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_MODIFY): %w", err)
	}
	return nil
}

func (a *TrayApp) deleteTrayIcon() {
	nid := a.notifyData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (a *TrayApp) notifyData() notifyIconData {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		HWND:            a.hwnd,
		ID:              trayIconID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTray,
		Icon:            a.activeTrayIcon(),
	}
	copy(nid.Tip[:], windows.StringToUTF16(a.tooltip))
	return nid
}

func (a *TrayApp) showMenu() {
	if a.sessionEndPending || a.reload != nil || a.reloadChecking || a.exclusion != nil || a.cronActionInFlight() {
		return
	}
	a.menuOpen = true
	defer func() {
		a.menuOpen = false
		if a.closePending && a.messageDepth == 0 && !a.closed {
			a.closing.Store(true)
			procPostMessageW.Call(a.hwnd, wmClose, 0, 0)
			return
		}
		if !a.sessionEndPending {
			if a.hotkeys.resumePending {
				procPostMessageW.Call(a.hwnd, wmResumeHotkey, 0, 0)
			} else if a.messageDepth == 0 {
				a.hotkeys.suspended = false
			}
			a.resumeCron(time.Now())
			a.resumePendingReload()
			a.resumeUpdateUI()
		}
	}()
	a.refreshEntries()
	text := i18n.Text(a.language)
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	if err := a.appendConfiguredMenus(menu); err != nil {
		ShowError(productName, err.Error())
		return
	}
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, commandHideAll, text.HideAll+a.hotkeyText("hotkey.hide_all"))
	allMenu, _, _ := procCreatePopupMenu.Call()
	if allMenu != 0 {
		appendMenu(allMenu, mfString, commandDisableAll, text.DisableAll+a.hotkeyText("hotkey.disable_all"))
		appendMenu(allMenu, mfString, commandEnableAll, text.EnableAll+a.hotkeyText("hotkey.enable_all"))
		appendMenu(allMenu, mfString, commandShowAll, text.ShowAll+a.hotkeyText("hotkey.show_all"))
		appendMenu(allMenu, mfString, commandRestartAll, text.RestartAll+a.hotkeyText("hotkey.restart_all"))
		appendMenu(menu, mfPopup, allMenu, text.All)
	}
	appendMenu(menu, mfSeparator, 0, "")
	startupFlags := uintptr(mfString)
	startupText := text.StartOnBoot
	if !a.startup.Available() {
		startupFlags |= mfGray | mfDisabled
		startupText += " (" + text.OriginalUserOnly + ")"
	} else {
		startupEnabled, err := a.startup.Enabled()
		if err != nil {
			ShowError(productName, err.Error())
			return
		}
		if startupEnabled {
			startupFlags |= mfChecked
		}
	}
	if err := appendMenu(menu, startupFlags, commandStartOnBoot, startupText); err != nil {
		ShowError(productName, err.Error())
		return
	}
	elevateFlags := uintptr(mfString)
	if IsElevated() {
		elevateFlags |= mfChecked
	}
	if err := appendMenu(menu, elevateFlags, commandElevate, text.Elevate+a.hotkeyText("hotkey.elevate")); err != nil {
		ShowError(productName, err.Error())
		return
	}
	appendMenu(menu, mfSeparator, 0, "")
	if err := a.appendHelpMenu(menu); err != nil {
		ShowError(productName, err.Error())
		return
	}
	a.appendDockedMenu(menu)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, commandExit, text.Exit+a.hotkeyText("hotkey.exit"))

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWnd.Call(a.hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton|tpmBottomAlign, uintptr(cursor.X), uintptr(cursor.Y), 0, a.hwnd, 0)
	procPostMessageW.Call(a.hwnd, wmNull, 0, 0)
}

func (a *TrayApp) appendConfiguredMenus(menu uintptr) error {
	if !a.config.GroupsEnabled() {
		for i := range a.entries {
			if err := a.appendEntryMenu(menu, i); err != nil {
				return err
			}
		}
		return nil
	}
	return a.appendGroupItems(menu, *a.config.Groups)
}

func (a *TrayApp) appendGroupItems(menu uintptr, items []config.GroupItem) error {
	for _, item := range items {
		if item.EntryIndex != nil {
			if err := a.appendEntryMenu(menu, *item.EntryIndex); err != nil {
				return err
			}
			continue
		}
		submenu, _, callErr := procCreatePopupMenu.Call()
		if submenu == 0 {
			return fmt.Errorf("CreatePopupMenu for group %q: %w", item.Group.Name, callErr)
		}
		if err := a.appendGroupItems(submenu, item.Group.Items); err != nil {
			procDestroyMenu.Call(submenu)
			return err
		}
		label := a.config.EffectiveGroupsMenuSymbol() + " " + item.Group.Name
		if err := appendMenu(menu, mfPopup, submenu, label); err != nil {
			procDestroyMenu.Call(submenu)
			return err
		}
	}
	return nil
}

func (a *TrayApp) appendEntryMenu(menu uintptr, index int) error {
	entry := &a.entries[index]
	text := i18n.Text(a.language)
	submenu, _, callErr := procCreatePopupMenu.Call()
	if submenu == 0 {
		return fmt.Errorf("CreatePopupMenu for configs[%d]: %w", index, callErr)
	}
	attached := false
	defer func() {
		if !attached {
			procDestroyMenu.Call(submenu)
		}
	}()
	pathFlags := uintptr(mfString)
	if entry.config.Cron != nil && entry.config.Cron.IsEnabled() {
		pathFlags |= mfChecked
	}
	if err := appendMenu(submenu, pathFlags, entryCommand(index, commandOpenPath), a.config.MenuText(entry.config.Path)); err != nil {
		return err
	}
	if err := appendMenu(submenu, pathFlags, entryCommand(index, commandSelectExecutable), a.config.MenuText(entry.config.Command)); err != nil {
		return err
	}
	if err := appendMenu(submenu, mfSeparator, 0, ""); err != nil {
		return err
	}
	showText := text.Show
	if entry.state.Show {
		showText = text.Hide
	}
	showFlags := uintptr(mfString)
	if entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		showFlags |= mfGray | mfDisabled
	}
	if err := appendMenu(submenu, showFlags, entryCommand(index, commandShowHide), showText+a.hotkeyText(fmt.Sprintf("configs[%d].hotkey.hide_show", index))); err != nil {
		return err
	}
	toggleText := text.Enable
	if entry.state.Running {
		toggleText = text.Disable
	}
	toggleFlags := uintptr(mfString)
	if entry.busy {
		toggleFlags |= mfGray | mfDisabled
	}
	if err := appendMenu(submenu, toggleFlags, entryCommand(index, commandToggle), toggleText+a.hotkeyText(fmt.Sprintf("configs[%d].hotkey.disable_enable", index))); err != nil {
		return err
	}
	restartFlags := uintptr(mfString)
	if entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		restartFlags |= mfGray | mfDisabled
	}
	if err := appendMenu(submenu, restartFlags, entryCommand(index, commandRestart), text.RestartCommand+a.hotkeyText(fmt.Sprintf("configs[%d].hotkey.restart", index))); err != nil {
		return err
	}
	if !IsElevated() {
		elevateFlags := uintptr(mfString)
		if entry.busy {
			elevateFlags |= mfGray | mfDisabled
		}
		if err := appendMenu(submenu, elevateFlags, entryCommand(index, commandElevateEntry), text.RunAsAdministrator+a.hotkeyText(fmt.Sprintf("configs[%d].hotkey.elevate", index))); err != nil {
			return err
		}
	}
	flags := uintptr(mfPopup)
	if entry.state.Enabled {
		flags |= mfChecked
	}
	if err := appendMenu(menu, flags, submenu, entry.config.Name); err != nil {
		return err
	}
	attached = true
	return nil
}

func (a *TrayApp) appendHelpMenu(menu uintptr) error {
	text := i18n.Text(a.language)
	submenu, _, callErr := procCreatePopupMenu.Call()
	if submenu == 0 {
		return fmt.Errorf("CreatePopupMenu for Help: %w", callErr)
	}
	attached := false
	defer func() {
		if !attached {
			procDestroyMenu.Call(submenu)
		}
	}()
	if err := appendMenu(submenu, mfString, commandHome, text.Home); err != nil {
		return err
	}
	updateFlags := uintptr(mfString)
	updateText := text.CheckForUpdates
	if a.update.checking || a.update.pending != nil {
		updateFlags |= mfGray | mfDisabled
		updateText = text.CheckingForUpdates
	}
	if err := appendMenu(submenu, updateFlags, commandCheckForUpdates, updateText); err != nil {
		return err
	}
	if err := appendMenu(submenu, mfString, commandAbout, text.About); err != nil {
		return err
	}
	if err := appendMenu(menu, mfPopup, submenu, text.Help); err != nil {
		return err
	}
	attached = true
	return nil
}

func (a *TrayApp) openEntryPath(index int) error {
	if a.processes == nil || index < 0 || index >= len(a.entries) {
		return errors.New("process controller is not available")
	}
	_, _, path, _, err := resolveEntryCommand(a.baseDir, a.entries[index].config)
	if err != nil {
		return err
	}
	return shellOpen(a.hwnd, path, "", "", windows.SW_SHOWNORMAL)
}

func (a *TrayApp) selectEntryExecutable(index int) error {
	if a.processes == nil || index < 0 || index >= len(a.entries) {
		return errors.New("process controller is not available")
	}
	executable, _, _, _, err := resolveEntryCommand(a.baseDir, a.entries[index].config)
	if err != nil {
		return err
	}
	return selectInExplorer(a.hwnd, executable)
}

func (a *TrayApp) toggleStartup() error {
	enabled, err := a.startup.Enabled()
	if err != nil {
		return err
	}
	if enabled {
		return a.startup.Disable()
	}
	return a.startup.Enable()
}

func (a *TrayApp) appendDockedMenu(menu uintptr) {
	a.pruneDockedWindows()
	if len(a.docked) == 0 {
		return
	}
	submenu, _, _ := procCreatePopupMenu.Call()
	if submenu == 0 {
		return
	}
	for i, docked := range a.docked {
		caption := docked.caption
		if docked.untitledCaption {
			caption = i18n.Text(a.language).UntitledWindow
		}
		appendMenu(submenu, mfString, commandDockedBase+uintptr(i), caption)
	}
	appendMenu(submenu, mfSeparator, 0, "")
	text := i18n.Text(a.language)
	appendMenu(submenu, mfString, commandShowAllDocked, text.ShowAll+a.hotkeyText("hotkey.show_all_docked"))
	appendMenu(menu, mfPopup, submenu, text.DockedWindows)
}

func (a *TrayApp) handleLeftClick() {
	if a.sessionEndPending || a.reload != nil || a.reloadChecking || a.exclusion != nil || a.cronActionInFlight() || a.menuOpen {
		return
	}
	if len(a.config.LeftClick) == 0 {
		if err := a.toggleConsoleFallback(); err != nil {
			ShowError(productName, err.Error())
		}
		return
	}
	var errs []error
	for _, index := range a.config.LeftClick {
		if index >= 0 && index < len(a.entries) {
			if err := a.toggleEntryWindow(index); err != nil {
				errs = append(errs, err)
			}
			if a.closing.Load() || a.closed || a.closePending {
				return
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		ShowError(productName, err.Error())
	}
}

func (a *TrayApp) updateConsoleFallback() error {
	if len(a.config.LeftClick) != 0 {
		if a.console.hwnd != 0 && isWindow(a.console.hwnd) {
			a.console.targetVisible = false
			a.console.pendingToggle = true
			a.console.foregroundPending = false
			a.console.findCount = 0
			a.console.findTimedOut = false
			a.console.retryWindowAt = time.Time{}
			showWindow(a.console.hwnd, false)
			return a.updateWindowTimer()
		}
		a.console.pendingToggle = false
		a.console.foregroundPending = false
		return nil
	}
	if a.console.process != nil {
		running, err := a.console.process.running()
		if err != nil {
			return err
		}
		if running {
			return nil
		}
		a.console.process.Close()
		a.resetConsoleWindow()
		a.console = consoleFallback{}
	}
	title := fmt.Sprintf("%s Console %08X", productName, uint32(time.Now().UnixNano()))
	process, err := a.processes.StartConsoleFallback(title)
	if err != nil {
		return err
	}
	a.console.process = process
	a.console.iconPending = a.desiredConsoleIcons() != nil
	return nil
}

func (a *TrayApp) toggleConsoleFallback() error {
	if err := a.updateConsoleFallback(); err != nil {
		return err
	}
	if a.console.hwnd != 0 && !isWindow(a.console.hwnd) {
		a.resetConsoleWindow()
	}
	if a.console.hwnd != 0 {
		if a.console.pendingToggle {
			a.console.targetVisible = !a.console.targetVisible
		} else {
			a.console.targetVisible = !isWindowVisible(a.console.hwnd)
		}
		if !showWindow(a.console.hwnd, a.console.targetVisible) {
			a.console.pendingToggle = true
			a.console.foregroundPending = a.console.targetVisible
			a.console.findCount = 1
			a.console.findTimedOut = false
			a.console.retryWindowAt = time.Time{}
			return errors.Join(fmt.Errorf("ShowWindowAsync failed for %s console", productName), a.updateWindowTimer())
		}
		a.console.pendingToggle = true
		a.console.foregroundPending = a.console.targetVisible
		return a.updateWindowTimer()
	}
	if a.console.pendingToggle {
		a.console.targetVisible = !a.console.targetVisible
	} else {
		a.console.targetVisible = true
	}
	a.console.pendingToggle = true
	a.console.foregroundPending = a.console.targetVisible
	a.console.findCount = 0
	a.console.findTimedOut = false
	a.console.retryWindowAt = time.Time{}
	return a.updateWindowTimer()
}

func (a *TrayApp) dockForegroundWindow() error {
	hwnd := topLevelWindow(foregroundWindow())
	if hwnd == 0 || hwnd == a.hwnd || !isWindow(hwnd) || !isWindowVisible(hwnd) {
		return nil
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	for _, docked := range a.docked {
		if docked.hwnd == hwnd && docked.pid == pid {
			return nil
		}
	}
	if len(a.docked) >= int(commandEntryBase-commandDockedBase) {
		return errors.New("too many docked windows")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return fmt.Errorf("open docked window process: %w", err)
	}
	if !showWindow(hwnd, false) {
		windows.CloseHandle(process)
		return errors.New("ShowWindowAsync failed while docking the foreground window")
	}
	caption, untitled := windowCaption(hwnd)
	if untitled {
		caption = windowClassName(hwnd)
		untitled = caption == ""
	}
	a.docked = append(a.docked, dockedWindow{hwnd: hwnd, pid: pid, caption: caption, untitledCaption: untitled, process: process})
	return nil
}

func (a *TrayApp) restoreDockedWindow(index int) {
	if index < 0 || index >= len(a.docked) {
		return
	}
	docked := a.docked[index]
	if a.validDockedWindow(docked) {
		ret, _, _ := procShowWindowAsync.Call(docked.hwnd, windows.SW_SHOW)
		if ret == 0 {
			return
		}
		procSetForegroundWnd.Call(docked.hwnd)
	}
	windows.CloseHandle(docked.process)
	a.docked = append(a.docked[:index], a.docked[index+1:]...)
}

func (a *TrayApp) restoreAllDockedWindows() {
	remaining := a.docked[:0]
	for _, docked := range a.docked {
		if a.validDockedWindow(docked) {
			ret, _, _ := procShowWindowAsync.Call(docked.hwnd, windows.SW_SHOWNA)
			if ret == 0 {
				remaining = append(remaining, docked)
				continue
			}
		}
		windows.CloseHandle(docked.process)
	}
	a.docked = remaining
}

func (a *TrayApp) pruneDockedWindows() {
	valid := a.docked[:0]
	for _, docked := range a.docked {
		if a.validDockedWindow(docked) {
			valid = append(valid, docked)
		} else {
			windows.CloseHandle(docked.process)
		}
	}
	a.docked = valid
}

func (a *TrayApp) validDockedWindow(docked dockedWindow) bool {
	if docked.process == 0 || !isWindow(docked.hwnd) {
		return false
	}
	result, err := windows.WaitForSingleObject(docked.process, 0)
	if err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(docked.hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid == docked.pid
}

func appendMenu(menu, flags, id uintptr, text string) error {
	value, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return fmt.Errorf("menu text %q: %w", text, err)
	}
	ret, _, callErr := procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(value)))
	runtime.KeepAlive(value)
	if ret == 0 {
		return fmt.Errorf("AppendMenuW for %q: %w", text, callErr)
	}
	return nil
}

func entryCommand(index int, operation uintptr) uintptr {
	return commandEntryBase + uintptr(index)*commandEntryStep + operation
}

func (a *TrayApp) startConfiguredEntries() {
	for i := range a.entries {
		if a.sessionEndPending || a.closing.Load() || a.closed || a.processes == nil {
			return
		}
		if !a.entries[i].state.Enabled || a.entries[i].state.Running || a.entries[i].busy {
			continue
		}
		if err := a.startEntry(i, true); err != nil {
			ShowError(productName, err.Error())
			if a.closing.Load() || a.closed {
				return
			}
		}
	}
}

func (a *TrayApp) startEntry(index int, updateCache bool) error {
	if a.processes == nil || a.closing.Load() || a.closed {
		return errors.New("process controller is not available")
	}
	entry := &a.entries[index]
	entry.state.Generation++
	process, err := a.processes.Start(entry.config, entry.ownership, a.config.EffectiveStartShowSilent(), entry.state.Show, entry.config.Name)
	if err != nil {
		entry.state.Enabled = false
		entry.state.Running = false
		entry.cronTransient = false
		if updateCache {
			a.updateCachedState(index)
			a.reportCacheError(a.flushCache())
		}
		return err
	}
	a.completeEntryStart(index, process, updateCache)
	return nil
}

func (a *TrayApp) completeEntryStart(index int, process *childProcess, updateCache bool) {
	entry := &a.entries[index]
	entry.process = process
	entry.launched = true
	entry.state.Running = process != nil
	entry.state.Enabled = process != nil
	entry.cronTransient = !updateCache && process != nil
	entry.hwnd = 0
	entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show)
	entry.findCount = 0
	entry.findTimedOut = false
	entry.retryWindowAt = time.Time{}
	entry.appearanceErr = nil
	entry.appearancePending = hasWindowGeometryAppearance(entry.config)
	entry.iconPending = entry.config.Icon != "" && a.desiredEntryIcons(index) != nil
	entry.retainedIcons.Close()
	entry.retainedIconHWND = 0
	entry.showPending = false
	entry.foregroundPending = false
	if updateCache {
		a.updateCachedState(index)
		a.reportCacheError(a.flushCache())
	}
	a.reportWindowTimerError(a.updateWindowTimer())
}

func (a *TrayApp) beginStopEntry(index int, operation processOperation, cacheShow bool) {
	a.beginStopEntryWithOptions(index, operation, a.entries[index].state.Show, cacheShow, true, 0)
}

func (a *TrayApp) beginStopEntryWithOptions(index int, operation processOperation, restartShow, cacheShow, updateCache bool, cronToken uint64) bool {
	entry := &a.entries[index]
	if entry.busy || entry.process == nil || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		return false
	}
	process := entry.process
	entryConfig := entry.config
	controller := a.processes
	name := entryConfig.Name
	result := processResult{
		index:       index,
		generation:  entry.state.Generation,
		operation:   operation,
		process:     process,
		show:        entry.state.Show,
		restartShow: restartShow,
		cacheShow:   cacheShow,
		updateCache: updateCache,
		cronToken:   cronToken,
		hwnd:        entry.hwnd,
	}
	a.cacheEntryWindow(index)
	var cacheErr error
	if operation == processStop && updateCache {
		entry.cronTransient = false
		entry.state.Enabled = false
		if result.cacheShow {
			entry.state.Show = false
		}
		a.updateCachedStopState(index, result.cacheShow)
		cacheErr = a.flushCache()
	}
	entry.process = nil
	entry.hwnd = 0
	entry.needsWindow = false
	entry.findCount = 0
	entry.findTimedOut = false
	entry.retryWindowAt = time.Time{}
	entry.appearanceErr = nil
	entry.showPending = false
	entry.foregroundPending = false
	entry.busy = true
	a.processWorkers.Add(1)
	go func() {
		defer a.processWorkers.Done()
		if err := controller.stop(process, entryConfig); err != nil {
			result.err = fmt.Errorf("stop %s: %w", name, err)
		}
		a.processResults <- result
		for !a.closing.Load() {
			ret, _, _ := procPostMessageW.Call(a.hwnd, wmProcessComplete, 0, 0)
			if ret != 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	a.reportCacheError(cacheErr)
	a.reportWindowTimerError(a.updateWindowTimer())
	return true
}

func (a *TrayApp) beginReloadStop(target reloadStopState, reloadID uint64) {
	entry := &a.entries[target.index]
	if entry.busy || entry.process == nil || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		return
	}
	process := entry.process
	result := processResult{
		index:      target.index,
		generation: entry.state.Generation,
		operation:  processReloadStop,
		process:    process,
		show:       target.show,
		reloadID:   reloadID,
		hwnd:       entry.hwnd,
	}
	name := entry.config.Name
	entryConfig := entry.config
	controller := a.processes
	entry.process = nil
	entry.hwnd = 0
	entry.needsWindow = false
	entry.showPending = false
	entry.foregroundPending = false
	entry.busy = true
	a.processWorkers.Add(1)
	go func() {
		defer a.processWorkers.Done()
		if err := controller.stop(process, entryConfig); err != nil {
			result.err = fmt.Errorf("stop %s for config reload: %w", name, err)
		}
		a.processResults <- result
		for !a.closing.Load() {
			ret, _, _ := procPostMessageW.Call(a.hwnd, wmProcessComplete, 0, 0)
			if ret != 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

func (a *TrayApp) toggleEntry(index int) error {
	if a.entries[index].busy {
		return nil
	}
	if a.entries[index].state.Running {
		a.beginStopEntry(index, processStop, false)
		return nil
	}
	return a.startEntryWithExclusion(index)
}

func (a *TrayApp) startEntryWithExclusion(index int) error {
	_, err := a.startEntryWithExclusionOptions(index, a.entries[index].config.EffectiveStartShow(), true, 0)
	return err
}

func (a *TrayApp) startEntryWithExclusionOptions(index int, show, updateCache bool, cronToken uint64) (bool, error) {
	entry := &a.entries[index]
	if entry.config.ExclusionID == nil {
		entry.state.Enabled = true
		entry.state.Show = show
		return false, a.startEntry(index, updateCache)
	}
	if a.exclusion != nil {
		return false, errors.New("another exclusion group is already switching")
	}
	configs := make([]config.EntryConfig, len(a.entries))
	states := make([]domain.EntryState, len(a.entries))
	for i := range a.entries {
		configs[i] = a.entries[i].config
		states[i] = a.entries[i].state
	}
	conflicts := domain.ExclusionConflicts(configs, states, index)
	for _, conflict := range conflicts {
		if a.entries[conflict].busy {
			return false, fmt.Errorf("cannot start %s while exclusion peer %s is busy", entry.config.Name, a.entries[conflict].config.Name)
		}
	}
	if len(conflicts) == 0 {
		entry.state.Enabled = true
		entry.state.Show = show
		return false, a.startEntry(index, updateCache)
	}
	pending := &pendingExclusionStart{
		target:      index,
		waiting:     make(map[int]uint64, len(conflicts)),
		startShow:   show,
		updateCache: updateCache,
		cronToken:   cronToken,
	}
	for _, conflict := range conflicts {
		pending.waiting[conflict] = a.entries[conflict].state.Generation
		pending.peers = append(pending.peers, exclusionPeerState{
			index:         conflict,
			enabled:       a.entries[conflict].state.Enabled,
			show:          a.entries[conflict].state.Show,
			cronTransient: a.entries[conflict].cronTransient,
		})
	}
	a.exclusion = pending
	entry.busy = true
	for _, conflict := range conflicts {
		a.beginStopEntryWithOptions(conflict, processExclusionStop, a.entries[conflict].state.Show, true, updateCache, cronToken)
	}
	return true, nil
}

func (a *TrayApp) finishExclusionStop(result processResult, stopped bool) error {
	pending := a.exclusion
	if pending == nil || pending.waiting[result.index] != result.generation {
		return nil
	}
	delete(pending.waiting, result.index)
	if !stopped {
		pending.failed = true
		pending.priorErr = errors.Join(pending.priorErr, result.err)
	}
	if len(pending.waiting) != 0 {
		return nil
	}
	if pending.target < 0 || pending.target >= len(a.entries) {
		a.exclusion = nil
		return nil
	}
	target := &a.entries[pending.target]
	if pending.failed {
		errs := a.restoreExclusionPeers(pending)
		target.state.Enabled = false
		if pending.updateCache {
			a.updateCachedState(pending.target)
			a.reportCacheError(a.flushCache())
		}
		target.busy = false
		a.exclusion = nil
		err := errors.Join(pending.priorErr, errors.Join(errs...))
		if pending.cronToken != 0 {
			err = errors.Join(err, a.completeCronAction(pending.target, pending.cronToken, err))
		}
		return err
	}
	if a.closing.Load() || a.closed {
		target.busy = false
		a.exclusion = nil
		return nil
	}
	if pending.updateCache {
		for _, peer := range pending.peers {
			if peer.index >= 0 && peer.index < len(a.entries) {
				a.updateCachedStopState(peer.index, true)
			}
		}
		if err := a.flushCache(); err != nil {
			errs := []error{err}
			errs = append(errs, a.restoreExclusionPeers(pending)...)
			target.state.Enabled = false
			target.busy = false
			a.exclusion = nil
			joined := errors.Join(errs...)
			if pending.cronToken != 0 {
				joined = errors.Join(joined, a.completeCronAction(pending.target, pending.cronToken, joined))
			}
			return joined
		}
	}
	target.state.Enabled = true
	target.state.Show = pending.startShow
	if err := a.startEntry(pending.target, pending.updateCache); err != nil {
		errs := []error{err}
		if !a.closing.Load() && !a.closed {
			errs = append(errs, a.restoreExclusionPeers(pending)...)
		}
		target.busy = false
		a.exclusion = nil
		joined := errors.Join(errs...)
		if pending.cronToken != 0 {
			joined = errors.Join(joined, a.completeCronAction(pending.target, pending.cronToken, joined))
		}
		return joined
	}
	if a.closing.Load() || a.closed {
		target.busy = false
		a.exclusion = nil
		return nil
	}
	target.busy = false
	a.exclusion = nil
	if pending.cronToken != 0 {
		return a.completeCronAction(pending.target, pending.cronToken, nil)
	}
	return nil
}

func (a *TrayApp) restoreExclusionPeers(pending *pendingExclusionStart) []error {
	var errs []error
	for _, peer := range pending.peers {
		if a.closing.Load() || a.closed {
			return errs
		}
		if peer.index < 0 || peer.index >= len(a.entries) {
			continue
		}
		entry := &a.entries[peer.index]
		if entry.state.Running {
			entry.state.Enabled = peer.enabled
			entry.state.Show = peer.show
			entry.cronTransient = peer.cronTransient
			if pending.updateCache && !peer.cronTransient {
				a.updateCachedStopState(peer.index, true)
			}
			continue
		}
		if !peer.enabled {
			continue
		}
		entry.state.Enabled = peer.enabled
		entry.state.Show = peer.show
		if err := a.startEntry(peer.index, pending.updateCache && !peer.cronTransient); err != nil {
			errs = append(errs, fmt.Errorf("restore %s after failed exclusion switch: %w", entry.config.Name, err))
		}
		entry.cronTransient = peer.cronTransient
	}
	if pending.updateCache && !a.closing.Load() && !a.closed {
		if err := a.flushCache(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (a *TrayApp) refreshEntries() {
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.process == nil {
			continue
		}
		running, err := entry.process.running()
		if err != nil || running {
			continue
		}
		a.resetStoppedEntry(i, false)
	}
	a.discoverWindows()
}

func (a *TrayApp) scheduleConfigReload() {
	if a == nil || a.hwnd == 0 || !a.config.HotReloadEnabled() || a.closing.Load() {
		return
	}
	a.reloadPending = true
	if a.sessionEndPending {
		return
	}
	procSetTimer.Call(a.hwnd, configReloadTimerID, configReloadDelay, 0)
}

func (a *TrayApp) resumePendingReload() {
	if a.sessionEndPending || !a.reloadPending || a.menuOpen || a.reloadChecking || a.reload != nil || a.hasBusyEntries() {
		return
	}
	a.scheduleConfigReload()
}

func (a *TrayApp) hasBusyEntries() bool {
	for i := range a.entries {
		if a.entries[i].busy {
			return true
		}
	}
	return false
}

func (a *TrayApp) checkConfigReload() {
	procKillTimer.Call(a.hwnd, configReloadTimerID)
	if a.sessionEndPending || !a.reloadPending || a.menuOpen || a.reloadChecking || a.reload != nil || a.hasBusyEntries() {
		return
	}
	a.reloadPending = false
	stamp, err := config.StatFile(a.configPath)
	if err != nil {
		a.reloadPending = true
		procSetTimer.Call(a.hwnd, configReloadTimerID, configReloadDelay, 0)
		return
	}
	if stamp.Equal(a.observedConfigStamp) {
		return
	}
	a.reloadChecking = true
	defer func() {
		a.reloadChecking = false
		a.resumeCron(time.Now())
		a.resumePendingReload()
	}()

	keep := a.config.AutoHotReloading
	if !keep {
		switch showReloadConfirm(a.language, filepath.Base(a.configPath)) {
		case idYes:
			keep = false
		case idNo:
			keep = true
		default:
			a.observedConfigStamp = stamp
			return
		}
	}
	if a.sessionEndPending {
		a.reloadPending = true
		return
	}
	if a.closing.Load() || a.closed {
		return
	}
	candidate, candidateStamp, err := config.LoadSnapshot(a.configPath)
	if err != nil {
		a.observedConfigStamp = stamp
		ShowError(productName, err.Error())
		return
	}
	if candidate.RequireAdmin && !IsElevated() {
		a.observedConfigStamp = candidateStamp
		ShowError(productName, i18n.Text(a.language).ReloadRequiresAdmin)
		return
	}
	policy := statecache.DiscardPrevious
	if keep {
		policy = statecache.KeepPrevious
	}
	a.prepareReload(&candidate, candidateStamp, keep, policy)
}

func (a *TrayApp) prepareReload(candidate *config.Config, stamp config.FileStamp, keep bool, policy statecache.RebasePolicy) {
	if a.sessionEndPending {
		return
	}
	a.refreshEntries()
	oldConfigs := make([]config.EntryConfig, len(a.entries))
	for i := range a.entries {
		a.cacheEntryWindow(i)
		oldConfigs[i] = a.entries[i].config
	}
	if a.closing.Load() || a.closed {
		return
	}
	candidateCron, cronErr := newCronRuntime(*candidate, time.Now())
	if cronErr != nil {
		a.observedConfigStamp = stamp
		ShowError(productName, cronErr.Error())
		return
	}
	candidateIcons, iconErr := loadIconResources(a.baseDir, *candidate)
	if iconErr != nil {
		if candidateIcons != nil {
			candidateIcons.Close()
		}
		a.observedConfigStamp = stamp
		ShowError(productName, iconErr.Error())
		return
	}
	candidateHotkeys, hotkeyErr := a.stageHotkeys(*candidate)
	if hotkeyErr != nil {
		candidateIcons.Close()
		a.observedConfigStamp = stamp
		ShowError(productName, hotkeyErr.Error())
		return
	}
	candidateCache, err := statecache.Rebase(a.cachePath, candidate, stamp, a.cache, policy)
	if err != nil {
		candidateIcons.Close()
		a.discardStagedHotkeys(candidateHotkeys)
		a.observedConfigStamp = stamp
		ShowError(productName, err.Error())
		return
	}
	matches := domain.MatchReloadEntries(oldConfigs, candidate.Configs)
	preserve := make([]bool, len(candidate.Configs))
	usedOld := make([]bool, len(a.entries))
	if keep {
		for newIndex, oldIndex := range matches {
			if oldIndex < 0 {
				continue
			}
			oldEntry := &a.entries[oldIndex]
			if oldEntry.state.Running && oldEntry.process != nil && oldEntry.ownership == domain.ManagedAndJobOwned &&
				domain.CanKeepManagedProcess(oldEntry.config, candidate.Configs[newIndex]) {
				preserve[newIndex] = true
				usedOld[oldIndex] = true
			}
		}
	}
	a.reloadSerial++
	reload := &pendingReload{
		id:               a.reloadSerial,
		config:           candidate,
		cache:            candidateCache,
		stamp:            stamp,
		keep:             keep,
		matches:          matches,
		preserve:         preserve,
		preserveLaunched: make([]bool, len(candidate.Configs)),
		hotkeys:          candidateHotkeys,
		cron:             candidateCron,
		icons:            candidateIcons,
		language:         i18n.Resolve(candidate.Lang, i18n.DetectSystemLocale()),
	}
	for newIndex, oldIndex := range matches {
		if oldIndex < 0 || a.entries[oldIndex].ownership == domain.ManagedAndJobOwned || !a.entries[oldIndex].launched ||
			!domain.SameLaunchIdentity(a.entries[oldIndex].config, candidate.Configs[newIndex]) {
			continue
		}
		reload.preserveLaunched[newIndex] = true
	}
	for i := range a.entries {
		entry := &a.entries[i]
		if usedOld[i] || !entry.state.Running || entry.process == nil || entry.ownership != domain.ManagedAndJobOwned {
			continue
		}
		reload.stops = append(reload.stops, reloadStopState{
			index:         i,
			enabled:       entry.state.Enabled,
			show:          entry.state.Show,
			cronTransient: entry.cronTransient,
		})
	}
	reload.pending = len(reload.stops)
	a.reload = reload
	a.suspendCron()
	procKillTimer.Call(a.hwnd, windowTimerID)
	if reload.pending == 0 {
		a.commitReload()
		return
	}
	for _, target := range reload.stops {
		a.beginReloadStop(target, reload.id)
	}
}

func (a *TrayApp) finishReloadStop(result processResult) {
	reload := a.reload
	if reload == nil || result.reloadID != reload.id {
		result.process.Close()
		return
	}
	if result.index < 0 || result.index >= len(a.entries) {
		result.process.Close()
		return
	}
	entry := &a.entries[result.index]
	entry.busy = false
	entry.process = result.process
	stopped := result.err == nil
	if !stopped {
		running, err := result.process.running()
		if err != nil {
			reload.errs = append(reload.errs, result.err, err)
			reload.failed = true
			entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show)
		} else if running {
			reload.errs = append(reload.errs, result.err)
			reload.failed = true
			if result.hwnd != 0 && entryOwnsWindow(entry, result.hwnd) {
				entry.hwnd = result.hwnd
			}
			entry.needsWindow = entry.hwnd == 0 && entryNeedsWindow(entry.config, entry.state.Show)
		} else {
			stopped = true
		}
	}
	if stopped {
		a.resetStoppedEntry(result.index, false)
	}
	reload.pending--
	if reload.pending != 0 {
		return
	}
	if reload.failed {
		a.rollbackReload()
		return
	}
	a.commitReload()
}

func (a *TrayApp) rollbackReload() {
	reload := a.reload
	if reload == nil {
		return
	}
	var errs []error
	a.rollbackStagedHotkeys(reload.hotkeys)
	if reload.icons != nil {
		reload.icons.Close()
	}
	errs = append(errs, reload.errs...)
	for _, target := range reload.stops {
		entry := &a.entries[target.index]
		if entry.state.Running {
			entry.cronTransient = target.cronTransient
			entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show)
			entry.findCount = 0
			entry.findTimedOut = false
			entry.retryWindowAt = time.Time{}
			continue
		}
		entry.state.Enabled = target.enabled
		entry.state.Show = target.show
		if target.enabled {
			if err := a.startEntry(target.index, !target.cronTransient); err != nil {
				errs = append(errs, fmt.Errorf("restore %s after failed config reload: %w", entry.config.Name, err))
			}
			entry.cronTransient = target.cronTransient
		}
	}
	a.observedConfigStamp = reload.stamp
	a.reloadPending = false
	if currentStamp, err := config.StatFile(a.configPath); err != nil || !currentStamp.Equal(reload.stamp) {
		a.reloadPending = true
	}
	procKillTimer.Call(a.hwnd, configReloadTimerID)
	if err := a.updateWindowTimer(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil && !a.sessionEndPending && !a.closing.Load() {
		ShowError(productName, err.Error())
	}
	a.reload = nil
	if a.closing.Load() || a.closed {
		return
	}
	a.resumeCron(time.Now())
	a.resumePendingReload()
	a.resumeUpdateUI()
}

func (a *TrayApp) commitReload() {
	reload := a.reload
	if reload == nil || a.closing.Load() || a.closed {
		return
	}
	if reload.cache == nil {
		if err := statecache.Remove(a.cachePath); err != nil {
			reload.errs = append(reload.errs, err)
			reload.failed = true
			a.rollbackReload()
			return
		}
	}
	newTrayIcon := a.defaultIcon
	if reload.icons != nil && reload.icons.tray.handle != 0 {
		newTrayIcon = reload.icons.tray.handle
	}
	if err := a.modifyTrayIcon(newTrayIcon); err != nil {
		reload.errs = append(reload.errs, err)
		reload.failed = true
		a.rollbackReload()
		return
	}
	newEntries := make([]trayEntry, len(reload.config.Configs))
	for i, cfg := range reload.config.Configs {
		newEntries[i] = trayEntry{
			config:    cfg,
			ownership: domain.OwnershipFor(cfg),
			state:     domain.EntryState{Enabled: cfg.Enabled, Show: cfg.EffectiveStartShow()},
		}
		if reload.preserveLaunched[i] {
			newEntries[i].state.Enabled = false
			newEntries[i].launched = true
			continue
		}
		if !reload.preserve[i] {
			continue
		}
		oldIndex := reload.matches[i]
		oldEntry := &a.entries[oldIndex]
		running, err := oldEntry.process.running()
		if err != nil || !running {
			oldEntry.process.Close()
			oldEntry.process = nil
			if err == nil {
				oldEntry.retainedIcons.Close()
				oldEntry.retainedIconHWND = 0
			}
			continue
		}
		newEntries[i].process = oldEntry.process
		oldEntry.process = nil
		newEntries[i].state = domain.PreserveReloadProcessState(newEntries[i].state, oldEntry.state)
		newEntries[i].hwnd = oldEntry.hwnd
		newEntries[i].needsWindow = oldEntry.needsWindow
		newEntries[i].findCount = oldEntry.findCount
		newEntries[i].findTimedOut = oldEntry.findTimedOut
		newEntries[i].retryWindowAt = oldEntry.retryWindowAt
		newEntries[i].appearanceErr = oldEntry.appearanceErr
		newEntries[i].appearancePending = oldEntry.appearancePending
		newEntries[i].iconPending = oldEntry.iconPending
		newEntries[i].retainedIconHWND = oldEntry.retainedIconHWND
		newEntries[i].showPending = oldEntry.showPending
		newEntries[i].foregroundPending = oldEntry.foregroundPending
		newEntries[i].cronTransient = oldEntry.cronTransient
		if newEntries[i].hwnd != 0 && !entryOwnsWindow(&newEntries[i], newEntries[i].hwnd) {
			newEntries[i].hwnd = 0
		}
		iconsChanged := oldEntry.config.Icon != "" || cfg.Icon != "" || oldEntry.iconPending
		if iconsChanged {
			newEntries[i].retainedIcons = detachCurrentWindowIcons(oldEntry, a.icons, oldIndex)
			if newEntries[i].retainedIconHWND == 0 && (newEntries[i].retainedIcons.big.handle != 0 || newEntries[i].retainedIcons.small.handle != 0) {
				newEntries[i].retainedIconHWND = oldEntry.hwnd
			}
			newEntries[i].iconPending = true
		}
		if newEntries[i].hwnd != 0 {
			appearanceErr := applyReloadWindowAppearance(newEntries[i].hwnd, oldEntry.config, cfg)
			newEntries[i].appearancePending = appearanceErr != nil
			var iconErr error
			if newEntries[i].iconPending {
				var desired *windowIconPair
				if reload.icons != nil && i < len(reload.icons.entries) {
					desired = reload.icons.entries[i]
				}
				iconErr = applyDesiredWindowIcons(newEntries[i].hwnd, desired, &newEntries[i].retainedIcons, &newEntries[i].retainedIconHWND)
				newEntries[i].iconPending = iconErr != nil
			}
			if appearanceErr != nil {
				newEntries[i].appearanceErr = appearanceErr
				newEntries[i].findCount = 1
				newEntries[i].retryWindowAt = time.Now().Add(windowTimerDelay * time.Millisecond)
			} else if iconErr != nil {
				newEntries[i].appearanceErr = iconErr
				newEntries[i].findCount = 1
				newEntries[i].retryWindowAt = time.Now().Add(windowTimerDelay * time.Millisecond)
			} else {
				newEntries[i].appearanceErr = nil
			}
		}
		if newEntries[i].hwnd == 0 {
			newEntries[i].needsWindow = entryNeedsWindow(cfg, newEntries[i].state.Show) || newEntries[i].iconPending || newEntries[i].appearancePending
			if newEntries[i].needsWindow {
				newEntries[i].findCount = 0
				newEntries[i].findTimedOut = false
				newEntries[i].retryWindowAt = time.Time{}
			}
		}
	}
	if a.console.process != nil && (a.config.Icon != "" || reload.config.Icon != "" || a.console.iconPending) {
		var desired *windowIconPair
		if reload.icons != nil {
			desired = reload.icons.console
		}
		if a.console.hwnd != 0 && isWindow(a.console.hwnd) {
			a.console.retainedIcons = detachCurrentConsoleIcons(&a.console, a.icons)
			if a.console.retainedIconHWND == 0 && (a.console.retainedIcons.big.handle != 0 || a.console.retainedIcons.small.handle != 0) {
				a.console.retainedIconHWND = a.console.hwnd
			}
			a.console.iconErr = applyDesiredWindowIcons(a.console.hwnd, desired, &a.console.retainedIcons, &a.console.retainedIconHWND)
			a.console.iconPending = a.console.iconErr != nil
			if a.console.iconPending {
				a.console.findCount = 1
				a.console.findTimedOut = false
				a.console.retryWindowAt = time.Now().Add(windowTimerDelay * time.Millisecond)
			}
		} else {
			a.resetConsoleWindow()
			a.console.iconPending = desired != nil
		}
	}
	a.config = *reload.config
	a.language = reload.language
	a.configStamp = reload.stamp
	a.observedConfigStamp = reload.stamp
	a.cache = reload.cache
	if a.icons != nil {
		a.icons.Close()
	}
	a.icons = reload.icons
	a.entries = newEntries
	a.processResults = make(chan processResult, len(newEntries))
	a.cron = reload.cron
	if err := a.resetCronSchedule(time.Now()); err != nil {
		reload.errs = append(reload.errs, err)
	}
	a.commitStagedHotkeys(reload.hotkeys)
	a.reload = nil
	if currentStamp, err := config.StatFile(a.configPath); err == nil && !currentStamp.Equal(a.configStamp) && a.config.HotReloadEnabled() {
		a.reloadPending = true
	}
	for i := range a.entries {
		if a.entries[i].state.Running && !a.entries[i].cronTransient {
			a.updateCachedState(i)
		}
	}
	a.reportCacheError(a.flushCache())
	if a.closing.Load() || a.closed {
		return
	}
	if err := a.updateConfigWatcher(); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return
		}
	}
	a.startConfiguredEntries()
	if err := a.updateConsoleFallback(); err != nil {
		reload.errs = append(reload.errs, err)
	}
	a.reportWindowTimerError(a.updateWindowTimer())
	a.resumeCron(time.Now())
	if err := errors.Join(reload.errs...); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed {
			return
		}
	}
	a.resumePendingReload()
	a.resumeUpdateUI()
}

func (a *TrayApp) updateConfigWatcher() error {
	if a.sessionEndPending || a.closing.Load() || a.closed || a.hwnd == 0 {
		return nil
	}
	if !a.config.HotReloadEnabled() {
		a.stopConfigWatcher()
		return nil
	}
	if a.watcher != nil {
		return nil
	}
	watcher, err := newDirectoryWatcher(filepath.Dir(a.configPath), a.hwnd)
	if err != nil {
		return err
	}
	a.watcher = watcher
	return nil
}

func (a *TrayApp) stopConfigWatcher() {
	if a.watcher == nil {
		return
	}
	if err := a.watcher.Close(); err != nil && !a.closing.Load() {
		ShowError(productName, err.Error())
	}
	a.watcher = nil
}

func (a *TrayApp) discoverWindows() {
	var timedOut []string
	targetPIDs := make(map[uint32]struct{})
	now := time.Now()
	if a.console.process != nil {
		if running, err := a.console.process.running(); err == nil && !running {
			a.console.process.Close()
			a.resetConsoleWindow()
			a.console = consoleFallback{}
		} else if a.console.hwnd != 0 && !isWindow(a.console.hwnd) {
			a.resetConsoleWindow()
		}
		if a.console.hwnd != 0 {
			if a.console.iconPending && (!a.console.findTimedOut || !now.Before(a.console.retryWindowAt)) {
				a.console.iconErr = applyDesiredWindowIcons(a.console.hwnd, a.desiredConsoleIcons(), &a.console.retainedIcons, &a.console.retainedIconHWND)
				a.console.iconPending = a.console.iconErr != nil
				if a.console.iconPending {
					a.recordConsoleIconFailure(now, &timedOut)
				} else {
					a.console.iconErr = nil
					if !a.console.pendingToggle {
						a.resetConsoleWindowRetry()
					}
				}
			}
			if a.console.pendingToggle && (!a.console.findTimedOut || !now.Before(a.console.retryWindowAt)) {
				if showWindow(a.console.hwnd, a.console.targetVisible) {
					a.console.pendingToggle = isWindowVisible(a.console.hwnd) != a.console.targetVisible
					if a.console.pendingToggle {
						a.console.findCount++
						if a.console.findCount >= windowFindLimit {
							a.console.findTimedOut = true
							a.console.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
						}
					} else if !a.console.iconPending {
						a.resetConsoleWindowRetry()
					}
				} else {
					a.console.findCount++
					if a.console.findCount >= windowFindLimit {
						a.console.findTimedOut = true
						a.console.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
					}
				}
			}
			if a.console.foregroundPending && isWindowVisible(a.console.hwnd) {
				procSetForegroundWnd.Call(a.console.hwnd)
				a.console.foregroundPending = false
			}
		} else if (a.console.pendingToggle || a.console.iconPending) && (!a.console.findTimedOut || !now.Before(a.console.retryWindowAt)) {
			a.console.hwnd = a.console.process.consoleWindow()
			if a.console.hwnd == 0 {
				a.console.findCount++
				if a.console.findCount >= windowFindLimit {
					a.console.findTimedOut = true
					a.console.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
				}
			} else {
				a.resetConsoleWindowRetry()
			}
		}
	}
	for i := range a.entries {
		entry := &a.entries[i]
		if !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned || entry.process == nil {
			continue
		}
		if running, err := entry.process.running(); err == nil && !running {
			a.resetStoppedEntry(i, false)
			continue
		}
		if entry.hwnd != 0 {
			if entryOwnsWindow(entry, entry.hwnd) {
				if entry.appearancePending && (!entry.findTimedOut || !now.Before(entry.retryWindowAt)) {
					appearanceErr := applyWindowAppearance(entry.hwnd, entry.config)
					entry.appearancePending = appearanceErr != nil
					if appearanceErr != nil {
						a.recordWindowFindFailure(entry, appearanceErr, now, &timedOut)
					} else if !entry.iconPending {
						entry.findCount = 0
						entry.findTimedOut = false
						entry.retryWindowAt = time.Time{}
						entry.appearanceErr = nil
					}
				}
				if entry.iconPending && (!entry.findTimedOut || !now.Before(entry.retryWindowAt)) {
					iconErr := applyDesiredWindowIcons(entry.hwnd, a.desiredEntryIcons(i), &entry.retainedIcons, &entry.retainedIconHWND)
					entry.iconPending = iconErr != nil
					if iconErr != nil {
						a.recordWindowFindFailure(entry, iconErr, now, &timedOut)
					} else {
						entry.findCount = 0
						entry.findTimedOut = false
						entry.retryWindowAt = time.Time{}
						entry.appearanceErr = nil
					}
				}
				if entry.showPending && (!entry.state.Show || !entry.appearancePending) && (!entry.findTimedOut || !now.Before(entry.retryWindowAt)) {
					showWindow(entry.hwnd, entry.state.Show)
					entry.showPending = isWindowVisible(entry.hwnd) != entry.state.Show
					if entry.showPending {
						a.recordWindowFindFailure(entry, nil, now, &timedOut)
					} else if !entry.appearancePending && !entry.iconPending {
						entry.findCount = 0
						entry.findTimedOut = false
						entry.retryWindowAt = time.Time{}
						entry.appearanceErr = nil
					}
				}
				visible := isWindowVisible(entry.hwnd)
				if !entry.showPending {
					entry.state.Show = visible
				}
				if entry.foregroundPending && visible && !entry.appearancePending {
					procSetForegroundWnd.Call(entry.hwnd)
					entry.foregroundPending = false
				}
				entry.needsWindow = false
				if !entry.appearancePending {
					a.cacheEntryWindow(i)
				}
				continue
			}
			entry.hwnd = 0
			entry.appearancePending = hasWindowGeometryAppearance(entry.config)
			entry.iconPending = entry.config.Icon != "" && a.desiredEntryIcons(i) != nil
			if entry.retainedIconHWND != 0 {
				if ret, _, _ := procIsWindow.Call(entry.retainedIconHWND); ret == 0 {
					entry.retainedIcons.Close()
				} else {
					entry.retainedIcons.Abandon()
				}
			}
			entry.retainedIconHWND = 0
			pendingShow := entry.showPending
			entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show) || entry.iconPending || entry.appearancePending || pendingShow
			entry.findCount = 0
			entry.findTimedOut = false
			entry.retryWindowAt = time.Time{}
			entry.showPending = pendingShow
			entry.foregroundPending = entry.config.IsGUI && entry.state.Show && entry.foregroundPending
		}
		if !entry.needsWindow {
			continue
		}
		if entry.findTimedOut && now.Before(entry.retryWindowAt) {
			continue
		}
		if !entry.config.IsGUI {
			entry.hwnd = entry.process.consoleWindow()
			if entry.hwnd == 0 {
				a.recordWindowFindFailure(entry, nil, now, &timedOut)
				continue
			}
			a.completeWindowDiscovery(i, now, &timedOut, true)
			continue
		}
		targetPIDs[entry.process.pid] = struct{}{}
	}
	if len(targetPIDs) != 0 {
		windowsByPID, err := findWindows(targetPIDs)
		if err != nil {
			if !a.windowEnumErrShown {
				timedOut = append(timedOut, err.Error())
				a.windowEnumErrShown = true
			}
			for i := range a.entries {
				entry := &a.entries[i]
				if entry.process == nil {
					continue
				}
				if _, due := targetPIDs[entry.process.pid]; due && entry.findTimedOut {
					entry.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
				}
			}
		} else {
			a.windowEnumErrShown = false
			for i := range a.entries {
				entry := &a.entries[i]
				if !entry.needsWindow || entry.process == nil {
					continue
				}
				if _, due := targetPIDs[entry.process.pid]; !due {
					continue
				}
				entry.hwnd = windowsByPID[entry.process.pid]
				if entry.hwnd == 0 {
					a.recordWindowFindFailure(entry, nil, now, &timedOut)
					continue
				}
				a.completeWindowDiscovery(i, now, &timedOut, true)
			}
		}
	}
	if err := a.updateWindowTimer(); err != nil {
		timedOut = append(timedOut, err.Error())
	}
	a.showWindowDiscoveryError(timedOut)
}

func (a *TrayApp) completeWindowDiscovery(index int, now time.Time, failures *[]string, applyAppearance bool) {
	entry := &a.entries[index]
	entry.showPending = true
	var appearanceErr error
	if applyAppearance {
		appearanceErr = applyWindowAppearance(entry.hwnd, entry.config)
		entry.appearancePending = appearanceErr != nil
	}
	var iconErr error
	if entry.iconPending {
		iconErr = applyDesiredWindowIcons(entry.hwnd, a.desiredEntryIcons(index), &entry.retainedIcons, &entry.retainedIconHWND)
		entry.iconPending = iconErr != nil
	}
	if appearanceErr != nil {
		a.recordWindowFindFailure(entry, appearanceErr, now, failures)
		return
	}
	if iconErr != nil {
		a.recordWindowFindFailure(entry, iconErr, now, failures)
	} else {
		entry.appearanceErr = nil
	}
	showWindow(entry.hwnd, entry.state.Show)
	if isWindowVisible(entry.hwnd) != entry.state.Show {
		entry.needsWindow = false
		return
	}

	entry.showPending = false
	if entry.foregroundPending && entry.state.Show && !entry.appearancePending {
		procSetForegroundWnd.Call(entry.hwnd)
		entry.foregroundPending = false
	}

	if entry.state.Show && entry.config.Topmost {
		if err := setWindowTopmost(entry.hwnd); err != nil {
			entry.appearancePending = true
			a.recordWindowFindFailure(entry, err, now, failures)
			return
		}
	}
	entry.needsWindow = false
	if !entry.iconPending {
		entry.findCount = 0
		entry.findTimedOut = false
		entry.retryWindowAt = time.Time{}
	}
}

func (a *TrayApp) recordWindowFindFailure(entry *trayEntry, appearanceErr error, now time.Time, failures *[]string) {
	if appearanceErr != nil {
		entry.appearanceErr = appearanceErr
	}
	if entry.findTimedOut {
		entry.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
		return
	}
	entry.findCount++
	if entry.findCount < windowFindLimit {
		return
	}
	entry.findTimedOut = true
	entry.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
	if entry.appearanceErr != nil {
		*failures = append(*failures, fmt.Sprintf("%s: %v", entry.config.Name, entry.appearanceErr))
	} else {
		*failures = append(*failures, a.windowDiscoveryTimeout(entry.config.Name))
	}
}

func (a *TrayApp) resetConsoleWindow() {
	if a.console.retainedIconHWND == 0 {
		a.console.retainedIcons.Close()
	} else if isWindow(a.console.retainedIconHWND) {
		a.console.retainedIcons.Abandon()
	} else {
		a.console.retainedIcons.Close()
	}
	a.console.retainedIconHWND = 0
	a.console.hwnd = 0
	a.console.iconPending = a.desiredConsoleIcons() != nil
	a.console.iconErr = nil
	a.resetConsoleWindowRetry()
}

func (a *TrayApp) resetConsoleWindowRetry() {
	a.console.findCount = 0
	a.console.findTimedOut = false
	a.console.retryWindowAt = time.Time{}
}

func (a *TrayApp) recordConsoleIconFailure(now time.Time, failures *[]string) {
	if a.console.findTimedOut {
		a.console.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
		return
	}
	a.console.findCount++
	if a.console.findCount < windowFindLimit {
		return
	}
	a.console.findTimedOut = true
	a.console.retryWindowAt = now.Add(windowRetryDelay * time.Millisecond)
	*failures = append(*failures, fmt.Sprintf("%s console: %v", productName, a.console.iconErr))
}

func (a *TrayApp) updateWindowTimer() error {
	if a == nil || a.hwnd == 0 {
		return nil
	}
	if a.sessionEndPending {
		procKillTimer.Call(a.hwnd, windowTimerID)
		return nil
	}
	if a.showingWindowError {
		return nil
	}
	now := time.Now()
	var delay time.Duration
	found := false
	if a.console.process != nil && (a.console.pendingToggle || a.console.foregroundPending || a.console.iconPending) {
		delay = windowTimerDelay * time.Millisecond
		if a.console.findTimedOut {
			delay = time.Until(a.console.retryWindowAt)
			if delay <= 0 {
				delay = time.Millisecond
			}
		}
		found = true
	}
	for i := range a.entries {
		entry := &a.entries[i]
		if !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned || entry.process == nil {
			continue
		}
		entryDelay, active := entryWindowTimerDelay(entry, now)
		if !active {
			continue
		}
		if !found || entryDelay < delay {
			delay = entryDelay
		}
		found = true
	}
	if found {
		ret, _, err := procSetTimer.Call(a.hwnd, windowTimerID, uintptr(delay/time.Millisecond), 0)
		if ret == 0 {
			return fmt.Errorf("window discovery timer could not be started: %v", err)
		}
		return nil
	}
	procKillTimer.Call(a.hwnd, windowTimerID)
	return nil
}

func entryWindowTimerDelay(entry *trayEntry, now time.Time) (time.Duration, bool) {
	switch {
	case entry.needsWindow && entry.hwnd == 0 && entry.findTimedOut:
		delay := entry.retryWindowAt.Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		return delay, true
	case entry.showPending && entry.findTimedOut:
		delay := entry.retryWindowAt.Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		return delay, true
	case entry.appearancePending && entry.findTimedOut:
		delay := entry.retryWindowAt.Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		return delay, true
	case entry.iconPending && entry.findTimedOut:
		delay := entry.retryWindowAt.Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		return delay, true
	case entry.showPending || entry.foregroundPending:
		return windowTimerDelay * time.Millisecond, true
	case entry.appearancePending:
		return windowTimerDelay * time.Millisecond, true
	case entry.iconPending:
		return windowTimerDelay * time.Millisecond, true
	case entry.needsWindow && entry.hwnd == 0:
		return windowTimerDelay * time.Millisecond, true
	default:
		return 0, false
	}
}

func (a *TrayApp) showWindowDiscoveryError(failures []string) {
	if len(failures) == 0 || a.showingWindowError {
		return
	}
	a.showingWindowError = true
	procKillTimer.Call(a.hwnd, windowTimerID)
	defer func() {
		a.showingWindowError = false
		a.reportWindowTimerError(a.updateWindowTimer())
	}()
	ShowError(productName, fmt.Sprintf("%s\n%s", i18n.Text(a.language).WindowInitFailedPrefix, strings.Join(failures, "\n")))
}

func (a *TrayApp) windowDiscoveryTimeout(name string) string {
	return fmt.Sprintf(i18n.Text(a.language).NoControllableWindow, name, windowFindLimit*windowTimerDelay/1000)
}

func (a *TrayApp) reportWindowTimerError(err error) {
	if err == nil || a.showingWindowError {
		return
	}
	a.showingWindowError = true
	procKillTimer.Call(a.hwnd, windowTimerID)
	ShowError(productName, err.Error())
	a.showingWindowError = false
	if retryErr := a.updateWindowTimer(); retryErr != nil {
		a.showingWindowError = true
		ShowError(productName, retryErr.Error())
		a.showingWindowError = false
		for i := range a.entries {
			entry := &a.entries[i]
			if entry.state.Running && entry.ownership == domain.ManagedAndJobOwned && entry.needsWindow && entry.hwnd == 0 {
				entry.findCount = windowFindLimit
				entry.findTimedOut = true
				entry.retryWindowAt = time.Now().Add(windowRetryDelay * time.Millisecond)
			}
		}
	}
}

func (a *TrayApp) toggleEntryWindow(index int) error {
	entry := &a.entries[index]
	if entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		return nil
	}
	if !entryOwnsWindow(entry, entry.hwnd) {
		hwnd, err := findEntryWindow(entry)
		if err != nil {
			return err
		}
		if hwnd == 0 {
			if !entry.config.IsGUI && !entry.state.Show {
				return nil
			}
		} else {
			entry.hwnd = hwnd
			entry.appearancePending = hasWindowGeometryAppearance(entry.config)
			entry.iconPending = entry.config.Icon != "" && a.desiredEntryIcons(index) != nil
			entry.findCount = 0
			entry.findTimedOut = false
			entry.retryWindowAt = time.Time{}
			entry.appearanceErr = nil
			entry.showPending = false
		}
	}
	a.setEntryWindowVisible(index, !entry.state.Show, true)
	return nil
}

func findEntryWindow(entry *trayEntry) (uintptr, error) {
	if entry == nil || entry.process == nil {
		return 0, nil
	}
	if !entry.config.IsGUI {
		return entry.process.consoleWindow(), nil
	}
	windowsByPID, err := findWindows(map[uint32]struct{}{entry.process.pid: {}})
	if err != nil {
		return 0, err
	}
	return windowsByPID[entry.process.pid], nil
}

func (a *TrayApp) setEntryWindowVisible(index int, visible, foreground bool) {
	entry := &a.entries[index]
	if entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
		return
	}
	initializeWindow := entry.appearancePending || entry.iconPending
	wasVisible := entry.state.Show
	if wasVisible != visible {
		entry.findCount = 0
		entry.findTimedOut = false
		entry.retryWindowAt = time.Time{}
		entry.appearanceErr = nil
	}
	if !entry.appearancePending {
		a.cacheEntryWindow(index)
	}
	entry.state.Show = visible
	entry.cronTransient = false
	a.updateCachedState(index)
	a.reportCacheError(a.flushCache())
	if entry.hwnd == 0 || !entryOwnsWindow(entry, entry.hwnd) {
		entry.hwnd = 0
		entry.appearancePending = hasWindowGeometryAppearance(entry.config)
		entry.iconPending = entry.config.Icon != "" && a.desiredEntryIcons(index) != nil
		entry.needsWindow = entryNeedsWindow(entry.config, visible) || entry.iconPending || entry.appearancePending || (entry.config.IsGUI && wasVisible)
		entry.findCount = 0
		entry.findTimedOut = false
		entry.retryWindowAt = time.Time{}
		entry.appearanceErr = nil
		entry.showPending = false
		entry.foregroundPending = entry.config.IsGUI && visible && foreground
		a.reportWindowTimerError(a.updateWindowTimer())
		return
	}
	if initializeWindow {
		entry.foregroundPending = visible && foreground
		var failures []string
		a.completeWindowDiscovery(index, time.Now(), &failures, entry.appearancePending)
		if err := a.updateWindowTimer(); err != nil {
			failures = append(failures, err.Error())
		}
		a.showWindowDiscoveryError(failures)
		return
	}
	if visible && !foreground {
		procShowWindowAsync.Call(entry.hwnd, windows.SW_SHOWNA)
	} else {
		showWindow(entry.hwnd, visible)
	}
	if visible && entry.config.Topmost {
		if err := setWindowTopmost(entry.hwnd); err != nil {
			entry.appearancePending = true
			entry.appearanceErr = err
		}
	}
	entry.showPending = true
	entry.foregroundPending = visible && foreground
	a.reportWindowTimerError(a.updateWindowTimer())
}

func (a *TrayApp) hideAll() {
	for i := range a.entries {
		if a.entries[i].state.Running && a.entries[i].state.Show {
			a.setEntryWindowVisible(i, false, false)
		}
	}
}

func (a *TrayApp) showAll() {
	for i := range a.entries {
		if a.entries[i].state.Running && !a.entries[i].state.Show {
			a.setEntryWindowVisible(i, true, false)
		}
	}
}

func (a *TrayApp) disableAll() {
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.config.IgnoreAll || entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
			continue
		}
		a.beginStopEntry(i, processStop, true)
	}
}

func (a *TrayApp) enableAll() error {
	var errs []error
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.config.IgnoreAll || entry.busy || entry.state.Running {
			continue
		}
		if entry.config.ExclusionID != nil && !entry.state.Enabled {
			continue
		}
		entry.state.Enabled = true
		entry.state.Show = entry.config.EffectiveStartShow()
		if err := a.startEntry(i, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *TrayApp) restartAll() {
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.config.IgnoreAll || entry.busy || !entry.state.Running || entry.ownership != domain.ManagedAndJobOwned {
			continue
		}
		a.beginStopEntry(i, processRestart, false)
	}
}

func isWindow(hwnd uintptr) bool {
	ret, _, _ := procIsWindow.Call(hwnd)
	return ret != 0
}

func isWindowVisible(hwnd uintptr) bool {
	ret, _, _ := procIsWindowVisible.Call(hwnd)
	return ret != 0
}

func entryOwnsWindow(entry *trayEntry, hwnd uintptr) bool {
	if entry == nil || entry.process == nil || hwnd == 0 || !isWindow(hwnd) {
		return false
	}
	if entry.config.IsGUI {
		return entry.process.ownsWindow(hwnd)
	}
	return entry.process.consoleWindow() == hwnd
}

func showWindow(hwnd uintptr, visible bool) bool {
	command := uintptr(windows.SW_HIDE)
	if visible {
		command = windows.SW_SHOW
	}
	ret, _, _ := procShowWindowAsync.Call(hwnd, command)
	return ret != 0
}

func entryNeedsWindow(entry config.EntryConfig, show bool) bool {
	return show || hasWindowAppearance(entry)
}

func hasWindowAppearance(entry config.EntryConfig) bool {
	return entry.Icon != "" || hasWindowGeometryAppearance(entry)
}

func hasWindowGeometryAppearance(entry config.EntryConfig) bool {
	return entry.Position != nil || entry.CachedPosition != nil || entry.Size != nil || entry.CachedSize != nil || entry.EffectiveAlpha() != nil || entry.Topmost
}

func (a *TrayApp) resetStoppedEntry(index int, updateCache bool) {
	entry := &a.entries[index]
	if entry.process != nil {
		entry.process.Close()
	}
	entry.process = nil
	entry.state.Running = false
	entry.state.Enabled = false
	entry.state.Show = false
	entry.hwnd = 0
	entry.needsWindow = false
	entry.findCount = 0
	entry.findTimedOut = false
	entry.retryWindowAt = time.Time{}
	entry.appearanceErr = nil
	entry.appearancePending = false
	entry.iconPending = false
	entry.retainedIcons.Close()
	entry.retainedIconHWND = 0
	entry.showPending = false
	entry.foregroundPending = false
	entry.busy = false
	entry.cronTransient = false
	if updateCache {
		a.updateCachedState(index)
		a.reportCacheError(a.flushCache())
	}
}

func (a *TrayApp) handleProcessResults() {
	var errs []error
	for {
		select {
		case result := <-a.processResults:
			if result.operation == processReloadStop {
				a.finishReloadStop(result)
				continue
			}
			if result.index < 0 || result.index >= len(a.entries) {
				result.process.Close()
				continue
			}
			entry := &a.entries[result.index]
			if !entry.busy || entry.state.Generation != result.generation {
				result.process.Close()
				continue
			}
			entry.busy = false
			entry.process = result.process
			if result.err != nil {
				errs = append(errs, result.err)
				running, err := entry.process.running()
				if err != nil {
					errs = append(errs, err)
					entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show)
					if result.operation == processExclusionStop {
						if err := a.finishExclusionStop(result, false); err != nil {
							errs = append(errs, err)
						}
					} else if result.operation == processCronStop || result.operation == processCronRestart {
						errs = append(errs, a.completeCronAction(result.index, result.cronToken, errors.Join(result.err, err)))
					} else if result.operation == processCronFinalStop {
						errs = append(errs, a.completeCronFinalStop(result.index, result.cronToken, errors.Join(result.err, err)))
					}
				} else if !running {
					a.resetStoppedEntry(result.index, false)
					if result.operation == processExclusionStop {
						if err := a.finishExclusionStop(result, true); err != nil {
							errs = append(errs, err)
						}
					} else if result.operation == processCronStop {
						errs = append(errs, a.completeCronAction(result.index, result.cronToken, nil))
					} else if result.operation == processCronRestart {
						pending, startErr := a.startEntryWithExclusionOptions(result.index, result.restartShow, false, result.cronToken)
						if !pending {
							errs = append(errs, a.completeCronAction(result.index, result.cronToken, startErr))
						}
					} else if result.operation == processCronFinalStop {
						errs = append(errs, a.completeCronFinalStop(result.index, result.cronToken, nil))
					} else if result.operation == processRestart && !a.closing.Load() {
						entry.state.Enabled = true
						entry.state.Show = result.show
						if err := a.startEntry(result.index, true); err != nil {
							errs = append(errs, err)
						}
					}
				} else {
					if result.operation == processStop {
						entry.state.Enabled = true
						entry.state.Show = result.show
						a.updateCachedStopState(result.index, result.cacheShow)
						a.reportCacheError(a.flushCache())
					}
					entry.needsWindow = entryNeedsWindow(entry.config, entry.state.Show)
					if result.hwnd != 0 && entryOwnsWindow(entry, result.hwnd) {
						entry.hwnd = result.hwnd
						entry.needsWindow = false
					}
					if result.operation == processExclusionStop {
						if err := a.finishExclusionStop(result, false); err != nil {
							errs = append(errs, err)
						}
					} else if result.operation == processCronStop || result.operation == processCronRestart {
						errs = append(errs, a.completeCronAction(result.index, result.cronToken, result.err))
					} else if result.operation == processCronFinalStop {
						errs = append(errs, a.completeCronFinalStop(result.index, result.cronToken, result.err))
					}
				}
				continue
			}
			a.resetStoppedEntry(result.index, false)
			if result.operation == processExclusionStop {
				if err := a.finishExclusionStop(result, true); err != nil {
					errs = append(errs, err)
				}
				continue
			}
			if result.operation == processCronStop {
				errs = append(errs, a.completeCronAction(result.index, result.cronToken, nil))
				continue
			}
			if result.operation == processCronRestart {
				pending, err := a.startEntryWithExclusionOptions(result.index, result.restartShow, false, result.cronToken)
				if !pending {
					errs = append(errs, a.completeCronAction(result.index, result.cronToken, err))
				}
				continue
			}
			if result.operation == processCronFinalStop {
				errs = append(errs, a.completeCronFinalStop(result.index, result.cronToken, nil))
				continue
			}
			if result.operation == processRestart && !a.closing.Load() {
				entry.state.Enabled = true
				entry.state.Show = result.show
				if err := a.startEntry(result.index, true); err != nil {
					errs = append(errs, err)
				}
			}
		default:
			a.reportWindowTimerError(a.updateWindowTimer())
			a.resumeCron(time.Now())
			a.resumePendingReload()
			if err := errors.Join(errs...); err != nil && !a.sessionEndPending {
				ShowError(productName, err.Error())
			}
			return
		}
	}
}

func (a *TrayApp) handleEntryCommand(command uintptr) bool {
	if command < commandEntryBase {
		return false
	}
	if a.sessionEndPending || a.reload != nil || a.reloadChecking || a.exclusion != nil || a.cronActionInFlight() {
		return true
	}
	offset := command - commandEntryBase
	index := int(offset / commandEntryStep)
	if index < 0 || index >= len(a.entries) {
		return false
	}
	var err error
	switch offset % commandEntryStep {
	case commandOpenPath:
		err = a.openEntryPath(index)
	case commandSelectExecutable:
		err = a.selectEntryExecutable(index)
	case commandShowHide:
		err = a.toggleEntryWindow(index)
	case commandToggle:
		err = a.toggleEntry(index)
	case commandRestart:
		a.beginStopEntry(index, processRestart, false)
	case commandElevateEntry:
		err = a.elevateEntry(index)
	default:
		return false
	}
	if err != nil {
		ShowError(productName, err.Error())
	}
	return true
}

func (a *TrayApp) cleanup() {
	if a == nil || a.closed {
		return
	}
	a.closed = true
	a.closing.Store(true)
	if a.hwnd != 0 {
		procKillTimer.Call(a.hwnd, windowTimerID)
		procKillTimer.Call(a.hwnd, configReloadTimerID)
		procKillTimer.Call(a.hwnd, cronTimerID)
		procKillTimer.Call(a.hwnd, trayIconRetryTimerID)
	}
	a.suspendCron()
	a.cron = nil
	a.stopConfigWatcher()
	a.unregisterAllHotkeys()
	a.cleanupUpdater()
	a.restoreAllDockedWindows()
	for i := range a.entries {
		a.cacheEntryWindow(i)
		if a.entries[i].state.Running && !a.entries[i].cronTransient {
			a.updateCachedState(i)
		}
	}
	a.reportCacheError(a.flushCache())
	a.processWorkers.Wait()
	for {
		select {
		case result := <-a.processResults:
			result.process.Close()
		default:
			goto resultsDrained
		}
	}

resultsDrained:
	if a.console.process != nil {
		_ = a.console.process.Stop(200, false, false)
		a.console.process = nil
	}
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.ownership == domain.ManagedAndJobOwned && entry.process != nil {
			if a.processes.stop(entry.process, entry.config) == nil {
				entry.process = nil
			}
		}
	}
	if a.processes != nil {
		a.processes.Close()
		a.processes = nil
	}
	for i := range a.entries {
		if a.entries[i].process != nil {
			a.entries[i].process.Close()
			a.entries[i].process = nil
		}
	}
	a.releaseIcons()
}

func (a *TrayApp) beginSessionEnd() {
	if a == nil || a.closed || a.closing.Load() || a.sessionEndPending {
		return
	}
	a.sessionEndPending = true
	if a.hwnd != 0 {
		procKillTimer.Call(a.hwnd, windowTimerID)
		procKillTimer.Call(a.hwnd, configReloadTimerID)
		procKillTimer.Call(a.hwnd, cronTimerID)
		procKillTimer.Call(a.hwnd, updateUITimerID)
		procKillTimer.Call(a.hwnd, trayIconRetryTimerID)
	}
	a.interruptUpdateForSession()
	a.suspendCron()
	a.hotkeys.suspended = true
}

func (a *TrayApp) cancelSessionEnd() {
	if a == nil || !a.sessionEndPending || a.closed || a.closing.Load() {
		return
	}
	a.sessionEndPending = false
	a.handleProcessResults()
	a.handleUpdateResults()
	a.resumeInterruptedUpdate()
	if a.closing.Load() || a.closed || a.sessionEndPending {
		return
	}
	if a.messageDepth == 0 && !a.menuOpen && !a.reloadChecking && a.reload == nil {
		if a.hotkeys.resumePending {
			procPostMessageW.Call(a.hwnd, wmResumeHotkey, 0, 0)
		} else {
			a.hotkeys.suspended = false
		}
	}
	if err := a.updateConfigWatcher(); err != nil {
		ShowError(productName, err.Error())
		if a.closing.Load() || a.closed || a.sessionEndPending {
			return
		}
	}
	a.reportWindowTimerError(a.updateWindowTimer())
	a.startConfiguredEntries()
	if a.closing.Load() || a.closed || a.sessionEndPending {
		return
	}
	a.resumeCron(time.Now())
	a.resumePendingReload()
	a.resumeUpdateUI()
}

func (a *TrayApp) completeSessionEnd() {
	if a == nil || a.closed {
		return
	}
	a.sessionEndPending = true
	a.closing.Store(true)
	reason, _ := windows.UTF16PtrFromString(i18n.Text(a.language).StoppingManaged)
	blocked := false
	if reason != nil {
		ret, _, _ := procShutdownBlockCreate.Call(a.hwnd, uintptr(unsafe.Pointer(reason)))
		blocked = ret != 0
	}
	a.deleteTrayIcon()
	a.cleanupSessionEnd(sessionCleanupTimeout)
	if blocked {
		procShutdownBlockDestroy.Call(a.hwnd)
	}
	runtime.KeepAlive(reason)
	procPostQuitMessage.Call(0)
}

func (a *TrayApp) cleanupSessionEnd(timeout time.Duration) {
	if a == nil || a.closed {
		return
	}
	a.closed = true
	a.closing.Store(true)
	deadline := time.Now().Add(timeout)
	if a.hwnd != 0 {
		procKillTimer.Call(a.hwnd, windowTimerID)
		procKillTimer.Call(a.hwnd, configReloadTimerID)
		procKillTimer.Call(a.hwnd, cronTimerID)
	}
	a.suspendCron()
	a.cron = nil
	a.unregisterAllHotkeys()
	a.cleanupUpdater()
	for i := range a.entries {
		a.cacheEntryWindow(i)
		if a.entries[i].state.Running && !a.entries[i].cronTransient {
			a.updateCachedState(i)
		}
	}
	a.reportCacheError(a.flushCache())

	var stops sync.WaitGroup
	if a.console.process != nil {
		process := a.console.process
		a.console.process = nil
		stops.Add(1)
		go func() {
			defer stops.Done()
			_ = process.Stop(200, false, false)
		}()
	}
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.ownership != domain.ManagedAndJobOwned || entry.process == nil {
			continue
		}
		process := entry.process
		entryConfig := entry.config
		controller := a.processes
		entry.process = nil
		stops.Add(1)
		go func() {
			defer stops.Done()
			_ = controller.stop(process, entryConfig)
		}()
	}
	a.restoreAllDockedWindows()
	workersDone := make(chan struct{})
	go func() {
		a.processWorkers.Wait()
		stops.Wait()
		close(workersDone)
	}()
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	select {
	case <-workersDone:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}
	for {
		select {
		case result := <-a.processResults:
			result.process.Close()
		default:
			goto resultsDrained
		}
	}

resultsDrained:
	if a.processes != nil {
		a.processes.Close()
		a.processes = nil
	}
	for i := range a.entries {
		if a.entries[i].process != nil {
			a.entries[i].process.Close()
			a.entries[i].process = nil
		}
	}
	a.releaseIcons()
}

func (a *TrayApp) releaseIcons() {
	if a.reload != nil && a.reload.icons != nil && a.reload.icons != a.icons {
		a.reload.icons.Close()
		a.reload.icons = nil
	}
	for i := range a.entries {
		a.entries[i].retainedIcons.big.handle = 0
		a.entries[i].retainedIcons.small.handle = 0
	}
	a.console.retainedIcons.Abandon()
	a.console.retainedIconHWND = 0
	if a.icons != nil {
		a.icons.CloseTrayAndAbandonWindowIcons()
		a.icons = nil
	}
}

func (a *TrayApp) updateCachedState(index int) {
	if a.cache == nil || index < 0 || index >= len(a.entries) {
		return
	}
	entry := &a.entries[index]
	if entry.ownership != domain.ManagedAndJobOwned {
		return
	}
	a.cache.UpdateState(index, entry.state.Enabled, entry.state.Show)
	if a.config.CacheEnabledStateEnabled() {
		entry.config.Enabled = entry.state.Enabled
	}
	if a.config.CacheShowEnabled() {
		show := entry.state.Show
		entry.config.StartShow = &show
		entry.config.CachedShow = &show
	}
}

func (a *TrayApp) updateCachedStopState(index int, cacheShow bool) {
	if a.cache == nil || index < 0 || index >= len(a.entries) {
		return
	}
	entry := &a.entries[index]
	if entry.ownership != domain.ManagedAndJobOwned {
		return
	}
	a.cache.UpdateEnabled(index, entry.state.Enabled)
	if a.config.CacheEnabledStateEnabled() {
		entry.config.Enabled = entry.state.Enabled
	}
	if cacheShow {
		a.cache.UpdateShow(index, entry.state.Show)
		if a.config.CacheShowEnabled() {
			show := entry.state.Show
			entry.config.StartShow = &show
			entry.config.CachedShow = &show
		}
	}
}

func (a *TrayApp) cacheEntryWindow(index int) {
	if a.cache == nil || index < 0 || index >= len(a.entries) || !a.cache.NeedsWindowState(index) {
		return
	}
	entry := &a.entries[index]
	if entry.hwnd == 0 || !entryOwnsWindow(entry, entry.hwnd) {
		return
	}
	if windowMinimized(entry.hwnd) {
		a.cacheEntryAlpha(index, entry)
		return
	}
	rect, err := windowRect(entry.hwnd)
	if err != nil {
		return
	}
	state := statecache.WindowState{
		Left:   rect.Left,
		Top:    rect.Top,
		Right:  rect.Right,
		Bottom: rect.Bottom,
	}
	if a.cache.NeedsAlpha(index) {
		if alpha, err := windowAlpha(entry.hwnd); err == nil {
			value := int64(alpha)
			state.Alpha = &value
		}
	}
	a.cache.UpdateWindow(index, state)
	if a.config.CachePositionEnabled() {
		entry.config.CachedPosition = &config.PixelPair{state.Left, state.Top}
	}
	if a.config.CacheSizeEnabled() {
		entry.config.CachedSize = &config.PixelPair{state.Right - state.Left, state.Bottom - state.Top}
	}
	if a.config.CacheAlphaEnabled() && entry.config.Alpha != nil && state.Alpha != nil {
		alpha := *state.Alpha
		entry.config.CachedAlpha = &alpha
	}
}

func (a *TrayApp) cacheEntryAlpha(index int, entry *trayEntry) {
	if !a.cache.NeedsAlpha(index) {
		return
	}
	alpha, err := windowAlpha(entry.hwnd)
	if err != nil {
		return
	}
	value := int64(alpha)
	a.cache.UpdateAlpha(index, value)
	if a.config.CacheAlphaEnabled() && entry.config.Alpha != nil {
		entry.config.CachedAlpha = &value
	}
}

func (a *TrayApp) flushCache() error {
	if a.cache == nil {
		return nil
	}
	return a.cache.Save()
}

func (a *TrayApp) reportCacheError(err error) {
	if err != nil && !a.sessionEndPending && !a.closing.Load() {
		ShowError(productName, err.Error())
	}
}

func windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	a := activeApp
	if a != nil && a.taskbarCreated != 0 && msg == a.taskbarCreated {
		if !a.sessionEndPending && !a.closing.Load() && !a.closed {
			a.trayIconRecovery.reset()
			a.restoreTrayIcon()
		}
		return 0
	}
	switch msg {
	case wmQueryEndSession:
		if a != nil {
			a.beginSessionEnd()
		}
		return 1
	case wmEndSession:
		if a != nil {
			if wparam == 0 {
				a.cancelSessionEnd()
			} else {
				a.completeSessionEnd()
			}
		}
		return 0
	case wmHotkey:
		if a != nil {
			a.handleHotkey(int32(wparam), lparam)
		}
		return 0
	case wmResumeHotkey:
		if a != nil && a.messageDepth == 0 && !a.sessionEndPending && !a.closePending && !a.closing.Load() && !a.closed {
			a.hotkeys.suspended = false
			a.hotkeys.resumePending = false
		}
		return 0
	case wmTray:
		if a != nil && (a.sessionEndPending || a.messageDepth != 0 || a.closePending || a.closing.Load() || a.closed) {
			return 0
		}
		switch uint32(lparam) {
		case wmLButtonUp:
			if a != nil {
				a.handleLeftClick()
			}
		case wmRButtonUp:
			if a != nil {
				a.showMenu()
			}
		}
		return 0
	case wmCommand:
		if a == nil {
			return 0
		}
		command := uintptr(uint16(wparam & 0xffff))
		if a.sessionEndPending || a.closePending || a.closing.Load() || a.closed {
			return 0
		}
		if a.messageDepth != 0 {
			if command == commandExit {
				a.closing.Store(true)
				a.closePending = true
			}
			return 0
		}
		if a != nil && a.handleEntryCommand(command) {
			return 0
		}
		if a.reload != nil || a.reloadChecking || a.exclusion != nil || a.cronActionInFlight() {
			if command == commandExit {
				procPostMessageW.Call(hwnd, wmClose, 0, 0)
			}
			return 0
		}
		switch command {
		case commandHideAll:
			a.hideAll()
		case commandDisableAll:
			a.disableAll()
		case commandEnableAll:
			if err := a.enableAll(); err != nil {
				ShowError(productName, err.Error())
			}
		case commandShowAll:
			a.showAll()
		case commandRestartAll:
			a.restartAll()
		case commandElevate:
			if err := a.elevateHost(); err != nil {
				ShowError(productName, err.Error())
			}
		case commandShowAllDocked:
			a.restoreAllDockedWindows()
		case commandHome:
			if err := a.openRepositoryPage(); err != nil {
				ShowError(productName, err.Error())
			}
		case commandCheckForUpdates:
			if err := a.startUpdateCheck(true); err != nil {
				ShowInfo(productName, err.Error())
			}
		case commandAbout:
			text := i18n.Text(a.language)
			ShowInfo(text.About, domain.AboutText(a.config.DisplayName(), a.language))
		case commandStartOnBoot:
			if err := a.toggleStartup(); err != nil {
				ShowError(productName, err.Error())
			}
		case commandExit:
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		default:
			if command >= commandDockedBase {
				a.restoreDockedWindow(int(command - commandDockedBase))
			}
		}
		return 0
	case wmTimer:
		if a != nil && (a.sessionEndPending || a.messageDepth != 0 || a.closePending || a.closing.Load() || a.closed) {
			return 0
		}
		if a != nil && wparam == windowTimerID {
			if a.showingWindowError {
				return 0
			}
			a.discoverWindows()
		} else if a != nil && wparam == configReloadTimerID {
			a.checkConfigReload()
		} else if a != nil && wparam == cronTimerID {
			a.handleCronTimer(time.Now())
		} else if a != nil && wparam == updateUITimerID {
			a.resumeUpdateUI()
		} else if a != nil && wparam == trayIconRetryTimerID {
			a.restoreTrayIcon()
		}
		return 0
	case wmConfigDirectoryChanged:
		if a != nil {
			a.scheduleConfigReload()
		}
		return 0
	case wmConfigWatcherFailed:
		if a != nil && !a.closing.Load() {
			a.stopConfigWatcher()
			if !a.sessionEndPending {
				ShowError(productName, i18n.Text(a.language).WatcherStopped)
			}
		}
		return 0
	case wmProcessComplete:
		if a != nil && !a.sessionEndPending && !a.closePending && !a.closing.Load() && !a.closed {
			a.handleProcessResults()
		}
		return 0
	case wmUpdateComplete:
		if a != nil && !a.closePending && !a.closing.Load() && !a.closed {
			a.handleUpdateResults()
		}
		return 0
	case wmClose:
		if a != nil && (a.messageDepth != 0 || a.menuOpen) {
			a.closing.Store(true)
			a.closePending = true
			return 0
		}
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if a != nil {
			a.deleteTrayIcon()
			a.cleanup()
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
}

func ShowError(title, message string) {
	if activeApp != nil && activeApp.sessionEndPending {
		return
	}
	showMessage(title, message, messageBoxOK|messageBoxError)
}

func ShowInfo(title, message string) {
	showMessage(title, message, messageBoxOK)
}

func ShowConfirm(title, message string) bool {
	return showMessage(title, message, messageBoxYesNo|messageBoxQuestion) == idYes
}

func showReloadConfirm(language i18n.Language, configName string) uintptr {
	prompt := strings.ReplaceAll(i18n.Text(language).ReloadPrompt, "config.json", configName)
	return showMessage(
		productName,
		prompt,
		messageBoxYesNoCancel|messageBoxQuestion,
	)
}

func showMessage(title, message string, flags uintptr) uintptr {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	messagePtr, _ := windows.UTF16PtrFromString(message)
	if activeApp != nil {
		activeApp.messageDepth++
		activeApp.hotkeys.suspended = true
	}
	result, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(messagePtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
	if activeApp != nil {
		activeApp.messageDepth--
		if activeApp.messageDepth == 0 && activeApp.closePending && !activeApp.closed {
			activeApp.closing.Store(true)
			activeApp.hotkeys.suspended = true
			procPostMessageW.Call(activeApp.hwnd, wmClose, 0, 0)
		} else if activeApp.messageDepth == 0 && !activeApp.sessionEndPending {
			if activeApp.hotkeys.resumePending {
				procPostMessageW.Call(activeApp.hwnd, wmResumeHotkey, 0, 0)
			} else {
				activeApp.hotkeys.suspended = false
			}
			activeApp.resumeCronAfterMessage()
			activeApp.resumeUpdateUI()
		}
	}
	runtime.KeepAlive(titlePtr)
	runtime.KeepAlive(messagePtr)
	return result
}
