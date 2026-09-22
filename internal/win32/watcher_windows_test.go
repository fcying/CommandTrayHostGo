//go:build windows && amd64

package win32

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDirectoryWatcherCloseWaitsAfterStopSignalFailure(t *testing.T) {
	watcher, err := newDirectoryWatcher(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	watcher.signalStop = func(_ windows.Handle) error {
		return errors.New("injected stop signal failure")
	}
	started := time.Now()
	if err := watcher.Close(); err == nil || err.Error() != "stop config directory watcher: injected stop signal failure" {
		t.Fatalf("Close error = %v, want injected signal error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close waited %s after signal failure", elapsed)
	}
}
