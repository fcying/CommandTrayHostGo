//go:build windows && amd64

package win32

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The copied test executable acts as an old host: it accepts only normal launch
// arguments and records them without understanding the update helper protocol.
func init() {
	if report := os.Getenv("COMMANDTRAYHOST_UPDATE_RECOVERY_REPORT"); report != "" && filepath.Base(os.Args[0]) == "previous-host.exe" {
		for i, argument := range os.Args[1:] {
			if argument != "--return-token" {
				continue
			}
			if i+2 >= len(os.Args) {
				os.Exit(4)
			}
			value, err := strconv.ParseUint(os.Args[i+2], 10, 64)
			token := windows.Token(value)
			var sid string
			if err == nil {
				var user *windows.Tokenuser
				user, err = token.GetTokenUser()
				if err == nil {
					sid = user.User.Sid.String()
					err = requireUnelevatedUser(token, sid)
				}
			}
			if err == nil {
				var primary windows.Token
				err = windows.DuplicateTokenEx(token, unelevatedReturnTokenAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary)
				if primary != 0 {
					primary.Close()
				}
			}
			token.Close()
			result := sid
			if err != nil {
				result = fmt.Sprintf("invalid inherited return token: %v", err)
			}
			if err := os.WriteFile(report+".token", []byte(result), 0600); err != nil {
				os.Exit(5)
			}
		}
		data, err := json.Marshal(os.Args[1:])
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(report, data, 0600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
}

func recoveryFixture(t *testing.T) (string, string, string, windows.Handle) {
	t.Helper()
	dir := t.TempDir()
	target, update, report := filepath.Join(dir, "previous-host.exe"), filepath.Join(dir, "download.exe"), filepath.Join(dir, "launch.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyUpdateFile(executable, target); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMMANDTRAYHOST_UPDATE_RECOVERY_REPORT", report)
	var process windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentProcess(), windows.CurrentProcess(), &process, windows.SYNCHRONIZE, true, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(process) })
	return target, update, report, process
}

