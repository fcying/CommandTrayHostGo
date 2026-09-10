package app

import (
	"errors"
	"fmt"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

type HotkeyActionKind uint8

const (
	HotkeyDisableAll HotkeyActionKind = iota
	HotkeyEnableAll
	HotkeyHideAll
	HotkeyShowAll
	HotkeyRestartAll
	HotkeyElevate
	HotkeyExit
	HotkeyLeftClick
	HotkeyRightClick
	HotkeyAddAlpha
	HotkeyMinusAlpha
	HotkeyTopmost
	HotkeyHideCurrent
	HotkeyShowAllDocked
	HotkeyEntryHideShow
	HotkeyEntryDisableEnable
	HotkeyEntryRestart
	HotkeyEntryElevate
)

type HotkeyAction struct {
	Kind       HotkeyActionKind
	EntryIndex int
}

type HotkeyBinding struct {
	Key    config.Hotkey
	Action HotkeyAction
	Source string
}

type HotkeyPlan struct {
	Bindings []HotkeyBinding
	NoRepeat bool
}

func PlanHotkeys(cfg config.Config) (HotkeyPlan, error) {
	plan := HotkeyPlan{NoRepeat: !cfg.RepeatModHotkey}
	var errs []error
	add := func(value string, action HotkeyAction, source string) {
		if value == "" {
			return
		}
		key, err := config.ParseHotkey(value)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source, err))
			return
		}
		for _, existing := range plan.Bindings {
			if existing.Key.Chord() == key.Chord() {
				errs = append(errs, fmt.Errorf("hotkey %s is assigned to both %s and %s", key.Text, existing.Source, source))
				return
			}
		}
		plan.Bindings = append(plan.Bindings, HotkeyBinding{Key: key, Action: action, Source: source})
	}
	global := []struct {
		value string
		kind  HotkeyActionKind
		name  string
	}{
		{cfg.Hotkey.DisableAll, HotkeyDisableAll, "disable_all"},
		{cfg.Hotkey.EnableAll, HotkeyEnableAll, "enable_all"},
		{cfg.Hotkey.HideAll, HotkeyHideAll, "hide_all"},
		{cfg.Hotkey.ShowAll, HotkeyShowAll, "show_all"},
		{cfg.Hotkey.RestartAll, HotkeyRestartAll, "restart_all"},
		{cfg.Hotkey.Elevate, HotkeyElevate, "elevate"},
		{cfg.Hotkey.Exit, HotkeyExit, "exit"},
		{cfg.Hotkey.LeftClick, HotkeyLeftClick, "left_click"},
		{cfg.Hotkey.RightClick, HotkeyRightClick, "right_click"},
		{cfg.Hotkey.AddAlpha, HotkeyAddAlpha, "add_alpha"},
		{cfg.Hotkey.MinusAlpha, HotkeyMinusAlpha, "minus_alpha"},
		{cfg.Hotkey.Topmost, HotkeyTopmost, "topmost"},
		{cfg.Hotkey.HideCurrent, HotkeyHideCurrent, "hide_current"},
		{cfg.Hotkey.ShowAllDocked, HotkeyShowAllDocked, "show_all_docked"},
	}
	for _, item := range global {
		add(item.value, HotkeyAction{Kind: item.kind, EntryIndex: -1}, "hotkey."+item.name)
	}
	for i, entry := range cfg.Configs {
		items := []struct {
			value string
			kind  HotkeyActionKind
			name  string
		}{
			{entry.Hotkey.HideShow, HotkeyEntryHideShow, "hide_show"},
			{entry.Hotkey.DisableEnable, HotkeyEntryDisableEnable, "disable_enable"},
			{entry.Hotkey.Restart, HotkeyEntryRestart, "restart"},
			{entry.Hotkey.Elevate, HotkeyEntryElevate, "elevate"},
		}
		for _, item := range items {
			source := fmt.Sprintf("configs[%d].hotkey.%s", i, item.name)
			add(item.value, HotkeyAction{Kind: item.kind, EntryIndex: i}, source)
		}
	}
	if !cfg.HotkeysEnabled() {
		plan.Bindings = nil
	}
	return plan, errors.Join(errs...)
}
