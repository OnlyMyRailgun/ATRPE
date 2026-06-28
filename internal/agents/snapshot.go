package agents

import (
	"context"
	"io"
)

// SnapshotStore persists raw fetched pages so they survive beyond the current workflow.
// Implementations: objectstore.ObjectStore (via adapter).
type SnapshotStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) error
}

// CitationStore records a fetched source's metadata for later audit.
// Implementations: knowledge.SQLiteStore (via adapter).
type CitationStore interface {
	RegisterCitation(ctx context.Context, url, contentHash, retrievedAt string) error
}

// SourcesSnapshotKey builds an ObjectStore key for a raw HTML snapshot.
// Format: research_snapshots/{content_hash}.html
func SourcesSnapshotKey(contentHash string) string {
	return "research_snapshots/" + contentHash + ".html"
}
