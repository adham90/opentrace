package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// LocalStorage implements Storage using the local filesystem.
type LocalStorage struct {
	baseDir string
}

// NewLocalStorage creates a LocalStorage that stores files under baseDir.
// The directory is created if it does not already exist.
func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base directory: %w", err)
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

// Put stores data under the given key, creating parent directories as needed.
// The write is atomic: data is written to a temporary file first, then renamed.
func (s *LocalStorage) Put(_ context.Context, key string, data io.Reader, contentType string) (*FileInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	fullPath := filepath.Join(s.baseDir, key)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create directories for key %q: %w", key, err)
	}

	// Write to a temp file in the same directory so rename is atomic.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("storage: create temp file for key %q: %w", key, err)
	}
	tmpName := tmp.Name()

	// Clean up the temp file on any error path.
	success := false
	defer func() {
		if !success {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	written, err := io.Copy(tmp, data)
	if err != nil {
		return nil, fmt.Errorf("storage: write data for key %q: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("storage: close temp file for key %q: %w", key, err)
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		return nil, fmt.Errorf("storage: rename temp file for key %q: %w", key, err)
	}

	success = true
	return &FileInfo{
		Key:         key,
		Size:        written,
		ContentType: contentType,
	}, nil
}

// Get retrieves stored data by key. The caller must close the returned ReadCloser.
func (s *LocalStorage) Get(_ context.Context, key string) (io.ReadCloser, *FileInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, nil, err
	}

	fullPath := filepath.Join(s.baseDir, key)
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("storage: open key %q: %w", key, err)
	}

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("storage: stat key %q: %w", key, err)
	}

	// Read up to 512 bytes to detect content type, then seek back.
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	contentType := http.DetectContentType(buf[:n])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("storage: seek key %q: %w", key, err)
	}

	info := &FileInfo{
		Key:         key,
		Size:        stat.Size(),
		ContentType: contentType,
	}
	return f, info, nil
}

// Delete removes stored data by key.
func (s *LocalStorage) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	fullPath := filepath.Join(s.baseDir, key)
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("storage: delete key %q: %w", key, err)
	}
	return nil
}

// Exists checks whether a key exists in storage.
func (s *LocalStorage) Exists(_ context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}

	fullPath := filepath.Join(s.baseDir, key)
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("storage: check existence of key %q: %w", key, err)
}

// URL returns a relative URL path for accessing the stored file via HTTP.
func (s *LocalStorage) URL(key string) string {
	return "/storage/" + key
}

// validateKey rejects keys that are empty, contain path traversal sequences,
// or are absolute paths.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("storage: key must not be empty")
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("storage: key must not be an absolute path: %q", key)
	}
	for _, part := range strings.Split(key, "/") {
		if part == ".." {
			return fmt.Errorf("storage: key must not contain path traversal: %q", key)
		}
	}
	return nil
}
