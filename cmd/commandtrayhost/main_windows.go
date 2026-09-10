//go:build windows && amd64

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"github.com/fcying/CommandTrayHostGo/internal/i18n"
	"github.com/fcying/CommandTrayHostGo/internal/statecache"
	"github.com/fcying/CommandTrayHostGo/internal/win32"
)

const applicationName = "CommandTrayHost"

func main() {
	if pid, ok := domain.ParseConsoleWindowQueryArgument(os.Args[1:]); ok {
		var result [8]byte
		binary.LittleEndian.PutUint64(result[:], uint64(win32.QueryConsoleWindow(pid)))
		_, _ = os.Stdout.Write(result[:])
		return
	}

	runtime.LockOSThread()

	executablePath, baseDir, err := executableInfo()
	if err != nil {
		win32.ShowError(applicationName, err.Error())
		return
	}
	if err := os.Chdir(baseDir); err != nil {
		win32.ShowError(applicationName, fmt.Sprintf("change working directory: %v", err))
		return
	}
	if err := os.Setenv("CWD", baseDir); err != nil {
		win32.ShowError(applicationName, fmt.Sprintf("set CWD: %v", err))
		return
	}
	options, err := domain.ParseLaunchOptions(os.Args[1:], baseDir)
	if err != nil {
		win32.ShowError(applicationName, err.Error())
		return
	}

	instance, err := win32.AcquireInstance(executablePath, options.ForceRestart)
	if err != nil {
		win32.ShowError(applicationName, err.Error())
		return
	}
	defer instance.Close()

	systemLocale := i18n.DetectSystemLocale()
	defaultLanguage := i18n.Resolve("", systemLocale)
	cfg, configStamp, err := config.LoadOrCreateSnapshot(options.ConfigPath, defaultLanguage, win32.SystemDirectory)
	if err != nil {
		win32.ShowError(applicationName, err.Error())
		return
	}
	language := i18n.Resolve(cfg.Lang, systemLocale)
	cache, cacheErr := statecache.OpenStartupSnapshot(options.CachePath, &cfg, configStamp)
	if errors.Is(cacheErr, statecache.ErrCacheExpired) {
		prompt := strings.ReplaceAll(i18n.Text(language).CacheExpiredPrompt, "config.json", filepath.Base(options.ConfigPath))
		prompt = strings.ReplaceAll(prompt, "command_tray_host.cache", filepath.Base(options.CachePath))
		if win32.ShowConfirm(
			applicationName,
			prompt,
		) {
			if err := cache.Save(); err != nil {
				win32.ShowError(applicationName, err.Error())
			}
		} else {
			cache, cacheErr = statecache.OpenSnapshot(options.CachePath, &cfg, configStamp, true)
		}
	}
	if cacheErr != nil && !errors.Is(cacheErr, statecache.ErrCacheExpired) {
		win32.ShowError(applicationName, cacheErr.Error())
	}
	if cfg.RequireAdmin && !win32.IsElevated() {
		if err := win32.RelaunchElevated(executablePath, baseDir, win32.StartupUserSID(), options.ConfigArgument); err != nil {
			win32.ShowError(applicationName, err.Error())
		}
		return
	}

	app := win32.NewTrayApp(applicationName, cfg.DisplayName(), executablePath, options.StartupUserSID, baseDir, options.ConfigPath, options.CachePath, options.ConfigArgument, cfg, configStamp, cache, language)
	if err := app.Run(); err != nil {
		win32.ShowError(applicationName, err.Error())
	}
}

func executableInfo() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("resolve executable path: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return "", "", fmt.Errorf("resolve absolute executable path: %w", err)
	}
	exe, err = finalExecutablePath(exe)
	if err != nil {
		return "", "", err
	}
	return exe, filepath.Dir(exe), nil
}
