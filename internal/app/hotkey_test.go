package app

import (
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestPlanHotkeys(t *testing.T) {
	cfg := config.Config{
		Hotkey:  config.GlobalHotkeys{DisableAll: "Ctrl+Alt+D"},
		Configs: []config.EntryConfig{{Hotkey: config.EntryHotkeys{Restart: "Ctrl+Alt+R"}}},
	}
	plan, err := PlanHotkeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NoRepeat || len(plan.Bindings) != 2 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Bindings[1].Action.Kind != HotkeyEntryRestart || plan.Bindings[1].Action.EntryIndex != 0 {
		t.Fatalf("unexpected entry binding: %+v", plan.Bindings[1])
	}
}

func TestPlanHotkeysDisabledStillValidates(t *testing.T) {
	disabled := false
	for _, tc := range []struct {
		name string
		cfg  config.Config
	}{
		{"global syntax", config.Config{Hotkey: config.GlobalHotkeys{Exit: "invalid?"}}},
		{"entry syntax", config.Config{Configs: []config.EntryConfig{{Hotkey: config.EntryHotkeys{Restart: "Ctrl+?"}}}}},
		{"duplicate chord", config.Config{
			Hotkey:  config.GlobalHotkeys{Exit: "Ctrl+R"},
			Configs: []config.EntryConfig{{Hotkey: config.EntryHotkeys{Restart: "ctrl+r"}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.EnableHotkey = &disabled
			plan, err := PlanHotkeys(tc.cfg)
			if err == nil {
				t.Fatal("disabled hotkeys bypassed validation")
			}
			if len(plan.Bindings) != 0 {
				t.Fatalf("disabled hotkeys scheduled registrations: %+v", plan.Bindings)
			}
		})
	}
}

func TestPlanHotkeysDisabledDoesNotRegister(t *testing.T) {
	disabled := false
	cfg := config.Config{
		EnableHotkey: &disabled,
		Hotkey:       config.GlobalHotkeys{Exit: "Ctrl+X"},
		Configs:      []config.EntryConfig{{Hotkey: config.EntryHotkeys{Restart: "Ctrl+R"}}},
	}
	plan, err := PlanHotkeys(cfg)
	if err != nil || len(plan.Bindings) != 0 {
		t.Fatalf("PlanHotkeys = (%+v, %v), want no registrations", plan, err)
	}
}

func TestPlanHotkeysRejectsDuplicateChord(t *testing.T) {
	cfg := config.Config{Hotkey: config.GlobalHotkeys{DisableAll: "Ctrl+D", EnableAll: "ctrl+d"}}
	if _, err := PlanHotkeys(cfg); err == nil {
		t.Fatal("PlanHotkeys succeeded, want duplicate error")
	}
}

func TestPlanHotkeysKeepsValidBindingsAfterInvalidValue(t *testing.T) {
	cfg := config.Config{Hotkey: config.GlobalHotkeys{DisableAll: "invalid?", Exit: "Ctrl+X"}}
	plan, err := PlanHotkeys(cfg)
	if err == nil {
		t.Fatal("PlanHotkeys succeeded, want parse error")
	}
	if len(plan.Bindings) != 1 || plan.Bindings[0].Action.Kind != HotkeyExit {
		t.Fatalf("PlanHotkeys bindings = %+v, want valid exit binding", plan.Bindings)
	}
}
