package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/OnlyMyRailgun/ATRPE/internal/objectstore"
)

// ArtifactMetaStore is the subset of KnowledgeStore needed by Repository.
type ArtifactMetaStore interface {
	SaveArtifactMeta(ctx context.Context, table, id, topicID string, uri objectstore.URI) error
	GetArtifactURI(ctx context.Context, table, id string) (objectstore.URI, error)
}

// Repository handles artifact persistence: JSON → ObjectStore, metadata → SQLite.
type Repository struct {
	meta  ArtifactMetaStore
	store objectstore.ObjectStore
}

// NewRepository creates an artifact repository.
func NewRepository(meta ArtifactMetaStore, store objectstore.ObjectStore) *Repository {
	return &Repository{meta: meta, store: store}
}

// SaveArtifact serializes the artifact to JSON, stores it in ObjectStore,
// and records the URI in the metadata store.
func (r *Repository) SaveArtifact(ctx context.Context, table, id, topicID string, artifact interface{}) (objectstore.URI, error) {
	data, err := json.Marshal(artifact)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	key := fmt.Sprintf("%s/%s.json", table, id)
	uri, err := r.store.Put(ctx, key, bytes.NewReader(data), "application/json")
	if err != nil {
		return "", fmt.Errorf("put object: %w", err)
	}

	if err := r.meta.SaveArtifactMeta(ctx, table, id, topicID, uri); err != nil {
		return "", fmt.Errorf("save meta: %w", err)
	}

	return uri, nil
}

// LoadArtifact retrieves an artifact from ObjectStore by looking up its URI
// in the metadata store, then deserializing the JSON content.
func (r *Repository) LoadArtifact(ctx context.Context, table, id string, target interface{}) error {
	uri, err := r.meta.GetArtifactURI(ctx, table, id)
	if err != nil {
		return fmt.Errorf("get uri: %w", err)
	}

	reader, err := r.store.Get(ctx, uri)
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read object: %w", err)
	}

	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	return nil
}
