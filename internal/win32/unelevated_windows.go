//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetShellWindow          = user32.NewProc("GetShellWindow")
	procCreateProcessWithTokenW = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithTokenW")
)

// requireUnelevatedUser checks TokenElevation directly: IsElevated deliberately
// collapses API failures to false, which is not safe at this trust boundary.
func requireUnelevatedUser(token windows.Token, originalSID string) error {
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read desktop token user: %w", err)
	}
	if originalSID == "" || user.User.Sid.String() != originalSID {
		return errors.New("desktop shell does not belong to the original user")
	}
	var elevated, size uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &size); err != nil {
		return fmt.Errorf("read desktop token elevation: %w", err)
	}
	if size != uint32(unsafe.Sizeof(elevated)) || elevated != 0 {
		return errors.New("desktop shell token is not non-elevated")
	}
	return nil
}

// launchUnelevated borrows only the current interactive shell's primary token.
// CreateProcessWithTokenW requires SeImpersonatePrivilege in the caller and runs
// in the caller's session, so a different-session shell is explicitly rejected.
// parameters must already be Windows-quoted (as for ShellExecuteExW).
func launchUnelevated(target, parameters, workingDirectory, originalSID string) (process windows.Handle, err error) {
	if !filepath.IsAbs(target) || !filepath.IsAbs(workingDirectory) {
		return 0, errors.New("unelevated executable and working directory must be absolute")
	}
	application, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return 0, err
	}
	command := syscall.EscapeArg(target)
	if parameters != "" {
		command += " " + parameters
	}
	commandLine, err := windows.UTF16FromString(command)
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return 0, err
	}
	shell, _, _ := procGetShellWindow.Call()
	if shell == 0 {
		return 0, errors.New("no interactive desktop shell is available")
	}
	var shellPID uint32
	threadID, _, callErr := procGetWindowThreadProcessID.Call(shell, uintptr(unsafe.Pointer(&shellPID)))
	if threadID == 0 || shellPID == 0 {
		return 0, fmt.Errorf("identify desktop shell: %w", callErr)
	}
	var currentSession, shellSession uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &currentSession); err != nil {
		return 0, fmt.Errorf("read current session: %w", err)
	}
	if err := windows.ProcessIdToSessionId(shellPID, &shellSession); err != nil {
		return 0, fmt.Errorf("read desktop shell session: %w", err)
	}
	if shellSession != currentSession {
		return 0, errors.New("desktop shell belongs to a different session")
	}
	var shellProcess windows.Handle
	var shellToken, primary windows.Token
	var environment *uint16
	var child windows.ProcessInformation
	// Keep the child suspended until token/environment cleanup has succeeded.
	// Any failure after creation destroys the child rather than leaving a second
	// instance running without an authenticated handoff owner.
	defer func() {
		if environment != nil {
			err = errors.Join(err, windows.DestroyEnvironmentBlock(environment))
		}
		if primary != 0 {
			err = errors.Join(err, primary.Close())
		}
		if shellToken != 0 {
			err = errors.Join(err, shellToken.Close())
		}
		if shellProcess != 0 {
			err = errors.Join(err, windows.CloseHandle(shellProcess))
		}
		if child.Thread != 0 {
			if err == nil {
				_, resumeErr := windows.ResumeThread(child.Thread)
				err = errors.Join(err, resumeErr)
			}
			err = errors.Join(err, windows.CloseHandle(child.Thread))
		}
		if err != nil && child.Process != 0 {
			terminateErr := windows.TerminateProcess(child.Process, 1)
			err = errors.Join(err, terminateErr)
			if terminateErr == nil {
				_, waitErr := windows.WaitForSingleObject(child.Process, windows.INFINITE)
				err = errors.Join(err, waitErr)
			}
			err = errors.Join(err, windows.CloseHandle(child.Process))
			process = 0
		}
	}()
	shellProcess, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, shellPID)
	if err != nil {
		return 0, fmt.Errorf("open desktop shell: %w", err)
	}
	if err = windows.OpenProcessToken(shellProcess, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &shellToken); err != nil {
		return 0, fmt.Errorf("open desktop shell token: %w", err)
	}
	if err = requireUnelevatedUser(shellToken, originalSID); err != nil {
		return 0, err
	}
	var image [32768]uint16
	imageSize := uint32(len(image))
	if err = windows.QueryFullProcessImageName(shellProcess, 0, &image[0], &imageSize); err != nil {
		return 0, fmt.Errorf("read desktop shell image: %w", err)
	}
	windowsDirectory, err := windows.GetWindowsDirectory()
	if err != nil {
		return 0, err
	}
	actualImage, err := os.Stat(windows.UTF16ToString(image[:imageSize]))
	if err != nil {
		return 0, err
	}
	expectedImage, err := os.Stat(filepath.Join(windowsDirectory, "explorer.exe"))
	if err != nil {
		return 0, err
	}
	if !os.SameFile(actualImage, expectedImage) {
		return 0, errors.New("interactive desktop shell is not Windows Explorer")
	}
	currentShell, _, _ := procGetShellWindow.Call()
	var currentShellPID uint32
	currentThread, _, _ := procGetWindowThreadProcessID.Call(currentShell, uintptr(unsafe.Pointer(&currentShellPID)))
	if currentShell != shell || currentThread == 0 || currentShellPID != shellPID {
		return 0, errors.New("interactive desktop shell changed during token acquisition")
	}
	// CreateProcessWithTokenW's Windows 10 launch path needs to adjust the
	// duplicated token's defaults and session, without changing the shell token.
	if err = windows.DuplicateTokenEx(shellToken, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return 0, fmt.Errorf("duplicate desktop shell token: %w", err)
	}
	if err = windows.CreateEnvironmentBlock(&environment, primary, false); err != nil {
		return 0, fmt.Errorf("create desktop user environment: %w", err)
	}
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	ok, _, callErr := procCreateProcessWithTokenW.Call(
		uintptr(primary), 0, uintptr(unsafe.Pointer(application)), uintptr(unsafe.Pointer(&commandLine[0])),
		windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_SUSPENDED,
		uintptr(unsafe.Pointer(environment)), uintptr(unsafe.Pointer(directory)),
		uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&child)),
	)
	if ok == 0 {
		return 0, fmt.Errorf("create non-elevated desktop user process (requires SeImpersonatePrivilege): %w", callErr)
	}
	return child.Process, nil
}
