package config

import "testing"

func TestParseHotkeySyntax(t *testing.T) {
	for _, tc := range []struct {
		value string
		mods  uint16
		key   uint16
	}{
		{"Alt+Win+Shift+D", HotkeyAlt | HotkeyWin | HotkeyShift, 'D'},
		{"Alt Win Shift E", HotkeyAlt | HotkeyWin | HotkeyShift, 'E'},
		{"Alt+Ctrl+Win+0x26", HotkeyAlt | HotkeyControl | HotkeyWin, 0x26},
		{"Ctrl++", HotkeyControl, vkOEMPlus},
		{"Ctrl+-", HotkeyControl, vkOEMMinus},
		{"Alt+ ", HotkeyAlt, vkSpace},
		{"Ctrl+AB", HotkeyControl, 'B'},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := ParseHotkey(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if got.Modifiers != tc.mods || got.VirtualKey != tc.key {
				t.Fatalf("ParseHotkey(%q) = %#v, want modifiers %#x key %#x", tc.value, got, tc.mods, tc.key)
			}
		})
	}
}

func TestParseHotkeyRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "Ctrl+Alt", "0x2", "Ctrl+?"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseHotkey(value); err == nil {
				t.Fatal("ParseHotkey succeeded, want error")
			}
		})
	}
}
