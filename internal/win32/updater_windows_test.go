//go:build windows && amd64

package win32

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateShutdownCancelsWorkerAndRemovesDownload(t *testing.T) {
	a := &TrayApp{update: newUpdateRuntime()}
	path := filepath.Join(t.TempDir(), "update.exe")
	started := make(chan struct{})
	a.startUpdateWorker(true, true, func(ctx context.Context) updateCheckResult {
		close(started)
		<-ctx.Done()
		if err := os.WriteFile(path, []byte("download"), 0600); err != nil {
			return updateCheckResult{err: err}
		}
		return updateCheckResult{download: true, updatePath: path}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	a.closing.Store(true)
	a.cleanupUpdater()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled download remains: %v", err)
	}
}

func TestSessionCancellationDiscardsQueuedSuccessfulDownload(t *testing.T) {
	a := &TrayApp{update: newUpdateRuntime()}
	path := filepath.Join(t.TempDir(), "update.exe")
	if err := os.WriteFile(path, []byte("download"), 0600); err != nil {
		t.Fatal(err)
	}
	a.update.checking = true
	a.update.downloading = true
	a.update.results <- updateCheckResult{download: true, updatePath: path}
	a.interruptUpdateForSession()
	a.handleUpdateResults()
	if a.update.pending != nil || a.update.resumeAfterSession {
		t.Fatal("canceled download scheduled for installation or restart")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("queued download remains: %v", err)
	}
}
