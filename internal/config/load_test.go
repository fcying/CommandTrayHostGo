package config

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/fcying/CommandTrayHostGo/internal/i18n"
)

const validConfig = `{
  // Existing files may contain comments.
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
  }],
}`

func TestParseJSONWithCommentsAndTrailingCommas(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Configs[0].Name; got != "demo" {
		t.Fatalf("name = %q, want demo", got)
	}
}

func TestParseSourceDigestBindsOriginalBytes(t *testing.T) {
	original := []byte(validConfig)
	changed := []byte(strings.Replace(validConfig, "Existing files", "Modified files", 1))
	if len(original) != len(changed) {
		t.Fatal("digest regression requires equal-length inputs")
	}
	wantOriginal, wantChanged := sha256.Sum256(original), sha256.Sum256(changed)
	first, err := Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.Configs[0].Name != second.Configs[0].Name {
		t.Fatal("comment change altered the parsed entry name")
	}
	if first.SourceDigest != wantOriginal || second.SourceDigest != wantChanged {
		t.Fatal("source digest does not match original input bytes")
	}
	if first.SourceDigest == second.SourceDigest {
		t.Fatal("distinct equal-length source configs have the same digest")
	}
}

func TestParseUTF16AndUTF32(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "utf16le", data: encodeUTF16(validConfig, binary.LittleEndian, []byte{0xff, 0xfe})},
		{name: "utf16be", data: encodeUTF16(validConfig, binary.BigEndian, []byte{0xfe, 0xff})},
		{name: "utf32le", data: encodeUTF32(validConfig, binary.LittleEndian, []byte{0xff, 0xfe, 0x00, 0x00})},
		{name: "utf32be", data: encodeUTF32(validConfig, binary.BigEndian, []byte{0x00, 0x00, 0xfe, 0xff})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Configs) != 1 {
				t.Fatalf("configs = %d, want 1", len(cfg.Configs))
			}
			if cfg.SourceDigest != sha256.Sum256(tc.data) {
				t.Fatal("source digest does not match the original encoded bytes")
			}
		})
	}
}

