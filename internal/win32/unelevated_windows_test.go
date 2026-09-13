//go:build windows && amd64

package win32

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRequireUnelevatedUserRejectsWrongSID(t *testing.T) {
	if err := requireUnelevatedUser(windows.GetCurrentProcessToken(), "S-1-0-0"); err == nil {
		t.Fatal("accepted a token belonging to a different user")
	}
}

func TestRequireUnelevatedUserRejectsUnreadableToken(t *testing.T) {
	if err := requireUnelevatedUser(0, "S-1-0-0"); err == nil {
		t.Fatal("accepted an unreadable token as non-elevated")
	}
}

// Run interactively after same-account UAC with the desktop user's SID. This
// exercises the no-retained-token fallback; cross-account transitions must
// transfer a private original-source token before attempting the return launch.
func TestLaunchUnelevatedInteractiveChildIdentity(t *testing.T) {
	sid := os.Getenv("CTH_TEST_DESKTOP_SID")
	if sid == "" {
		t.Skip("requires elevated interactive Explorer session and CTH_TEST_DESKTOP_SID")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("interactive test must run elevated")
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	process, err := launchUnelevated(target, `-NoLogo -NoProfile -NonInteractive -Command "Start-Sleep -Seconds 60"`, systemDirectory, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.TerminateProcess(process, 0); err != nil {
			t.Error(err)
		}
		if _, err := windows.WaitForSingleObject(process, 5000); err != nil {
			t.Error(err)
		}
		if err := windows.CloseHandle(process); err != nil {
			t.Error(err)
		}
	})
	status, err := windows.WaitForSingleObject(process, 0)
	if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("child is not running: status=%d err=%v", status, err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	if err := requireUnelevatedUser(token, sid); err != nil {
		t.Fatalf("created child's identity or elevation is wrong: %v", err)
	}
	pid, err := windows.GetProcessId(process)
	if err != nil {
		t.Fatal(err)
	}
	var parentSession, childSession uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &parentSession); err != nil {
		t.Fatal(err)
	}
	if err := windows.ProcessIdToSessionId(pid, &childSession); err != nil {
		t.Fatal(err)
	}
	if childSession != parentSession {
		t.Fatalf("child session %d differs from interactive source session %d", childSession, parentSession)
	}
	if rejected, err := launchUnelevated(target, "", systemDirectory, "S-1-0-0", 0); err == nil || rejected != 0 {
		if rejected != 0 {
			windows.TerminateProcess(rejected, 1)
			windows.CloseHandle(rejected)
		}
		t.Fatal("mismatched original SID did not fail without launching a child")
	}
}

func tokenInformationForTest(t *testing.T, token windows.Token, class uint32) []byte {
	t.Helper()
	var size uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		t.Fatalf("size token information: %v", err)
	}
	data := make([]byte, size)
	if err := windows.GetTokenInformation(token, class, &data[0], size, &size); err != nil {
		t.Fatal(err)
	}
	return data[:size]
}

func threadTokenSnapshotForTest(t *testing.T) []byte {
	t.Helper()
	var token windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token)
	if errors.Is(err, windows.ERROR_NO_TOKEN) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	// TokenStatistics starts with TokenId: compare the actual token identity,
	// not merely its user SID, as well as its complete privilege state.
	return append(tokenInformationForTest(t, token, windows.TokenStatistics), tokenInformationForTest(t, token, windows.TokenPrivileges)...)
}

func requireScopedDebugPrivilegeForTest(t *testing.T) {
	t.Helper()
	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	var debug windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &debug); err != nil {
		t.Fatal(err)
	}
	data := tokenInformationForTest(t, token, windows.TokenPrivileges)
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&data[0]))
	for _, privilege := range privileges.AllPrivileges() {
		if privilege.Luid == debug && privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			return
		}
	}
	t.Fatal("effective thread token does not have enabled SeDebugPrivilege")
}

func installDebugTestToken(t *testing.T, attributes uint32) {
	t.Helper()
	var source, token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &source); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := windows.DuplicateTokenEx(source, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_IMPERSONATE|windows.TOKEN_ADJUST_PRIVILEGES, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &privileges.Privileges[0].Luid); err != nil {
		t.Fatal(err)
	}
	privileges.Privileges[0].Attributes = attributes
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetThreadToken(nil, token); err != nil {
		t.Fatal(err)
	}
}

func TestDebugPrivilegeScopeRestoresThreadToken(t *testing.T) {
	for _, previous := range []bool{false, true} {
		for _, failOpen := range []bool{false, true} {
			t.Run("previous="+strconv.FormatBool(previous)+"/openFailure="+strconv.FormatBool(failOpen), func(t *testing.T) {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				if previous {
					installDebugTestToken(t, 0)
					defer windows.RevertToSelf()
				}
				before := threadTokenSnapshotForTest(t)
				processBefore := tokenInformationForTest(t, windows.GetCurrentProcessToken(), windows.TokenPrivileges)
				called := false
				var openErr error
				err := withDebugPrivilege(func() error {
					called = true
					requireScopedDebugPrivilegeForTest(t)
					pid := uint32(os.Getpid())
					if failOpen {
						pid = 0 // Windows rejects the System Idle Process.
					}
					handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
					openErr = err
					if err == nil {
						return windows.CloseHandle(handle)
					}
					return err
				})
				if !bytes.Equal(before, threadTokenSnapshotForTest(t)) {
					t.Fatal("caller thread token identity or privileges changed")
				}
				if !bytes.Equal(processBefore, tokenInformationForTest(t, windows.GetCurrentProcessToken(), windows.TokenPrivileges)) {
					t.Fatal("shared process privileges changed")
				}
				if errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED) {
					t.Skip("success/open-error coverage requires an elevated token with SeDebugPrivilege")
				}
				if !called || (failOpen && (openErr == nil || !errors.Is(err, openErr))) || (!failOpen && err != nil) {
					t.Fatalf("called=%v openErr=%v scopeErr=%v", called, openErr, err)
				}
			})
		}
	}
}

