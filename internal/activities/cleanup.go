package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// CleanupWorkspaceInput specifies which workspace to clean up.
type CleanupWorkspaceInput struct {
	Workdir string `json:"workdir"`
	Outcome string `json:"outcome"` // "success", "failure", "abort"
}

// CleanupWorkspace deletes experiment workspaces based on the retention policy.
// - success: retain 24h then delete
// - failure: retain 72h then delete
// - abort: delete immediately
func (a *Activities) CleanupWorkspace(ctx context.Context, input CleanupWorkspaceInput) error {
	info, err := os.Stat(input.Workdir)
	if os.IsNotExist(err) {
		return nil // already cleaned
	}
	if err != nil {
		return fmt.Errorf("stat workspace: %w", err)
	}

	switch input.Outcome {
	case "abort":
		return os.RemoveAll(input.Workdir)
	case "failure":
		if time.Since(info.ModTime()) > 72*time.Hour {
			return os.RemoveAll(input.Workdir)
		}
	case "success":
		if time.Since(info.ModTime()) > 24*time.Hour {
			return os.RemoveAll(input.Workdir)
		}
	}
	return nil
}

// -- Engagement Metrics Collection --

type zennArticleStats struct {
	Article struct {
		ID             int    `json:"id"`
		Title          string `json:"title"`
		Slug           string `json:"slug"`
		LikedCount     int    `json:"liked_count"`
		CommentsCount  int    `json:"comments_count"`
		BookmarkCount  int    `json:"bookmarked_count"`
		PublishedAt    string `json:"published_at"`
	} `json:"article"`
}

// CollectEngagementInput specifies which articles to collect metrics for.
type CollectEngagementInput struct {
	Slugs []string `json:"slugs"` // Zenn article slugs to check
}

// CollectEngagementResult contains updated metrics for each article.
type CollectEngagementResult struct {
	Metrics []artifacts.EngagementMetrics `json:"metrics"`
}

// CollectEngagementMetrics queries the Zenn API for article engagement data.
func (a *Activities) CollectEngagementMetrics(ctx context.Context, input CollectEngagementInput) (*CollectEngagementResult, error) {
	var result CollectEngagementResult

	for _, slug := range input.Slugs {
		url := fmt.Sprintf("https://zenn.dev/api/articles/%s", slug)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}

		var stats zennArticleStats
		if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		metrics := artifacts.EngagementMetrics{
			TopicID:     slug,
			Platform:    "zenn",
			PublishDate: stats.Article.PublishedAt,
			Views:       stats.Article.LikedCount, // Zenn API doesn't expose views directly; use likes as proxy
			Likes:       stats.Article.LikedCount,
		}

		if err := a.Store.SaveEngagementMetrics(ctx, metrics); err != nil {
			fmt.Printf("⚠️ Failed to save engagement metrics for %s: %v\n", slug, err)
			continue
		}
		result.Metrics = append(result.Metrics, metrics)
	}

	return &result, nil
}
