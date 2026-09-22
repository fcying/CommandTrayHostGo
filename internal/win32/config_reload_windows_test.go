//go:build windows && amd64

package win32

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestConfigReloadDetectsSameStampMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte("config-A")
	changed := []byte("config-B")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	app := &TrayApp{
		configPath: path,
		observedConfigStamp: config.FileStamp{
			ModTime: info.ModTime().UnixNano(),
			Size:    int64(len(original)),
			Digest:  sha256.Sum256(original),
		},
	}
	if err := os.WriteFile(path, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	stamp, err := config.StatFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp, changedFlag, err := app.changedConfigStamp(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if !changedFlag {
		t.Fatal("same-stamp content mutation was ignored")
	}
	if stamp.Digest == app.observedConfigStamp.Digest {
		t.Fatal("same-stamp mutation retained the old digest")
	}
}
