//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

const updateHelperArgument = "--apply-update"

// LaunchUpdateHelper starts a detached copy of the current executable. The helper
// waits for this process to exit, atomically replaces target, and relaunches it.
func LaunchUpdateHelper(target, update, configArgument, startupUserSID string) error {
	var process windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentProcess(), windows.CurrentProcess(), &process, windows.SYNCHRONIZE, true, 0); err != nil {
		return fmt.Errorf("duplicate current process handle: %w", err)
	}
	defer windows.CloseHandle(process)
	directory := filepath.Dir(target)
	helper := update + ".helper.exe"
	if err := copyUpdateFile(target, helper); err != nil {
		return fmt.Errorf("create update helper: %w", err)
	}
	arguments := []string{updateHelperArgument, strconv.FormatUint(uint64(process), 10), update, target, configArgument, startupUserSID}
	command := exec.Command(helper, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(process)}}
	command.Dir = directory
	if err := command.Start(); err != nil {
		os.Remove(helper)
		return fmt.Errorf("start update helper: %w", err)
	}
	return command.Process.Release()
}

// ApplyUpdate waits for the old process, replaces its executable, and restarts it.
func ApplyUpdate(arguments []string) error {
	if len(arguments) != 6 || arguments[0] != updateHelperArgument {
		return errors.New("invalid update helper arguments")
	}
	value, err := strconv.ParseUint(arguments[1], 10, 64)
	if err != nil || value == 0 {
		return errors.New("invalid old process handle")
	}
	update, target, configArgument, startupUserSID := arguments[2], arguments[3], arguments[4], arguments[5]
	process := windows.Handle(value)
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for old process: %w", err)
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("unexpected old process wait status: %d", status)
	}
	backup := update + ".previous"
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("backup current executable: %w", err)
	}
	if err := os.Rename(update, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install update: %w", err)
	}
	var helperProcess windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentProcess(), windows.CurrentProcess(), &helperProcess, windows.SYNCHRONIZE, true, 0); err != nil {
		return fmt.Errorf("duplicate update helper handle: %w", err)
	}
	defer windows.CloseHandle(helperProcess)
	command := exec.Command(target, "--finish-update", strconv.FormatUint(uint64(helperProcess), 10), update)
	command.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(helperProcess)}}
	command.Dir = filepath.Dir(target)
	if configArgument != "" {
		command.Args = append(command.Args, "-c", configArgument)
	}
	if startupUserSID != "" {
		command.Args = append(command.Args, "startup-user="+startupUserSID)
	}
	if err := command.Start(); err != nil {
		_ = os.Rename(target, update)
		_ = os.Rename(backup, target)
		return fmt.Errorf("restart updated application: %w", err)
	}
	return command.Process.Release()
}

// FinishUpdate waits for the inherited helper process handle before removing update files.
func FinishUpdate(handle, update string) error {
	value, err := strconv.ParseUint(handle, 10, 64)
	if err != nil || value == 0 {
		return errors.New("invalid update helper handle")
	}
	process := windows.Handle(value)
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for update helper: %w", err)
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("unexpected update helper wait status: %d", status)
	}
	var cleanupErrors []error
	for _, path := range []string{update + ".previous", update + ".helper.exe"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove update file %s: %w", path, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func copyUpdateFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := output.ReadFrom(input); err != nil {
		output.Close()
		os.Remove(destination)
		return err
	}
	return output.Close()
}
