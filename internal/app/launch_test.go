package app

import (
	"path/filepath"
	"testing"
)

func TestParseLaunchOptionsDefaultPaths(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "app")
	options, err := ParseLaunchOptions(nil, baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if options.ConfigPath != filepath.Join(baseDir, "config.json") {
		t.Fatalf("ConfigPath = %q", options.ConfigPath)
	}
	if options.CachePath != filepath.Join(baseDir, "command_tray_host.cache") {
		t.Fatalf("CachePath = %q", options.CachePath)
	}
	if options.ConfigArgument != "" {
		t.Fatalf("ConfigArgument = %q", options.ConfigArgument)
	}
}

func TestParseLaunchOptionsCustomConfig(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "app")
	options, err := ParseLaunchOptions([]string{"-c", filepath.Join("profiles", "work.json")}, baseDir)
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := filepath.Join(baseDir, "profiles", "work.json")
	if options.ConfigPath != wantConfig || options.ConfigArgument != wantConfig {
		t.Fatalf("config paths = %q, %q, want %q", options.ConfigPath, options.ConfigArgument, wantConfig)
	}
	if options.CachePath != wantConfig+".cache" {
		t.Fatalf("CachePath = %q, want %q", options.CachePath, wantConfig+".cache")
	}
}

func TestParseLaunchOptionsInternalArguments(t *testing.T) {
	baseDir := t.TempDir()
	options, err := ParseLaunchOptions([]string{"force-restart", "startup-user=S-1-5-21", "-c", "test.json"}, baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if !options.ForceRestart || options.StartupUserSID != "S-1-5-21" {
		t.Fatalf("options = %+v", options)
	}
}

func TestConsoleWindowQueryArgument(t *testing.T) {
	const wantPID = uint32(4294967295)
	argument := ConsoleWindowQueryArgument(wantPID)
	pid, ok := ParseConsoleWindowQueryArgument([]string{argument})
	if !ok || pid != wantPID {
		t.Fatalf("ParseConsoleWindowQueryArgument(%q) = %d, %v", argument, pid, ok)
	}

	for _, args := range [][]string{
		nil,
		{"--internal-console-window="},
		{"--internal-console-window=0"},
		{"--internal-console-window=-1"},
		{"--internal-console-window=4294967296"},
		{"--internal-console-window=1", "-c", "config.json"},
	} {
		if pid, ok := ParseConsoleWindowQueryArgument(args); ok {
			t.Fatalf("ParseConsoleWindowQueryArgument(%q) = %d, true", args, pid)
		}
	}
}

func TestParseLaunchOptionsRejectsInvalidConfigArgument(t *testing.T) {
	for _, args := range [][]string{
		{"-c"},
		{"-c", ""},
		{"-c", "-c"},
		{"-c", "one.json", "-c", "two.json"},
		{"unknown"},
		{"-c", `\profile.json`},
		{"-c", `/profile.json`},
		{"-c", `C:profile.json`},
	} {
		if _, err := ParseLaunchOptions(args, t.TempDir()); err == nil {
			t.Fatalf("ParseLaunchOptions(%q) succeeded", args)
		}
	}
}
