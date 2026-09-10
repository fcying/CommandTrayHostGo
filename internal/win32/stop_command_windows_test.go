//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

func TestStopCommandRunsBeforeBuiltInStop(t *testing.T) {
	command, process, directory := startStopTestProcess(t)
	timeout := int64(0)
	stopScript := filepath.Join(directory, "stop command.cmd")
	if err := os.WriteFile(stopScript, []byte("@echo ran>stop-command-ran.txt\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := config.EntryConfig{
		Path:             directory,
		Command:          "worker.exe",
		WorkingDirectory: directory,
		StopCommand:      `"` + stopScript + `"`,
		KillTimeout:      &timeout,
	}
	controller := &processController{baseDir: directory}

	if err := controller.stop(process, entry); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for built-in termination: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "stop-command-ran.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "ran" {
		t.Fatalf("stop command marker = %q, want ran", got)
	}
}

func TestStopCommandFailureStillUsesBuiltInStop(t *testing.T) {
	command, process, directory := startStopTestProcess(t)
	timeout := int64(0)
	entry := config.EntryConfig{
		Path:             directory,
		Command:          "worker.exe",
		WorkingDirectory: directory,
		StopCommand:      "exit /b 7",
		KillTimeout:      &timeout,
	}
	controller := &processController{baseDir: directory}

	err := controller.stop(process, entry)
	if err == nil || !strings.Contains(err.Error(), "run stop_cmd") {
		t.Fatalf("stop error = %v, want stop_cmd error", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for built-in termination: %v", err)
	}
	if process.Running() {
		t.Fatal("process survived built-in fallback after stop_cmd failure")
	}
}

func TestStopCommandFailureStillAdvancesManualRestart(t *testing.T) {
	command, process, directory := startStopTestProcess(t)
	timeout := int64(0)
	entry := config.EntryConfig{
		Name:             "worker",
		Path:             directory,
		Command:          "missing-worker.exe",
		WorkingDirectory: directory,
		StopCommand:      "exit /b 7",
		KillTimeout:      &timeout,
	}
	controller := &processController{baseDir: directory}
	stopErr := controller.stop(process, entry)
	if stopErr == nil || !strings.Contains(stopErr.Error(), "run stop_cmd") {
		t.Fatalf("stop error = %v, want stop_cmd error", stopErr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for built-in termination: %v", err)
	}

	const generation = 7
	app := TrayApp{
		entries: []trayEntry{{
			config: entry,
			state: domain.EntryState{
				Enabled:    true,
				Running:    true,
				Generation: generation,
			},
			busy: true,
		}},
		processes:         controller,
		processResults:    make(chan processResult, 1),
		sessionEndPending: true,
	}
	app.processResults <- processResult{
		index:      0,
		generation: generation,
		operation:  processRestart,
		process:    process,
		err:        fmt.Errorf("stop worker: %w", stopErr),
	}
	app.handleProcessResults()

	if got := app.entries[0].state.Generation; got != generation+1 {
		t.Fatalf("generation after restart = %d, want %d", got, generation+1)
	}
}

func TestEmptyStopCommandUsesBuiltInTermination(t *testing.T) {
	command, process, directory := startStopTestProcess(t)
	timeout := int64(0)
	entry := config.EntryConfig{
		Path:             directory,
		Command:          "worker.exe",
		WorkingDirectory: directory,
		KillTimeout:      &timeout,
	}
	controller := &processController{baseDir: directory}

	if err := controller.stop(process, entry); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for built-in termination: %v", err)
	}
}

func TestStopCommandRootExitStillTerminatesTree(t *testing.T) {
	directory := t.TempDir()
	gatePath := filepath.Join(directory, "start-child")
	childPIDPath := filepath.Join(directory, "child-pid.txt")
	childScriptPath := filepath.Join(directory, "child.vbs")
	if err := os.WriteFile(childScriptPath, []byte("WScript.Sleep 30000\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootScriptPath := filepath.Join(directory, "root.vbs")
	rootScript := "Set fso = CreateObject(\"Scripting.FileSystemObject\")\r\n" +
		"Do While Not fso.FileExists(WScript.Arguments(0))\r\n" +
		"  WScript.Sleep 10\r\n" +
		"Loop\r\n" +
		"Set child = CreateObject(\"WScript.Shell\").Exec(\"\"\"\" & WScript.Arguments(1) & \"\"\" //B //Nologo \"\"\" & WScript.Arguments(2) & \"\"\"\")\r\n" +
		"Set file = fso.CreateTextFile(WScript.Arguments(3), True)\r\n" +
		"file.WriteLine child.ProcessID\r\n" +
		"file.Close\r\n" +
		"WScript.Sleep 30000\r\n"
	if err := os.WriteFile(rootScriptPath, []byte(rootScript), 0o600); err != nil {
		t.Fatal(err)
	}
	systemDirectory, err := SystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	wscriptPath := filepath.Join(systemDirectory, "wscript.exe")
	rootCommand := exec.Command(wscriptPath, "//B", "//Nologo", rootScriptPath, gatePath, wscriptPath, childScriptPath, childPIDPath)
	rootCommand.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := rootCommand.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rootCommand.Process.Kill()
		_ = rootCommand.Wait()
	})

	treeJob, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var process *childProcess
	t.Cleanup(func() {
		if process != nil {
			if process.treeJob != 0 {
				_ = windows.TerminateJobObject(process.treeJob, 1)
			}
			process.Close()
		} else {
			windows.CloseHandle(treeJob)
		}
	})
	rootPID := uint32(rootCommand.Process.Pid)
	rootHandle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA, false, rootPID)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(treeJob, rootHandle); err != nil {
		windows.CloseHandle(rootHandle)
		t.Fatal(err)
	}
	process = &childProcess{handle: rootHandle, treeJob: treeJob, pid: rootPID}
	if err := os.WriteFile(gatePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var childPID uint64
	for childPID == 0 {
		data, err := os.ReadFile(childPIDPath)
		if err == nil {
			childPID, err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32)
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("root process did not report its child PID")
		}
		time.Sleep(10 * time.Millisecond)
	}
	childHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(childHandle) })

	entry := config.EntryConfig{
		Path:            directory,
		Command:         "worker.exe",
		StopCommand:     fmt.Sprintf("taskkill.exe /F /PID %d", rootPID),
		KillProcessTree: true,
	}
	controller := &processController{baseDir: directory}
	if err := controller.stop(process, entry); err != nil {
		t.Fatal(err)
	}
	if result, err := windows.WaitForSingleObject(childHandle, 5000); result != windows.WAIT_OBJECT_0 {
		t.Fatalf("child process survived tree stop: result=%d err=%v", result, err)
	}
}

func startStopTestProcess(t *testing.T) (*exec.Cmd, *childProcess, string) {
	t.Helper()
	directory := t.TempDir()
	scriptPath := filepath.Join(directory, "sleep.vbs")
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
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	process := &childProcess{handle: handle, pid: uint32(command.Process.Pid)}
	t.Cleanup(process.Close)
	return command, process, directory
}
