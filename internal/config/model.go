package config

import (
	"encoding/json"
	"errors"

	"github.com/fcying/CommandTrayHostGo/internal/cronexpr"
)

type Config struct {
	Lang                  string        `json:"lang"`
	RequireAdmin          bool          `json:"require_admin"`
	StartShowSilent       *bool         `json:"start_show_silent"`
	EnableCache           *bool         `json:"enable_cache"`
	ConformCacheExpire    *bool         `json:"conform_cache_expire"`
	DisableCachePosition  *bool         `json:"disable_cache_position"`
	DisableCacheSize      *bool         `json:"disable_cache_size"`
	DisableCacheEnabled   *bool         `json:"disable_cache_enabled"`
	DisableCacheShow      *bool         `json:"disable_cache_show"`
	DisableCacheAlpha     *bool         `json:"disable_cache_alpha"`
	AutoHotReloading      bool          `json:"auto_hot_reloading_config"`
	EnableHotkey          *bool         `json:"enable_hotkey"`
	RepeatModHotkey       bool          `json:"repeat_mod_hotkey"`
	ShowHotkeyInMenu      *bool         `json:"show_hotkey_in_menu"`
	GlobalHotkeyAlphaStep *int64        `json:"global_hotkey_alpha_step"`
	Hotkey                GlobalHotkeys `json:"hotkey"`
	LeftClick             []string      `json:"left_click"`
	EnableGroups          bool          `json:"enable_groups"`
	Groups                *[]GroupItem  `json:"groups"`
	GroupsMenuSymbol      *string       `json:"groups_menu_symbol"`
	CmdMenuMaxLength      int64         `json:"cmd_menu_max_length"`
	Icon                  string        `json:"icon"`
	IconSize              *int64        `json:"icon_size"`
	AutoUpdate            *bool         `json:"auto_update"`
	SkipPrerelease        *bool         `json:"skip_prerelease"`
	Configs               []EntryConfig `json:"configs"`
}

type EntryConfig struct {
	Name              string       `json:"name"`
	Path              string       `json:"path"`
	Command           string       `json:"cmd"`
	WorkingDirectory  string       `json:"working_directory"`
	AdditionalEnvPath string       `json:"addition_env_path"`
	UseBuiltinConsole bool         `json:"use_builtin_console"`
	IsGUI             bool         `json:"is_gui"`
	Enabled           bool         `json:"enabled"`
	RequireAdmin      bool         `json:"require_admin"`
	StartShow         *bool        `json:"start_show"`
	IgnoreAll         bool         `json:"ignore_all"`
	Position          *Pair        `json:"position"`
	Size              *Pair        `json:"size"`
	Alpha             *int64       `json:"alpha"`
	Topmost           bool         `json:"topmost"`
	NotHosted         bool         `json:"not_host_by_commandtrayhost"`
	NotMonitored      bool         `json:"not_monitor_by_commandtrayhost"`
	StopCommand       string       `json:"stop_cmd"`
	KillTimeout       *int64       `json:"kill_timeout"`
	KillProcessTree   bool         `json:"kill_process_tree"`
	ExclusionID       *int64       `json:"exclusion_id"`
	Hotkey            EntryHotkeys `json:"hotkey"`
	Cron              *CronConfig  `json:"crontab_config"`
	Icon              string       `json:"icon"`
	CachedPosition    *PixelPair   `json:"-"`
	CachedSize        *PixelPair   `json:"-"`
	CachedAlpha       *int64       `json:"-"`
	CachedShow        *bool        `json:"-"`
}

func (c Config) EffectiveIconSize() int32 {
	if c.IconSize == nil {
		return 256
	}
	return int32(*c.IconSize)
}

func (c Config) AutoUpdateEnabled() bool {
	return c.AutoUpdate == nil || *c.AutoUpdate
}

func (c Config) SkipPrereleases() bool {
	return c.SkipPrerelease == nil || *c.SkipPrerelease
}

type CronConfig struct {
	Enabled    *bool                `json:"enabled"`
	Expression string               `json:"crontab"`
	Method     CronMethod           `json:"method"`
	Count      int64                `json:"count"`
	StartShow  *bool                `json:"start_show"`
	Log        string               `json:"log"`
	LogLevel   int64                `json:"log_level"`
	Parsed     *cronexpr.Expression `json:"-"`
}

type CronMethod string

const (
	CronStart            CronMethod = "start"
	CronRestart          CronMethod = "restart"
	CronStop             CronMethod = "stop"
	CronStartCountStop   CronMethod = "start_count_stop"
	CronRestartCountStop CronMethod = "restart_count_stop"
)

func (c CronConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c CronConfig) StopsWhenExhausted() bool {
	return c.Method == CronStartCountStop || c.Method == CronRestartCountStop
}

type GlobalHotkeys struct {
	DisableAll    string `json:"disable_all"`
	EnableAll     string `json:"enable_all"`
	HideAll       string `json:"hide_all"`
	ShowAll       string `json:"show_all"`
	RestartAll    string `json:"restart_all"`
	Elevate       string `json:"elevate"`
	Exit          string `json:"exit"`
	LeftClick     string `json:"left_click"`
	RightClick    string `json:"right_click"`
	AddAlpha      string `json:"add_alpha"`
	MinusAlpha    string `json:"minus_alpha"`
	Topmost       string `json:"topmost"`
	HideCurrent   string `json:"hide_current"`
	ShowAllDocked string `json:"show_all_docked"`
}

