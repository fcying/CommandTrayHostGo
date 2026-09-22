//go:build windows && amd64

package win32

import (
	"strings"
	"testing"
)

func TestInstanceMutexNameDistinguishesPathCollisions(t *testing.T) {
	first := instanceMutexName(`C:\a_b\c.exe`)
	second := instanceMutexName(`C:\a\b_c.exe`)
	if first == second {
		t.Fatalf("distinct executable paths share mutex name %q", first)
	}
}

func TestInstanceMutexNameIsBoundedAndDeterministic(t *testing.T) {
	path := `\\?\C:\` + strings.Repeat("a", 10000)
	first := instanceMutexName(path)
	if len(first) > 260 {
		t.Fatalf("mutex name length = %d, want at most 260", len(first))
	}
	if first != instanceMutexName(path) {
		t.Fatal("mutex name is not deterministic")
	}
}
