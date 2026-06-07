package objectstore

import (
	"context"
	"io"
)

// URI is a pointer to stored object content.
type URI string

// ObjectMetadata contains basic metadata about a stored object.
type ObjectMetadata struct {
	ContentType string
	SizeBytes   int64
	ETag        string
}

// ObjectStore is the abstract interface for artifact storage.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) (URI, error)
	Get(ctx context.Context, uri URI) (io.ReadCloser, error)
	Head(ctx context.Context, uri URI) (ObjectMetadata, error)
	Delete(ctx context.Context, uri URI) error
}
