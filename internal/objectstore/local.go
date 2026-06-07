package objectstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalObjectStore implements ObjectStore using the local filesystem.
type LocalObjectStore struct {
	rootDir string
}

// NewLocalObjectStore creates a local store rooted at the given directory.
func NewLocalObjectStore(rootDir string) *LocalObjectStore {
	return &LocalObjectStore{rootDir: rootDir}
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, body io.Reader, contentType string) (URI, error) {
	fullPath := filepath.Join(s.rootDir, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, body); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return URI("file://" + fullPath), nil
}

func (s *LocalObjectStore) Get(ctx context.Context, uri URI) (io.ReadCloser, error) {
	path := strings.TrimPrefix(string(uri), "file://")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return f, nil
}

func (s *LocalObjectStore) Head(ctx context.Context, uri URI) (ObjectMetadata, error) {
	path := strings.TrimPrefix(string(uri), "file://")
	info, err := os.Stat(path)
	if err != nil {
		return ObjectMetadata{}, fmt.Errorf("stat: %w", err)
	}
	return ObjectMetadata{
		ContentType: "application/octet-stream",
		SizeBytes:   info.Size(),
	}, nil
}

func (s *LocalObjectStore) Delete(ctx context.Context, uri URI) error {
	path := strings.TrimPrefix(string(uri), "file://")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	return nil
}
