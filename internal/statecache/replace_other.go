//go:build !windows

package statecache

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
