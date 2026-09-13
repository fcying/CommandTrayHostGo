package config

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/fcying/CommandTrayHostGo/internal/cronexpr"
	"github.com/fcying/CommandTrayHostGo/internal/i18n"
	"github.com/tailscale/hujson"
)

type FileStamp struct {
	ModTime int64
	Size    int64
}

func (s FileStamp) Equal(other FileStamp) bool {
	return s == other
}

const (
	maxConfigSize = 100 << 20
	// MaxConfigEntries bounds config entries and their associated runtime state.
	MaxConfigEntries = 3584
)

const defaultConfigEnglish = `{
  // Generated test configuration. Examples are disabled; enable them from the tray menu.
  // Use / in Windows paths to avoid escaping backslashes.
  "lang": "auto",
  "auto_update": false,
  "start_show_silent": true,
  "enable_cache": true,
  "conform_cache_expire": true,
  "disable_cache_position": false,
  "disable_cache_size": false,
  "disable_cache_enabled": true,
  "disable_cache_show": false,
  "disable_cache_alpha": false,
  "left_click": ["Window Control Test", "Ping Console Test"],
  "enable_groups": true,
  "groups_menu_symbol": "+",
  "groups": [
    {
      "name": "Tests",
      "groups": ["Window Control Test", "Ping Console Test"]
    }
  ],
  "configs": [
    {
      "name": "Window Control Test",
      "path": {{SYSTEM_DIRECTORY}},
      "cmd": "charmap.exe",
      "working_directory": "",
      "addition_env_path": "",
      "use_builtin_console": false,
      "is_gui": true,
      "enabled": false,
      "start_show": true,
      "ignore_all": false,
      "position": [0.15, 0.15],
      "size": [0.45, 0.65],
      "alpha": 235,
      "topmost": false,
      "stop_cmd": "",
      "kill_timeout": 500
    },
    {
      "name": "Ping Console Test",
      "path": {{SYSTEM_DIRECTORY}},
      "cmd": "cmd.exe /d /k ping -t 1.1.1.1",
      "working_directory": "",
      "addition_env_path": "",
      "use_builtin_console": false,
      "is_gui": false,
      "enabled": false,
      "start_show": true,
      "ignore_all": false,
      "position": [0.52, 0.15],
      "size": [0.4, 0.4],
      "alpha": 235,
      "topmost": false,
      "stop_cmd": "",
      "kill_timeout": 500,
      "kill_process_tree": true
    }
  ]
}
`

const defaultConfigChinese = `{
  // 自动生成的测试配置. 示例默认禁用, 可从托盘菜单手动启用.
  // Windows 路径可使用 /, 无需转义反斜杠.
  "lang": "auto",
  "auto_update": false,
  "start_show_silent": true,
  "enable_cache": true,
  "conform_cache_expire": true,
  "disable_cache_position": false,
  "disable_cache_size": false,
  "disable_cache_enabled": true,
  "disable_cache_show": false,
  "disable_cache_alpha": false,
  "left_click": ["窗口控制测试", "Ping 控制台测试"],
  "enable_groups": true,
  "groups_menu_symbol": "+",
  "groups": [
    {
      "name": "测试",
      "groups": ["窗口控制测试", "Ping 控制台测试"]
    }
  ],
  "configs": [
    {
      "name": "窗口控制测试",
      "path": {{SYSTEM_DIRECTORY}},
      "cmd": "charmap.exe",
      "working_directory": "",
      "addition_env_path": "",
      "use_builtin_console": false,
      "is_gui": true,
      "enabled": false,
      "start_show": true,
      "ignore_all": false,
      "position": [0.15, 0.15],
      "size": [0.45, 0.65],
      "alpha": 235,
      "topmost": false,
      "stop_cmd": "",
      "kill_timeout": 500
    },
    {
      "name": "Ping 控制台测试",
      "path": {{SYSTEM_DIRECTORY}},
      "cmd": "cmd.exe /d /k ping -t 1.1.1.1",
      "working_directory": "",
      "addition_env_path": "",
      "use_builtin_console": false,
      "is_gui": false,
      "enabled": false,
      "start_show": true,
      "ignore_all": false,
      "position": [0.52, 0.15],
      "size": [0.4, 0.4],
      "alpha": 235,
      "topmost": false,
      "stop_cmd": "",
      "kill_timeout": 500,
      "kill_process_tree": true
    }
  ]
}
`