func TestParseUTF16AndUTF32WithoutBOM(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "utf16le", data: encodeUTF16(validConfig, binary.LittleEndian, nil)},
		{name: "utf16be", data: encodeUTF16(validConfig, binary.BigEndian, nil)},
		{name: "utf32le", data: encodeUTF32(validConfig, binary.LittleEndian, nil)},
		{name: "utf32be", data: encodeUTF32(validConfig, binary.BigEndian, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseRejectsEmptyConfigs(t *testing.T) {
	if _, err := Parse([]byte(`{"configs": []}`)); err == nil {
		t.Fatal("Parse succeeded, want error")
	}
}

func TestParseRejectsNULInEntryName(t *testing.T) {
	data := strings.Replace(validConfig, `"demo"`, `"worker\u0000one"`, 1)
	if _, err := Parse([]byte(data)); err == nil || !strings.Contains(err.Error(), "name must not contain NUL") {
		t.Fatalf("Parse error = %v, want NUL name validation error", err)
	}
}

func TestParseRejectsMissingHistoricalField(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true
  }]
}`)
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse succeeded, want missing enabled error")
	}
}

func TestEntryDefaults(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	entry := cfg.Configs[0]
	if entry.EffectiveStartShow() {
		t.Fatal("EffectiveStartShow() = true, want false")
	}
	if got := entry.EffectiveKillTimeout(); got != 200 {
		t.Fatalf("EffectiveKillTimeout() = %d, want 200", got)
	}
	if !cfg.EffectiveStartShowSilent() {
		t.Fatal("EffectiveStartShowSilent() = false, want true")
	}
	if cfg.CacheEnabled() {
		t.Fatal("CacheEnabled() = true without cache options")
	}
	if cfg.AutoHotReloading {
		t.Fatal("AutoHotReloading = true, want false")
	}
	if cfg.CmdMenuMaxLength != 0 {
		t.Fatalf("CmdMenuMaxLength = %d, want 0", cfg.CmdMenuMaxLength)
	}
	if got := cfg.EffectiveIconSize(); got != 256 {
		t.Fatalf("EffectiveIconSize() = %d, want 256", got)
	}
	if !cfg.AutoUpdateEnabled() || !cfg.SkipPrereleases() {
		t.Fatalf("update defaults = auto:%v skip:%v", cfg.AutoUpdateEnabled(), cfg.SkipPrereleases())
	}
}

func TestParseRejectsNULInStopCommand(t *testing.T) {
	data := strings.Replace(validConfig, `"enabled": true,`, `"enabled": true,
    "stop_cmd": "echo\u0000bad",`, 1)
	if _, err := Parse([]byte(data)); err == nil || !strings.Contains(err.Error(), "stop_cmd must not contain NUL") {
		t.Fatalf("Parse error = %v, want NUL stop_cmd validation error", err)
	}
}

func TestUpdateOptions(t *testing.T) {
	cfg, err := parseWithRootFields(`"auto_update": false, "skip_prerelease": false`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoUpdateEnabled() || cfg.SkipPrereleases() {
		t.Fatalf("update options = auto:%v skip:%v", cfg.AutoUpdateEnabled(), cfg.SkipPrereleases())
	}
	for _, fields := range []string{
		`"auto_update": null`,
		`"skip_prerelease": null`,
	} {
		if _, err := parseWithRootFields(fields); err == nil {
			t.Fatalf("Parse succeeded for %s", fields)
		}
	}
}

func TestIconConfig(t *testing.T) {
	for _, size := range []int{16, 32, 256} {
		cfg, err := parseWithRootFields(fmt.Sprintf(`"icon": "tray.ico", "icon_size": %d`, size))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Icon != "tray.ico" || cfg.EffectiveIconSize() != int32(size) {
			t.Fatalf("icon config = %q/%d", cfg.Icon, cfg.EffectiveIconSize())
		}
	}
	cfg, err := Parse([]byte(strings.Replace(validConfig, `"enabled": true,`, `"enabled": true, "icon": "entry.ico",`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Configs[0].Icon != "entry.ico" {
		t.Fatalf("entry icon = %q", cfg.Configs[0].Icon)
	}
}

func TestIconConfigRejectsInvalidValues(t *testing.T) {
	for _, fields := range []string{
		`"icon": null`,
		`"icon_size": null`,
		`"icon_size": 0`,
		`"icon_size": 24`,
	} {
		if _, err := parseWithRootFields(fields); err == nil {
			t.Fatalf("Parse succeeded for %s, want error", fields)
		}
	}
	data := strings.Replace(validConfig, `"enabled": true,`, `"enabled": true, "icon": null,`, 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("Parse succeeded for null entry icon, want error")
	}
}

func TestCmdMenuMaxLength(t *testing.T) {
	cfg, err := parseWithRootFields(`"cmd_menu_max_length": 3`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MenuText("abcdef"); got != "abc..." {
		t.Fatalf("MenuText() = %q, want %q", got, "abc...")
	}
	if got := cfg.MenuText("你好世界"); got != "你好世..." {
		t.Fatalf("MenuText() = %q, want Unicode-safe truncation", got)
	}
	if got := cfg.MenuText("abc"); got != "abc" {
		t.Fatalf("MenuText() = %q, want unchanged text", got)
	}
}

func TestCmdMenuMaxLengthRejectsInvalidValues(t *testing.T) {
	for _, fields := range []string{
		`"cmd_menu_max_length": null`,
		`"cmd_menu_max_length": -1`,
		`"cmd_menu_max_length": 2147483648`,
		`"cmd_menu_max_length": 1.5`,
	} {
		if _, err := parseWithRootFields(fields); err == nil {
			t.Fatalf("Parse succeeded for %s, want error", fields)
		}
	}
}

func TestAutoHotReloadingConfig(t *testing.T) {
	cfg, err := Parse([]byte(`{
  "auto_hot_reloading_config": true,
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoHotReloading {
		t.Fatal("AutoHotReloading = false, want true")
	}
}

