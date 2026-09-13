//go:build windows && amd64

package win32

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

func TestElevationSnapshotRefreshesExitedAndHiddenEntries(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(systemDirectory, "cmd.exe"), "/d", "/q")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
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
	window := createTestTopLevelWindow(t, false)
	app := TrayApp{config: config.Config{SourceDigest: [32]byte{1}}, entries: []trayEntry{
		{config: config.EntryConfig{Name: "exited", IsGUI: true}, ownership: domain.ManagedAndJobOwned, state: domain.EntryState{Enabled: true, Running: true, Show: true}, process: process, needsWindow: true, findCount: windowFindLimit - 1},
		{config: config.EntryConfig{Name: "hidden", IsGUI: true}, ownership: domain.ManagedAndJobOwned, state: domain.EntryState{Enabled: true, Running: true, Show: true}, process: &childProcess{handle: windows.CurrentProcess(), pid: uint32(os.Getpid())}, hwnd: window},
	}}
	before := append([]trayEntry(nil), app.entries...)
	initial, ready, err := app.prepareElevationState()
	if err != nil || !ready || !initial.Entries[0].Enabled || !initial.Entries[1].Enabled || initial.Entries[1].Show {
		t.Fatalf("initial snapshot did not capture live/hidden state: ready=%v entries=%+v err=%v", ready, initial.Entries, err)
	}
	if !reflect.DeepEqual(app.entries, before) {
		t.Fatal("snapshot advanced window discovery or mutated source state")
	}
	// Model changes while UAC and receiver readiness delay the final snapshot.
	if _, err := stdin.Write([]byte("exit 0\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	state, ready, err := app.prepareElevationState()
	if err != nil || !ready {
		t.Fatalf("idle live snapshot was rejected: ready=%v err=%v", ready, err)
	}
	if state.Entries[0].Enabled || state.Entries[0].Show {
		t.Fatal("exited process would be restarted from stale state")
	}
	if !state.Entries[1].Enabled || state.Entries[1].Show {
		t.Fatal("hidden window would be shown from stale state")
	}
	if !reflect.DeepEqual(app.entries, before) {
		t.Fatal("final snapshot mutated source process or window state")
	}
	app.sessionEndPending = true
	if _, ready, err := app.prepareElevationState(); ready || err != nil {
		t.Fatalf("session-ending host did not cancel cleanly: ready=%v err=%v", ready, err)
	}
}

func TestElevationSnapshotFindsReplacementWindowWithoutMutation(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	old := createTestTopLevelWindow(t, false)
	// Create the replacement first so Windows cannot reuse the stale HWND.
	replacement := createTestTopLevelWindow(t, false)
	if result, _, err := procDestroyWindow.Call(old); result == 0 {
		t.Fatal(err)
	}
	app := TrayApp{config: config.Config{SourceDigest: [32]byte{1}}, entries: []trayEntry{{
		config:    config.EntryConfig{Name: "replaced", IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Enabled: true, Running: true, Show: true},
		process:   &childProcess{handle: windows.CurrentProcess(), pid: uint32(os.Getpid())},
		hwnd:      old,
	}}}
	before := append([]trayEntry(nil), app.entries...)
	state, ready, err := app.prepareElevationState()
	if err != nil || !ready || !state.Entries[0].Enabled || state.Entries[0].Show {
		t.Fatalf("replacement visibility was not captured: state=%+v ready=%v err=%v", state, ready, err)
	}
	if !reflect.DeepEqual(app.entries, before) || !isWindow(replacement) || isWindowVisible(replacement) {
		t.Fatal("snapshot mutated source entry or replacement window")
	}
	app.entries[0].showPending = true
	state, ready, err = app.prepareElevationState()
	if err != nil || !ready || !state.Entries[0].Show {
		t.Fatal("snapshot discarded a pending explicit show request")
	}
}

func TestElevationSnapshotPreservesShowBeforeWindowDiscovery(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	app := TrayApp{config: config.Config{SourceDigest: [32]byte{1}}, entries: []trayEntry{{
		config:    config.EntryConfig{Name: "delayed", IsGUI: true},
		ownership: domain.ManagedAndJobOwned,
		state:     domain.EntryState{Enabled: true, Running: true},
		process:   &childProcess{handle: windows.CurrentProcess(), pid: uint32(os.Getpid())},
	}}}
	app.setEntryWindowVisible(0, true, true)
	hidden := createTestTopLevelWindow(t, false)
	before := append([]trayEntry(nil), app.entries...)
	state, ready, err := app.prepareElevationState()
	if err != nil || !ready || !state.Entries[0].Show {
		t.Fatalf("pending Show was overwritten by the new hidden window: state=%+v ready=%v err=%v", state, ready, err)
	}
	if !reflect.DeepEqual(app.entries, before) || isWindowVisible(hidden) {
		t.Fatal("snapshot applied or changed the pending Show operation")
	}
}

func TestElevationSnapshotReportsClosedProcessHandle(t *testing.T) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	process := &childProcess{handle: handle, pid: uint32(os.Getpid())}
	app := TrayApp{config: config.Config{SourceDigest: [32]byte{1}}, entries: []trayEntry{
		{config: config.EntryConfig{Name: "failed-query"}, ownership: domain.ManagedAndJobOwned, state: domain.EntryState{Enabled: true, Running: true, Show: true}, process: process, launched: true},
	}}
	before := append([]trayEntry(nil), app.entries...)
	// Close a real handle, retaining its stale value to exercise the native query error.
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	state, ready, err := app.prepareElevationState()
	if ready || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("closed process handle did not report its native error: ready=%v err=%v", ready, err)
	}
	if !strings.Contains(err.Error(), app.entries[0].config.Name) {
		t.Fatalf("query error does not identify the affected entry: %v", err)
	}
	if !reflect.DeepEqual(state, ElevationState{}) {
		t.Fatal("failed query returned a usable partial snapshot")
	}
	if !reflect.DeepEqual(app.entries, before) || process.handle != handle || app.closing.Load() {
		t.Fatal("failed query changed source runtime state")
	}
}

