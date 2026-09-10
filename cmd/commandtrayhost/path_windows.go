//go:build windows && amd64

package main

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func finalExecutablePath(path string) (string, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", fmt.Errorf("encode executable path: %w", err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return "", fmt.Errorf("open executable path: %w", err)
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolve final executable path: %w", err)
	}
	if length >= uint32(len(buffer)) {
		return "", errors.New("final executable path exceeds the Windows path limit")
	}
	finalPath := windows.UTF16ToString(buffer[:length])
	if strings.HasPrefix(finalPath, `\\?\UNC\`) {
		finalPath = `\\` + strings.TrimPrefix(finalPath, `\\?\UNC\`)
	} else {
		finalPath = strings.TrimPrefix(finalPath, `\\?\`)
	}
	return finalPath, nil
}
