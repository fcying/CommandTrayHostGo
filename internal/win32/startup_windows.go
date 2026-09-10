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
	return s.expectedUserSID == "" || s.expectedUserSID == currentUserSID()
}

func currentUserSID() string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return ""
	}
	return user.User.Sid.String()
}

func (s startupRegistration) Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open current-user startup registry: %w", err)
	}
	defer key.Close()
	value, _, err := key.GetStringValue(s.valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read startup registry value %q: %w", s.valueName, err)
	}
	return startupValueMatches(value, s.command), nil
}

func (s startupRegistration) Enable() error {
	if !s.Available() {
		return errors.New("Start on Boot belongs to the original Windows user and cannot be changed from an instance elevated with different credentials")
	}
	if len([]rune(s.command)) > maxRunCommandLength {
		return fmt.Errorf("startup command exceeds the Windows Run key limit of %d characters", maxRunCommandLength)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupRegistryPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open current-user startup registry for writing: %w", err)
	}
	defer key.Close()
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
	return deleteStartupValueIfOwned(key, s.valueName, s.command)
}

func deleteStartupValueIfOwned(key registry.Key, name, command string) error {
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read startup registry value %q: %w", name, err)
	}
	if !startupValueMatches(value, command) {
		return nil
	}
	if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete startup registry value %q: %w", name, err)
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
