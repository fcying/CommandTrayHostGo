package statecache

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestOpenDisabledWithoutCacheOptions(t *testing.T) {
	cfg := testConfig()
	store, err := Open("unused", filepath.Join(t.TempDir(), "command_tray_host.cache"), &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Fatal("Open returned a store, want cache disabled")
	}
}

func TestRemoveIgnoresMissingFile(t *testing.T) {
	if err := Remove(filepath.Join(t.TempDir(), "missing.cache")); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTripAppliesCachedStateByName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	falseValue := false
	cfg := testConfig()
	cfg.DisableCacheEnabled = &falseValue
	trueValue := true
	cfg.EnableCache = &trueValue
	cfg.ConformCacheExpire = &falseValue
	cfg.Configs[0].Alpha = int64Pointer(200)
	store, err := Open(configPath, cachePath, &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	alpha := int64(123)
	store.UpdateState(0, false, true)
	store.UpdateWindow(0, WindowState{Left: 0, Top: 1, Right: 800, Bottom: 601, Alpha: &alpha})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	second := config.Config{
		EnableCache:         &trueValue,
		ConformCacheExpire:  &falseValue,
		DisableCacheEnabled: &falseValue,
		Configs: []config.EntryConfig{
			{Name: "other", Enabled: true},
			{Name: "demo", Enabled: true, Alpha: int64Pointer(200)},
		},
	}
	store, err = Open(configPath, cachePath, &second, false)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("Open returned nil store")
	}
	entry := second.Configs[1]
	if entry.Enabled {
		t.Fatal("cached enabled state was not applied")
	}
	if !entry.EffectiveStartShow() {
		t.Fatal("cached show state was not applied")
	}
	if x, y, ok := entry.PositionPixels(1920, 1080); !ok || x != 0 || y != 1 {
		t.Fatalf("cached position = (%d, %d, %v), want (0, 1, true)", x, y, ok)
	}
	if width, height, ok := entry.SizePixels(1920, 1080); !ok || width != 800 || height != 600 {
		t.Fatalf("cached size = (%d, %d, %v), want (800, 600, true)", width, height, ok)
	}
	if got := entry.EffectiveAlpha(); got == nil || *got != 123 {
		t.Fatalf("cached alpha = %v, want 123", got)
	}
}
func TestCacheExpiresOnSameStampConfigMutation(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	cachePath := filepath.Join(directory, "command_tray_host.cache")
	original := []byte("config-A")
	changed := []byte("config-B")
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	trueValue := true
	initial := testConfig()
	initial.EnableCache = &trueValue
	initial.SourceDigest = sha256.Sum256(original)
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenSnapshot(cachePath, &initial, stamp, false)
	if err != nil || store == nil {
		t.Fatalf("OpenSnapshot initial = %v, %v", store, err)
	}
	if err := os.WriteFile(configPath, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(configPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	reloaded := testConfig()
	reloaded.EnableCache = &trueValue
	reloaded.SourceDigest = sha256.Sum256(changed)
	stamp, err = config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSnapshot(cachePath, &reloaded, stamp, false); !errors.Is(err, ErrCacheExpired) {
		t.Fatalf("OpenSnapshot same-stamp mutation error = %v, want ErrCacheExpired", err)
	}
}

func TestOpenDiscardsCacheNotNewerThanConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	falseValue := false
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.DisableCacheEnabled = &falseValue
	store, err := Open(configPath, cachePath, &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	store.UpdateState(0, false, true)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatal(err)
	}

	reloaded := testConfig()
	reloaded.EnableCache = &trueValue
	reloaded.DisableCacheEnabled = &falseValue
	store, err = Open(configPath, cachePath, &reloaded, false)
	if err == nil {
		t.Fatal("Open succeeded, want expired cache warning")
	}
	if store == nil {
		t.Fatal("Open returned nil store")
	}
	if !reloaded.Configs[0].Enabled || reloaded.Configs[0].EffectiveStartShow() {
		t.Fatal("expired cache changed config state")
	}
	store, err = Open(configPath, cachePath, &reloaded, true)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Configs[0].Enabled || !reloaded.Configs[0].EffectiveStartShow() {
		t.Fatal("allowExpired did not apply cached state")
	}

	third := testConfig()
	third.EnableCache = &trueValue
	third.DisableCacheEnabled = &falseValue
	if _, err := Open(configPath, cachePath, &third, false); err != nil {
		t.Fatalf("Open after allowing expired cache: %v", err)
	}
	if third.Configs[0].Enabled || !third.Configs[0].EffectiveStartShow() {
		t.Fatal("refreshed cache did not apply cached state")
	}
}

func TestConformCacheExpireFalseStillExpiresCache(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	falseValue := false
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.ConformCacheExpire = &falseValue
	if _, err := Open(configPath, cachePath, &cfg, false); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatal(err)
	}

	reloaded := testConfig()
	reloaded.EnableCache = &trueValue
	reloaded.ConformCacheExpire = &falseValue
	if _, err := Open(configPath, cachePath, &reloaded, false); !errors.Is(err, ErrCacheExpired) {
		t.Fatalf("Open error = %v, want ErrCacheExpired", err)
	}
}

