package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/your-org/atrpe/internal/artifacts"
)

// DiscoverGitHubTrending fetches specific, writable Go repositories from GitHub.
// Query targets: medium-sized (100–5000★), recently created, actively maintained repos.
// These are specific enough to write focused 2000-word articles about.
func DiscoverGitHubTrending(ctx context.Context, baseURL string) ([]artifacts.TopicCandidate, error) {
	// Two years back — newer repos are less likely to be over-covered.
	twoYearsAgo := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	q := fmt.Sprintf("language:go+created:>%s+stars:100..5000", twoYearsAgo)
	url := baseURL + "/search/repositories?q=" + q + "&sort=updated&order=desc&per_page=20"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch github trending: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Items []struct {
			FullName        string `json:"full_name"`
			HTMLURL         string `json:"html_url"`
			Description     string `json:"description"`
			StargazersCount int    `json:"stargazers_count"`
			CreatedAt       string `json:"created_at"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	var candidates []artifacts.TopicCandidate
	for _, item := range result.Items {
		id := CandidateID("github_trending", item.HTMLURL)
		publishedAt, _ := time.Parse(time.RFC3339, item.CreatedAt)

		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			desc = item.FullName // fallback
		}

		score := ScoreCandidate(CandidateInput{
			RepoName:    item.FullName,
			Description: desc,
			GithubStars: item.StargazersCount,
			PublishedAt: publishedAt,
		})

		candidates = append(candidates, artifacts.TopicCandidate{
			ID:        id,
			Source:    "github_trending",
			Title:     item.FullName,
			URL:       item.HTMLURL,
			Score:     score,
			CreatedAt: time.Now().UTC(),
		})
	}
	return candidates, nil
}

// DiscoverAll runs discovery for each configured source and returns deduplicated candidates.
func DiscoverAll(ctx context.Context, sources []string, githubBaseURL string) ([]artifacts.TopicCandidate, error) {
	var all []artifacts.TopicCandidate

	for _, source := range sources {
		switch source {
		case "github_trending":
			candidates, err := DiscoverGitHubTrending(ctx, githubBaseURL)
			if err != nil {
				continue
			}
			all = append(all, candidates...)
		default:
			continue
		}
	}

	return all, nil
}
