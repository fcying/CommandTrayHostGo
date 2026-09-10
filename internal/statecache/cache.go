package statecache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

const (
	maxCacheSize    = 100 << 20
	maxCacheEntries = 3584
	validPosition   = 1 << iota
	validSize
	validEnabled
	validShow
	validAlpha
	validMask = validPosition | validSize | validEnabled | validShow | validAlpha
)

var ErrCacheExpired = errors.New("cache is not newer than config")

func Remove(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove cache: %w", err)
}

type Store struct {
	path          string
	config        *config.Config
	entries       []entry
	dirty         bool
	configModTime int64
	configSize    int64
}

type fileData struct {
	ConfigModTime    int64   `json:"config_mod_time"`
	HasConfigModTime bool    `json:"-"`
	ConfigSize       int64   `json:"config_size"`
	HasConfigSize    bool    `json:"-"`
	Configs          []entry `json:"configs"`
}

type entry struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	StartShow bool   `json:"start_show"`
	Left      int32  `json:"left"`
	Top       int32  `json:"top"`
	Right     int32  `json:"right"`
	Bottom    int32  `json:"bottom"`
	Alpha     int64  `json:"alpha"`
	Valid     uint8  `json:"valid"`
}

type WindowState struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
	Alpha  *int64
}

type RebasePolicy uint8

const (
	DiscardPrevious RebasePolicy = iota
	KeepPrevious
)

func Open(configPath, cachePath string, cfg *config.Config, allowExpired bool) (*Store, error) {
	stamp, err := config.StatFile(configPath)
	if err != nil {
		if cfg == nil || !cfg.CacheEnabled() {
			return nil, nil
		}
		return newStore(cachePath, cfg), fmt.Errorf("stat config for cache validation: %w", err)
	}
	return OpenSnapshot(cachePath, cfg, stamp, allowExpired)
}

func OpenSnapshot(cachePath string, cfg *config.Config, stamp config.FileStamp, allowExpired bool) (*Store, error) {
	if cfg == nil || !cfg.CacheEnabled() {
		return nil, nil
	}
	store := newStore(cachePath, cfg)
	store.configModTime = stamp.ModTime
	store.configSize = stamp.Size
	cacheInfo, err := os.Stat(cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return store, store.Save()
	}
	if err != nil {
		return store, fmt.Errorf("stat cache: %w", err)
	}
	data, err := load(cachePath)
	if err != nil {
		if saveErr := store.Save(); saveErr != nil {
			return store, errors.Join(err, saveErr)
		}
		return store, fmt.Errorf("invalid cache was discarded: %w", err)
	}
	expired := false
	if data.HasConfigModTime {
		expired = data.ConfigModTime != store.configModTime || (data.HasConfigSize && data.ConfigSize != store.configSize)
	} else {
		expired = cacheInfo.ModTime().UnixNano() <= stamp.ModTime
	}
	if expired && !allowExpired {
		return store, ErrCacheExpired
	}
	store.dirty = false
	if data.HasConfigModTime && !data.HasConfigSize {
		store.dirty = true
	}
	if store.merge(data.Configs) {
		store.dirty = true
	}
	store.apply()
	if expired {
		store.dirty = true
	}
	if store.dirty {
		if err := store.Save(); err != nil {
			return store, err
		}
	}
	return store, nil
}

func OpenStartupSnapshot(cachePath string, cfg *config.Config, stamp config.FileStamp) (*Store, error) {
	return OpenSnapshot(cachePath, cfg, stamp, cfg != nil && cfg.AutoHotReloading)
}

func newStore(path string, cfg *config.Config) *Store {
	entries := make([]entry, len(cfg.Configs))
	for i := range cfg.Configs {
		entries[i] = entry{
			Name:      cfg.Configs[i].Name,
			Enabled:   cfg.Configs[i].Enabled,
			StartShow: cfg.Configs[i].EffectiveStartShow(),
			Alpha:     255,
		}
		if cfg.Configs[i].Alpha != nil {
			entries[i].Alpha = *cfg.Configs[i].Alpha
		}
	}
	return &Store{path: path, config: cfg, entries: entries, dirty: true}
}

