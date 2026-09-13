//go:build windows && amd64

package win32

import (
	"os"
	"path/filepath"
	"testing"

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

// Run interactively from an elevated test process, setting this SID to the
// original desktop user's SID (not the alternate administrator's SID). Repeat
// once after same-account UAC and once after alternate-credential UAC.
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
	process, err := launchUnelevated(target, `-NoLogo -NoProfile -NonInteractive -Command "Start-Sleep -Seconds 60"`, systemDirectory, sid)
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
	if rejected, err := launchUnelevated(target, "", systemDirectory, "S-1-0-0"); err == nil || rejected != 0 {
		if rejected != 0 {
			windows.TerminateProcess(rejected, 1)
			windows.CloseHandle(rejected)
		}
		t.Fatal("mismatched original SID did not fail without launching a child")
	}
}
