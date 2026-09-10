package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type LaunchOptions struct {
	ForceRestart   bool
	StartupUserSID string
	ConfigPath     string
	CachePath      string
	ConfigArgument string
}

func ParseLaunchOptions(args []string, baseDir string) (LaunchOptions, error) {
	options := LaunchOptions{
		ConfigPath: filepath.Join(baseDir, "config.json"),
		CachePath:  filepath.Join(baseDir, "command_tray_host.cache"),
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case i == 0 && arg == "force-restart":
			options.ForceRestart = true
		case strings.HasPrefix(arg, "startup-user="):
			options.StartupUserSID = strings.TrimPrefix(arg, "startup-user=")
		case arg == "-c":
			if options.ConfigArgument != "" {
				return LaunchOptions{}, errors.New("-c may only be specified once")
			}
			if i+1 >= len(args) || args[i+1] == "" || args[i+1] == "-c" {
				return LaunchOptions{}, errors.New("-c requires a configuration file path")
			}
			i++
			configPath := args[i]
			if isIncompleteWindowsPath(configPath) {
				return LaunchOptions{}, fmt.Errorf("-c path %q must be fully qualified or relative to the executable directory", configPath)
			}
			if !filepath.IsAbs(configPath) {
				configPath = filepath.Join(baseDir, configPath)
			}
			configPath = filepath.Clean(configPath)
			options.ConfigPath = configPath
			options.CachePath = configPath + ".cache"
			options.ConfigArgument = configPath
		default:
			return LaunchOptions{}, errors.New("unknown argument: " + arg)
		}
	}
	return options, nil
}

const consoleWindowQueryPrefix = "--internal-console-window="

func ConsoleWindowQueryArgument(pid uint32) string {
	return consoleWindowQueryPrefix + strconv.FormatUint(uint64(pid), 10)
}

func ParseConsoleWindowQueryArgument(args []string) (uint32, bool) {
	if len(args) != 1 || !strings.HasPrefix(args[0], consoleWindowQueryPrefix) {
		return 0, false
	}
	pid, err := strconv.ParseUint(strings.TrimPrefix(args[0], consoleWindowQueryPrefix), 10, 32)
	if err != nil || pid == 0 {
		return 0, false
	}
	return uint32(pid), true
}

func isIncompleteWindowsPath(path string) bool {
	if path == "" {
		return false
	}
	if path[0] == '\\' || path[0] == '/' {
		return len(path) < 2 || (path[1] != '\\' && path[1] != '/')
	}
	return len(path) >= 2 && path[1] == ':' && (len(path) == 2 || (path[2] != '\\' && path[2] != '/'))
}