func TestConformCacheExpireOnlyControlsHotReload(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HotReloadEnabled() {
		t.Fatal("HotReloadEnabled() = false by default")
	}
	disabled := false
	cfg.ConformCacheExpire = &disabled
	if cfg.HotReloadEnabled() {
		t.Fatal("HotReloadEnabled() = true when conform_cache_expire is false")
	}
}

func TestParseGroups(t *testing.T) {
	cfg, err := Parse([]byte(`{
  "enable_groups": true,
  "groups_menu_symbol": "++",
  "groups": ["two", {"name": "Tools", "groups": ["one", "two"]}, "two"],
  "configs": [{
    "name": "one", "path": ".", "cmd": "one.exe", "working_directory": "",
    "addition_env_path": "", "use_builtin_console": false, "is_gui": true, "enabled": true
  }, {
    "name": "two", "path": ".", "cmd": "two.exe", "working_directory": "",
    "addition_env_path": "", "use_builtin_console": false, "is_gui": true, "enabled": false
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GroupsEnabled() || cfg.EffectiveGroupsMenuSymbol() != "++" || cfg.Groups == nil {
		t.Fatalf("unexpected groups config: %+v", cfg)
	}
	items := *cfg.Groups
	if len(items) != 3 || items[0].EntryName == nil || *items[0].EntryName != "two" ||
		items[1].Group == nil || items[1].Group.Name != "Tools" || len(items[1].Group.Items) != 2 ||
		items[2].EntryName == nil || *items[2].EntryName != "two" {
		t.Fatalf("unexpected groups tree: %+v", items)
	}
	children := items[1].Group.Items
	if children[0].EntryName == nil || *children[0].EntryName != "one" || children[1].EntryName == nil || *children[1].EntryName != "two" {
		t.Fatalf("unexpected nested entry references: %+v", children)
	}
	cfg.Configs[0], cfg.Configs[1] = cfg.Configs[1], cfg.Configs[0]
	if err := cfg.validate(); err != nil {
		t.Fatalf("reordered configs rejected: %v", err)
	}
}

func TestGroupsDefaultsAndEmptyTree(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GroupsEnabled() || cfg.EffectiveGroupsMenuSymbol() != "+" {
		t.Fatalf("unexpected groups defaults: %+v", cfg)
	}

	cfg, err = parseWithRootFields(`"enable_groups": true, "groups": [], "groups_menu_symbol": ""`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GroupsEnabled() || cfg.Groups == nil || len(*cfg.Groups) != 0 || cfg.EffectiveGroupsMenuSymbol() != "" {
		t.Fatalf("unexpected empty groups config: %+v", cfg)
	}

	cfg, err = parseWithRootFields(`"enable_groups": true`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GroupsEnabled() {
		t.Fatal("GroupsEnabled() = true without groups")
	}
}

func TestParseRejectsInvalidGroups(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
	}{
		{name: "null root", fields: `"enable_groups": true, "groups": null`},
		{name: "legacy numeric index", fields: `"enable_groups": true, "groups": [0]`},
		{name: "unknown entry", fields: `"enable_groups": true, "groups": ["missing"]`},
		{name: "case mismatch", fields: `"enable_groups": true, "groups": [{"name": "x", "groups": ["Demo"]}]`},
		{name: "missing name", fields: `"enable_groups": true, "groups": [{"groups": []}]`},
		{name: "null name", fields: `"enable_groups": true, "groups": [{"name": null}]`},
		{name: "null children", fields: `"enable_groups": true, "groups": [{"name": "x", "groups": null}]`},
		{name: "non-array children", fields: `"enable_groups": true, "groups": [{"name": "x", "groups": {}}]`},
		{name: "null symbol", fields: `"groups_menu_symbol": null`},
		{name: "duplicate name", fields: `"enable_groups": true, "groups": [{"name": "x", "NAME": "y"}]`},
		{name: "duplicate children", fields: `"enable_groups": true, "groups": [{"name": "x", "groups": ["demo"], "GROUPS": []}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWithRootFields(tc.fields); err == nil {
				t.Fatal("Parse succeeded, want error")
			}
		})
	}
}

