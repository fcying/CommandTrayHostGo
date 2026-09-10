//go:build windows && amd64

package win32

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
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
}
