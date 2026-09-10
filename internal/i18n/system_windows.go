//go:build windows

package i18n

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const localeNameMaxLength = 85

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
	procGetSystemDefaultLCID     = kernel32.NewProc("GetSystemDefaultLCID")
	procGetACP                   = kernel32.NewProc("GetACP")
)

func DetectSystemLocale() SystemLocale {
	var locale SystemLocale
	var name [localeNameMaxLength]uint16
	if count, _, _ := procGetUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&name[0])), uintptr(len(name))); count != 0 {
		locale.Name = windows.UTF16ToString(name[:])
	}
	lcid, _, _ := procGetSystemDefaultLCID.Call()
	locale.LCID = uint32(lcid)
	acp, _, _ := procGetACP.Call()
	locale.ACP = uint32(acp)
	return locale
}
