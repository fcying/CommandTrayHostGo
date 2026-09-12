//go:build windows && amd64

package win32

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"github.com/fcying/CommandTrayHostGo/internal/statecache"
	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if pid, ok := domain.ParseConsoleWindowQueryArgument(os.Args[1:]); ok {
		var result [8]byte
		binary.LittleEndian.PutUint64(result[:], uint64(QueryConsoleWindow(pid)))
		_, _ = os.Stdout.Write(result[:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHiddenManagedBackgroundProcessSkipsWindowDiscovery(t *testing.T) {
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "background.vbs")
	markerPath := filepath.Join(directory, "started.txt")
	script := "Set file = CreateObject(\"Scripting.FileSystemObject\").CreateTextFile(WScript.Arguments(0), True)\r\n" +
		"file.WriteLine \"started\"\r\n" +
		"file.Close\r\n" +
		"WScript.Sleep 30000\r\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	systemDirectory, err := SystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	executablePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(systemDirectory, "wscript.exe"), "//B", "//Nologo", scriptPath, markerPath)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	process := &childProcess{handle: handle, pid: uint32(command.Process.Pid), helperPath: executablePath}
	t.Cleanup(process.Close)
	app := TrayApp{
		config: config.Config{LeftClick: []string{"background"}},
		entries: []trayEntry{{
			config: config.EntryConfig{
				Name:  "background",
				IsGUI: false,
			},
			ownership: domain.ManagedAndJobOwned,
			state:     domain.EntryState{Show: false},
		}},
	}
	app.completeEntryStart(0, process, false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(markerPath)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("background process did not create its startup marker")
		}
		if !app.entries[0].process.Running() {
			t.Fatal("background process exited before creating its startup marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hwnd := app.entries[0].process.consoleWindow(); hwnd != 0 {
		t.Fatalf("background process exposed console window %#x", hwnd)
	}

	app.discoverWindows()
	if app.entries[0].findCount != 0 || app.entries[0].findTimedOut {
		t.Fatalf("window discovery ran for hidden background process: count=%d timedOut=%v", app.entries[0].findCount, app.entries[0].findTimedOut)
	}
	if !app.entries[0].process.Running() {
		t.Fatal("background process stopped during window discovery")
	}

	app.handleLeftClick()
	entry := &app.entries[0]
	if entry.state.Show || entry.needsWindow || entry.findCount != 0 || entry.findTimedOut {
		t.Fatalf("tray click started window discovery for background process: show=%v needsWindow=%v count=%d timedOut=%v", entry.state.Show, entry.needsWindow, entry.findCount, entry.findTimedOut)
	}
}

func TestToggleEntryWindowWaitsForDelayedGUIWindow(t *testing.T) {
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "background.vbs")
	if err := os.WriteFile(scriptPath, []byte("WScript.Sleep 30000\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	systemDirectory, err := SystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(systemDirectory, "wscript.exe"), "//B", "//Nologo", scriptPath)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	process := &childProcess{handle: handle, pid: uint32(command.Process.Pid)}
	t.Cleanup(process.Close)
	app := TrayApp{entries: []trayEntry{{
		config:    config.EntryConfig{Name: "delayed GUI", IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Running: true, Show: false},
		process:   process,
	}}}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	entry := &app.entries[0]
	if !entry.state.Show || !entry.needsWindow {
		t.Fatalf("tray click did not wait for delayed GUI window: show=%v needsWindow=%v", entry.state.Show, entry.needsWindow)
	}
	if !entry.foregroundPending {
		t.Fatal("tray click did not preserve the pending foreground request")
	}
}

func TestSetEntryWindowVisibleKeepsDiscoveringMissingVisibleGUIWindow(t *testing.T) {
	app := TrayApp{entries: []trayEntry{{
		config:    config.EntryConfig{IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Running: true, Show: true},
		process:   &childProcess{handle: 1},
	}}}

	app.setEntryWindowVisible(0, false, false)
	entry := &app.entries[0]
	if entry.state.Show || !entry.needsWindow {
		t.Fatalf("missing visible GUI window did not retain the hide request: show=%v needsWindow=%v", entry.state.Show, entry.needsWindow)
	}
}

func TestDiscoverWindowsAppliesAppearanceWithoutPendingFlag(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	discovered := createTestTopLevelWindow(t, false)
	position := config.PixelPair{40, 50}
	size := config.PixelPair{240, 180}
	entryConfig := config.EntryConfig{
		Name:           "reloaded appearance",
		IsGUI:          true,
		CachedPosition: &position,
		CachedSize:     &size,
	}
	app := TrayApp{entries: []trayEntry{{
		config:      entryConfig,
		ownership:   domain.ManagedAndJobOwned,
		state:       domain.EntryState{Running: true, Show: false},
		process:     &childProcess{handle: 1, pid: uint32(os.Getpid())},
		needsWindow: true,
	}}}

	app.discoverWindows()
	if got := app.entries[0].hwnd; got != discovered {
		t.Fatalf("discovered window = %#x, want %#x", got, discovered)
	}
	assertTestWindowRect(t, discovered, position, size)
}

func TestDiscoverWindowsPreservesForegroundAcrossWindowReplacement(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	replacement := createTestTopLevelWindow(t, false)
	app := TrayApp{entries: []trayEntry{{
		config:            config.EntryConfig{IsGUI: true},
		ownership:         domain.ManagedAndJobOwned,
		state:             domain.EntryState{Running: true, Show: true},
		process:           &childProcess{handle: 1, pid: uint32(os.Getpid())},
		hwnd:              1,
		foregroundPending: true,
	}}}

	app.discoverWindows()
	entry := &app.entries[0]
	if entry.hwnd != replacement {
		t.Fatalf("replacement window = %#x, want %#x", entry.hwnd, replacement)
	}
	if !entry.foregroundPending {
		t.Fatal("window replacement cleared the pending foreground request")
	}
}

func TestDiscoverWindowsPreservesPendingHideAcrossMissingReplacement(t *testing.T) {
	app := TrayApp{entries: []trayEntry{{
		config:      config.EntryConfig{IsGUI: true},
		ownership:   domain.ManagedAndJobOwned,
		state:       domain.EntryState{Running: true, Show: false},
		process:     &childProcess{handle: 1},
		hwnd:        1,
		showPending: true,
	}}}

	app.discoverWindows()
	entry := &app.entries[0]
	if !entry.needsWindow || !entry.showPending {
		t.Fatalf("missing replacement lost the pending hide: needsWindow=%v showPending=%v", entry.needsWindow, entry.showPending)
	}
}

func TestCompleteWindowDiscoveryCompletesSatisfiedVisibility(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd := createTestTopLevelWindow(t, false)
	app := TrayApp{entries: []trayEntry{{
		state: domain.EntryState{Show: false},
		hwnd:  hwnd,
	}}}
	failures := []string{}

	app.completeWindowDiscovery(0, time.Now(), &failures, false)
	if app.entries[0].showPending {
		t.Fatal("already hidden window remained pending")
	}
}

func TestCompleteWindowDiscoveryPreservesShowUntilAppearanceSucceeds(t *testing.T) {
	position := config.PixelPair{40, 50}
	app := TrayApp{entries: []trayEntry{{
		config: config.EntryConfig{
			CachedPosition: &position,
		},
		state:             domain.EntryState{Show: true},
		foregroundPending: true,
	}}}
	failures := []string{}

	app.completeWindowDiscovery(0, time.Now(), &failures, true)
	entry := &app.entries[0]
	if !entry.appearancePending {
		t.Fatal("appearance failure did not remain pending")
	}
	if !entry.showPending {
		t.Fatal("appearance failure did not preserve the requested show state")
	}
	if !entry.state.Show || !entry.foregroundPending {
		t.Fatalf("appearance failure lost the pending request: show=%v foreground=%v", entry.state.Show, entry.foregroundPending)
	}
}

func TestEntryWindowTimerDelayHonorsTimedOutDiscoveryBackoff(t *testing.T) {
	now := time.Now()
	retryWindowAt := now.Add(windowRetryDelay * time.Millisecond)
	delay, active := entryWindowTimerDelay(&trayEntry{
		needsWindow:       true,
		foregroundPending: true,
		findTimedOut:      true,
		retryWindowAt:     retryWindowAt,
	}, now)
	if !active {
		t.Fatal("timed-out window discovery did not request a timer")
	}
	if delay != windowRetryDelay*time.Millisecond {
		t.Fatalf("timer delay = %v, want %v", delay, windowRetryDelay*time.Millisecond)
	}
}

func TestEntryWindowTimerDelayHonorsTimedOutAppearanceBackoffBeforeForeground(t *testing.T) {
	now := time.Now()
	retryWindowAt := now.Add(windowRetryDelay * time.Millisecond)
	delay, active := entryWindowTimerDelay(&trayEntry{
		hwnd:              1,
		appearancePending: true,
		foregroundPending: true,
		findTimedOut:      true,
		retryWindowAt:     retryWindowAt,
	}, now)
	if !active {
		t.Fatal("timed-out appearance retry did not request a timer")
	}
	if delay != windowRetryDelay*time.Millisecond {
		t.Fatalf("timer delay = %v, want %v", delay, windowRetryDelay*time.Millisecond)
	}
}

func TestToggleEntryWindowPreservesKnownGUIWindow(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	controlled := createTestTopLevelWindow(t, false)
	auxiliary := createTestTopLevelWindow(t, true)
	process := &childProcess{handle: 1, pid: uint32(os.Getpid())}
	app := TrayApp{entries: []trayEntry{{
		config:    config.EntryConfig{IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Running: true, Show: false},
		process:   process,
		hwnd:      controlled,
	}}}

	rediscovered, err := findEntryWindow(&app.entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if rediscovered != auxiliary {
		t.Fatalf("findEntryWindow() = %#x, want visible auxiliary window %#x", rediscovered, auxiliary)
	}
	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	if app.entries[0].hwnd != controlled {
		t.Fatalf("controlled window changed from %#x to %#x", controlled, app.entries[0].hwnd)
	}
}

func TestToggleEntryWindowHidesReplacementGUIWindow(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	replacement := createTestTopLevelWindow(t, true)
	app := TrayApp{entries: []trayEntry{{
		config:    config.EntryConfig{IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Running: true, Show: true},
		process:   &childProcess{handle: 1, pid: uint32(os.Getpid())},
		hwnd:      1,
	}}}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	entry := &app.entries[0]
	if entry.hwnd != replacement {
		t.Fatalf("replacement window = %#x, want %#x", entry.hwnd, replacement)
	}
	if entry.state.Show || !entry.showPending {
		t.Fatalf("replacement GUI hide was not queued: show=%v showPending=%v", entry.state.Show, entry.showPending)
	}
}

func TestToggleEntryWindowResetsRetryForReplacement(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	replacement := createTestTopLevelWindow(t, false)
	app := TrayApp{entries: []trayEntry{{
		config:        config.EntryConfig{IsGUI: true},
		ownership:     domain.ManagedAndJobOwned,
		state:         domain.EntryState{Running: true, Show: false},
		process:       &childProcess{handle: 1, pid: uint32(os.Getpid())},
		hwnd:          1,
		findCount:     windowFindLimit,
		findTimedOut:  true,
		retryWindowAt: time.Now().Add(windowRetryDelay * time.Millisecond),
		appearanceErr: errors.New("stale window error"),
	}}}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	entry := &app.entries[0]
	if entry.hwnd != replacement {
		t.Fatalf("replacement window = %#x, want %#x", entry.hwnd, replacement)
	}
	if entry.findCount != 0 || entry.findTimedOut || !entry.retryWindowAt.IsZero() || entry.appearanceErr != nil {
		t.Fatalf("replacement retained retry state: count=%d timedOut=%v retryAt=%v err=%v", entry.findCount, entry.findTimedOut, entry.retryWindowAt, entry.appearanceErr)
	}
}

func TestToggleEntryWindowResetsRetryForFreshVisibility(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd := createTestTopLevelWindow(t, false)
	app := TrayApp{entries: []trayEntry{{
		config:        config.EntryConfig{IsGUI: true},
		ownership:     domain.ManagedAndJobOwned,
		state:         domain.EntryState{Running: true, Show: false},
		process:       &childProcess{handle: 1, pid: uint32(os.Getpid())},
		hwnd:          hwnd,
		findCount:     windowFindLimit,
		findTimedOut:  true,
		retryWindowAt: time.Now().Add(windowRetryDelay * time.Millisecond),
		appearanceErr: errors.New("stale retry error"),
	}}}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	entry := &app.entries[0]
	if entry.findCount != 0 || entry.findTimedOut || !entry.retryWindowAt.IsZero() || entry.appearanceErr != nil {
		t.Fatalf("fresh visibility retained retry state: count=%d timedOut=%v retryAt=%v err=%v", entry.findCount, entry.findTimedOut, entry.retryWindowAt, entry.appearanceErr)
	}
}

func TestToggleEntryWindowRestoresCachedAppearanceBeforeCaching(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	discovered := createTestTopLevelWindow(t, false)
	cachedPosition := config.PixelPair{40, 50}
	cachedSize := config.PixelPair{240, 180}
	enableCache := true
	entryConfig := config.EntryConfig{
		Name:           "delayed",
		IsGUI:          true,
		CachedPosition: &cachedPosition,
		CachedSize:     &cachedSize,
	}
	cfg := config.Config{EnableCache: &enableCache, Configs: []config.EntryConfig{entryConfig}}
	cache, err := statecache.OpenSnapshot(filepath.Join(t.TempDir(), "state.cache"), &cfg, config.FileStamp{}, true)
	if err != nil {
		t.Fatal(err)
	}
	app := TrayApp{
		config: cfg,
		cache:  cache,
		entries: []trayEntry{{
			config:            entryConfig,
			ownership:         domain.ManagedAndJobOwned,
			state:             domain.EntryState{Running: true, Show: false},
			process:           &childProcess{handle: 1, pid: uint32(os.Getpid())},
			appearancePending: true,
		}},
	}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	entry := &app.entries[0]
	if entry.hwnd != discovered {
		t.Fatalf("discovered window = %#x, want %#x", entry.hwnd, discovered)
	}
	rect, err := windowRect(discovered)
	if err != nil {
		t.Fatal(err)
	}
	if position := (config.PixelPair{rect.Left, rect.Top}); position != cachedPosition {
		t.Fatalf("window position = %v, want %v", position, cachedPosition)
	}
	if size := (config.PixelPair{rect.Right - rect.Left, rect.Bottom - rect.Top}); size != cachedSize {
		t.Fatalf("window size = %v, want %v", size, cachedSize)
	}
	if entry.config.CachedPosition == nil || *entry.config.CachedPosition != cachedPosition {
		t.Fatalf("cached position = %v, want %v", entry.config.CachedPosition, cachedPosition)
	}
	if entry.config.CachedSize == nil || *entry.config.CachedSize != cachedSize {
		t.Fatalf("cached size = %v, want %v", entry.config.CachedSize, cachedSize)
	}
}

func TestToggleEntryWindowCachesGeometryWhileIconRetries(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd := createTestTopLevelWindow(t, true)
	cachedPosition := config.PixelPair{40, 50}
	cachedSize := config.PixelPair{240, 180}
	currentPosition := config.PixelPair{300, 250}
	currentSize := config.PixelPair{280, 210}
	setTestWindowRect(t, hwnd, currentPosition, currentSize)
	enableCache := true
	entryConfig := config.EntryConfig{
		Name:           "icon retry",
		IsGUI:          true,
		CachedPosition: &cachedPosition,
		CachedSize:     &cachedSize,
	}
	cfg := config.Config{EnableCache: &enableCache, Configs: []config.EntryConfig{entryConfig}}
	cache, err := statecache.OpenSnapshot(filepath.Join(t.TempDir(), "state.cache"), &cfg, config.FileStamp{}, true)
	if err != nil {
		t.Fatal(err)
	}
	app := TrayApp{
		config: cfg,
		cache:  cache,
		entries: []trayEntry{{
			config:      entryConfig,
			ownership:   domain.ManagedAndJobOwned,
			state:       domain.EntryState{Running: true, Show: true},
			process:     &childProcess{handle: 1, pid: uint32(os.Getpid())},
			hwnd:        hwnd,
			iconPending: true,
		}},
	}

	if err := app.toggleEntryWindow(0); err != nil {
		t.Fatal(err)
	}
	assertTestWindowRect(t, hwnd, currentPosition, currentSize)
	entry := &app.entries[0]
	if entry.config.CachedPosition == nil || *entry.config.CachedPosition != currentPosition {
		t.Fatalf("cached position = %v, want %v", entry.config.CachedPosition, currentPosition)
	}
	if entry.config.CachedSize == nil || *entry.config.CachedSize != currentSize {
		t.Fatalf("cached size = %v, want %v", entry.config.CachedSize, currentSize)
	}
}

func TestNamedLeftClickFollowsReorderedEntries(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	first := createTestTopLevelWindow(t, false)
	second := createTestTopLevelWindow(t, false)
	process := &childProcess{handle: windows.CurrentProcess(), pid: uint32(os.Getpid())}
	app := TrayApp{
		config: config.Config{LeftClick: []string{"second"}},
		entries: []trayEntry{
			{config: config.EntryConfig{Name: "first", IsGUI: true}, ownership: domain.ManagedAndJobOwned, state: domain.EntryState{Running: true}, process: process, hwnd: first},
			{config: config.EntryConfig{Name: "second", IsGUI: true}, ownership: domain.ManagedAndJobOwned, state: domain.EntryState{Running: true}, process: process, hwnd: second},
		},
	}
	app.handleLeftClick()
	if app.entries[0].state.Show || !app.entries[1].state.Show || !app.entries[1].showPending {
		t.Fatal("left click did not request showing only the named window")
	}
	app.entries[0], app.entries[1] = app.entries[1], app.entries[0]
	app.handleLeftClick()
	if app.entries[0].state.Show || app.entries[1].state.Show {
		t.Fatal("left click did not follow the named window after reordering")
	}
}

func TestNamedGroupMenuFollowsReorderedEntries(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	name := "second"
	items := []config.GroupItem{{Group: &config.Group{Name: "Tools", Items: []config.GroupItem{{EntryName: &name}}}}}
	app := TrayApp{entries: []trayEntry{{config: config.EntryConfig{Name: "first"}}, {config: config.EntryConfig{Name: "second"}}}}
	getSubMenu := user32.NewProc("GetSubMenu")
	getMenuItemID := user32.NewProc("GetMenuItemID")
	for _, index := range []int{1, 0} {
		menu, _, err := procCreatePopupMenu.Call()
		if menu == 0 {
			t.Fatal(err)
		}
		if err := app.appendGroupItems(menu, items); err != nil {
			procDestroyMenu.Call(menu)
			t.Fatal(err)
		}
		groupMenu, _, _ := getSubMenu.Call(menu, 0)
		entryMenu, _, _ := getSubMenu.Call(groupMenu, 0)
		command, _, _ := getMenuItemID.Call(entryMenu, 3)
		procDestroyMenu.Call(menu)
		if command != entryCommand(index, commandShowHide) {
			t.Fatalf("named submenu targets command %d, want entry %d", command, index)
		}
		app.entries[0], app.entries[1] = app.entries[1], app.entries[0]
	}
}

func createTestTopLevelWindow(t *testing.T, visible bool) uintptr {
	t.Helper()
	className, _ := windows.UTF16PtrFromString("STATIC")
	windowName, _ := windows.UTF16PtrFromString(t.Name())
	style := uintptr(0x00cf0000) // WS_OVERLAPPEDWINDOW
	if visible {
		style |= 0x10000000 // WS_VISIBLE
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		style,
		0,
		0,
		320,
		200,
		0,
		0,
		0,
		0,
	)
	runtime.KeepAlive(className)
	runtime.KeepAlive(windowName)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwnd) })
	return hwnd
}

func setTestWindowRect(t *testing.T, hwnd uintptr, position, size config.PixelPair) {
	t.Helper()
	ret, _, err := procSetWindowPos.Call(
		hwnd,
		0,
		uintptr(position[0]),
		uintptr(position[1]),
		uintptr(size[0]),
		uintptr(size[1]),
		swpNoZOrder|swpNoActivate,
	)
	if ret == 0 {
		t.Fatalf("SetWindowPos: %v", err)
	}
}

func assertTestWindowRect(t *testing.T, hwnd uintptr, position, size config.PixelPair) {
	t.Helper()
	rect, err := windowRect(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	if got := (config.PixelPair{rect.Left, rect.Top}); got != position {
		t.Fatalf("window position = %v, want %v", got, position)
	}
	if got := (config.PixelPair{rect.Right - rect.Left, rect.Bottom - rect.Top}); got != size {
		t.Fatalf("window size = %v, want %v", got, size)
	}
}
