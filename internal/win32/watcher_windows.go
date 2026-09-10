//go:build windows && amd64

package win32

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

type directoryWatcher struct {
	change windows.Handle
	stop   windows.Handle
	done   chan struct{}
	once   sync.Once
}

func newDirectoryWatcher(path string, hwnd uintptr) (*directoryWatcher, error) {
	change, err := windows.FindFirstChangeNotification(
		path,
		false,
		windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_SIZE|windows.FILE_NOTIFY_CHANGE_LAST_WRITE,
	)
	if err != nil {
		return nil, fmt.Errorf("watch config directory: %w", err)
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.FindCloseChangeNotification(change)
		return nil, fmt.Errorf("create config watcher stop event: %w", err)
	}
	w := &directoryWatcher{change: change, stop: stop, done: make(chan struct{})}
	go w.run(hwnd)
	return w, nil
}

func (w *directoryWatcher) run(hwnd uintptr) {
	defer close(w.done)
	for {
		event, err := windows.WaitForMultipleObjects([]windows.Handle{w.change, w.stop}, false, windows.INFINITE)
		if err != nil {
			postWindowMessage(hwnd, wmConfigWatcherFailed)
			return
		}
		switch event {
		case windows.WAIT_OBJECT_0:
			if err := windows.FindNextChangeNotification(w.change); err != nil {
				postWindowMessage(hwnd, wmConfigWatcherFailed)
				return
			}
			postWindowMessage(hwnd, wmConfigDirectoryChanged)
		case windows.WAIT_OBJECT_0 + 1:
			return
		default:
			postWindowMessage(hwnd, wmConfigWatcherFailed)
			return
		}
	}
}

func (w *directoryWatcher) Close() error {
	if w == nil {
		return nil
	}
	var closeErr error
	w.once.Do(func() {
		if err := windows.SetEvent(w.stop); err != nil {
			closeErr = fmt.Errorf("stop config directory watcher: %w", err)
			_ = windows.FindCloseChangeNotification(w.change)
			_ = windows.CloseHandle(w.stop)
			return
		}
		<-w.done
		_ = windows.FindCloseChangeNotification(w.change)
		_ = windows.CloseHandle(w.stop)
	})
	return closeErr
}

func postWindowMessage(hwnd uintptr, message uint32) {
	if hwnd == 0 {
		return
	}
	procPostMessageW.Call(hwnd, uintptr(message), 0, 0)
}
