//go:build windows && amd64

package win32

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"golang.org/x/sys/windows"
)

type Instance struct {
	handle windows.Handle
}

func instanceMutexName(executablePath string) string {
	digest := sha256.Sum256([]byte(executablePath))
	return "CommandTrayHostGo_" + hex.EncodeToString(digest[:])
}

func AcquireInstance(executablePath string, waitForHandoff bool) (*Instance, error) {
	name := instanceMutexName(executablePath)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("create instance name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, true, namePtr)
	if err == windows.ERROR_ALREADY_EXISTS {
		if waitForHandoff {
			result, waitErr := windows.WaitForSingleObject(handle, 5000)
			if waitErr == nil && result == windows.WAIT_OBJECT_0 {
				return &Instance{handle: handle}, nil
			}
		}
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("%s is already running from %s", productName, executablePath)
	}
	if err != nil {
		return nil, fmt.Errorf("CreateMutexW: %w", err)
	}
	return &Instance{handle: handle}, nil
}

func (i *Instance) Close() {
	if i == nil || i.handle == 0 {
		return
	}
	_ = windows.ReleaseMutex(i.handle)
	_ = windows.CloseHandle(i.handle)
	i.handle = 0
}
