package statecache

import (
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