func TestGroupsMenuItemLimit(t *testing.T) {
	items := strings.Repeat(`"demo",`, maxGroupItems-1) + `"demo"`
	if _, err := parseWithRootFields(`"enable_groups": true, "groups": [` + items + `]`); err != nil {
		t.Fatalf("rejected %d group menu items: %v", maxGroupItems, err)
	}
	items += `,"demo"`
	if _, err := parseWithRootFields(`"enable_groups": true, "groups": [` + items + `]`); err == nil {
		t.Fatalf("accepted more than %d group menu items", maxGroupItems)
	}
}

func TestGroupsMaximumDepth(t *testing.T) {
	name := "demo"
	leaf := GroupItem{EntryName: &name}
	names := map[string]int{name: 0}
	items := []GroupItem{leaf}
	for depth := 0; depth < maxGroupDepth; depth++ {
		items = []GroupItem{{Group: &Group{Name: "group", Items: items}}}
	}
	if err := validateGroups(items, names); err != nil {
		t.Fatalf("depth %d rejected: %v", maxGroupDepth, err)
	}
	tooDeep := []GroupItem{{Group: &Group{Name: "group", Items: items}}}
	if err := validateGroups(tooDeep, names); err == nil {
		t.Fatalf("depth %d accepted", maxGroupDepth+1)
	}
}

func parseWithRootFields(fields string) (Config, error) {
	return Parse([]byte(strings.Replace(validConfig, `"configs":`, fields+`, "configs":`, 1)))
}

func TestHotkeyOptionDefaults(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HotkeysEnabled() || !cfg.ShowHotkeysInMenu() || cfg.RepeatModHotkey || cfg.EffectiveGlobalHotkeyAlphaStep() != 5 {
		t.Fatalf("unexpected hotkey defaults: %+v", cfg)
	}
}

