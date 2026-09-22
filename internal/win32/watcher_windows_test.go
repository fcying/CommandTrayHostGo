//go:build windows && amd64

package win32

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDirectoryWatcherCloseWaitsAfterStopSignalFailure(t *testing.T) {
	watcher, err := newDirectoryWatcher(filepath.Join(t.TempDir(), "config.json"), 0)
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

func TestDirectoryChangesContainTargetName(t *testing.T) {
	data := appendDirectoryChange(nil, "other.txt", false)
	data = appendDirectoryChange(data, "CONFIG.JSON", true)
	if !directoryChangesContain(data, "config.json") {
		t.Fatal("directory change list did not match the config file")
	}
	if directoryChangesContain(data, "missing.json") {
		t.Fatal("directory change list matched an unrelated file")
	}
}

func appendDirectoryChange(data []byte, name string, last bool) []byte {
	encoded := windows.StringToUTF16(name)
	recordLength := 12 + (len(encoded)-1)*2
	paddedLength := (recordLength + 3) &^ 3
	record := make([]byte, paddedLength)
	if !last {
		binary.LittleEndian.PutUint32(record[:4], uint32(paddedLength))
	}
	binary.LittleEndian.PutUint32(record[4:8], windows.FILE_ACTION_MODIFIED)
	binary.LittleEndian.PutUint32(record[8:12], uint32((len(encoded)-1)*2))
	for i, value := range encoded[:len(encoded)-1] {
		binary.LittleEndian.PutUint16(record[12+i*2:], value)
	}
	return append(data, record...)
}