func TestOpenStartupSnapshotKeepsExpiredCacheForAutoReload(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	falseValue := false
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.DisableCacheEnabled = &falseValue
	store, err := Open(configPath, cachePath, &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	store.UpdateState(0, false, true)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(configPath, future, future); err != nil {
		t.Fatal(err)
	}
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	reloaded := testConfig()
	reloaded.EnableCache = &trueValue
	reloaded.DisableCacheEnabled = &falseValue
	reloaded.AutoHotReloading = true
	if _, err := OpenStartupSnapshot(cachePath, &reloaded, stamp); err != nil {
		t.Fatal(err)
	}
	if reloaded.Configs[0].Enabled || !reloaded.Configs[0].EffectiveStartShow() {
		t.Fatal("OpenStartupSnapshot did not preserve expired cache state")
	}
}

func TestConfigChangeExpiresCacheEvenAfterLaterCacheSave(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := time.Now().Add(-time.Hour)
	if err := os.Chtimes(configPath, initial, initial); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	store, err := Open(configPath, cachePath, &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	changed := initial.Add(30 * time.Minute)
	if err := os.Chtimes(configPath, changed, changed); err != nil {
		t.Fatal(err)
	}
	store.UpdateShow(0, true)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if cacheInfo, err := os.Stat(cachePath); err != nil || !cacheInfo.ModTime().After(changed) {
		t.Fatalf("cache was not saved after changed config: info=%v err=%v", cacheInfo, err)
	}

	reloaded := testConfig()
	reloaded.EnableCache = &trueValue
	if _, err := Open(configPath, cachePath, &reloaded, false); !errors.Is(err, ErrCacheExpired) {
		t.Fatalf("Open error = %v, want ErrCacheExpired", err)
	}
}

func TestConfigModTimeAtUnixEpochUsesNewFormatValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(0, 0)
	if err := os.Chtimes(configPath, epoch, epoch); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	if _, err := Open(configPath, cachePath, &cfg, false); err != nil {
		t.Fatal(err)
	}
	data, err := load(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !data.HasConfigModTime || data.ConfigModTime != 0 {
		t.Fatalf("config mod time = (%d, %v), want (0, true)", data.ConfigModTime, data.HasConfigModTime)
	}
}

func TestAlphaCacheRequiresConfiguredAlpha(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	store, err := Open(configPath, cachePath, &cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	alpha := int64(100)
	store.UpdateWindow(0, WindowState{Alpha: &alpha})
	if store.entries[0].Valid&validAlpha != 0 {
		t.Fatal("alpha was cached without an alpha field in config")
	}
}

func TestUpdateAlphaPreservesWindowGeometry(t *testing.T) {
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.Configs[0].Alpha = int64Pointer(200)
	store := newStore("unused", &cfg)
	store.UpdateWindow(0, WindowState{Left: 100, Top: 200, Right: 500, Bottom: 600})
	store.UpdateAlpha(0, 125)
	item := store.entries[0]
	if item.Left != 100 || item.Top != 200 || item.Right != 500 || item.Bottom != 600 {
		t.Fatalf("UpdateAlpha changed window geometry: %+v", item)
	}
	if item.Alpha != 125 || item.Valid&validAlpha == 0 {
		t.Fatalf("cached alpha = (%d, %d), want (125, valid)", item.Alpha, item.Valid)
	}
}

func TestUpdateWindowTracksSizeIndependentOfPosition(t *testing.T) {
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.DisableCachePosition = &trueValue
	store := newStore("unused", &cfg)
	store.UpdateWindow(0, WindowState{Left: 100, Top: 100, Right: 500, Bottom: 400})
	store.dirty = false
	store.UpdateWindow(0, WindowState{Left: 50, Top: 75, Right: 500, Bottom: 400})
	if !store.dirty {
		t.Fatal("size change from left and top edges was not cached")
	}
	item := store.entries[0]
	if width, height := item.Right-item.Left, item.Bottom-item.Top; width != 450 || height != 325 {
		t.Fatalf("cached size = (%d, %d), want (450, 325)", width, height)
	}
}

func TestUpdateWindowMoveOnlyPreservesCachedSize(t *testing.T) {
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	store := newStore("unused", &cfg)
	store.UpdateWindow(0, WindowState{Left: 100, Top: 100, Right: 500, Bottom: 400})
	store.dirty = false
	store.UpdateWindow(0, WindowState{Left: 200, Top: 100, Right: 600, Bottom: 400})
	item := store.entries[0]
	if item.Left != 200 || item.Top != 100 || item.Right != 600 || item.Bottom != 400 {
		t.Fatalf("cached rectangle = (%d, %d, %d, %d), want (200, 100, 600, 400)", item.Left, item.Top, item.Right, item.Bottom)
	}
	if width, height := item.Right-item.Left, item.Bottom-item.Top; width != 400 || height != 300 {
		t.Fatalf("cached size = (%d, %d), want (400, 300)", width, height)
	}
}

func TestUpdateWindowDoesNotMutateDisabledCachedSize(t *testing.T) {
	cfg := exerciseWindowCacheToggle(t, true)
	cachedPosition := cfg.Configs[0].CachedPosition
	if cachedPosition == nil || *cachedPosition != (config.PixelPair{200, 100}) {
		t.Fatalf("cached position = %v, want (200, 100)", cachedPosition)
	}
	cachedSize := cfg.Configs[0].CachedSize
	if cachedSize == nil || *cachedSize != (config.PixelPair{400, 300}) {
		t.Fatalf("cached size = %v, want (400, 300)", cachedSize)
	}
}

func TestUpdateWindowDoesNotMutateDisabledCachedPosition(t *testing.T) {
	cfg := exerciseWindowCacheToggle(t, false)
	cachedPosition := cfg.Configs[0].CachedPosition
	if cachedPosition == nil || *cachedPosition != (config.PixelPair{100, 100}) {
		t.Fatalf("cached position = %v, want (100, 100)", cachedPosition)
	}
	cachedSize := cfg.Configs[0].CachedSize
	if cachedSize == nil || *cachedSize != (config.PixelPair{600, 400}) {
		t.Fatalf("cached size = %v, want (600, 400)", cachedSize)
	}
}

func exerciseWindowCacheToggle(t *testing.T, disableSize bool) config.Config {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	disabled := true
	reenabled := false
	cfg := config.Config{
		EnableCache: &enabled,
		Configs:     []config.EntryConfig{{Name: "demo", Enabled: true}},
	}
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := Rebase(cachePath, &cfg, stamp, nil, KeepPrevious)
	if err != nil {
		t.Fatal(err)
	}
	previous.UpdateWindow(0, WindowState{Left: 100, Top: 100, Right: 500, Bottom: 400})
	if err := previous.Save(); err != nil {
		t.Fatal(err)
	}

	disabledConfig := cfg
	if disableSize {
		disabledConfig.DisableCacheSize = &disabled
	} else {
		disabledConfig.DisableCachePosition = &disabled
	}
	disabledStore, err := Rebase(cachePath, &disabledConfig, stamp, previous, KeepPrevious)
	if err != nil {
		t.Fatal(err)
	}
	disabledStore.UpdateWindow(0, WindowState{Left: 200, Top: 100, Right: 800, Bottom: 500})
	item := disabledStore.entries[0]
	if item.Valid&(validPosition|validSize) != validPosition|validSize {
		t.Fatalf("cached validity = %d, want both geometry bits", item.Valid)
	}
	if disableSize {
		if item.Left != 200 || item.Top != 100 || item.Right != 600 || item.Bottom != 400 {
			t.Fatalf("cached rectangle with size disabled = (%d, %d, %d, %d), want (200, 100, 600, 400)", item.Left, item.Top, item.Right, item.Bottom)
		}
	} else if item.Left != 100 || item.Top != 100 || item.Right != 700 || item.Bottom != 500 {
		t.Fatalf("cached rectangle with position disabled = (%d, %d, %d, %d), want (100, 100, 700, 500)", item.Left, item.Top, item.Right, item.Bottom)
	}
	if err := disabledStore.Save(); err != nil {
		t.Fatal(err)
	}

	reenabledConfig := disabledConfig
	if disableSize {
		reenabledConfig.DisableCacheSize = &reenabled
	} else {
		reenabledConfig.DisableCachePosition = &reenabled
	}
	if _, err := OpenSnapshot(cachePath, &reenabledConfig, stamp, false); err != nil {
		t.Fatal(err)
	}
	return reenabledConfig
}

func TestMergeDoesNotReuseDuplicateNames(t *testing.T) {
	trueValue := true
	cfg := config.Config{
		EnableCache: &trueValue,
		Configs: []config.EntryConfig{
			{Name: "B"},
			{Name: "B"},
			{Name: "A"},
		},
	}
	store := newStore("unused", &cfg)
	store.merge([]entry{
		{Name: "A", StartShow: true, Valid: validShow},
		{Name: "B", Enabled: true, Valid: validEnabled},
		{Name: "B", StartShow: true, Valid: validShow},
	})
	if store.entries[0].Valid != validEnabled || store.entries[1].Valid != validShow || store.entries[2].Name != "A" {
		t.Fatalf("duplicate-name merge reused an entry: %+v", store.entries)
	}
}

func TestLoadRejectsMissingRequiredCacheFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command_tray_host.cache")
	if err := os.WriteFile(path, []byte(`{"configs":[{"name":"demo","valid":4}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err == nil {
		t.Fatal("load succeeded, want missing field error")
	}
}

func TestOpenMigratesCacheWithoutSourceDigest(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	rawConfig := []byte("{}")
	if err := os.WriteFile(configPath, rawConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	cfg.SourceDigest = sha256.Sum256(rawConfig)
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stamp := config.FileStamp{ModTime: info.ModTime().UnixNano(), Size: info.Size(), Digest: cfg.SourceDigest}
	legacy := struct {
		ConfigModTime int64   `json:"config_mod_time"`
		ConfigSize    int64   `json:"config_size"`
		Configs       []entry `json:"configs"`
	}{
		ConfigModTime: stamp.ModTime,
		ConfigSize:    stamp.Size,
		Configs: []entry{{
			Name: "demo", Enabled: true, Alpha: 255,
		}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := load(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if before.HasSourceDigest {
		t.Fatal("legacy cache unexpectedly contains source_digest")
	}
	if _, err := OpenSnapshot(cachePath, &cfg, stamp, false); err != nil {
		t.Fatal(err)
	}
	after, err := load(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.HasSourceDigest || after.SourceDigest != cfg.SourceDigest {
		t.Fatalf("migrated cache digest = (%v, %v), want (%v, true)", after.SourceDigest, after.HasSourceDigest, cfg.SourceDigest)
	}
	if _, err := OpenSnapshot(cachePath, &cfg, stamp, false); err != nil {
		t.Fatal(err)
	}
}

func TestRebaseKeepsUnsavedStateAndUpdatesConfigTime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	falseValue := false
	oldConfig := config.Config{
		EnableCache:         &trueValue,
		DisableCacheEnabled: &falseValue,
		Configs: []config.EntryConfig{
			{Name: "a", Enabled: true},
			{Name: "b", Enabled: true},
		},
	}
	oldStore, err := Open(configPath, cachePath, &oldConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	oldStore.UpdateState(0, false, true)
	oldStore.UpdateState(1, true, false)

	changed := time.Now().Add(time.Second)
	if err := os.Chtimes(configPath, changed, changed); err != nil {
		t.Fatal(err)
	}
	newConfig := config.Config{
		EnableCache:         &trueValue,
		DisableCacheEnabled: &falseValue,
		Configs: []config.EntryConfig{
			{Name: "b", Enabled: false},
			{Name: "a", Enabled: true},
		},
	}
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	newStore, err := Rebase(cachePath, &newConfig, stamp, oldStore, KeepPrevious)
	if err != nil {
		t.Fatal(err)
	}
	if !newConfig.Configs[0].Enabled || newConfig.Configs[0].EffectiveStartShow() {
		t.Fatal("rebase did not preserve unsaved state for b")
	}
	if newConfig.Configs[1].Enabled || !newConfig.Configs[1].EffectiveStartShow() {
		t.Fatal("rebase did not preserve unsaved state for a")
	}
	if newStore.configModTime != changed.UnixNano() {
		t.Fatalf("configModTime = %d, want %d", newStore.configModTime, changed.UnixNano())
	}
}

func TestRebaseRenamePersistsNewCacheIdentity(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cachePath := filepath.Join(dir, "command_tray_host.cache")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	falseValue := false
	oldConfig := config.Config{
		EnableCache:         &trueValue,
		DisableCacheEnabled: &falseValue,
		Configs:             []config.EntryConfig{{Name: "old", Path: ".", Command: "demo.exe"}},
	}
	previous := newStore(cachePath, &oldConfig)
	previous.UpdateState(0, false, true)
	newConfig := config.Config{
		EnableCache:         &trueValue,
		DisableCacheEnabled: &falseValue,
		Configs:             []config.EntryConfig{{Name: "new", Path: ".", Command: "demo.exe"}},
	}
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Rebase(cachePath, &newConfig, stamp, previous, KeepPrevious)
	if err != nil {
		t.Fatal(err)
	}
	if store.entries[0].Name != "new" {
		t.Fatalf("rebased cache name = %q, want new", store.entries[0].Name)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	reloadedConfig := newConfig
	if _, err := OpenSnapshot(cachePath, &reloadedConfig, stamp, false); err != nil {
		t.Fatal(err)
	}
	if reloadedConfig.Configs[0].Enabled || !reloadedConfig.Configs[0].EffectiveStartShow() {
		t.Fatal("renamed entry did not restore cached state after reload")
	}
}

func TestRebaseDiscardClearsCachedState(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	cfg := testConfig()
	cfg.EnableCache = &trueValue
	previous := newStore("unused", &cfg)
	previous.UpdateShow(0, true)

	newConfig := testConfig()
	newConfig.EnableCache = &trueValue
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Rebase(filepath.Join(dir, "cache"), &newConfig, stamp, previous, DiscardPrevious)
	if err != nil {
		t.Fatal(err)
	}
	if store.entries[0].Valid != 0 {
		t.Fatalf("valid = %d, want 0", store.entries[0].Valid)
	}
	if newConfig.Configs[0].EffectiveStartShow() {
		t.Fatal("discard applied previous show state")
	}
}

func TestRebaseDisabledDoesNotRetainPreviousState(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	previousConfig := testConfig()
	previousConfig.EnableCache = &trueValue
	previous := newStore("unused", &previousConfig)
	previous.UpdateShow(0, true)
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	disabled := testConfig()
	disabled.EnableCache = boolPointer(false)
	store, err := Rebase(filepath.Join(dir, "cache"), &disabled, stamp, previous, KeepPrevious)
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Fatal("Rebase returned a store while cache is disabled")
	}
}

func TestRebaseMatchesDuplicateNamesByLaunchIdentity(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	oldConfig := config.Config{
		EnableCache: &trueValue,
		Configs: []config.EntryConfig{
			{Name: "same", Command: "one.exe"},
			{Name: "same", Command: "two.exe"},
		},
	}
	previous := newStore("unused", &oldConfig)
	previous.UpdateShow(0, true)
	previous.UpdateShow(1, false)
	newConfig := config.Config{
		EnableCache: &trueValue,
		Configs: []config.EntryConfig{
			{Name: "same", Command: "two.exe"},
			{Name: "same", Command: "one.exe"},
		},
	}
	stamp, err := config.StatFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rebase(filepath.Join(dir, "cache"), &newConfig, stamp, previous, KeepPrevious); err != nil {
		t.Fatal(err)
	}
	if newConfig.Configs[0].EffectiveStartShow() || !newConfig.Configs[1].EffectiveStartShow() {
		t.Fatal("duplicate-name states were not matched by launch identity")
	}
}

func testConfig() config.Config {
	return config.Config{Configs: []config.EntryConfig{{Name: "demo", Enabled: true}}}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