func TestParseHotkeyConfig(t *testing.T) {
	cfg, err := Parse([]byte(`{
  "enable_hotkey": true,
  "repeat_mod_hotkey": true,
  "show_hotkey_in_menu": false,
  "global_hotkey_alpha_step": 12,
  "hotkey": {"disable_all": "Ctrl+Alt+D"},
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "hotkey": {"restart": "Ctrl+Alt+R"}
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.DisableAll != "Ctrl+Alt+D" || cfg.Configs[0].Hotkey.Restart != "Ctrl+Alt+R" ||
		!cfg.RepeatModHotkey || cfg.ShowHotkeysInMenu() || cfg.EffectiveGlobalHotkeyAlphaStep() != 12 {
		t.Fatalf("unexpected hotkey config: %+v", cfg)
	}
}

func TestParseLeftClick(t *testing.T) {
	entry := func(name string, enabled bool) string {
		return fmt.Sprintf(`{
    "name": %q, "path": ".", "cmd": "demo.exe", "working_directory": "",
    "addition_env_path": "", "use_builtin_console": false, "is_gui": true, "enabled": %t
  }`, name, enabled)
	}
	for _, test := range []struct {
		name      string
		leftClick string
		entries   string
		wantError bool
	}{
		{name: "Exact names including disabled", leftClick: `["demo", "Demo"]`, entries: entry("demo", true) + "," + entry("Demo", false)},
		{name: "Reordered configs", leftClick: `["demo", "Demo"]`, entries: entry("Demo", false) + "," + entry("demo", true)},
		{name: "Empty list", leftClick: `[]`, entries: entry("demo", true)},
		{name: "Missing name", leftClick: `["missing"]`, entries: entry("demo", true), wantError: true},
		{name: "Case mismatch", leftClick: `["Demo"]`, entries: entry("demo", true), wantError: true},
		{name: "Legacy numeric index", leftClick: `[0]`, entries: entry("demo", true), wantError: true},
		{name: "Duplicate enabled names", leftClick: `[]`, entries: entry("demo", true) + "," + entry("demo", true), wantError: true},
		{name: "Duplicate disabled names", leftClick: `[]`, entries: entry("demo", false) + "," + entry("demo", false), wantError: true},
		{name: "Duplicate mixed enabled names", leftClick: `[]`, entries: entry("demo", true) + "," + entry("demo", false), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`{"LEFT_CLICK": %s, "configs": [%s]}`, test.leftClick, test.entries)
			cfg, err := Parse([]byte(data))
			if test.wantError {
				if err == nil {
					t.Fatal("Parse succeeded, want invalid name or reference error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.leftClick == `[]` {
				if len(cfg.LeftClick) != 0 {
					t.Fatalf("LeftClick = %v, want empty list", cfg.LeftClick)
				}
			} else if len(cfg.LeftClick) != 2 || cfg.LeftClick[0] != "demo" || cfg.LeftClick[1] != "Demo" {
				t.Fatalf("LeftClick = %v, want [demo Demo]", cfg.LeftClick)
			}
		})
	}
}

func TestParseRejectsInvalidHotkeyOptions(t *testing.T) {
	for _, field := range []string{
		`"enable_hotkey": null,`,
		`"repeat_mod_hotkey": null,`,
		`"show_hotkey_in_menu": null,`,
		`"hotkey": null,`,
		`"hotkey": {"exit": null},`,
		`"left_click": null,`,
		`"global_hotkey_alpha_step": 0,`,
		`"global_hotkey_alpha_step": 256,`,
	} {
		data := []byte(`{` + field + `
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`)
		if _, err := Parse(data); err == nil {
			t.Fatalf("Parse succeeded with %s, want error", field)
		}
	}
	for _, field := range []string{
		`"hotkey": null,`,
		`"hotkey": {"restart": null},`,
	} {
		data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    ` + field + `
  }]
}`)
		if _, err := Parse(data); err == nil {
			t.Fatalf("Parse succeeded with entry %s, want error", field)
		}
	}
}

func TestCacheOptionDefaults(t *testing.T) {
	data := []byte(`{
  "enable_cache": true,
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CacheEnabled() || !cfg.CachePositionEnabled() || !cfg.CacheSizeEnabled() ||
		cfg.CacheEnabledStateEnabled() || !cfg.CacheShowEnabled() || !cfg.CacheAlphaEnabled() {
		t.Fatalf("unexpected cache defaults: %+v", cfg)
	}
}

func TestStartShowSilentCanBeDisabled(t *testing.T) {
	data := []byte(`{
  "start_show_silent": false,
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveStartShowSilent() {
		t.Fatal("EffectiveStartShowSilent() = true, want false")
	}
}

func TestParseRejectsNullOptionalBooleans(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{
			name: "start show silent",
			data: `{
  "start_show_silent": null,
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`,
		},
		{
			name: "start show",
			data: `{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "start_show": null
  }]
}`,
		},
		{
			name: "ignore all",
			data: `{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "ignore_all": null
  }]
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.data)); err == nil {
				t.Fatal("Parse succeeded, want null validation error")
			}
		})
	}
}

func TestParseRejectsNullKnownFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
	}{
		{name: "required string", field: `"path": null`},
		{name: "required boolean", field: `"enabled": null`},
		{name: "optional boolean", field: `"require_admin": null`},
		{name: "optional timeout", field: `"kill_timeout": null`},
		{name: "optional command", field: `"stop_cmd": null`},
		{name: "ownership boolean", field: `"not_host_by_commandtrayhost": null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    ` + tc.field + `
  }]
}`)
			if _, err := Parse(data); err == nil {
				t.Fatal("Parse succeeded, want null validation error")
			}
		})
	}
}

func TestParseRejectsCaseInsensitiveNullKnownFields(t *testing.T) {
	data := []byte(`{
  "START_SHOW_SILENT": null,
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true
  }]
}`)
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse succeeded, want null validation error")
	}
}

func TestParseAcceptsCaseInsensitiveRequiredFields(t *testing.T) {
	data := []byte(`{
  "CONFIGS": [{
    "NAME": "demo",
    "PATH": ".",
    "CMD": "demo.exe",
    "WORKING_DIRECTORY": "",
    "ADDITION_ENV_PATH": "",
    "USE_BUILTIN_CONSOLE": false,
    "IS_GUI": true,
    "ENABLED": true
  }]
}`)
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsTooManyEntries(t *testing.T) {
	cfg := Config{Configs: make([]EntryConfig, MaxConfigEntries+1)}
	if err := cfg.validate(); err == nil {
		t.Fatal("validate succeeded, want too many entries error")
	}
}

func TestNotHostedDefaultsToVisible(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "not_host_by_commandtrayhost": true
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Configs[0].EffectiveStartShow() {
		t.Fatal("EffectiveStartShow() = false, want true")
	}
}

func TestParseIgnoreAll(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "ignore_all": true
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Configs[0].IgnoreAll {
		t.Fatal("IgnoreAll = false, want true")
	}
}

func TestParseExclusionID(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "exclusion_id": 7
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Configs[0].ExclusionID == nil || *cfg.Configs[0].ExclusionID != 7 {
		t.Fatalf("ExclusionID = %v, want 7", cfg.Configs[0].ExclusionID)
	}
}

func TestParseRejectsInvalidExclusionID(t *testing.T) {
	for _, value := range []string{"null", "0", "-1", "2147483648"} {
		t.Run(value, func(t *testing.T) {
			data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "exclusion_id": ` + value + `
  }]
}`)
			if _, err := Parse(data); err == nil {
				t.Fatal("Parse succeeded, want invalid exclusion_id error")
			}
		})
	}
}