func requireRecoveredLaunch(t *testing.T, report string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(report)
		if err == nil {
			var arguments []string
			if json.Unmarshal(data, &arguments) == nil {
				want := []string{"-c", "config with spaces.json", "startup-user=S-1-5-21-123"}
				if !reflect.DeepEqual(arguments, want) {
					t.Fatalf("recovered host arguments = %q, want %q", arguments, want)
				}
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("previous host did not start")
}

func TestUpdateBackupRenameFailureRestartsPreviousHost(t *testing.T) {
	target, update, report, process := recoveryFixture(t)
	const sentinel = "another transaction backup"
	if err := os.WriteFile(update+".previous", []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	err := applyUpdateFiles(update, target, "config with spaces.json", "S-1-5-21-123", process, 0)
	if err == nil || !strings.Contains(err.Error(), "backup current executable") {
		t.Fatalf("backup failure = %v", err)
	}
	requireRecoveredLaunch(t, report)
	data, err := os.ReadFile(update + ".previous")
	if err != nil || string(data) != sentinel {
		t.Fatalf("unowned backup modified: %q, %v", data, err)
	}
}

func TestUpdateInstallRenameFailureRestartsPreviousHost(t *testing.T) {
	target, update, report, process := recoveryFixture(t)
	// No download exists: backup succeeds, installation fails on the real FS.
	err := applyUpdateFiles(update, target, "config with spaces.json", "S-1-5-21-123", process, 0)
	if err == nil || !strings.Contains(err.Error(), "install update") {
		t.Fatalf("installation failure = %v", err)
	}
	requireRecoveredLaunch(t, report)
}

func TestUpdateInvalidExecutableRestartsPreviousHost(t *testing.T) {
	target, update, report, process := recoveryFixture(t)
	const invalid = "not a Windows executable"
	if err := os.WriteFile(update, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	err := applyUpdateFiles(update, target, "config with spaces.json", "S-1-5-21-123", process, 0)
	if err == nil || !strings.Contains(err.Error(), "restart updated application") {
		t.Fatalf("new executable failure = %v", err)
	}
	requireRecoveredLaunch(t, report)
	data, err := os.ReadFile(update)
	if err != nil || string(data) != invalid {
		t.Fatalf("failed download not preserved: %q, %v", data, err)
	}
}

func TestUpdateRollbackFailurePreservesBackupWithoutLaunchingTarget(t *testing.T) {
	for _, installed := range []bool{false, true} {
		name := "restore blocked"
		if installed {
			name = "move aside blocked"
		}
		t.Run(name, func(t *testing.T) {
			target, update, report, _ := recoveryFixture(t)
			if err := moveUpdateFile(target, update+".previous"); err != nil {
				t.Fatal(err)
			}
			// Put a real executable at target. A failed rollback must not start it.
			if err := copyUpdateFile(update+".previous", target); err != nil {
				t.Fatal(err)
			}
			if installed {
				if err := os.WriteFile(update, []byte("unowned download"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := restorePreviousUpdate(update, target, "config with spaces.json", "S-1-5-21-123", installed, 0)
			if err == nil || !strings.Contains(err.Error(), "retained at") {
				t.Fatalf("rollback failure = %v", err)
			}
			if _, err := os.Stat(update + ".previous"); err != nil {
				t.Fatalf("recoverable backup lost: %v", err)
			}
			time.Sleep(200 * time.Millisecond)
			if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrong host launched: %v", err)
			}
		})
	}
}

func TestUpdateRecoveryStartFailureJoinsInstallationError(t *testing.T) {
	dir := t.TempDir()
	target, update := filepath.Join(dir, "old.exe"), filepath.Join(dir, "absent.exe")
	const invalid = "old executable cannot start"
	if err := os.WriteFile(target, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	err := applyUpdateFiles(update, target, "config.json", "S-1-5-21-123", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "install update") || !strings.Contains(err.Error(), "restart previous application") {
		t.Fatalf("joined installation and recovery failure = %v", err)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("both errors must remain inspectable: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != invalid {
		t.Fatalf("old executable not restored: %q, %v", data, readErr)
	}
}

func restrictedReturnTokenForTest(t *testing.T) windows.Token {
	t.Helper()
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), unelevatedReturnTokenAccess, &source); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	admin, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		t.Fatal(err)
	}
	disabled := windows.SIDAndAttributes{Sid: admin}
	var restricted windows.Token
	// DISABLE_MAX_PRIVILEGE | LUA_TOKEN: build a real restricted token even
	// when the test runner and its Explorer both run with a full admin token.
	result, _, callErr := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken").Call(
		uintptr(source), 0x1|0x4, 1, uintptr(unsafe.Pointer(&disabled)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)),
	)
	runtime.KeepAlive(admin)
	if result == 0 {
		t.Fatal(callErr)
	}
	t.Cleanup(func() { restricted.Close() })
	medium, err := windows.StringToSid("S-1-16-8192")
	if err != nil {
		t.Fatal(err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: medium, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(medium)
	return restricted
}

func TestUpdateRestartPreservesOriginalUserReturnToken(t *testing.T) {
	source := restrictedReturnTokenForTest(t)
	user, err := source.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	retained, err := duplicateUnelevatedReturnToken(source, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var inherited windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(retained), windows.CurrentProcess(), &inherited, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(inherited)
	for _, rollback := range []bool{false, true} {
		t.Run("rollback="+strconv.FormatBool(rollback), func(t *testing.T) {
			target, update, report, process := recoveryFixture(t)
			if rollback {
				if err := os.WriteFile(update, []byte("invalid updated executable"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := copyUpdateFile(target, update); err != nil {
				t.Fatal(err)
			}
			err := applyUpdateFiles(update, target, "config with spaces.json", sid, process, windows.Token(inherited))
			if (err != nil) != rollback {
				t.Fatalf("rollback=%v update result=%v", rollback, err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				_, err := os.Stat(report) // Written only after the token result is closed.
				if err == nil {
					data, err := os.ReadFile(report + ".token")
					if err != nil {
						t.Fatal(err)
					}
					if string(data) != sid {
						t.Fatalf("restarted host return capability: %s", data)
					}
					return
				}
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("restarted host did not receive a usable original-user return token")
		})
	}
}
