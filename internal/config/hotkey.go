package config

import (
	"errors"
	"fmt"
	"strings"
)

const (
	HotkeyAlt uint16 = 1 << iota
	HotkeyControl
	HotkeyShift
	HotkeyWin
)

const (
	vkSpace    = 0x20
	vkOEMPlus  = 0xbb
	vkOEMMinus = 0xbd
)

type Hotkey struct {
	Modifiers  uint16
	VirtualKey uint16
	Text       string
}

type HotkeyChord struct {
	Modifiers  uint16
	VirtualKey uint16
}

func (h Hotkey) Chord() HotkeyChord {
	return HotkeyChord{Modifiers: h.Modifiers, VirtualKey: h.VirtualKey}
}

// ParseHotkey parses the supported token syntax. If several non-modifier keys
// are present, the last one wins.
func ParseHotkey(value string) (Hotkey, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return Hotkey{}, errors.New("hotkey must not be empty")
	}
	lower := strings.ToLower(value)
	var modifiers uint16
	var virtualKey uint16
	foundKey := false
	for i := 0; i < len(lower); {
		switch {
		case lower[i] == ' ':
			i++
		case lower[i] == '+' && i+1 < len(lower) && lower[i+1] == '+':
			virtualKey, foundKey = vkOEMPlus, true
			i += 2
		case lower[i] == '+' && i+1 < len(lower) && lower[i+1] == '-':
			virtualKey, foundKey = vkOEMMinus, true
			i += 2
		case lower[i] == '+' && i+2 == len(lower) && lower[i+1] == ' ':
			virtualKey, foundKey = vkSpace, true
			i += 2
		case lower[i] == '+':
			i++
		case hasTokenPrefix(lower[i:], "alt"):
			modifiers |= HotkeyAlt
			i += 3
		case hasTokenPrefix(lower[i:], "ctrl"):
			modifiers |= HotkeyControl
			i += 4
		case hasTokenPrefix(lower[i:], "shift"):
			modifiers |= HotkeyShift
			i += 5
		case hasTokenPrefix(lower[i:], "win"):
			modifiers |= HotkeyWin
			i += 3
		case i+1 < len(lower) && lower[i] == '0' && lower[i+1] == 'x':
			if i+3 >= len(lower) {
				return Hotkey{}, fmt.Errorf("invalid virtual-key code at offset %d", i)
			}
			hi, ok1 := hexDigit(lower[i+2])
			lo, ok2 := hexDigit(lower[i+3])
			if !ok1 || !ok2 {
				return Hotkey{}, fmt.Errorf("invalid virtual-key code at offset %d", i)
			}
			virtualKey, foundKey = uint16(hi<<4|lo), true
			i += 4
		case lower[i] >= '0' && lower[i] <= '9':
			virtualKey, foundKey = uint16(lower[i]), true
			i++
		case lower[i] >= 'a' && lower[i] <= 'z':
			virtualKey, foundKey = uint16(lower[i]-'a'+'A'), true
			i++
		default:
			return Hotkey{}, fmt.Errorf("unsupported hotkey token at offset %d", i)
		}
	}
	if !foundKey || virtualKey == 0 {
		return Hotkey{}, errors.New("hotkey must include a non-modifier key")
	}
	return Hotkey{Modifiers: modifiers, VirtualKey: virtualKey, Text: text}, nil
}

func hasTokenPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}

func hexDigit(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	default:
		return 0, false
	}
}
