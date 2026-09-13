//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetShellWindow          = user32.NewProc("GetShellWindow")
	procCreateProcessWithTokenW = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithTokenW")
	procAdjustTokenPrivileges   = windows.NewLazySystemDLL("advapi32.dll").NewProc("AdjustTokenPrivileges")
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

// withDebugPrivilege changes only a private impersonation token on this OS
// thread. In particular, it never enables privileges on the shared process
// token or mutates an existing caller's impersonation token.
func withDebugPrivilege(open func() error) (err error) {
	runtime.LockOSThread()
	unlock := true
	defer func() {
		if unlock {
			runtime.UnlockOSThread()
		}
	}()
	var previous windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_IMPERSONATE, true, &previous); err != nil && !errors.Is(err, windows.ERROR_NO_TOKEN) {
		return fmt.Errorf("save caller thread token: %w", err)
	}
	if previous != 0 {
		defer func() { err = errors.Join(err, previous.Close()) }()
	}
	source := previous
	if source == 0 {
		if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &source); err != nil {
			return fmt.Errorf("open caller token for debug privilege: %w", err)
		}
		defer func() { err = errors.Join(err, source.Close()) }()
	}
	var scoped windows.Token
	if err := windows.DuplicateTokenEx(source, windows.TOKEN_QUERY|windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_IMPERSONATE, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &scoped); err != nil {
		return fmt.Errorf("duplicate caller token for debug privilege: %w", err)
	}
	defer func() { err = errors.Join(err, scoped.Close()) }()
	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &privileges.Privileges[0].Luid); err != nil {
		return fmt.Errorf("look up debug privilege: %w", err)
	}
	privileges.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	// x/sys's wrapper checks only BOOL and loses ERROR_NOT_ALL_ASSIGNED,
	// which Windows reports even when AdjustTokenPrivileges returns success.
	ok, _, adjustErr := procAdjustTokenPrivileges.Call(uintptr(scoped), 0, uintptr(unsafe.Pointer(&privileges)), 0, 0, 0)
	if ok == 0 || adjustErr != windows.ERROR_SUCCESS {
		return fmt.Errorf("enable debug privilege: %w", adjustErr)
	}
	if err := windows.SetThreadToken(nil, scoped); err != nil {
		return fmt.Errorf("install debug thread token: %w", err)
	}
	defer func() {
		if restoreErr := windows.SetThreadToken(nil, previous); restoreErr != nil {
			// Never let a failed restoration return a privileged thread to Go's
			// pool. Disable the private token and drop impersonation as a
			// fail-closed fallback; keep the lock until this goroutine exits.
			disableErr := windows.AdjustTokenPrivileges(scoped, true, nil, 0, nil, nil)
			err = errors.Join(err, fmt.Errorf("restore caller thread token: %w", restoreErr), disableErr, windows.RevertToSelf())
			unlock = false
		}
	}()
	return open()
}

const unelevatedReturnTokenAccess = windows.TOKEN_QUERY | windows.TOKEN_DUPLICATE | windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID

// captureUnelevatedReturnToken runs in the original medium-integrity source,
// before UAC. The private token can later be duplicated by the authenticated
// elevated receiver without changing the source or Explorer token's DACL.
func captureUnelevatedReturnToken(originalSID string) (token windows.Token, err error) {
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &source); err != nil {
		return 0, fmt.Errorf("open original user token: %w", err)
	}
	defer func() {
		err = errors.Join(err, source.Close())
		if err != nil && token != 0 {
			err = errors.Join(err, token.Close())
			token = 0
		}
	}()
	return duplicateUnelevatedReturnToken(source, originalSID)
}

func duplicateUnelevatedReturnToken(source windows.Token, originalSID string) (windows.Token, error) {
	if err := requireUnelevatedUser(source, originalSID); err != nil {
		return 0, err
	}
	// Grant only launch/duplication rights to the original user and elevated
	// administrators. Never broaden the DACL on any pre-existing token.
	access := fmt.Sprintf("0x%x", unelevatedReturnTokenAccess)
	sd, err := windows.SecurityDescriptorFromString("O:" + originalSID + "D:P(A;;" + access + ";;;" + originalSID + ")(A;;" + access + ";;;BA)")
	if err != nil {
		return 0, fmt.Errorf("build original-user return token security: %w", err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	var token windows.Token
	if err := windows.DuplicateTokenEx(source, unelevatedReturnTokenAccess, &sa, windows.SecurityImpersonation, windows.TokenPrimary, &token); err != nil {
		return 0, fmt.Errorf("capture original-user return token: %w", err)
	}
	return token, nil
}

// launchUnelevated uses the authenticated original source's retained token, or
// the current interactive shell's token when no retained token is available.
// retained is borrowed: ownership remains with the caller.
// CreateProcessWithTokenW requires SeImpersonatePrivilege in the caller and runs
// in the caller's session, so a different-session shell is explicitly rejected.
// parameters must already be Windows-quoted (as for ShellExecuteExW).
func launchUnelevated(target, parameters, workingDirectory, originalSID string, retained windows.Token) (process windows.Handle, err error) {
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
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return 0, fmt.Errorf("read current user before opening desktop shell: %w", err)
	}
	if retained == 0 && originalSID != currentUser.User.Sid.String() {
		return 0, errors.New("original-user return token is unavailable; start as the original user before elevating")
	}
	openShell := func() error {
		var openErr error
		shellProcess, openErr = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, shellPID)
		if openErr != nil {
			return fmt.Errorf("open desktop shell: %w", openErr)
		}
		if retained == 0 {
			if openErr = windows.OpenProcessToken(shellProcess, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &shellToken); openErr != nil {
				return fmt.Errorf("open desktop shell token: %w", openErr)
			}
		}
		return nil
	}
	if originalSID != currentUser.User.Sid.String() {
		err = withDebugPrivilege(openShell)
	} else {
		err = openShell()
	}
	if err != nil {
		return 0, err
	}
	launchToken := retained
	if launchToken == 0 {
		launchToken = shellToken
	}
	if err = requireUnelevatedUser(launchToken, originalSID); err != nil {
		return 0, err
	}
	var tokenSession, tokenSessionSize uint32
	if err := windows.GetTokenInformation(launchToken, windows.TokenSessionId, (*byte)(unsafe.Pointer(&tokenSession)), uint32(unsafe.Sizeof(tokenSession)), &tokenSessionSize); err != nil {
		return 0, fmt.Errorf("read original user token session: %w", err)
	}
	if tokenSessionSize != uint32(unsafe.Sizeof(tokenSession)) || tokenSession != currentSession {
		return 0, errors.New("original user token belongs to a different session")
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
	// duplicated token's defaults and session, without changing the source token.
	if err = windows.DuplicateTokenEx(launchToken, unelevatedReturnTokenAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return 0, fmt.Errorf("duplicate original user launch token: %w", err)
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
