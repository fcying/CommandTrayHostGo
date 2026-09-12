package app

import (
	"testing"
)

func TestStartupValueName(t *testing.T) {
	a := StartupValueName(`C:\Apps\A\CommandTrayHostGo.exe`)
	b := StartupValueName(`C:\Apps_A\CommandTrayHostGo.exe`)
	if len(a) > 64 {
		t.Fatalf("startup value name exceeds the tested safe length: %d characters", len(a))
	}
	if a != StartupValueName(`C:\Apps\A\CommandTrayHostGo.exe`) {
		t.Fatal("startup value name is not stable for the same executable path")
	}
	if a == b {
		t.Fatalf("distinct paths produced the same startup value name: %q", a)
	}
	if a == StartupValueName(`c:\Apps\A\CommandTrayHostGo.exe`) {
		t.Fatal("case-sensitive paths produced the same startup value name")
	}
}
