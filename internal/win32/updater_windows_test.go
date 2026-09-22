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
func TestSessionCleanupAppliesHardDeadlineToUpdaterWorker(t *testing.T) {
	a := &TrayApp{update: newUpdateRuntime()}
	path := filepath.Join(t.TempDir(), "update.exe")
	started := make(chan struct{})
	a.startUpdateWorker(true, true, func(ctx context.Context) updateCheckResult {
		close(started)
		<-ctx.Done()
		time.Sleep(250 * time.Millisecond)
		if err := os.WriteFile(path, []byte("download"), 0o600); err != nil {
			return updateCheckResult{err: err}
		}
		return updateCheckResult{download: true, updatePath: path}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	startedAt := time.Now()
	a.cleanupSessionEnd(50 * time.Millisecond)
	if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
		t.Fatalf("session cleanup took %s past updater deadline", elapsed)
	}
	a.update.workers.Wait()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled download remains after delayed worker: %v", err)
	}
}

func TestOrdinaryUpdaterCleanupWaitsForWorker(t *testing.T) {
	a := &TrayApp{update: newUpdateRuntime()}
	a.closing.Store(true)
	started := make(chan struct{})
	const delay = 100 * time.Millisecond
	a.startUpdateWorker(false, false, func(ctx context.Context) updateCheckResult {
		close(started)
		<-ctx.Done()
		time.Sleep(delay)
		return updateCheckResult{}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	startedAt := time.Now()
	a.cleanupUpdater()
	if elapsed := time.Since(startedAt); elapsed < delay {
		t.Fatalf("ordinary cleanup returned after %s, want at least %s", elapsed, delay)
	}
}