func Rebase(cachePath string, cfg *config.Config, stamp config.FileStamp, previous *Store, policy RebasePolicy) (*Store, error) {
	if cfg == nil {
		return nil, nil
	}
	store := newStore(cachePath, cfg)
	store.configModTime = stamp.ModTime
	store.configSize = stamp.Size
	if policy == KeepPrevious && cfg.CacheEnabled() {
		if previous != nil {
			store.mergePrevious(previous)
		} else {
			data, err := load(cachePath)
			if err == nil {
				store.merge(data.Configs)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
	if cfg.CacheEnabled() {
		store.apply()
	} else {
		return nil, nil
	}
	store.dirty = true
	return store, nil
}

func load(path string) (fileData, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileData{}, fmt.Errorf("open cache: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCacheSize+1))
	if err != nil {
		return fileData{}, fmt.Errorf("read cache: %w", err)
	}
	if len(raw) > maxCacheSize {
		return fileData{}, fmt.Errorf("cache exceeds the %d MiB limit", maxCacheSize>>20)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return fileData{}, fmt.Errorf("decode cache: %w", err)
	}
	rawConfigs, ok := root["configs"]
	if !ok || string(rawConfigs) == "null" {
		return fileData{}, errors.New("cache is missing required field configs")
	}
	decoder := json.NewDecoder(bytes.NewReader(rawConfigs))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return fileData{}, errors.New("cache field configs must be an array of objects")
	}
	rawEntries := make([]json.RawMessage, 0)
	for decoder.More() {
		if len(rawEntries) == maxCacheEntries {
			return fileData{}, fmt.Errorf("cache contains more than %d configs", maxCacheEntries)
		}
		var rawEntry json.RawMessage
		if err := decoder.Decode(&rawEntry); err != nil {
			return fileData{}, errors.New("cache field configs must be an array of objects")
		}
		rawEntries = append(rawEntries, rawEntry)
	}
	if _, err := decoder.Token(); err != nil {
		return fileData{}, errors.New("cache field configs must be an array of objects")
	}

	data := fileData{Configs: make([]entry, len(rawEntries))}
	if rawConfigModTime, exists := root["config_mod_time"]; exists {
		if string(rawConfigModTime) == "null" {
			return fileData{}, errors.New("cache field config_mod_time must not be null")
		}
		if err := json.Unmarshal(rawConfigModTime, &data.ConfigModTime); err != nil {
			return fileData{}, errors.New("cache field config_mod_time must be an integer")
		}
		data.HasConfigModTime = true
	}
	if rawConfigSize, exists := root["config_size"]; exists {
		if string(rawConfigSize) == "null" {
			return fileData{}, errors.New("cache field config_size must not be null")
		}
		if err := json.Unmarshal(rawConfigSize, &data.ConfigSize); err != nil || data.ConfigSize < 0 {
			return fileData{}, errors.New("cache field config_size must be a non-negative integer")
		}
		data.HasConfigSize = true
	}
	for i, rawEntry := range rawEntries {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawEntry, &fields); err != nil {
			return fileData{}, fmt.Errorf("configs[%d] must be an object", i)
		}
		for _, field := range [...]string{"name", "enabled", "start_show", "left", "top", "right", "bottom", "valid"} {
			value, exists := fields[field]
			if !exists || string(value) == "null" {
				return fileData{}, fmt.Errorf("configs[%d] is missing required field %s", i, field)
			}
		}
		if alpha, exists := fields["alpha"]; exists && string(alpha) == "null" {
			return fileData{}, fmt.Errorf("configs[%d].alpha must not be null", i)
		}
		if err := json.Unmarshal(rawEntry, &data.Configs[i]); err != nil {
			return fileData{}, fmt.Errorf("decode configs[%d]: %w", i, err)
		}
		item := data.Configs[i]
		if _, exists := fields["alpha"]; !exists {
			data.Configs[i].Alpha = 255
			data.Configs[i].Valid &^= validAlpha
			item = data.Configs[i]
		}
		if item.Name == "" {
			return fileData{}, fmt.Errorf("configs[%d].name must not be empty", i)
		}
		if item.Valid&^uint8(validMask) != 0 {
			return fileData{}, fmt.Errorf("configs[%d].valid contains unknown bits", i)
		}
		if item.Alpha < 0 || item.Alpha > 255 {
			return fileData{}, fmt.Errorf("configs[%d].alpha must be between 0 and 255", i)
		}
		if item.Valid&validSize != 0 && (item.Right < item.Left || item.Bottom < item.Top) {
			return fileData{}, fmt.Errorf("configs[%d] has an invalid window rectangle", i)
		}
	}
	return data, nil
}

func (s *Store) merge(cached []entry) bool {
	used := make([]bool, len(cached))
	changed := len(cached) != len(s.entries)
	for i := range s.entries {
		match := -1
		if i < len(cached) && !used[i] && cached[i].Name == s.entries[i].Name {
			match = i
		} else {
			for j := range cached {
				if !used[j] && cached[j].Name == s.entries[i].Name {
					match = j
					break
				}
			}
		}
		if match >= 0 {
			s.entries[i] = cached[match]
			used[match] = true
			if match != i {
				changed = true
			}
		} else {
			changed = true
		}
	}
	return changed
}

func (s *Store) mergePrevious(previous *Store) {
	used := make([]bool, len(previous.entries))
	for i := range s.entries {
		match := -1
		for j := range previous.entries {
			if !used[j] && s.entries[i].Name == previous.entries[j].Name &&
				s.config.Configs[i].SameLaunchIdentity(previous.config.Configs[j]) {
				match = j
				break
			}
		}
		if match < 0 {
			for j := range previous.entries {
				if !used[j] && s.config.Configs[i].SameLaunchIdentity(previous.config.Configs[j]) {
					match = j
					break
				}
			}
		}
		if match < 0 {
			for j := range previous.entries {
				if !used[j] && s.entries[i].Name == previous.entries[j].Name {
					match = j
					break
				}
			}
		}
		if match >= 0 {
			s.entries[i] = previous.entries[match]
			used[match] = true
		}
	}
}

func (s *Store) apply() {
	for i := range s.entries {
		cached := s.entries[i]
		entryConfig := &s.config.Configs[i]
		if s.config.CachePositionEnabled() && cached.Valid&validPosition != 0 {
			entryConfig.CachedPosition = &config.PixelPair{cached.Left, cached.Top}
		}
		if s.config.CacheSizeEnabled() && cached.Valid&validSize != 0 {
			entryConfig.CachedSize = &config.PixelPair{cached.Right - cached.Left, cached.Bottom - cached.Top}
		}
		if s.config.CacheEnabledStateEnabled() && cached.Valid&validEnabled != 0 {
			entryConfig.Enabled = cached.Enabled
		}
		if s.config.CacheShowEnabled() && cached.Valid&validShow != 0 {
			show := cached.StartShow
			entryConfig.StartShow = &show
			entryConfig.CachedShow = &show
		}
		if s.config.CacheAlphaEnabled() && entryConfig.Alpha != nil && cached.Valid&validAlpha != 0 {
			alpha := cached.Alpha
			entryConfig.CachedAlpha = &alpha
		}
	}
}

func (s *Store) UpdateState(index int, enabled, show bool) {
	if s == nil || index < 0 || index >= len(s.entries) {
		return
	}
	item := &s.entries[index]
	if s.config.CacheEnabledStateEnabled() && (item.Enabled != enabled || item.Valid&validEnabled == 0) {
		item.Enabled = enabled
		item.Valid |= validEnabled
		s.dirty = true
	}
	if s.config.CacheShowEnabled() && (item.StartShow != show || item.Valid&validShow == 0) {
		item.StartShow = show
		item.Valid |= validShow
		s.dirty = true
	}
}

func (s *Store) UpdateEnabled(index int, enabled bool) {
	if s == nil || index < 0 || index >= len(s.entries) || !s.config.CacheEnabledStateEnabled() {
		return
	}
	item := &s.entries[index]
	if item.Enabled != enabled || item.Valid&validEnabled == 0 {
		item.Enabled = enabled
		item.Valid |= validEnabled
		s.dirty = true
	}
}

func (s *Store) UpdateShow(index int, show bool) {
	if s == nil || index < 0 || index >= len(s.entries) || !s.config.CacheShowEnabled() {
		return
	}
	item := &s.entries[index]
	if item.StartShow != show || item.Valid&validShow == 0 {
		item.StartShow = show
		item.Valid |= validShow
		s.dirty = true
	}
}

func (s *Store) UpdateWindow(index int, state WindowState) {
	if s == nil || index < 0 || index >= len(s.entries) {
		return
	}
	item := &s.entries[index]
	oldWidth := int64(item.Right) - int64(item.Left)
	oldHeight := int64(item.Bottom) - int64(item.Top)
	newWidth := int64(state.Right) - int64(state.Left)
	newHeight := int64(state.Bottom) - int64(state.Top)
	if s.config.CachePositionEnabled() && (item.Left != state.Left || item.Top != state.Top || item.Valid&validPosition == 0) {
		item.Left = state.Left
		item.Top = state.Top
		item.Valid |= validPosition
		s.dirty = true
	}
	if s.config.CacheSizeEnabled() && (oldWidth != newWidth || oldHeight != newHeight || item.Valid&validSize == 0) {
		item.Left = state.Left
		item.Top = state.Top
		item.Right = state.Right
		item.Bottom = state.Bottom
		item.Valid |= validSize
		s.dirty = true
	}
	if state.Alpha != nil {
		s.UpdateAlpha(index, *state.Alpha)
	}
}

func (s *Store) UpdateAlpha(index int, alpha int64) {
	if s == nil || index < 0 || index >= len(s.entries) ||
		!s.config.CacheAlphaEnabled() || s.config.Configs[index].Alpha == nil {
		return
	}
	item := &s.entries[index]
	if item.Alpha != alpha || item.Valid&validAlpha == 0 {
		item.Alpha = alpha
		item.Valid |= validAlpha
		s.dirty = true
	}
}

func (s *Store) NeedsWindowState(index int) bool {
	if s == nil || index < 0 || index >= len(s.entries) {
		return false
	}
	return s.config.CachePositionEnabled() || s.config.CacheSizeEnabled() ||
		(s.config.CacheAlphaEnabled() && s.config.Configs[index].Alpha != nil)
}

func (s *Store) NeedsAlpha(index int) bool {
	return s != nil && index >= 0 && index < len(s.entries) &&
		s.config.CacheAlphaEnabled() && s.config.Configs[index].Alpha != nil
}

func (s *Store) Save() error {
	if s == nil || !s.dirty {
		return nil
	}
	dir := filepath.Dir(s.path)
	temp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create cache temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	encoder := json.NewEncoder(temp)
	if err := encoder.Encode(fileData{ConfigModTime: s.configModTime, ConfigSize: s.configSize, Configs: s.entries}); err != nil {
		temp.Close()
		return fmt.Errorf("encode cache: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync cache: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close cache temp file: %w", err)
	}
	if err := replaceFile(tempPath, s.path); err != nil {
		return fmt.Errorf("replace cache: %w", err)
	}
	s.dirty = false
	return nil
}
