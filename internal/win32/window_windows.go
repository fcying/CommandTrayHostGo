//go:build windows && amd64

package win32

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

const (
	smCXFullScreen = 16
	smCYFullScreen = 17
	wsExLayered    = 0x00080000
	lwaAlpha       = 0x00000002
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	swpAsyncWindow = 0x4000
	wsExTopmost    = 0x00000008
)

var (
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procIsIconic                   = user32.NewProc("IsIconic")
	procGetLayeredWindowAttributes = user32.NewProc("GetLayeredWindowAttributes")
	procSetWindowLongPtrW          = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procSetLastError               = kernel32.NewProc("SetLastError")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetDesktopWindow           = user32.NewProc("GetDesktopWindow")
	procGetParent                  = user32.NewProc("GetParent")
	procGetClassNameW              = user32.NewProc("GetClassNameW")
	procGetWindowTextLengthW       = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
)

func applyWindowAppearance(hwnd uintptr, entry config.EntryConfig) error {
	if entry.Position != nil || entry.CachedPosition != nil || entry.Size != nil || entry.CachedSize != nil || entry.Topmost {
		flags := uintptr(swpNoActivate | swpAsyncWindow)
		var x, y, width, height int32
		screenWidth, _, _ := procGetSystemMetrics.Call(smCXFullScreen)
		screenHeight, _, _ := procGetSystemMetrics.Call(smCYFullScreen)
		if px, py, ok := entry.PositionPixels(int32(screenWidth), int32(screenHeight)); !ok {
			flags |= swpNoMove
		} else {
			x, y = px, py
		}
		if cx, cy, ok := entry.SizePixels(int32(screenWidth), int32(screenHeight)); !ok {
			flags |= swpNoSize
		} else {
			width, height = cx, cy
		}
		insertAfter := uintptr(0)
		if entry.Topmost {
			insertAfter = ^uintptr(0) // HWND_TOPMOST
		} else {
			flags |= swpNoZOrder
		}
		ret, _, err := procSetWindowPos.Call(
			hwnd,
			insertAfter,
			uintptr(x),
			uintptr(y),
			uintptr(width),
			uintptr(height),
			flags,
		)
		if ret == 0 {
			return fmt.Errorf("SetWindowPos: %w", err)
		}
	}

	if alpha := entry.EffectiveAlpha(); alpha != nil {
		exStyle, err := getWindowLongPtr(hwnd, ^uintptr(19)) // GWL_EXSTYLE
		if err != nil {
			return err
		}
		if exStyle&wsExLayered == 0 {
			if err := setWindowLongPtr(hwnd, ^uintptr(19), exStyle|wsExLayered); err != nil {
				return err
			}
		}
		ret, _, err := procSetLayeredWindowAttributes.Call(hwnd, 0, uintptr(*alpha), lwaAlpha)
		if ret == 0 {
			return fmt.Errorf("SetLayeredWindowAttributes: %w", err)
		}
	}
	return nil
}

func setWindowTopmost(hwnd uintptr) error {
	ret, _, err := procSetWindowPos.Call(
		hwnd,
		^uintptr(0), // HWND_TOPMOST
		0,
		0,
		0,
		0,
		swpNoMove|swpNoSize|swpNoActivate|swpAsyncWindow,
	)
	if ret == 0 {
		return fmt.Errorf("set topmost: %w", err)
	}
	return nil
}

func applyReloadWindowAppearance(hwnd uintptr, oldEntry, newEntry config.EntryConfig) error {
	if oldEntry.Topmost && !newEntry.Topmost {
		ret, _, err := procSetWindowPos.Call(
			hwnd,
			^uintptr(1), // HWND_NOTOPMOST
			0,
			0,
			0,
			0,
			swpNoMove|swpNoSize|swpNoActivate|swpAsyncWindow,
		)
		if ret == 0 {
			return fmt.Errorf("clear topmost: %w", err)
		}
	}
	if oldEntry.EffectiveAlpha() != nil && newEntry.EffectiveAlpha() == nil {
		ret, _, err := procSetLayeredWindowAttributes.Call(hwnd, 0, 255, lwaAlpha)
		if ret == 0 {
			return fmt.Errorf("reset window alpha: %w", err)
		}
	}
	return applyWindowAppearance(hwnd, newEntry)
}

