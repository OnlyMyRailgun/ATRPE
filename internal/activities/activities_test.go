package activities

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/knowledge"
	"github.com/OnlyMyRailgun/ATRPE/internal/objectstore"
	"github.com/stretchr/testify/require"
)

func setupTestActivities(t *testing.T) (*Activities, *knowledge.SQLiteStore) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := knowledge.NewSQLiteStore(dbPath)
	require.NoError(t, err)

	objects := objectstore.NewLocalObjectStore(dir)

	acts := &Activities{
		Store:   store,
		Objects: objects,
	}
	return acts, store
}

func TestCleanupWorkspace_Success(t *testing.T) {
	acts, _ := setupTestActivities(t)
	ctx := context.Background()

	// Create a temp workspace
	dir := t.TempDir()
	require.DirExists(t, dir)

	// Successful workspace should be retained (not old enough)
	err := acts.CleanupWorkspace(ctx, CleanupWorkspaceInput{
		Workdir: dir,
		Outcome: "success",
	})
	require.NoError(t, err)
	require.DirExists(t, dir) // still exists because < 24h
}

func TestCleanupWorkspace_Abort(t *testing.T) {
	acts, _ := setupTestActivities(t)
	ctx := context.Background()

	dir := t.TempDir()
	require.DirExists(t, dir)

	err := acts.CleanupWorkspace(ctx, CleanupWorkspaceInput{
		Workdir: dir,
		Outcome: "abort",
	})
	require.NoError(t, err)
	_, statErr := os.Stat(dir)
	require.True(t, os.IsNotExist(statErr), "workspace should be deleted immediately on abort")
}

func TestCleanupWorkspace_Nonexistent(t *testing.T) {
	acts, _ := setupTestActivities(t)
	ctx := context.Background()

	err := acts.CleanupWorkspace(ctx, CleanupWorkspaceInput{
		Workdir: "/tmp/nonexistent-atrpe-test",
		Outcome: "success",
	})
	require.NoError(t, err) // should not error on missing dir
}

func TestCleanupWorkspace_Failure_Old(t *testing.T) {
	acts, _ := setupTestActivities(t)
	ctx := context.Background()

	dir := t.TempDir()

	// Make the dir itself look 73h old
	oldTime := time.Now().Add(-73 * time.Hour)
	require.NoError(t, os.Chtimes(dir, oldTime, oldTime))

	err := acts.CleanupWorkspace(ctx, CleanupWorkspaceInput{
		Workdir: dir,
		Outcome: "failure",
	})
	require.NoError(t, err)
	_, statErr := os.Stat(dir)
	require.True(t, os.IsNotExist(statErr), "workspace older than 72h should be deleted")
}

func TestDiscoverTopics_StoresCandidates(t *testing.T) {
	_, store := setupTestActivities(t)
	ctx := context.Background()

	// Insert a candidate directly
	err := store.SaveTopicCandidate(ctx, artifacts.TopicCandidate{
		ID:        "test-001",
		Source:    "github_trending",
		Title:     "test-repo",
		URL:       "https://example.com",
		Score:     0.95,
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	// Verify retrieval
	c, err := store.GetTopicCandidate(ctx, "test-001")
	require.NoError(t, err)
	require.Equal(t, "test-repo", c.Title)
	require.Equal(t, 0.95, c.Score)
}

func TestResolveCandidateID_ByIndex(t *testing.T) {
	acts, store := setupTestActivities(t)
	ctx := context.Background()

	require.NoError(t, store.SaveTopicCandidate(ctx, artifacts.TopicCandidate{
		ID: "cand-1", Source: "test", Title: "first", URL: "a", Score: 1.0, CreatedAt: time.Now(),
	}))
	require.NoError(t, store.SaveTopicCandidate(ctx, artifacts.TopicCandidate{
		ID: "cand-2", Source: "test", Title: "second", URL: "b", Score: 0.9, CreatedAt: time.Now(),
	}))

	result, err := acts.ResolveCandidateID(ctx, ResolveCandidateInput{Selection: "1"})
	require.NoError(t, err)
	require.Equal(t, "cand-1", result.CandidateID) // highest score (1.0) = position 1
}

func TestResolveCandidateID_DirectID(t *testing.T) {
	acts, store := setupTestActivities(t)
	ctx := context.Background()

	_ = store.SaveTopicCandidate(ctx, artifacts.TopicCandidate{
		ID: "abc123def456", Source: "test", Title: "repo", URL: "x", Score: 1.0, CreatedAt: time.Now(),
	})

	result, err := acts.ResolveCandidateID(ctx, ResolveCandidateInput{Selection: "abc123def456"})
	require.NoError(t, err)
	require.Equal(t, "abc123def456", result.CandidateID)
}

func TestCollectEngagementMetrics(t *testing.T) {
	acts, _ := setupTestActivities(t)
	ctx := context.Background()

	// Testing with a non-existent slug — should not error, just return empty
	result, err := acts.CollectEngagementMetrics(ctx, CollectEngagementInput{
		Slugs: []string{"non-existent-article-slug"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
}
