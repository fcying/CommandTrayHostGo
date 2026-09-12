//go:build windows && amd64

package win32

import (
	"errors"
	"fmt"
	"strings"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const startupRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const startupApprovedRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
const maxRunCommandLength = 260

type startupRegistration struct {
	command         string
	valueName       string
	expectedUserSID string
}

func newStartupRegistration(executablePath, expectedUserSID, configPath string) startupRegistration {
	if expectedUserSID == "" {
		expectedUserSID = currentUserSID()
	}
	return startupRegistration{
		command:         startupCommand(executablePath, configPath),
		valueName:       domain.StartupValueName(executablePath),
		expectedUserSID: expectedUserSID,
	}
}

func StartupUserSID() string {
	return currentUserSID()
}

func (s startupRegistration) Available() bool {
	return s.expectedUserSID != "" && s.expectedUserSID == currentUserSID()
}

func currentUserSID() string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return ""
	}
	return user.User.Sid.String()
}

func (s startupRegistration) legacyValueName() string {
	return "CommandTrayHost_" + strings.TrimPrefix(s.valueName, "CommandTrayHostGo_")
}

// Migrate updates an owned legacy registration independently of status queries.
func (s startupRegistration) Migrate() error {
	if !s.Available() {
		return nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open current-user startup registry for migration: %w", err)
	}
	defer key.Close()
	return s.migrate(key)
}

func (s startupRegistration) Enabled() (bool, error) {
	if !s.Available() {
		return false, nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open current-user startup registry: %w", err)
	}
	defer key.Close()
	for _, name := range []string{s.valueName, s.legacyValueName()} {
		_, owned, err := s.readValue(key, name)
		if err != nil || owned {
			return owned, err
		}
	}
	return false, nil
}

func (s startupRegistration) Enable() error {
	if !s.Available() {
		return errors.New("Start on Boot belongs to the original Windows user and cannot be changed from an instance elevated with different credentials")
	}
	if len([]rune(s.command)) > maxRunCommandLength {
		return fmt.Errorf("startup command exceeds the Windows Run key limit of %d characters", maxRunCommandLength)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open current-user startup registry for writing: %w", err)
	}
	defer key.Close()
	exists, owned, err := s.readValue(key, s.valueName)
	if err != nil {
		return err
	}
	if exists && !owned {
		return fmt.Errorf("startup registry value %q belongs to another command", s.valueName)
	}
	if err := s.migrate(key); err != nil {
		return err
	}
	if err := key.SetStringValue(s.valueName, s.command); err != nil {
		return fmt.Errorf("write startup registry value: %w", err)
	}
	return nil
}

func (s startupRegistration) Disable() error {
	if !s.Available() {
		return errors.New("Start on Boot belongs to the original Windows user and cannot be changed from an instance elevated with different credentials")
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open current-user startup registry for writing: %w", err)
	}
	defer key.Close()
	var errs []error
	for _, name := range []string{s.valueName, s.legacyValueName()} {
		_, owned, err := s.readValue(key, name)
		if err == nil && owned {
			err = removeOwnedStartupValue(key, name)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s startupRegistration) readValue(key registry.Key, name string) (exists, owned bool, err error) {
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return false, false, nil
	}
	if errors.Is(err, registry.ErrUnexpectedType) {
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read startup registry value %q: %w", name, err)
	}
	return true, startupValueMatches(value, s.command), nil
}

func (s startupRegistration) migrate(key registry.Key) error {
	legacy := s.legacyValueName()
	_, owned, err := s.readValue(key, legacy)
	if err != nil || !owned {
		return err
	}
	exists, owned, err := s.readValue(key, s.valueName)
	if err != nil || (exists && !owned) {
		// Keep a conflicting legacy registration usable and removable via Disable.
		return err
	}
	writable, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open startup registry for migration: %w", err)
	}
	defer writable.Close()
	if !exists {
		// Approval precedes Run creation: a failed copy must not enable a disabled entry.
		if err := copyStartupApproval(legacy, s.valueName); err != nil {
			return err
		}
		if err := writable.SetStringValue(s.valueName, s.command); err != nil {
			return fmt.Errorf("write migrated startup registry value: %w", err)
		}
	}
	// An existing owned destination (including its approval state) wins conflicts.
	return removeOwnedStartupValue(writable, legacy)
}

func copyStartupApproval(oldName, newName string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open startup approval registry: %w", err)
	}
	defer key.Close()
	state, _, err := key.GetBinaryValue(oldName)
	if errors.Is(err, registry.ErrNotExist) {
		// Absence is also state; do not inherit an orphaned destination approval.
		err = key.DeleteValue(newName)
	} else if err == nil {
		err = key.SetBinaryValue(newName, state)
	}
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("migrate startup approval %q: %w", oldName, err)
	}
	return nil
}

func removeOwnedStartupValue(key registry.Key, name string) error {
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read startup registry value %q before removal: %w", name, err)
	}
	approved, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedRegistryPath, registry.SET_VALUE)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("open startup approval %q for removal: %w", name, err)
	}
	if err == nil {
		defer approved.Close()
	}
	// Remove Run first: failure must not turn a disabled registration into an enabled one.
	if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete startup registry value %q: %w", name, err)
	}
	if approved != 0 {
		if err := approved.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			// Retain ownership for a retry rather than silently abandoning approval state.
			restoreErr := key.SetStringValue(name, value)
			if restoreErr != nil {
				restoreErr = fmt.Errorf("restore startup registry value %q: %w", name, restoreErr)
			}
			return errors.Join(fmt.Errorf("delete startup approval %q: %w", name, err), restoreErr)
		}
	}
	return nil
}

func startupCommand(executablePath, configPath string) string {
	command := `"` + executablePath + `"`
	if configPath != "" {
		command += " -c " + windows.EscapeArg(configPath)
	}
	return command
}

func startupValueMatches(value, command string) bool {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, command) {
		return true
	}
	if strings.Contains(command, " -c ") || len(command) < 2 || command[0] != '"' || command[len(command)-1] != '"' {
		return false
	}
	return strings.EqualFold(value, command[1:len(command)-1])
}
