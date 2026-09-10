package cronlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestWriteAppendsUTF8Lines(t *testing.T) {
	dir := t.TempDir()
	writer := New(dir)
	if err := writer.Write("cron.log", "first"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write("cron.log", "第二行"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dir, "cron.log"), "first\r\n第二行\r\n")
}

func TestWriteResolvesRelativeAndAbsolutePaths(t *testing.T) {
	baseDir := t.TempDir()
	absoluteDir := t.TempDir()
	absolutePath := filepath.Join(absoluteDir, "absolute.log")
	writer := New(baseDir)

	if err := os.Mkdir(filepath.Join(baseDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(filepath.Join("logs", "relative.log"), "relative"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(absolutePath, "absolute"); err != nil {
		t.Fatal(err)
	}

	assertFileContent(t, filepath.Join(baseDir, "logs", "relative.log"), "relative\r\n")
	assertFileContent(t, absolutePath, "absolute\r\n")
}

func TestWriteRejectsEmptyPath(t *testing.T) {
	if err := New(t.TempDir()).Write("", "must not be written"); err == nil {
		t.Fatal("Write succeeded with an empty path")
	}
}

func TestWriteRotatesOnlyWhenSizeExceedsLimit(t *testing.T) {
	tests := []struct {
		name       string
		size       int64
		wantRotate bool
	}{
		{name: "equal", size: defaultSizeLimit},
		{name: "greater", size: defaultSizeLimit + 1, wantRotate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cron.log")
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(path, tt.size); err != nil {
				t.Fatal(err)
			}
			if err := New(dir).Write("cron.log", "next"); err != nil {
				t.Fatal(err)
			}

			rotatedInfo, err := os.Stat(path + ".1")
			if tt.wantRotate {
				if err != nil {
					t.Fatalf("stat rotated file: %v", err)
				}
				if rotatedInfo.Size() != tt.size {
					t.Fatalf("rotated size = %d, want %d", rotatedInfo.Size(), tt.size)
				}
				assertFileContent(t, path, "next\r\n")
				return
			}
			if !os.IsNotExist(err) {
				t.Fatalf("rotated file exists or stat failed: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != tt.size+int64(len("next\r\n")) {
				t.Fatalf("log size = %d, want %d", info.Size(), tt.size+int64(len("next\r\n")))
			}
		})
	}
}

func TestWriteUsesFirstAvailableRotationPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.log")
	writeFile(t, path, "xx")
	if err := os.WriteFile(path+".1", []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := New(dir)
	writer.sizeLimit = 1

	if err := writer.Write("cron.log", "new"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path+".1", "occupied")
	assertFileContent(t, path+".2", "xx")
	assertFileContent(t, path, "new\r\n")
}

func TestWriteAppendsOriginalWhenAllRotationPathsOccupied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.log")
	writeFile(t, path, "xx")
	for i := 1; i <= 2; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writer := New(dir)
	writer.sizeLimit = 1
	writer.rotateLimit = 2

	err := writer.Write("cron.log", "new")
	if err == nil || !strings.Contains(err.Error(), "all 2 destinations are occupied") {
		t.Fatalf("Write error = %v, want occupied rotation error", err)
	}
	assertFileContent(t, path, "xxnew\r\n")
}

func TestConcurrentWritesToSamePath(t *testing.T) {
	dir := t.TempDir()
	writer := New(dir)
	const count = 100
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writer.Write("cron.log", fmt.Sprintf("line-%03d", i)); err != nil {
				t.Errorf("Write: %v", err)
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, "cron.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\r\n"), "\r\n")
	if len(lines) != count {
		t.Fatalf("line count = %d, want %d", len(lines), count)
	}
	sort.Strings(lines)
	for i, line := range lines {
		want := fmt.Sprintf("line-%03d", i)
		if line != want {
			t.Fatalf("line %d = %q, want %q", i, line, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
