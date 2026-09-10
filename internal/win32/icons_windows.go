//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

const (
	imageIcon       = 1
	lrLoadFromFile  = 0x0010
	wmSetIcon       = 0x0080
	iconSmall       = 0
	iconBig         = 1
	smtoAbortIfHung = 0x0002
	smtoBlock       = 0x0001
	smtoErrorOnExit = 0x0020
	iconSendTimeout = 200
)

var (
	procLoadImageW          = user32.NewProc("LoadImageW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

type ownedIcon struct {
	handle uintptr
}

func (i *ownedIcon) Close() {
	if i == nil || i.handle == 0 {
		return
	}
	procDestroyIcon.Call(i.handle)
	i.handle = 0
}

type windowIconPair struct {
	big   ownedIcon
	small ownedIcon
}

func (p *windowIconPair) Close() {
	if p == nil {
		return
	}
	p.big.Close()
	p.small.Close()
}

func (p *windowIconPair) Abandon() {
	if p == nil {
		return
	}
	p.big.handle = 0
	p.small.handle = 0
}

type iconResources struct {
	tray    ownedIcon
	console *windowIconPair
	entries []*windowIconPair
}

func (r *iconResources) Close() {
	if r == nil {
		return
	}
	r.tray.Close()
	r.console.Close()
	for _, pair := range r.entries {
		pair.Close()
	}
	r.entries = nil
}

func (r *iconResources) CloseTrayAndAbandonWindowIcons() {
	if r == nil {
		return
	}
	r.tray.Close()
	r.console.Abandon()
	for _, pair := range r.entries {
		if pair == nil {
			continue
		}
		pair.big.handle = 0
		pair.small.handle = 0
	}
	r.entries = nil
}

func loadIconResources(baseDir string, cfg config.Config) (*iconResources, error) {
	resources := &iconResources{entries: make([]*windowIconPair, len(cfg.Configs))}
	var errs []error
	if cfg.Icon != "" {
		icon, err := loadIconFile(baseDir, cfg.Icon, cfg.EffectiveIconSize(), cfg.EffectiveIconSize())
		if err != nil {
			errs = append(errs, fmt.Errorf("load icon: %w", err))
		} else {
			resources.tray = icon
			if len(cfg.LeftClick) == 0 {
				big, bigErr := loadIconFile(baseDir, cfg.Icon, 32, 32)
				if bigErr != nil {
					errs = append(errs, fmt.Errorf("load console icon: %w", bigErr))
				} else {
					small, smallErr := loadIconFile(baseDir, cfg.Icon, 16, 16)
					if smallErr != nil {
						big.Close()
						errs = append(errs, fmt.Errorf("load console icon: %w", smallErr))
					} else {
						resources.console = &windowIconPair{big: big, small: small}
					}
				}
			}
		}
	}
	for i := range cfg.Configs {
		if cfg.Configs[i].Icon == "" {
			continue
		}
		big, err := loadIconFile(baseDir, cfg.Configs[i].Icon, 32, 32)
		if err != nil {
			errs = append(errs, fmt.Errorf("load configs[%d].icon: %w", i, err))
			continue
		}
		small, err := loadIconFile(baseDir, cfg.Configs[i].Icon, 16, 16)
		if err != nil {
			big.Close()
			errs = append(errs, fmt.Errorf("load configs[%d].icon: %w", i, err))
			continue
		}
		resources.entries[i] = &windowIconPair{big: big, small: small}
	}
	return resources, errors.Join(errs...)
}

func loadIconFile(baseDir, name string, width, height int32) (ownedIcon, error) {
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return ownedIcon{}, err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ownedIcon{}, err
	}
	procSetLastError.Call(0)
	handle, _, callErr := procLoadImageW.Call(
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		imageIcon,
		uintptr(width),
		uintptr(height),
		lrLoadFromFile,
	)
	runtime.KeepAlive(pathPtr)
	if handle == 0 {
		if callErr == syscall.Errno(0) {
			callErr = errors.New("failed without a Win32 error code")
		}
		return ownedIcon{}, fmt.Errorf("LoadImageW %s: %w", path, callErr)
	}
	return ownedIcon{handle: handle}, nil
}

func (a *TrayApp) activeTrayIcon() uintptr {
	if a.icons != nil && a.icons.tray.handle != 0 {
		return a.icons.tray.handle
	}
	return a.defaultIcon
}

func (a *TrayApp) desiredEntryIcons(index int) *windowIconPair {
	if a.icons == nil || index < 0 || index >= len(a.icons.entries) {
		return nil
	}
	return a.icons.entries[index]
}

func (a *TrayApp) desiredConsoleIcons() *windowIconPair {
	if a.icons == nil {
		return nil
	}
	return a.icons.console
}

func setWindowIconPair(hwnd uintptr, pair *windowIconPair) error {
	var big, small uintptr
	if pair != nil {
		big = pair.big.handle
		small = pair.small.handle
	}
	_, bigErr := setWindowIcon(hwnd, iconBig, big)
	_, smallErr := setWindowIcon(hwnd, iconSmall, small)
	return errors.Join(bigErr, smallErr)
}

func applyDesiredWindowIcons(hwnd uintptr, desired *windowIconPair, retained *windowIconPair, retainedHWND *uintptr) error {
	if *retainedHWND != 0 && *retainedHWND != hwnd {
		if ret, _, _ := procIsWindow.Call(*retainedHWND); ret == 0 {
			retained.Close()
		} else {
			retained.Abandon()
		}
		*retainedHWND = 0
	}
	var errs []error
	apply := func(kind uintptr, desiredIcon *ownedIcon, retainedIcon *ownedIcon) {
		previous, err := setWindowIcon(hwnd, kind, desiredIcon.handle)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if retainedIcon.handle != 0 {
			if *retainedHWND == hwnd && previous == retainedIcon.handle {
				retainedIcon.Close()
			} else {
				retainedIcon.handle = 0
			}
		}
	}
	var desiredBig, desiredSmall ownedIcon
	if desired != nil {
		desiredBig = desired.big
		desiredSmall = desired.small
	}
	apply(iconBig, &desiredBig, &retained.big)
	apply(iconSmall, &desiredSmall, &retained.small)
	if retained.big.handle == 0 && retained.small.handle == 0 {
		*retainedHWND = 0
	} else {
		*retainedHWND = hwnd
	}
	return errors.Join(errs...)
}

func detachCurrentWindowIcons(entry *trayEntry, resources *iconResources, index int) windowIconPair {
	current := entry.retainedIcons
	entry.retainedIcons = windowIconPair{}
	if resources == nil || index < 0 || index >= len(resources.entries) || resources.entries[index] == nil {
		return current
	}
	pair := resources.entries[index]
	if current.big.handle == 0 {
		current.big = pair.big
		pair.big.handle = 0
	}
	if current.small.handle == 0 {
		current.small = pair.small
		pair.small.handle = 0
	}
	return current
}

func detachCurrentConsoleIcons(console *consoleFallback, resources *iconResources) windowIconPair {
	current := console.retainedIcons
	console.retainedIcons = windowIconPair{}
	if resources == nil || resources.console == nil {
		return current
	}
	if current.big.handle == 0 {
		current.big = resources.console.big
		resources.console.big.handle = 0
	}
	if current.small.handle == 0 {
		current.small = resources.console.small
		resources.console.small.handle = 0
	}
	return current
}

func setWindowIcon(hwnd, kind, icon uintptr) (uintptr, error) {
	var result uintptr
	procSetLastError.Call(0)
	ret, _, callErr := procSendMessageTimeoutW.Call(
		hwnd,
		wmSetIcon,
		kind,
		icon,
		smtoBlock|smtoAbortIfHung|smtoErrorOnExit,
		iconSendTimeout,
		uintptr(unsafe.Pointer(&result)),
	)
	if ret == 0 {
		if callErr == syscall.Errno(0) {
			return 0, fmt.Errorf("WM_SETICON %d failed or timed out (%d ms limit)", kind, iconSendTimeout)
		}
		return 0, fmt.Errorf("WM_SETICON %d: %w", kind, callErr)
	}
	return result, nil
}
