package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPutAndGetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	ctx := context.Background()
	content := "hello, storage"
	info, err := s.Put(ctx, "test.txt", bytes.NewReader([]byte(content)), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if info.Key != "test.txt" {
		t.Errorf("Put returned key %q, want %q", info.Key, "test.txt")
	}
	if info.Size != int64(len(content)) {
		t.Errorf("Put returned size %d, want %d", info.Size, len(content))
	}
	if info.ContentType != "text/plain" {
		t.Errorf("Put returned content type %q, want %q", info.ContentType, "text/plain")
	}

	rc, getInfo, err := s.Get(ctx, "test.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Errorf("Get returned %q, want %q", string(got), content)
	}
	if getInfo.Key != "test.txt" {
		t.Errorf("Get info key %q, want %q", getInfo.Key, "test.txt")
	}
	if getInfo.Size != int64(len(content)) {
		t.Errorf("Get info size %d, want %d", getInfo.Size, len(content))
	}
}

func TestExistsTrueAfterPut(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	ctx := context.Background()
	key := "exists-test.txt"

	exists, err := s.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists before Put: %v", err)
	}
	if exists {
		t.Error("Exists returned true before Put, want false")
	}

	_, err = s.Put(ctx, key, bytes.NewReader([]byte("data")), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	exists, err = s.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after Put: %v", err)
	}
	if !exists {
		t.Error("Exists returned false after Put, want true")
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	ctx := context.Background()
	key := "delete-test.txt"

	_, err = s.Put(ctx, key, bytes.NewReader([]byte("to be deleted")), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	err = s.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	exists, err := s.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after Delete: %v", err)
	}
	if exists {
		t.Error("Exists returned true after Delete, want false")
	}

	_, _, err = s.Get(ctx, key)
	if err == nil {
		t.Error("Get after Delete returned no error, want error")
	}
}

func TestKeySanitizationRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	ctx := context.Background()
	badKeys := []string{
		"../etc/passwd",
		"foo/../../etc/shadow",
		"foo/../bar",
		"/absolute/path.txt",
		"",
	}

	for _, key := range badKeys {
		t.Run("Put_"+key, func(t *testing.T) {
			_, err := s.Put(ctx, key, bytes.NewReader([]byte("x")), "text/plain")
			if err == nil {
				t.Errorf("Put(%q) should have returned an error", key)
			}
		})
		t.Run("Get_"+key, func(t *testing.T) {
			_, _, err := s.Get(ctx, key)
			if err == nil {
				t.Errorf("Get(%q) should have returned an error", key)
			}
		})
		t.Run("Delete_"+key, func(t *testing.T) {
			err := s.Delete(ctx, key)
			if err == nil {
				t.Errorf("Delete(%q) should have returned an error", key)
			}
		})
		t.Run("Exists_"+key, func(t *testing.T) {
			_, err := s.Exists(ctx, key)
			if err == nil {
				t.Errorf("Exists(%q) should have returned an error", key)
			}
		})
	}
}

func TestPutCreatesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	ctx := context.Background()
	key := "reports/2024/file.txt"
	content := "nested file content"

	_, err = s.Put(ctx, key, bytes.NewReader([]byte(content)), "text/plain")
	if err != nil {
		t.Fatalf("Put nested key: %v", err)
	}

	// Verify the file was actually created at the right path.
	fullPath := filepath.Join(dir, "reports", "2024", "file.txt")
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content %q, want %q", string(data), content)
	}

	// Verify Get also works for nested keys.
	rc, _, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get nested key: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Errorf("Get returned %q, want %q", string(got), content)
	}
}

func TestURL(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalStorage(dir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	got := s.URL("images/photo.png")
	want := "/storage/images/photo.png"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestNewLocalStorageCreatesDir(t *testing.T) {
	parent := t.TempDir()
	newDir := filepath.Join(parent, "sub", "dir")

	_, err := NewLocalStorage(newDir)
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	stat, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !stat.IsDir() {
		t.Errorf("expected %q to be a directory", newDir)
	}
}

// Verify LocalStorage satisfies the Storage interface at compile time.
var _ Storage = (*LocalStorage)(nil)