func TestParseRejectsExclusionIDForUnmanagedEntry(t *testing.T) {
	for _, field := range []string{`"not_host_by_commandtrayhost": true`, `"not_monitor_by_commandtrayhost": true`} {
		data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "exclusion_id": 1,
    ` + field + `
  }]
}`)
		if _, err := Parse(data); err == nil {
			t.Fatalf("Parse succeeded with %s, want ownership validation error", field)
		}
	}
}

func TestParseCronConfig(t *testing.T) {
	cfg, err := Parse([]byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": false,
    "crontab_config": {
      "crontab": "0 */5 * * * MON-FRI",
      "method": "restart_count_stop",
      "count": 3,
      "start_show": true,
      "log": "cron.log",
      "log_level": 2
    }
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	cron := cfg.Configs[0].Cron
	if cron == nil || !cron.IsEnabled() || cron.Parsed == nil || cron.Method != CronRestartCountStop ||
		cron.Count != 3 || cron.StartShow == nil || !*cron.StartShow || cron.Log != "cron.log" || cron.LogLevel != 2 {
		t.Fatalf("unexpected cron config: %+v", cron)
	}
}

func TestCronStartShowPrecedence(t *testing.T) {
	entry := EntryConfig{Cron: &CronConfig{}}
	if entry.EffectiveCronStartShow() {
		t.Fatal("default cron start_show = true, want false")
	}
	cached := true
	entry.CachedShow = &cached
	if !entry.EffectiveCronStartShow() {
		t.Fatal("cached cron start_show = false, want true")
	}
	explicit := false
	entry.Cron.StartShow = &explicit
	if entry.EffectiveCronStartShow() {
		t.Fatal("explicit cron start_show did not override cache")
	}
}

func TestParseRejectsInvalidCronConfig(t *testing.T) {
	for _, test := range []struct {
		name string
		cron string
	}{
		{name: "null", cron: `null`},
		{name: "missing expression", cron: `{"method":"start","count":0}`},
		{name: "missing method", cron: `{"crontab":"0 * * * * *","count":0}`},
		{name: "missing count", cron: `{"crontab":"0 * * * * *","method":"start"}`},
		{name: "reserved field", cron: `{"crontab":"0 * * * * *","method":"start","count":0,"need_renew":false}`},
		{name: "null enabled", cron: `{"enabled":null,"crontab":"0 * * * * *","method":"start","count":0}`},
		{name: "five fields", cron: `{"crontab":"* * * * *","method":"start","count":0}`},
		{name: "bad method", cron: `{"crontab":"0 * * * * *","method":"launch","count":0}`},
		{name: "negative count", cron: `{"crontab":"0 * * * * *","method":"start","count":-1}`},
		{name: "large count", cron: `{"crontab":"0 * * * * *","method":"start","count":2147483648}`},
		{name: "bad log level", cron: `{"crontab":"0 * * * * *","method":"start","count":0,"log_level":4}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(`{
  "configs": [{
    "name": "demo", "path": ".", "cmd": "demo.exe", "working_directory": "",
    "addition_env_path": "", "use_builtin_console": false, "is_gui": true, "enabled": false,
    "crontab_config": ` + test.cron + `
  }]
}`)
			if _, err := Parse(data); err == nil {
				t.Fatal("Parse succeeded, want cron validation error")
			}
		})
	}
}