func TestDebugPrivilegeScopeRejectsUnassignedPrivilege(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	installDebugTestToken(t, windows.SE_PRIVILEGE_REMOVED)
	defer windows.RevertToSelf()
	before := threadTokenSnapshotForTest(t)
	called := false
	err := withDebugPrivilege(func() error { called = true; return nil })
	if called || !errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED) {
		t.Fatalf("missing debug privilege: called=%v err=%v", called, err)
	}
	if !bytes.Equal(before, threadTokenSnapshotForTest(t)) {
		t.Fatal("failed privilege acquisition changed the caller thread token")
	}
}

// This also runs from a batch-logon session. The existing original-user source
// must keep its captured private return token open for the duration of the test.
func TestDuplicateOtherAccountReturnToken(t *testing.T) {
	pidText, tokenText, sid := os.Getenv("CTH_TEST_SOURCE_PID"), os.Getenv("CTH_TEST_SOURCE_TOKEN"), os.Getenv("CTH_TEST_DESKTOP_SID")
	if pidText == "" || tokenText == "" || sid == "" {
		t.Skip("requires CTH_TEST_SOURCE_PID, CTH_TEST_SOURCE_TOKEN, and CTH_TEST_DESKTOP_SID")
	}
	pid, err := strconv.ParseUint(pidText, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	tokenValue, err := strconv.ParseUint(tokenText, 0, 64)
	if err != nil || tokenValue == 0 {
		t.Fatalf("invalid source token handle %q: %v", tokenText, err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if current.User.Sid.String() == sid {
		t.Fatal("cross-account coverage requires a different caller SID")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before := threadTokenSnapshotForTest(t)
	processBefore := tokenInformationForTest(t, windows.GetCurrentProcessToken(), windows.TokenPrivileges)
	var duplicated windows.Handle
	err = withDebugPrivilege(func() error {
		requireScopedDebugPrivilegeForTest(t)
		process, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(pid))
		if err != nil {
			return fmt.Errorf("open other-account source process %d for handle duplication: %w", pid, err)
		}
		defer windows.CloseHandle(process)
		return windows.DuplicateHandle(process, windows.Handle(tokenValue), windows.CurrentProcess(), &duplicated, 0, false, windows.DUPLICATE_SAME_ACCESS)
	})
	token := windows.Token(duplicated)
	if token != 0 {
		defer token.Close()
	}
	if !bytes.Equal(before, threadTokenSnapshotForTest(t)) || !bytes.Equal(processBefore, tokenInformationForTest(t, windows.GetCurrentProcessToken(), windows.TokenPrivileges)) {
		t.Fatal("cross-account open did not restore caller token/privileges")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := requireUnelevatedUser(token, sid); err != nil {
		t.Fatal(err)
	}
	// A transferred handle alone does not prove the private token's DACL lets
	// the alternate administrator duplicate it for CreateProcessWithTokenW.
	var primary windows.Token
	if err := windows.DuplicateTokenEx(token, unelevatedReturnTokenAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		t.Fatalf("duplicate transferred original-user launch token: %v", err)
	}
	defer primary.Close()
	if err := requireUnelevatedUser(primary, sid); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureUnelevatedReturnTokenRejectsWrongIdentity(t *testing.T) {
	if token, err := captureUnelevatedReturnToken("S-1-0-0"); err == nil || token != 0 {
		if token != 0 {
			token.Close()
		}
		t.Fatal("captured a return token for the wrong original identity")
	}
}

func TestCaptureUnelevatedReturnTokenIdentityAndElevation(t *testing.T) {
	current := windows.GetCurrentProcessToken()
	user, err := current.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	token, err := captureUnelevatedReturnToken(user.User.Sid.String())
	if token != 0 {
		defer token.Close()
	}
	if current.IsElevated() {
		if err == nil || token != 0 {
			t.Fatal("captured an elevated token as the original medium user")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := requireUnelevatedUser(token, user.User.Sid.String()); err != nil {
		t.Fatal(err)
	}
}

func TestReturnTokenSourceProcess(t *testing.T) {
	report := os.Getenv("CTH_TEST_SOURCE_REPORT")
	if report == "" {
		return
	}
	pid, err := strconv.ParseUint(os.Getenv("CTH_TEST_DESKTOP_PID"), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(shell)
	var original windows.Token
	if err := windows.OpenProcessToken(shell, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &original); err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	token, err := duplicateUnelevatedReturnToken(original, os.Getenv("CTH_TEST_DESKTOP_SID"))
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	if err := os.WriteFile(report, []byte(fmt.Sprintf("%d %d", os.Getpid(), token)), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(report + ".stop"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("return-token source was not released")
}
