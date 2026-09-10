package app

import (
	"reflect"
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestSameLaunchIdentity(t *testing.T) {
	base := config.EntryConfig{Path: "bin", Command: "demo.exe -a", WorkingDirectory: "work"}
	for _, tc := range []struct {
		name   string
		change func(*config.EntryConfig)
		want   bool
	}{
		{name: "same", want: true},
		{name: "name", change: func(e *config.EntryConfig) { e.Name = "renamed" }, want: true},
		{name: "appearance", change: func(e *config.EntryConfig) { e.Topmost = true }, want: true},
		{name: "kill timeout", change: func(e *config.EntryConfig) { value := int64(500); e.KillTimeout = &value }, want: true},
		{name: "path", change: func(e *config.EntryConfig) { e.Path = "other" }},
		{name: "command", change: func(e *config.EntryConfig) { e.Command = "other.exe" }},
		{name: "working directory", change: func(e *config.EntryConfig) { e.WorkingDirectory = "other" }},
		{name: "window type", change: func(e *config.EntryConfig) { e.IsGUI = !e.IsGUI }},
		{name: "ownership", change: func(e *config.EntryConfig) { e.NotHosted = true }},
		{name: "admin", change: func(e *config.EntryConfig) { e.RequireAdmin = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			if tc.change != nil {
				tc.change(&candidate)
			}
			if got := SameLaunchIdentity(base, candidate); got != tc.want {
				t.Fatalf("SameLaunchIdentity() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCanKeepManagedProcess(t *testing.T) {
	console := config.EntryConfig{Name: "old", Path: "bin", Command: "demo.exe", WorkingDirectory: "work"}
	renamedConsole := console
	renamedConsole.Name = "new"
	gui := console
	gui.IsGUI = true
	renamedGUI := gui
	renamedGUI.Name = "new"
	changedCommand := console
	changedCommand.Command = "other.exe"
	for _, tc := range []struct {
		name string
		old  config.EntryConfig
		new  config.EntryConfig
		want bool
	}{
		{"unchanged console", console, console, true},
		{"renamed console", console, renamedConsole, false},
		{"renamed GUI", gui, renamedGUI, true},
		{"changed command", console, changedCommand, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanKeepManagedProcess(tc.old, tc.new); got != tc.want {
				t.Fatalf("CanKeepManagedProcess() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchReloadEntries(t *testing.T) {
	entry := func(name, command string) config.EntryConfig {
		return config.EntryConfig{Name: name, Command: command}
	}
	for _, tc := range []struct {
		name string
		old  []config.EntryConfig
		new  []config.EntryConfig
		want []int
	}{
		{name: "same order", old: []config.EntryConfig{entry("a", "a.exe"), entry("b", "b.exe")}, new: []config.EntryConfig{entry("a", "a.exe"), entry("b", "b.exe")}, want: []int{0, 1}},
		{name: "reordered", old: []config.EntryConfig{entry("a", "a.exe"), entry("b", "b.exe")}, new: []config.EntryConfig{entry("b", "b.exe"), entry("a", "a.exe")}, want: []int{1, 0}},
		{name: "added", old: []config.EntryConfig{entry("a", "a.exe")}, new: []config.EntryConfig{entry("a", "a.exe"), entry("b", "b.exe")}, want: []int{0, -1}},
		{name: "renamed same identity", old: []config.EntryConfig{entry("a", "a.exe")}, new: []config.EntryConfig{entry("renamed", "a.exe")}, want: []int{0}},
		{name: "duplicate names", old: []config.EntryConfig{entry("a", "1.exe"), entry("a", "2.exe")}, new: []config.EntryConfig{entry("a", "1.exe"), entry("a", "2.exe"), entry("a", "3.exe")}, want: []int{0, 1, -1}},
		{name: "duplicate names reordered", old: []config.EntryConfig{entry("a", "1.exe"), entry("a", "2.exe")}, new: []config.EntryConfig{entry("a", "2.exe"), entry("a", "1.exe")}, want: []int{1, 0}},
		{name: "renamed and reordered", old: []config.EntryConfig{entry("a", "1.exe"), entry("b", "2.exe")}, new: []config.EntryConfig{entry("renamed", "2.exe"), entry("a", "1.exe")}, want: []int{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchReloadEntries(tc.old, tc.new); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MatchReloadEntries() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPreserveReloadProcessState(t *testing.T) {
	candidate := EntryState{Enabled: false}
	current := EntryState{Enabled: true, Running: true, Show: true, Generation: 7}

	got := PreserveReloadProcessState(candidate, current)
	if got.Enabled {
		t.Fatal("PreserveReloadProcessState() overwrote candidate enabled state")
	}
	if !got.Running || !got.Show || got.Generation != 8 {
		t.Fatalf("PreserveReloadProcessState() = %+v", got)
	}
}