func windowRect(hwnd uintptr) (windows.Rect, error) {
	var rect windows.Rect
	ret, _, err := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return windows.Rect{}, fmt.Errorf("GetWindowRect: %w", err)
	}
	return rect, nil
}

func windowMinimized(hwnd uintptr) bool {
	minimized, _, _ := procIsIconic.Call(hwnd)
	return minimized != 0
}

func windowAlpha(hwnd uintptr) (uint8, error) {
	exStyle, err := getWindowLongPtr(hwnd, ^uintptr(19))
	if err != nil {
		return 0, err
	}
	if exStyle&wsExLayered == 0 {
		return 255, nil
	}
	var alpha uint8
	var flags uint32
	ret, _, err := procGetLayeredWindowAttributes.Call(
		hwnd,
		0,
		uintptr(unsafe.Pointer(&alpha)),
		uintptr(unsafe.Pointer(&flags)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("GetLayeredWindowAttributes: %w", err)
	}
	if flags&lwaAlpha == 0 {
		return 255, nil
	}
	return alpha, nil
}

func adjustForegroundWindowAlpha(delta int) error {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil
	}
	alpha, err := windowAlpha(hwnd)
	if err != nil {
		return err
	}
	value := int(alpha) + delta
	if value < 0 {
		value = 0
	} else if value > 255 {
		value = 255
	}
	exStyle, err := getWindowLongPtr(hwnd, ^uintptr(19))
	if err != nil {
		return err
	}
	if exStyle&wsExLayered == 0 {
		if err := setWindowLongPtr(hwnd, ^uintptr(19), exStyle|wsExLayered); err != nil {
			return err
		}
	}
	ret, _, err := procSetLayeredWindowAttributes.Call(hwnd, 0, uintptr(value), lwaAlpha)
	if ret == 0 {
		return fmt.Errorf("SetLayeredWindowAttributes: %w", err)
	}
	return nil
}

func toggleForegroundWindowTopmost() error {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil
	}
	exStyle, err := getWindowLongPtr(hwnd, ^uintptr(19))
	if err != nil {
		return err
	}
	insertAfter := ^uintptr(0) // HWND_TOPMOST
	if exStyle&wsExTopmost != 0 {
		insertAfter = ^uintptr(1) // HWND_NOTOPMOST
	}
	ret, _, err := procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpAsyncWindow)
	if ret == 0 {
		return fmt.Errorf("SetWindowPos: %w", err)
	}
	return nil
}

func foregroundWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

func topLevelWindow(hwnd uintptr) uintptr {
	desktop, _, _ := procGetDesktopWindow.Call()
	for hwnd != 0 && hwnd != desktop {
		parent, _, _ := procGetParent.Call(hwnd)
		owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
		if parent == owner {
			break
		}
		if parent == 0 || parent == desktop {
			break
		}
		hwnd = parent
	}
	return hwnd
}

func windowCaption(hwnd uintptr) (string, bool) {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return "", true
	}
	buffer := make([]uint16, int(length)+1)
	ret, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if ret == 0 {
		return "", true
	}
	return windows.UTF16ToString(buffer), false
}

func windowClassName(hwnd uintptr) string {
	buffer := make([]uint16, 256)
	length, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:length])
}

func getWindowLongPtr(hwnd, index uintptr) (uintptr, error) {
	procSetLastError.Call(0)
	value, _, err := procGetWindowLongPtrW.Call(hwnd, index)
	if value == 0 && err != syscall.Errno(0) {
		return 0, fmt.Errorf("GetWindowLongPtrW: %w", err)
	}
	return value, nil
}

func setWindowLongPtr(hwnd, index, value uintptr) error {
	procSetLastError.Call(0)
	previous, _, err := procSetWindowLongPtrW.Call(hwnd, index, value)
	if previous == 0 && err != syscall.Errno(0) {
		return fmt.Errorf("SetWindowLongPtrW: %w", err)
	}
	return nil
}