type EntryHotkeys struct {
	HideShow      string `json:"hide_show"`
	DisableEnable string `json:"disable_enable"`
	Restart       string `json:"restart"`
	Elevate       string `json:"elevate"`
}

type Pair [2]float64

type PixelPair [2]int32

func (p *Pair) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if len(values) != len(p) {
		return errors.New("must contain exactly two numbers")
	}
	for i, raw := range values {
		if string(raw) == "null" {
			return errors.New("values must be numbers")
		}
		if err := json.Unmarshal(raw, &p[i]); err != nil {
			return errors.New("values must be numbers")
		}
	}
	return nil
}

func (p Pair) Pixels(width, height int32) (int32, int32) {
	return dimensionPixels(p[0], width), dimensionPixels(p[1], height)
}

func dimensionPixels(value float64, screen int32) int32 {
	if value <= 1 {
		value *= float64(screen)
	}
	return int32(value)
}

func (e EntryConfig) EffectiveStartShow() bool {
	if e.StartShow != nil {
		return *e.StartShow
	}
	return e.NotHosted || e.NotMonitored
}

func (e EntryConfig) EffectiveCronStartShow() bool {
	if e.Cron != nil && e.Cron.StartShow != nil {
		return *e.Cron.StartShow
	}
	if e.CachedShow != nil {
		return *e.CachedShow
	}
	return false
}

func (e EntryConfig) EffectiveKillTimeout() uint32 {
	if e.KillTimeout == nil {
		return 200
	}
	return uint32(*e.KillTimeout)
}

func (e EntryConfig) PositionPixels(width, height int32) (int32, int32, bool) {
	if e.CachedPosition != nil {
		return e.CachedPosition[0], e.CachedPosition[1], true
	}
	if e.Position == nil {
		return 0, 0, false
	}
	x, y := e.Position.Pixels(width, height)
	return x, y, true
}

func (e EntryConfig) SizePixels(width, height int32) (int32, int32, bool) {
	if e.CachedSize != nil {
		return e.CachedSize[0], e.CachedSize[1], true
	}
	if e.Size == nil {
		return 0, 0, false
	}
	cx, cy := e.Size.Pixels(width, height)
	return cx, cy, true
}

func (e EntryConfig) EffectiveAlpha() *int64 {
	if e.CachedAlpha != nil {
		return e.CachedAlpha
	}
	return e.Alpha
}

func (e EntryConfig) SameLaunchIdentity(other EntryConfig) bool {
	return e.Path == other.Path &&
		e.Command == other.Command &&
		e.WorkingDirectory == other.WorkingDirectory &&
		e.IsGUI == other.IsGUI &&
		entryOwnershipKey(e) == entryOwnershipKey(other) &&
		e.RequireAdmin == other.RequireAdmin
}

func entryOwnershipKey(entry EntryConfig) uint8 {
	if entry.NotMonitored {
		return 1
	}
	if entry.NotHosted {
		return 2
	}
	return 0
}

func (c Config) DisplayName() string {
	return "CommandTrayHostGo"
}

func (c Config) EffectiveStartShowSilent() bool {
	return c.StartShowSilent == nil || *c.StartShowSilent
}

func (c Config) CacheEnabled() bool {
	if c.EnableCache == nil && c.ConformCacheExpire == nil && c.DisableCachePosition == nil &&
		c.DisableCacheSize == nil && c.DisableCacheEnabled == nil && c.DisableCacheShow == nil &&
		c.DisableCacheAlpha == nil {
		return false
	}
	return c.EnableCache == nil || *c.EnableCache
}

func (c Config) HotReloadEnabled() bool {
	return c.ConformCacheExpire == nil || *c.ConformCacheExpire
}

func (c Config) HotkeysEnabled() bool {
	return enabledByDefault(c.EnableHotkey, true)
}

func (c Config) ShowHotkeysInMenu() bool {
	return enabledByDefault(c.ShowHotkeyInMenu, true)
}

func (c Config) EffectiveGlobalHotkeyAlphaStep() uint8 {
	if c.GlobalHotkeyAlphaStep == nil {
		return 5
	}
	return uint8(*c.GlobalHotkeyAlphaStep)
}

func (c Config) MenuText(value string) string {
	if c.CmdMenuMaxLength == 0 {
		return value
	}
	runes := []rune(value)
	if int64(len(runes)) <= c.CmdMenuMaxLength {
		return value
	}
	return string(runes[:c.CmdMenuMaxLength]) + "..."
}

func (c Config) GroupsEnabled() bool {
	return c.EnableGroups && c.Groups != nil
}

func (c Config) EffectiveGroupsMenuSymbol() string {
	if c.GroupsMenuSymbol == nil {
		return "+"
	}
	return *c.GroupsMenuSymbol
}

func enabledByDefault(value *bool, defaultValue bool) bool {
	if value == nil {
		return defaultValue
	}
	return *value
}

func (c Config) CachePositionEnabled() bool {
	return c.CacheEnabled() && !enabledByDefault(c.DisableCachePosition, false)
}

func (c Config) CacheSizeEnabled() bool {
	return c.CacheEnabled() && !enabledByDefault(c.DisableCacheSize, false)
}

func (c Config) CacheEnabledStateEnabled() bool {
	return c.CacheEnabled() && !enabledByDefault(c.DisableCacheEnabled, true)
}

func (c Config) CacheShowEnabled() bool {
	return c.CacheEnabled() && !enabledByDefault(c.DisableCacheShow, false)
}

func (c Config) CacheAlphaEnabled() bool {
	return c.CacheEnabled() && !enabledByDefault(c.DisableCacheAlpha, false)
}
