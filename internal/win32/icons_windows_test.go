//go:build windows && amd64

package win32

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestLoadIconFileDoesNotWrapSuccessErrno(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := procLoadImageW.Find(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "invalid.ico"), []byte("not an icon"), 0o600); err != nil {
		t.Fatal(err)
	}
	procSetLastError.Call(0)
	icon, err := loadIconFile(directory, "invalid.ico", 32, 32)
	defer icon.Close()
	if err == nil {
		t.Fatal("malformed icon unexpectedly loaded")
	}
	if errors.Is(err, syscall.Errno(0)) {
		t.Fatalf("icon failure wraps a successful Win32 status: %v", err)
	}
}
