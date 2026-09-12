//go:build windows && amd64

package win32

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func startupRegistryFixture(t *testing.T) (startupRegistration, registry.Key, registry.Key) {
	t.Helper()
	dir := t.TempDir()
	s := newStartupRegistration(filepath.Join(dir, "startup.exe"), "", filepath.Join(dir, "selected.json"))
	if !s.Available() {
		t.Fatal("cannot determine original Windows user")
	}
	run, _, err := registry.CreateKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Close() })
	approved, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { approved.Close() })
	for _, key := range []registry.Key{run, approved} {
		for _, name := range []string{s.valueName, s.legacyValueName()} {
			if _, _, err := key.GetValue(name, nil); !errors.Is(err, registry.ErrNotExist) {
				t.Fatalf("refusing to touch preexisting value %q: %v", name, err)
			}
		}
	}
	t.Cleanup(func() {
		for _, key := range []registry.Key{run, approved} {
			for _, name := range []string{s.valueName, s.legacyValueName()} {
				if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
					t.Errorf("clean up isolated value %q: %v", name, err)
				}
			}
		}
	})
	return s, run, approved
}

func setStartupRun(t *testing.T, key registry.Key, name, command string) {
	t.Helper()
	if err := key.SetStringValue(name, command); err != nil {
		t.Fatal(err)
	}
}

func setStartupApproval(t *testing.T, key registry.Key, name string, state []byte) {
	t.Helper()
	if err := key.SetBinaryValue(name, state); err != nil {
		t.Fatal(err)
	}
}

func requireStartupAbsent(t *testing.T, key registry.Key, name string) {
	t.Helper()
	if _, _, err := key.GetValue(name, nil); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("value %q should be absent: %v", name, err)
	}
}

func requireStartupRun(t *testing.T, key registry.Key, name, command string) {
	t.Helper()
	got, _, err := key.GetStringValue(name)
	if err != nil || got != command {
		t.Fatalf("Run %q = %q, %v; want %q", name, got, err, command)
	}
}

func requireStartupApproval(t *testing.T, key registry.Key, name string, state []byte) {
	t.Helper()
	got, kind, err := key.GetBinaryValue(name)
	if err != nil || kind != registry.BINARY || !bytes.Equal(got, state) {
		t.Fatalf("approval %q = %x, type %d, %v; want binary %x", name, got, kind, err, state)
	}
}

func TestStartupMigratesLegacyApproval(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state []byte
	}{
		{"enabled", []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"disabled", []byte{3, 0, 0, 0, 17, 28, 39, 40, 51, 62, 73, 84}},
		{"absent", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, run, approved := startupRegistryFixture(t)
			setStartupRun(t, run, s.legacyValueName(), s.command)
			if tc.state != nil {
				setStartupApproval(t, approved, s.legacyValueName(), tc.state)
			}
			if err := s.Migrate(); err != nil {
				t.Fatal(err)
			}
			enabled, err := s.Enabled()
			if err != nil || !enabled {
				t.Fatalf("registered legacy entry not resolved: %v, %v", enabled, err)
			}
			requireStartupRun(t, run, s.valueName, s.command)
			requireStartupAbsent(t, run, s.legacyValueName())
			requireStartupAbsent(t, approved, s.legacyValueName())
			if tc.state == nil {
				requireStartupAbsent(t, approved, s.valueName)
			} else {
				requireStartupApproval(t, approved, s.valueName, tc.state)
			}
		})
	}
}

func TestStartupMigrationPreservesOwnedDestination(t *testing.T) {
	s, run, approved := startupRegistryFixture(t)
	oldState := []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	newState := []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	setStartupRun(t, run, s.legacyValueName(), s.command)
	setStartupRun(t, run, s.valueName, s.command)
	setStartupApproval(t, approved, s.legacyValueName(), oldState)
	setStartupApproval(t, approved, s.valueName, newState)
	if err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	requireStartupApproval(t, approved, s.valueName, newState)
	requireStartupAbsent(t, run, s.legacyValueName())
	requireStartupAbsent(t, approved, s.legacyValueName())
}