func TestParseRejectsManagedCronMethodsForUnmanagedEntry(t *testing.T) {
	for _, ownership := range []string{`"not_host_by_commandtrayhost":true,`, `"not_monitor_by_commandtrayhost":true,`} {
		for _, method := range []string{"restart", "stop", "start_count_stop", "restart_count_stop"} {
			data := []byte(`{
  "configs": [{
    "name": "demo", "path": ".", "cmd": "demo.exe", "working_directory": "",
    "addition_env_path": "", "use_builtin_console": false, "is_gui": true, "enabled": false,
    ` + ownership + `
    "crontab_config": {"crontab":"0 * * * * *","method":"` + method + `","count":0}
  }]
}`)
			if _, err := Parse(data); err == nil {
				t.Fatalf("Parse succeeded for unmanaged %s", method)
			}
		}
	}
}

func TestWindowAppearanceConfig(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "position": [0.25, 200],
    "size": [0.5, 1],
    "alpha": 170,
    "topmost": true
  }]
}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	entry := cfg.Configs[0]
	if entry.Position == nil || entry.Size == nil || entry.Alpha == nil || !entry.Topmost {
		t.Fatalf("appearance fields were not decoded: %+v", entry)
	}
	if x, y := entry.Position.Pixels(1920, 1080); x != 480 || y != 200 {
		t.Fatalf("position pixels = (%d, %d), want (480, 200)", x, y)
	}
	if width, height := entry.Size.Pixels(1920, 1080); width != 960 || height != 1080 {
		t.Fatalf("size pixels = (%d, %d), want (960, 1080)", width, height)
	}
}

func TestParseRejectsInvalidWindowAppearance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
	}{
		{name: "negative position", field: `"position": [-1, 0],`},
		{name: "negative size", field: `"size": [1, -1],`},
		{name: "alpha too large", field: `"alpha": 256,`},
		{name: "position wrong length", field: `"position": [1, 2, 3],`},
		{name: "position null element", field: `"position": [null, 1],`},
		{name: "position null", field: `"position": null,`},
		{name: "alpha null", field: `"alpha": null,`},
		{name: "topmost null", field: `"topmost": null,`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    ` + tc.field + `
  }]
}`)
			if _, err := Parse(data); err == nil {
				t.Fatal("Parse succeeded, want appearance validation error")
			}
		})
	}
}

func TestParseRejectsInvalidKillTimeout(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "kill_timeout": -1
  }]
}`)
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse succeeded, want invalid kill_timeout error")
	}
}

func TestParseRejectsInfiniteKillTimeout(t *testing.T) {
	data := []byte(`{
  "configs": [{
    "name": "demo",
    "path": ".",
    "cmd": "demo.exe",
    "working_directory": "",
    "addition_env_path": "",
    "use_builtin_console": false,
    "is_gui": true,
    "enabled": true,
    "kill_timeout": 4294967295
  }]
}`)
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse succeeded, want INFINITE kill_timeout error")
	}
}

