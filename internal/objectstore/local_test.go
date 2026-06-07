package objectstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalObjectStore_PutGet(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalObjectStore(dir)
	ctx := context.Background()

	content := "hello world"
	uri, err := store.Put(ctx, "test/hello.txt", strings.NewReader(content), "text/plain")
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	if uri == "" {
		t.Fatal("expected non-empty URI")
	}

	reader, err := store.Get(ctx, uri)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer reader.Close()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != content {
		t.Errorf("expected %q, got %q", content, string(got))
	}
}

func TestLocalObjectStore_Head(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalObjectStore(dir)
	ctx := context.Background()

	uri, _ := store.Put(ctx, "test/data.bin", strings.NewReader("12345"), "application/octet-stream")
	meta, err := store.Head(ctx, uri)
	if err != nil {
		t.Fatalf("Head error: %v", err)
	}
	if meta.SizeBytes != 5 {
		t.Errorf("expected SizeBytes=5, got %d", meta.SizeBytes)
	}
}

func TestLocalObjectStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalObjectStore(dir)
	ctx := context.Background()

	uri, _ := store.Put(ctx, "test/to-delete.txt", strings.NewReader("data"), "text/plain")
	if err := store.Delete(ctx, uri); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	_, err := store.Get(ctx, uri)
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestLocalObjectStore_Get_Missing(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalObjectStore(dir)
	_, err := store.Get(context.Background(), URI("file:///nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLocalObjectStore_NestedKeys(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalObjectStore(dir)
	ctx := context.Background()

	uri, _ := store.Put(ctx, "a/b/c/nested.txt", strings.NewReader("nested"), "text/plain")
	expectedPath := filepath.Join(dir, "a", "b", "c", "nested.txt")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected file at %s", expectedPath)
	}
	_ = uri
}