func TestUnelevatedRestartRejectsRequiredPrivileges(t *testing.T) {
	for _, test := range []struct {
		name      string
		rootAdmin bool
		ownership domain.Ownership
		state     ElevationEntry
		wantError bool
	}{
		{name: "root requires admin", rootAdmin: true, wantError: true},
		{name: "enabled managed requires admin", state: ElevationEntry{Enabled: true}, wantError: true},
		{name: "disabled managed", state: ElevationEntry{}},
		{name: "launched job-only requires admin", ownership: domain.UnmanagedButJobOwned, state: ElevationEntry{Launched: true}, wantError: true},
		{name: "detached survives", ownership: domain.FullyDetached, state: ElevationEntry{Enabled: true, Launched: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := TrayApp{config: config.Config{RequireAdmin: test.rootAdmin}, entries: []trayEntry{{config: config.EntryConfig{Name: "admin entry", RequireAdmin: true}, ownership: test.ownership}}}
			err := app.validateUnelevatedRestart(ElevationState{Entries: []ElevationEntry{test.state}})
			if (err != nil) != test.wantError {
				t.Fatalf("downgrade permission validation = %v", err)
			}
		})
	}
}

func TestRestoreElevationRejectsConfigMismatchAtomically(t *testing.T) {
	for _, test := range []struct {
		name         string
		configDigest [32]byte
		stateDigest  [32]byte
		count        int
	}{
		{name: "changed content", configDigest: [32]byte{1}, stateDigest: [32]byte{2}, count: 2},
		{name: "missing state digest", configDigest: [32]byte{1}, count: 2},
		{name: "missing config digest", stateDigest: [32]byte{1}, count: 2},
		{name: "both digests missing", count: 2},
		{name: "missing entry", configDigest: [32]byte{1}, stateDigest: [32]byte{1}, count: 1},
		{name: "extra entry", configDigest: [32]byte{1}, stateDigest: [32]byte{1}, count: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := TrayApp{config: config.Config{SourceDigest: test.configDigest}, entries: []trayEntry{
				{config: config.EntryConfig{Name: "first"}, state: domain.EntryState{Enabled: true}},
				{config: config.EntryConfig{Name: "second"}, state: domain.EntryState{Show: true}},
			}}
			before := append([]trayEntry(nil), app.entries...)
			state := ElevationState{SourceConfigDigest: test.stateDigest, Entries: make([]ElevationEntry, test.count)}
			for i := range state.Entries {
				state.Entries[i] = ElevationEntry{Launched: true, CronTransient: true}
			}
			if err := app.RestoreElevationState(state); err == nil {
				t.Fatal("accepted mismatched config snapshot")
			}
			if !reflect.DeepEqual(app.entries, before) || app.elevationRestored {
				t.Fatal("rejected snapshot changed startup state")
			}
		})
	}
}