func TestLoadOrCreate(t *testing.T) {
	const systemDirectory = "D:/Windows-Test/System32"
	directoryProvider := func() (string, error) { return systemDirectory, nil }
	tests := []struct {
		name             string
		language         i18n.Language
		windowEntryName  string
		consoleEntryName string
		comment          string
	}{
		{name: "English", language: i18n.English, windowEntryName: "Window Control Test", consoleEntryName: "Ping Console Test", comment: "Examples are disabled"},
		{name: "Chinese", language: i18n.SimplifiedChinese, windowEntryName: "窗口控制测试", consoleEntryName: "Ping 控制台测试", comment: "示例默认禁用"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			cfg, err := LoadOrCreate(path, test.language, directoryProvider)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Lang != "auto" {
				t.Fatalf("lang = %q, want auto", cfg.Lang)
			}
			if len(cfg.Configs) != 2 || cfg.Configs[0].Name != test.windowEntryName || cfg.Configs[1].Name != test.consoleEntryName {
				t.Fatalf("configs = %+v, want entries %q and %q", cfg.Configs, test.windowEntryName, test.consoleEntryName)
			}
			windowEntry := cfg.Configs[0]
			if windowEntry.Path != systemDirectory || windowEntry.Command != "charmap.exe" || windowEntry.Enabled || !windowEntry.IsGUI {
				t.Fatalf("window test entry = %+v", windowEntry)
			}
			if windowEntry.StartShow == nil || !*windowEntry.StartShow || windowEntry.Position == nil || windowEntry.Size == nil || windowEntry.Alpha == nil || *windowEntry.Alpha != 235 {
				t.Fatalf("window test entry appearance = %+v", windowEntry)
			}
			consoleEntry := cfg.Configs[1]
			if consoleEntry.Path != systemDirectory || consoleEntry.Command != "cmd.exe /d /k ping -t 1.1.1.1" || consoleEntry.Enabled || consoleEntry.IsGUI || !consoleEntry.KillProcessTree {
				t.Fatalf("console test entry = %+v", consoleEntry)
			}
			if consoleEntry.StartShow == nil || !*consoleEntry.StartShow || consoleEntry.Position == nil || consoleEntry.Size == nil || consoleEntry.Alpha == nil || *consoleEntry.Alpha != 235 {
				t.Fatalf("console test entry appearance = %+v", consoleEntry)
			}
			if cfg.AutoUpdateEnabled() || !cfg.CacheEnabled() || !cfg.CachePositionEnabled() || !cfg.CacheSizeEnabled() || cfg.CacheEnabledStateEnabled() || !cfg.CacheShowEnabled() || !cfg.CacheAlphaEnabled() {
				t.Fatalf("test config options = %+v", cfg)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.Valid(data) || !strings.Contains(string(data), test.comment) || !strings.Contains(string(data), systemDirectory) {
				t.Fatalf("created config = %q", data)
			}
		})
	}
	t.Run("Existing file is not rewritten", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		original := []byte(validConfig)
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		called := false
		if _, err := LoadOrCreate(path, i18n.SimplifiedChinese, func() (string, error) {
			called = true
			return "", errors.New("must not be called")
		}); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("system directory provider was called for an existing config")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(original) {
			t.Fatalf("existing config was rewritten: %q", got)
		}
	})
	t.Run("Invalid system directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if _, err := LoadOrCreate(path, i18n.English, func() (string, error) {
			return `relative/System32`, nil
		}); err == nil {
			t.Fatal("LoadOrCreate succeeded with a relative system directory")
		}
	})
	t.Run("System directory error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		want := errors.New("system directory failed")
		if _, err := LoadOrCreate(path, i18n.English, func() (string, error) {
			return "", want
		}); !errors.Is(err, want) {
			t.Fatalf("LoadOrCreate error = %v, want %v", err, want)
		}
	})
}

func TestLoadOrCreateCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles", "work.json")
	if _, err := LoadOrCreate(path, i18n.English, func() (string, error) {
		return `C:\Windows\System32`, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat created config: %v", err)
	}
}

func encodeUTF16(s string, order binary.ByteOrder, bom []byte) []byte {
	words := utf16.Encode([]rune(s))
	data := append([]byte(nil), bom...)
	for _, word := range words {
		var buf [2]byte
		order.PutUint16(buf[:], word)
		data = append(data, buf[:]...)
	}
	return data
}

func encodeUTF32(s string, order binary.ByteOrder, bom []byte) []byte {
	data := append([]byte(nil), bom...)
	for _, r := range s {
		var buf [4]byte
		order.PutUint32(buf[:], uint32(r))
		data = append(data, buf[:]...)
	}
	return data
}
