//go:build windows && amd64

package win32

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

const (
	directoryWatcherBufferSize = 64 * 1024
	directoryWatcherFilter     = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE
)

type directoryWatcher struct {
	directory      windows.Handle
	change         windows.Handle
	stop           windows.Handle
	targetName     string
	done           chan struct{}
	once           sync.Once
	closeRequested atomic.Bool
	signalStop     func(windows.Handle) error
}

func newDirectoryWatcher(configPath string, hwnd uintptr) (*directoryWatcher, error) {
	directoryPath := filepath.Dir(configPath)
	targetName := filepath.Base(configPath)
	path, err := windows.UTF16PtrFromString(directoryPath)
	if err != nil {
		return nil, fmt.Errorf("create config watcher path: %w", err)
	}
	directory, err := windows.CreateFile(
		path,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("watch config directory: %w", err)
	}
	change, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(directory)
		return nil, fmt.Errorf("create config watcher change event: %w", err)
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(change)
		_ = windows.CloseHandle(directory)
		return nil, fmt.Errorf("create config watcher stop event: %w", err)
	}
	w := &directoryWatcher{
		directory:  directory,
		change:     change,
		stop:       stop,
		targetName: targetName,
		done:       make(chan struct{}),
	}
	go w.run(hwnd)
	return w, nil
}

func (w *directoryWatcher) run(hwnd uintptr) {
	defer close(w.done)
	buffer := make([]byte, directoryWatcherBufferSize)
	for {
		if w.closeRequested.Load() {
			return
		}
		if err := windows.ResetEvent(w.change); err != nil {
			w.fail(hwnd)
			return
		}
		var overlapped windows.Overlapped
		overlapped.HEvent = w.change
		err := windows.ReadDirectoryChanges(
			w.directory,
			&buffer[0],
			uint32(len(buffer)),
			false,
			directoryWatcherFilter,
			nil,
			&overlapped,
			0,
		)
		if err != nil && err != windows.ERROR_IO_PENDING {
			w.fail(hwnd)
			return
		}
		event, err := windows.WaitForMultipleObjects([]windows.Handle{w.change, w.stop}, false, windows.INFINITE)
		if err != nil {
			w.cancel(&overlapped)
			w.fail(hwnd)
			return
		}
		if event == windows.WAIT_OBJECT_0+1 || w.closeRequested.Load() {
			w.cancel(&overlapped)
			return
		}
		if event != windows.WAIT_OBJECT_0 {
			w.cancel(&overlapped)
			w.fail(hwnd)
			return
		}
		var bytesReturned uint32
		if err := windows.GetOverlappedResult(w.directory, &overlapped, &bytesReturned, false); err != nil {
			if err == windows.ERROR_NOTIFY_ENUM_DIR {
				postWindowMessage(hwnd, wmConfigDirectoryChanged)
				continue
			}
			if w.closeRequested.Load() {
				return
			}
			w.fail(hwnd)
			return
		}
		if bytesReturned == 0 || directoryChangesContain(buffer[:bytesReturned], w.targetName) {
			postWindowMessage(hwnd, wmConfigDirectoryChanged)
		}
	}
}

func (w *directoryWatcher) cancel(overlapped *windows.Overlapped) {
	_ = windows.CancelIoEx(w.directory, overlapped)
	var bytesReturned uint32
	_ = windows.GetOverlappedResult(w.directory, overlapped, &bytesReturned, true)
}

func (w *directoryWatcher) fail(hwnd uintptr) {
	if !w.closeRequested.Load() {
		postWindowMessage(hwnd, wmConfigWatcherFailed)
	}
}

func directoryChangesContain(data []byte, targetName string) bool {
	for len(data) >= 12 {
		nextOffset := int(binary.LittleEndian.Uint32(data[:4]))
		recordLength := len(data)
		if nextOffset != 0 {
			if nextOffset < 12 || nextOffset%4 != 0 || nextOffset > len(data) {
				return false
			}
			recordLength = nextOffset
		}
		nameLength := binary.LittleEndian.Uint32(data[8:12])
		if nameLength%2 != 0 || nameLength > uint32(recordLength-12) {
			return false
		}
		name := make([]uint16, int(nameLength)/2)
		for i := range name {
			name[i] = binary.LittleEndian.Uint16(data[12+i*2:])
		}
		if strings.EqualFold(windows.UTF16ToString(name), targetName) {
			return true
		}
		if nextOffset == 0 {
			return false
		}
		data = data[nextOffset:]
	}
	return false
}

func (w *directoryWatcher) Close() error {
	if w == nil {
		return nil
	}
	var closeErr error
	w.once.Do(func() {
		w.closeRequested.Store(true)
		signalStop := w.signalStop
		if signalStop == nil {
			signalStop = windows.SetEvent
		}
		if err := signalStop(w.stop); err != nil {
			closeErr = fmt.Errorf("stop config directory watcher: %w", err)
			_ = windows.SetEvent(w.stop)
			_ = windows.CancelIoEx(w.directory, nil)
		}
		<-w.done
		_ = windows.CloseHandle(w.change)
		_ = windows.CloseHandle(w.stop)
		_ = windows.CloseHandle(w.directory)
	})
	return closeErr
}

func postWindowMessage(hwnd uintptr, message uint32) {
	if hwnd == 0 {
		return
	}
	procPostMessageW.Call(hwnd, uintptr(message), 0, 0)
}
