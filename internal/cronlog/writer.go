package cronlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	defaultSizeLimit   = 10 << 20
	defaultRotateLimit = 499
)

type Writer struct {
	mu          sync.Mutex
	baseDir     string
	sizeLimit   int64
	rotateLimit int
}

func New(baseDir string) *Writer {
	return &Writer{
		baseDir:     baseDir,
		sizeLimit:   defaultSizeLimit,
		rotateLimit: defaultRotateLimit,
	}
}

func (w *Writer) Write(path, line string) error {
	if path == "" {
		return errors.New("cron log path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.baseDir, path)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var rotateErr error
	info, err := os.Stat(path)
	if err == nil && info.Size() > w.sizeLimit {
		rotateErr = rotate(path, w.rotateLimit)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		rotateErr = fmt.Errorf("stat cron log %q: %w", path, err)
	}

	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil {
		return errors.Join(rotateErr, fmt.Errorf("open cron log %q: %w", path, openErr))
	}
	content := line + "\r\n"
	written, writeErr := io.WriteString(file, content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("append cron log %q: %w", path, writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close cron log %q: %w", path, closeErr)
	}
	return errors.Join(rotateErr, writeErr, closeErr)
}

func rotate(path string, limit int) error {
	for i := 1; i <= limit; i++ {
		rotatedPath := path + "." + strconv.Itoa(i)
		_, err := os.Lstat(rotatedPath)
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat rotated cron log %q: %w", rotatedPath, err)
		}
		if err := os.Rename(path, rotatedPath); err != nil {
			return fmt.Errorf("rotate cron log %q to %q: %w", path, rotatedPath, err)
		}
		return nil
	}
	return fmt.Errorf("rotate cron log %q: all %d destinations are occupied", path, limit)
}
