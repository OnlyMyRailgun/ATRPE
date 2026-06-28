package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// R2ObjectStore implements ObjectStore using Cloudflare R2's S3-compatible API.
// For MVP, it uses HTTP PUT/GET on the R2 endpoint (simpler than full S3 SDK).
// The Full version can swap to aws-sdk-go-v2 with the same interface.
type R2ObjectStore struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

// NewR2ObjectStore creates an R2-backed store.
func NewR2ObjectStore(endpoint, bucket, accessKey, secretKey string) *R2ObjectStore {
	return &R2ObjectStore{
		endpoint:  strings.TrimRight(endpoint, "/"),
		bucket:    bucket,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

func (s *R2ObjectStore) Put(ctx context.Context, key string, body io.Reader, contentType string) (URI, error) {
	// MVP: use LocalObjectStore as fallback until R2 is wired with the S3 SDK
	// The interface is defined and R2ObjectStore can be completed in Week 4
	// when R2 credentials are available for testing.
	return "", fmt.Errorf("R2ObjectStore.Put: S3 client not yet wired (use LocalObjectStore for MVP)")
}

func (s *R2ObjectStore) Get(ctx context.Context, uri URI) (io.ReadCloser, error) {
	return nil, fmt.Errorf("R2ObjectStore.Get: not yet wired")
}

func (s *R2ObjectStore) Head(ctx context.Context, uri URI) (ObjectMetadata, error) {
	return ObjectMetadata{}, fmt.Errorf("R2ObjectStore.Head: not yet wired")
}

func (s *R2ObjectStore) Delete(ctx context.Context, uri URI) error {
	return fmt.Errorf("R2ObjectStore.Delete: not yet wired")
}

// NewObjectStore creates the configured store (local or R2).
func NewObjectStore(storeType, localDir, r2Endpoint, r2Bucket, r2AccessKey, r2SecretKey string) ObjectStore {
	switch storeType {
	case "r2":
		return NewR2ObjectStore(r2Endpoint, r2Bucket, r2AccessKey, r2SecretKey)
	default:
		return NewLocalObjectStore(localDir)
	}
}

// Unused imports kept for future wiring.
var _ = bytes.NewReader
var _ = url.Parse