func TestStartupMigrationForeignDestinationCanDisableLegacy(t *testing.T) {
	s, run, approved := startupRegistryFixture(t)
	foreign := s.command + " --foreign"
	state := []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	setStartupRun(t, run, s.legacyValueName(), s.command)
	setStartupRun(t, run, s.valueName, foreign)
	setStartupApproval(t, approved, s.legacyValueName(), state)
	setStartupApproval(t, approved, s.valueName, state)
	if enabled, err := s.Enabled(); err != nil || !enabled {
		t.Fatalf("legacy registration stranded by conflict: %v, %v", enabled, err)
	}
	if err := s.Enable(); err == nil {
		t.Fatal("Enable accepted foreign destination")
	}
	requireStartupRun(t, run, s.legacyValueName(), s.command)
	if err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	requireStartupAbsent(t, run, s.legacyValueName())
	requireStartupAbsent(t, approved, s.legacyValueName())
	requireStartupRun(t, run, s.valueName, foreign)
	requireStartupApproval(t, approved, s.valueName, state)
}

func TestStartupMigrationRejectsOtherConfigAndUser(t *testing.T) {
	for _, otherUser := range []bool{false, true} {
		name := "config"
		if otherUser {
			name = "user"
		}
		t.Run(name, func(t *testing.T) {
			s, run, approved := startupRegistryFixture(t)
			command := s.command + ".other"
			if otherUser {
				command = s.command
				s.expectedUserSID = "S-1-0-0"
			}
			state := []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
			setStartupRun(t, run, s.legacyValueName(), command)
			setStartupApproval(t, approved, s.legacyValueName(), state)
			if err := s.Migrate(); err != nil {
				t.Fatal(err)
			}
			if enabled, err := s.Enabled(); err != nil || enabled {
				t.Fatalf("foreign startup reported owned: %v, %v", enabled, err)
			}
			if err := s.Disable(); otherUser && err == nil {
				t.Fatal("different-user Disable accepted")
			} else if !otherUser && err != nil {
				t.Fatal(err)
			}
			if otherUser {
				if err := s.Enable(); err == nil {
					t.Fatal("different-user Enable accepted")
				}
			}
			requireStartupRun(t, run, s.legacyValueName(), command)
			requireStartupApproval(t, approved, s.legacyValueName(), state)
			requireStartupAbsent(t, run, s.valueName)
			requireStartupAbsent(t, approved, s.valueName)
		})
	}
}

func TestStartupDisableDirectlyRemovesBothOwnedNamespaces(t *testing.T) {
	s, run, approved := startupRegistryFixture(t)
	for _, name := range []string{s.valueName, s.legacyValueName()} {
		setStartupRun(t, run, name, s.command)
		setStartupApproval(t, approved, name, []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8})
	}
	if err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{s.valueName, s.legacyValueName()} {
		requireStartupAbsent(t, run, name)
		requireStartupAbsent(t, approved, name)
	}
}

func TestStartupMigrationApprovalFailureRetainsLegacy(t *testing.T) {
	s, run, approved := startupRegistryFixture(t)
	setStartupRun(t, run, s.legacyValueName(), s.command)
	setStartupRun(t, approved, s.legacyValueName(), "invalid non-binary approval")
	if err := s.Migrate(); err == nil {
		t.Fatal("migration ignored invalid approval state")
	}
	if enabled, err := s.Enabled(); err != nil || !enabled {
		t.Fatalf("failed migration blocked status query: %v, %v", enabled, err)
	}
	requireStartupRun(t, run, s.legacyValueName(), s.command)
	requireStartupRun(t, approved, s.legacyValueName(), "invalid non-binary approval")
	requireStartupAbsent(t, run, s.valueName)
	requireStartupAbsent(t, approved, s.valueName)
	state := []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	setStartupApproval(t, approved, s.legacyValueName(), state)
	if err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	requireStartupRun(t, run, s.valueName, s.command)
	requireStartupApproval(t, approved, s.valueName, state)
	requireStartupAbsent(t, run, s.legacyValueName())
}

func TestStartupCanDisableAfterMigrationFailure(t *testing.T) {
	s, run, approved := startupRegistryFixture(t)
	setStartupRun(t, run, s.legacyValueName(), s.command)
	setStartupRun(t, approved, s.legacyValueName(), "invalid non-binary approval")
	if err := s.Migrate(); err == nil {
		t.Fatal("migration accepted invalid approval")
	}
	app := TrayApp{startup: s}
	if err := app.toggleStartup(); err != nil {
		t.Fatalf("migration failure blocked menu toggle: %v", err)
	}
	requireStartupAbsent(t, run, s.legacyValueName())
	requireStartupAbsent(t, approved, s.legacyValueName())
}
