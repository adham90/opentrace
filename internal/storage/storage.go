package storage

import (
	"context"
	"io"
)

// FileInfo describes a stored file.
type FileInfo struct {
	Key         string
	Size        int64
	ContentType string
}

// Storage defines a pluggable file storage backend.
type Storage interface {
	// Put stores data under the given key.
	Put(ctx context.Context, key string, data io.Reader, contentType string) (*FileInfo, error)
	// Get retrieves stored data by key.
	Get(ctx context.Context, key string) (io.ReadCloser, *FileInfo, error)
	// Delete removes stored data by key.
	Delete(ctx context.Context, key string) error
	// Exists checks if a key exists in storage.
	Exists(ctx context.Context, key string) (bool, error)
	// URL returns a URL/path for accessing the stored file.
	URL(key string) string
}