func TestElevationStartupReportsLaunchErrors(t *testing.T) {
	app := TrayApp{
		processes:         &processController{},
		elevationRestored: true,
		entries:           []trayEntry{{config: config.EntryConfig{Name: "invalid"}, state: domain.EntryState{Enabled: true}}},
	}
	if err := app.startConfiguredEntries(); err == nil {
		t.Fatal("invalid launch did not return an error")
	}
}

func TestElevationStartupWithoutCacheRestartsOnlyOwnedChildren(t *testing.T) {
	directory := t.TempDir()
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"managed", "disabled", "job-only", "detached"}
	app := TrayApp{baseDir: directory, config: config.Config{SourceDigest: [32]byte{1}}}
	for _, name := range names {
		cfg := config.EntryConfig{
			Name: name, Path: systemDirectory, WorkingDirectory: directory,
			Command:      `cmd.exe /d /c "echo launched>>` + name + `.txt"`,
			NotMonitored: name == "job-only", NotHosted: name == "detached",
		}
		if name == "managed" {
			cfg.Command = `cmd.exe /d /c "echo launched>>managed.txt & ping -n 30 127.0.0.1 >nul"`
		}
		app.entries = append(app.entries, trayEntry{config: cfg, ownership: domain.OwnershipFor(cfg)})
	}
	state := ElevationState{SourceConfigDigest: app.config.SourceDigest, Entries: []ElevationEntry{
		{Enabled: true, Launched: true, CronTransient: true},
		{Launched: true},
		{Launched: true},
		{Enabled: true, Launched: true},
	}}
	if err := app.RestoreElevationState(state); err != nil {
		t.Fatal(err)
	}
	controller, err := newProcessController(directory, "")
	if err != nil {
		t.Fatal(err)
	}
	app.processes = controller
	t.Cleanup(func() {
		controller.Close()
		for i := range app.entries {
			if process := app.entries[i].process; process != nil {
				_, _ = windows.WaitForSingleObject(process.handle, 5000)
				process.Close()
			}
		}
	})
	// A canceled session-end can interrupt startup before any restored entry starts.
	app.sessionEndPending = true
	if err := app.startConfiguredEntries(); err != nil {
		t.Fatal(err)
	}
	app.sessionEndPending = false
	if err := app.startConfiguredEntries(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, managedErr := os.Stat(filepath.Join(directory, "managed.txt"))
		_, jobErr := os.Stat(filepath.Join(directory, "job-only.txt"))
		if managedErr == nil && jobErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("handoff children did not restart: managed=%v job-only=%v", managedErr, jobErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !app.entries[0].cronTransient || !app.entries[0].process.Running() {
		t.Fatal("transient managed process was not resumed as transient")
	}
	for _, index := range []int{1, 3} {
		if app.entries[index].state.Enabled || app.entries[index].state.Running {
			t.Fatalf("%s was enabled by handoff", names[index])
		}
		if _, err := os.Stat(filepath.Join(directory, names[index]+".txt")); !os.IsNotExist(err) {
			t.Fatalf("%s was launched by handoff: %v", names[index], err)
		}
	}
}