var requiredEntryFields = [...]string{
	"name",
	"path",
	"cmd",
	"working_directory",
	"addition_env_path",
	"use_builtin_console",
	"is_gui",
	"enabled",
}

var knownEntryFields = [...]string{
	"name",
	"path",
	"cmd",
	"working_directory",
	"addition_env_path",
	"use_builtin_console",
	"is_gui",
	"enabled",
	"require_admin",
	"start_show",
	"ignore_all",
	"position",
	"size",
	"alpha",
	"topmost",
	"not_host_by_commandtrayhost",
	"not_monitor_by_commandtrayhost",
	"stop_cmd",
	"kill_timeout",
	"kill_process_tree",
	"exclusion_id",
	"hotkey",
	"crontab_config",
	"icon",
}

var globalHotkeyFields = [...]string{
	"disable_all", "enable_all", "hide_all", "show_all", "restart_all", "elevate", "exit",
	"left_click", "right_click", "add_alpha", "minus_alpha", "topmost", "hide_current", "show_all_docked",
}

var entryHotkeyFields = [...]string{"hide_show", "disable_enable", "restart", "elevate"}

var cronFields = [...]string{"enabled", "crontab", "method", "count", "start_show", "log", "log_level", "need_renew"}

func LoadOrCreate(path string, language i18n.Language, systemDirectory func() (string, error)) (Config, error) {
	cfg, err := Load(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	template := defaultConfigEnglish
	if language == i18n.SimplifiedChinese {
		template = defaultConfigChinese
	}
	directory, err := systemDirectory()
	if err != nil {
		return Config{}, err
	}
	data, err := renderDefaultConfig(template, directory)
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Config{}, fmt.Errorf("create config directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Config{}, fmt.Errorf("create %s: %w", path, err)
	}
	return Parse(data)
}

func renderDefaultConfig(template, systemDirectory string) ([]byte, error) {
	systemDirectory = strings.TrimRight(strings.ReplaceAll(systemDirectory, `\`, "/"), "/")
	if len(systemDirectory) < 3 || !((systemDirectory[0] >= 'A' && systemDirectory[0] <= 'Z') || (systemDirectory[0] >= 'a' && systemDirectory[0] <= 'z')) || systemDirectory[1:3] != ":/" {
		return nil, errors.New("default system directory must be an absolute Windows path")
	}
	encodedDirectory, err := json.Marshal(systemDirectory)
	if err != nil {
		return nil, fmt.Errorf("encode default system directory: %w", err)
	}
	return []byte(strings.ReplaceAll(template, "{{SYSTEM_DIRECTORY}}", string(encodedDirectory))), nil
}

func LoadOrCreateSnapshot(path string, language i18n.Language, systemDirectory func() (string, error)) (Config, FileStamp, error) {
	cfg, err := LoadOrCreate(path, language, systemDirectory)
	if err != nil {
		return Config{}, FileStamp{}, err
	}
	for range 3 {
		before, err := StatFile(path)
		if err != nil {
			return Config{}, FileStamp{}, err
		}
		cfg, err = Load(path)
		if err != nil {
			return Config{}, before, err
		}
		after, err := StatFile(path)
		if err != nil {
			return Config{}, before, err
		}
		if before.Equal(after) {
			return cfg, after, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return Config{}, FileStamp{}, fmt.Errorf("%s changed while it was being read", path)
}

func LoadSnapshot(path string) (Config, FileStamp, error) {
	for range 3 {
		before, err := StatFile(path)
		if err != nil {
			return Config{}, FileStamp{}, err
		}
		cfg, err := Load(path)
		if err != nil {
			return Config{}, before, err
		}
		after, err := StatFile(path)
		if err != nil {
			return Config{}, before, err
		}
		if before.Equal(after) {
			return cfg, after, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return Config{}, FileStamp{}, fmt.Errorf("%s changed while it was being read", path)
}

func StatFile(path string) (FileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileStamp{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return FileStamp{ModTime: info.ModTime().UnixNano(), Size: info.Size()}, nil
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxConfigSize {
		return Config{}, fmt.Errorf("%s exceeds the %d MiB limit", path, maxConfigSize>>20)
	}
	return Parse(raw)
}

func Parse(raw []byte) (Config, error) {
	// JSONC standardization can mutate the input buffer.
	sourceDigest := sha256.Sum256(raw)
	utf8Data, err := decodeText(raw)
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	standard, err := hujson.Standardize(utf8Data)
	if err != nil {
		return Config{}, fmt.Errorf("parse JSON with comments: %w", err)
	}

	if err := validateRequiredFields(standard); err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(standard, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config object: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	cfg.SourceDigest = sourceDigest
	return cfg, nil
}

func validateRequiredFields(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode config fields: %w", err)
	}
	for key, raw := range root {
		for _, field := range [...]string{
			"lang",
			"require_admin",
			"start_show_silent",
			"enable_cache",
			"conform_cache_expire",
			"disable_cache_position",
			"disable_cache_size",
			"disable_cache_enabled",
			"disable_cache_show",
			"disable_cache_alpha",
			"auto_hot_reloading_config",
			"enable_hotkey",
			"repeat_mod_hotkey",
			"show_hotkey_in_menu",
			"global_hotkey_alpha_step",
			"hotkey",
			"left_click",
			"enable_groups",
			"groups",
			"groups_menu_symbol",
			"cmd_menu_max_length",
			"icon",
			"icon_size",
			"auto_update",
			"skip_prerelease",
			"configs",
		} {
			if strings.EqualFold(key, field) && string(raw) == "null" {
				return fmt.Errorf("config field %s must not be null", key)
			}
		}
	}
	if rawHotkey, ok := findJSONField(root, "hotkey"); ok {
		if err := validateHotkeyObject(rawHotkey, "hotkey", globalHotkeyFields[:]); err != nil {
			return err
		}
	}
	rawConfigs, ok := findJSONField(root, "configs")
	if !ok {
		return errors.New("config is missing required field configs")
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(rawConfigs, &entries); err != nil {
		return errors.New("config field configs must be an array of objects")
	}
	for _, field := range []string{"enable_groups", "groups", "groups_menu_symbol"} {
		if _, _, err := findUniqueJSONField(root, field, "config"); err != nil {
			return err
		}
	}
	if rawGroups, ok, _ := findUniqueJSONField(root, "groups", "config"); ok {
		if err := validateGroupsJSON(rawGroups); err != nil {
			return err
		}
	}
	for i, entry := range entries {
		for _, field := range requiredEntryFields {
			if _, ok := findJSONField(entry, field); !ok {
				return fmt.Errorf("configs[%d] is missing required field %s", i, field)
			}
		}
		for key, raw := range entry {
			for _, field := range knownEntryFields {
				if strings.EqualFold(key, field) && string(raw) == "null" {
					return fmt.Errorf("configs[%d].%s must not be null", i, key)
				}
			}
		}
		if rawHotkey, ok := findJSONField(entry, "hotkey"); ok {
			if err := validateHotkeyObject(rawHotkey, fmt.Sprintf("configs[%d].hotkey", i), entryHotkeyFields[:]); err != nil {
				return err
			}
		}
		if rawCron, ok := findJSONField(entry, "crontab_config"); ok {
			if err := validateCronObject(rawCron, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCronObject(raw json.RawMessage, index int) error {
	path := fmt.Sprintf("configs[%d].crontab_config", index)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%s must be an object", path)
	}
	for key, value := range object {
		for _, field := range cronFields {
			if strings.EqualFold(key, field) && string(value) == "null" {
				return fmt.Errorf("%s.%s must not be null", path, key)
			}
		}
		if strings.EqualFold(key, "need_renew") {
			return fmt.Errorf("%s.need_renew is reserved for internal use", path)
		}
	}
	for _, field := range [...]string{"crontab", "method", "count"} {
		if _, ok := findJSONField(object, field); !ok {
			return fmt.Errorf("%s is missing required field %s", path, field)
		}
	}
	return nil
}

func validateHotkeyObject(raw json.RawMessage, path string, fields []string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%s must be an object", path)
	}
	for key, value := range object {
		for _, field := range fields {
			if strings.EqualFold(key, field) && string(value) == "null" {
				return fmt.Errorf("%s.%s must not be null", path, key)
			}
		}
	}
	return nil
}

func findJSONField(object map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	for key, raw := range object {
		if strings.EqualFold(key, name) {
			return raw, true
		}
	}
	return nil, false
}

func (c Config) validate() error {
	if len(c.Configs) == 0 {
		return errors.New("config must contain at least one entry in configs")
	}
	if len(c.Configs) > MaxConfigEntries {
		return fmt.Errorf("config contains %d entries; maximum is %d", len(c.Configs), MaxConfigEntries)
	}
	if c.GlobalHotkeyAlphaStep != nil && (*c.GlobalHotkeyAlphaStep < 1 || *c.GlobalHotkeyAlphaStep > 255) {
		return errors.New("global_hotkey_alpha_step must be between 1 and 255")
	}
	if c.CmdMenuMaxLength < 0 || c.CmdMenuMaxLength > math.MaxInt32 {
		return fmt.Errorf("cmd_menu_max_length must be between 0 and %d", math.MaxInt32)
	}
	if c.IconSize != nil && *c.IconSize != 16 && *c.IconSize != 32 && *c.IconSize != 256 {
		return errors.New("icon_size must be 16, 32, or 256")
	}
	names := make(map[string]int, len(c.Configs))
	for i, entry := range c.Configs {
		if entry.Name == "" {
			return fmt.Errorf("configs[%d].name must not be empty", i)
		}
		if strings.IndexByte(entry.Name, 0) >= 0 {
			return fmt.Errorf("configs[%d].name must not contain NUL", i)
		}
		if previous, exists := names[entry.Name]; exists {
			return fmt.Errorf("configs[%d].name %q duplicates configs[%d].name", i, entry.Name, previous)
		}
		names[entry.Name] = i
		if entry.Command == "" {
			return fmt.Errorf("configs[%d].cmd must not be empty", i)
		}
		if strings.IndexByte(entry.StopCommand, 0) >= 0 {
			return fmt.Errorf("configs[%d].stop_cmd must not contain NUL", i)
		}
		if entry.KillTimeout != nil && (*entry.KillTimeout < 0 || *entry.KillTimeout >= int64(^uint32(0))) {
			return fmt.Errorf("configs[%d].kill_timeout must be between 0 and %d", i, uint64(^uint32(0)-1))
		}
		if entry.ExclusionID != nil && (*entry.ExclusionID <= 0 || *entry.ExclusionID > math.MaxInt32) {
			return fmt.Errorf("configs[%d].exclusion_id must be between 1 and %d", i, math.MaxInt32)
		}
		if entry.ExclusionID != nil && (entry.NotHosted || entry.NotMonitored) {
			return fmt.Errorf("configs[%d].exclusion_id requires a managed process", i)
		}
		if entry.Position != nil && !validPair(*entry.Position) {
			return fmt.Errorf("configs[%d].position values must be finite and non-negative", i)
		}
		if entry.Size != nil && !validPair(*entry.Size) {
			return fmt.Errorf("configs[%d].size values must be finite and non-negative", i)
		}
		if entry.Alpha != nil && (*entry.Alpha < 0 || *entry.Alpha > 255) {
			return fmt.Errorf("configs[%d].alpha must be between 0 and 255", i)
		}
		if entry.Cron != nil {
			if err := validateCronConfig(i, entry); err != nil {
				return err
			}
		}
	}
	for i, name := range c.LeftClick {
		if _, exists := names[name]; !exists {
			return fmt.Errorf("left_click[%d] must reference an existing configs name: %q", i, name)
		}
	}
	if c.Groups != nil {
		if err := validateGroups(*c.Groups, names); err != nil {
			return err
		}
	}
	return nil
}

func validateCronConfig(index int, entry EntryConfig) error {
	cron := entry.Cron
	path := fmt.Sprintf("configs[%d].crontab_config", index)
	if cron.Expression == "" {
		return fmt.Errorf("%s.crontab must not be empty", path)
	}
	if len(cron.Expression) > 255 {
		return fmt.Errorf("%s.crontab must not exceed 255 bytes", path)
	}
	switch cron.Method {
	case CronStart, CronRestart, CronStop, CronStartCountStop, CronRestartCountStop:
	default:
		return fmt.Errorf("%s.method is not supported", path)
	}
	if cron.Count < 0 || cron.Count > math.MaxInt32 {
		return fmt.Errorf("%s.count must be between 0 and %d", path, math.MaxInt32)
	}
	if cron.LogLevel < 0 || cron.LogLevel > 3 {
		return fmt.Errorf("%s.log_level must be between 0 and 3", path)
	}
	if entry.NotHosted || entry.NotMonitored {
		switch cron.Method {
		case CronRestart, CronStop, CronStartCountStop, CronRestartCountStop:
			return fmt.Errorf("%s.method %s requires a managed process", path, cron.Method)
		}
	}
	expression, err := cronexpr.Parse(cron.Expression)
	if err != nil {
		return fmt.Errorf("%s.crontab: %w", path, err)
	}
	if _, err := expression.Next(time.Now()); err != nil {
		return fmt.Errorf("%s.crontab: %w", path, err)
	}
	cron.Parsed = expression
	return nil
}

func validPair(pair Pair) bool {
	for _, value := range pair {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > math.MaxInt32 {
			return false
		}
	}
	return true
}

func decodeText(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("config is empty")
	}

	switch {
	case hasPrefix(raw, 0xef, 0xbb, 0xbf):
		return raw[3:], nil
	case hasPrefix(raw, 0xff, 0xfe, 0x00, 0x00):
		return decodeUTF32(raw[4:], binary.LittleEndian)
	case hasPrefix(raw, 0x00, 0x00, 0xfe, 0xff):
		return decodeUTF32(raw[4:], binary.BigEndian)
	case hasPrefix(raw, 0xff, 0xfe):
		return decodeUTF16(raw[2:], binary.LittleEndian)
	case hasPrefix(raw, 0xfe, 0xff):
		return decodeUTF16(raw[2:], binary.BigEndian)
	}

	if len(raw)%4 == 0 {
		if raw[0] == 0 && raw[1] == 0 && raw[2] == 0 {
			return decodeUTF32(raw, binary.BigEndian)
		}
		if raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
			return decodeUTF32(raw, binary.LittleEndian)
		}
	}
	if len(raw)%2 == 0 {
		if raw[0] == 0 {
			return decodeUTF16(raw, binary.BigEndian)
		}
		if raw[1] == 0 {
			return decodeUTF16(raw, binary.LittleEndian)
		}
	}
	if utf8.Valid(raw) {
		return raw, nil
	}
	return nil, errors.New("unsupported or invalid text encoding")
}

func decodeUTF16(raw []byte, order binary.ByteOrder) ([]byte, error) {
	if len(raw)%2 != 0 {
		return nil, errors.New("invalid UTF-16 byte length")
	}
	words := make([]uint16, len(raw)/2)
	for i := range words {
		words[i] = order.Uint16(raw[i*2:])
	}
	return []byte(string(utf16.Decode(words))), nil
}

func decodeUTF32(raw []byte, order binary.ByteOrder) ([]byte, error) {
	if len(raw)%4 != 0 {
		return nil, errors.New("invalid UTF-32 byte length")
	}
	runes := make([]rune, 0, len(raw)/4)
	for i := 0; i < len(raw); i += 4 {
		r := rune(order.Uint32(raw[i:]))
		if !utf8.ValidRune(r) {
			return nil, fmt.Errorf("invalid UTF-32 code point %#x", r)
		}
		runes = append(runes, r)
	}
	return []byte(string(runes)), nil
}

func hasPrefix(data []byte, prefix ...byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := range prefix {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}
