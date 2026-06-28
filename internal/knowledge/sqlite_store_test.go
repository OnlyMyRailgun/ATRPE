package knowledge

import (
	"context"
	"testing"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

func setupStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSaveAndGetTopicCandidate(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	candidate := artifacts.TopicCandidate{
		ID: "abc123", Source: "github_trending", Title: "Kubernetes Operators in Go",
		URL: "https://github.com/example/operator", Score: 0.85, CreatedAt: time.Now().UTC(),
	}

	if err := store.SaveTopicCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveTopicCandidate error: %v", err)
	}

	got, err := store.GetTopicCandidate(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetTopicCandidate error: %v", err)
	}
	if got.Title != candidate.Title {
		t.Errorf("title mismatch: %s != %s", got.Title, candidate.Title)
	}
}

func TestListTopicCandidates(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	for _, c := range []artifacts.TopicCandidate{
		{ID: "a", Source: "s1", Title: "T1", URL: "u1", Score: 0.9, CreatedAt: time.Now()},
		{ID: "b", Source: "s2", Title: "T2", URL: "u2", Score: 0.5, CreatedAt: time.Now()},
		{ID: "c", Source: "s3", Title: "T3", URL: "u3", Score: 0.1, CreatedAt: time.Now()},
	} {
		store.SaveTopicCandidate(ctx, c)
	}

	list, err := store.ListTopicCandidates(ctx, 2)
	if err != nil {
		t.Fatalf("ListTopicCandidates error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(list))
	}
}

func TestSaveTechnicalBrief(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	uri := artifacts.URI("file:///data/artifacts/briefs/b1.json")
	if err := store.SaveTechnicalBrief(ctx, "b1", "topic-1", uri); err != nil {
		t.Fatalf("SaveTechnicalBrief error: %v", err)
	}
}

func TestSavePublishedArticle(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	article := artifacts.PublishedArticle{
		ID: "pub-1", Slug: "my-go-article", Title: "My Go Article",
		PublishedAt: time.Now().UTC(), Platform: "zenn",
		URL: "https://zenn.dev/example/articles/my-go-article",
	}
	if err := store.SavePublishedArticle(ctx, article); err != nil {
		t.Fatalf("SavePublishedArticle error: %v", err)
	}
}

func TestRegisterCitation(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	citation := artifacts.CitationRecord{
		ID: "cite-1", SourceURL: "https://go.dev/doc/effective_go",
		ContentHash: "abc123hash", HashAlgorithm: "sha256",
		RetrievedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.RegisterCitation(ctx, citation); err != nil {
		t.Fatalf("RegisterCitation error: %v", err)
	}
	// Duplicate should be ignored (UNIQUE constraint)
	if err := store.RegisterCitation(ctx, citation); err != nil {
		t.Fatalf("RegisterCitation duplicate error: %v", err)
	}
}

func TestSaveArtifactMeta(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	uri := artifacts.URI("file:///data/artifacts/design_artifacts/d1.json")
	if err := store.SaveArtifactMeta(ctx, "design_artifacts", "d1", "topic-1", uri); err != nil {
		t.Fatalf("SaveArtifactMeta error: %v", err)
	}

	gotURI, err := store.GetArtifactURI(ctx, "design_artifacts", "d1")
	if err != nil {
		t.Fatalf("GetArtifactURI error: %v", err)
	}
	if gotURI != uri {
		t.Errorf("expected URI %s, got %s", uri, gotURI)
	}
}
