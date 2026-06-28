package topics

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// -- GitHub Trending --

// DiscoverGitHubTrending fetches specific, writable Go repositories from GitHub.
func DiscoverGitHubTrending(ctx context.Context, baseURL string) ([]artifacts.TopicCandidate, error) {
	twoYearsAgo := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	// Broader query — specificity scoring in ScoreCandidate will filter out generic repos
	q := fmt.Sprintf("language:go+created:>%s+stars:>10", twoYearsAgo)
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
			desc = item.FullName
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

// -- Hacker News --

type hnItem struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Time        int64  `json:"time"`
}

// DiscoverHackerNews fetches top stories from HN and scores tech-related ones.
func DiscoverHackerNews(ctx context.Context) ([]artifacts.TopicCandidate, error) {
	// Fetch top story IDs
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://hacker-news.firebaseio.com/v0/topstories.json", nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch hn top stories: %w", err)
	}
	defer resp.Body.Close()

	var ids []int
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, fmt.Errorf("decode hn ids: %w", err)
	}

	// Fetch first 30 items concurrently
	if len(ids) > 30 {
		ids = ids[:30]
	}

	var candidates []artifacts.TopicCandidate
	sem := make(chan struct{}, 5) // 5 concurrent fetches
	results := make(chan hnItem, len(ids))

	for _, id := range ids {
		go func(itemID int) {
			sem <- struct{}{}
			defer func() { <-sem }()

			item, err := fetchHNItem(ctx, itemID)
			if err == nil {
				results <- item
			} else {
				results <- hnItem{ID: itemID, Type: "error"}
			}
		}(id)
	}

	// Collect results
	collected := make(map[int]hnItem)
	for range ids {
		item := <-results
		if item.Type == "story" && item.URL != "" {
			collected[item.ID] = item
		}
	}

	techDomains := map[string]bool{
		"github.com": true, "gitlab.com": true, "dev.to": true,
		"medium.com": true, "arxiv.org": true, "blog.": false,
	}

	for _, item := range collected {
		if !isTechURL(item.URL, techDomains) {
			continue
		}

		publishedAt := time.Unix(item.Time, 0)
		id := CandidateID("hackernews", fmt.Sprintf("https://news.ycombinator.com/item?id=%d", item.ID))

		score := ScoreCandidate(CandidateInput{
			RepoName:    item.Title, // HN uses title for specificity scoring
			Description: item.Title,
			GithubStars: item.Score, // HN score as popularity proxy
			PublishedAt: publishedAt,
		})

		candidates = append(candidates, artifacts.TopicCandidate{
			ID:        id,
			Source:    "hackernews",
			Title:     item.Title,
			URL:       item.URL,
			Score:     score,
			CreatedAt: time.Now().UTC(),
		})
	}

	return candidates, nil
}

func fetchHNItem(ctx context.Context, id int) (hnItem, error) {
	url := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return hnItem{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return hnItem{}, err
	}
	defer resp.Body.Close()

	var item hnItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return hnItem{}, err
	}
	return item, nil
}

func isTechURL(url string, domains map[string]bool) bool {
	lower := strings.ToLower(url)
	for domain := range domains {
		if strings.Contains(lower, domain) {
			return true
		}
	}
	// Also include URLs with tech subdomains
	return false
}

// -- Zenn Trending --

type zennArticle struct {
	ID           int      `json:"id"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	Topics       []string `json:"topics"` // tags
	LikedCount   int      `json:"liked_count"`
	PublishedAt  string   `json:"published_at"`
}

type zennResponse struct {
	Articles []zennArticle `json:"articles"`
}

// DiscoverZennTrending fetches trending Zenn articles for competitive gap analysis.
func DiscoverZennTrending(ctx context.Context) ([]artifacts.TopicCandidate, error) {
	url := "https://zenn.dev/api/articles?order=latest&count=20"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch zenn: %w", err)
	}
	defer resp.Body.Close()

	var result zennResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode zenn: %w", err)
	}

	var candidates []artifacts.TopicCandidate
	for _, article := range result.Articles {
		publishedAt, _ := time.Parse(time.RFC3339, article.PublishedAt)
		articleURL := fmt.Sprintf("https://zenn.dev/articles/%s", article.Slug)
		id := CandidateID("zenn_trending", articleURL)

		// Score based on engagement
		engagementScore := math.Min(float64(article.LikedCount)/200.0, 1.0)
		recencyScore := recencyScore(publishedAt)
		score := engagementScore*0.5 + recencyScore*0.3 + topicNoveltyScore(article.Topics)*0.2

		candidates = append(candidates, artifacts.TopicCandidate{
			ID:        id,
			Source:    "zenn_trending",
			Title:     article.Title,
			URL:       articleURL,
			Score:     score,
			CreatedAt: time.Now().UTC(),
		})
	}

	return candidates, nil
}

// topicNoveltyScore favors topics that are less saturated.
func topicNoveltyScore(topics []string) float64 {
	if len(topics) == 0 {
		return 0.5
	}
	// More specialized tags = better
	if len(topics) >= 3 {
		return 0.8
	}
	return 0.5
}

// -- Qiita Trending --

type qiitaItem struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	Tags         []qiitaTag `json:"tags"`
	StocksCount  int      `json:"stocks_count"`
	CreatedAt    string   `json:"created_at"`
}

type qiitaTag struct {
	Name string `json:"name"`
}

// DiscoverQiitaTrending fetches trending Qiita articles.
func DiscoverQiitaTrending(ctx context.Context) ([]artifacts.TopicCandidate, error) {
	url := "https://qiita.com/api/v2/items?page=1&per_page=20&query=stocks%3A%3E3"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch qiita: %w", err)
	}
	defer resp.Body.Close()

	var items []qiitaItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode qiita: %w", err)
	}

	var candidates []artifacts.TopicCandidate
	for _, item := range items {
		publishedAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
		id := CandidateID("qiita_trending", item.URL)

		tagNames := make([]string, len(item.Tags))
		for i, t := range item.Tags {
			tagNames[i] = t.Name
		}

		stockScore := math.Min(float64(item.StocksCount)/100.0, 1.0)
		recencyScore := recencyScore(publishedAt)
		score := stockScore*0.5 + recencyScore*0.3 + topicNoveltyScore(tagNames)*0.2

		candidates = append(candidates, artifacts.TopicCandidate{
			ID:        id,
			Source:    "qiita_trending",
			Title:     item.Title,
			URL:       item.URL,
			Score:     score,
			CreatedAt: time.Now().UTC(),
		})
	}

	return candidates, nil
}

// -- RSS Feeds --

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type atomFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Entries []atomEntry  `xml:"entry"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Link    atomLink `xml:"link"`
	Updated string `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
}

// DiscoverRSS fetches and parses RSS/Atom feeds.
func DiscoverRSS(ctx context.Context, feedURLs []string) ([]artifacts.TopicCandidate, error) {
	var candidates []artifacts.TopicCandidate

	for _, feedURL := range feedURLs {
		items, err := fetchRSSFeed(ctx, feedURL)
		if err != nil {
			continue // skip failed feeds
		}
		candidates = append(candidates, items...)
	}

	return candidates, nil
}

func fetchRSSFeed(ctx context.Context, feedURL string) ([]artifacts.TopicCandidate, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rss %s: %w", feedURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var candidates []artifacts.TopicCandidate

	// Try RSS 2.0
	var rss rssFeed
	if err := xml.Unmarshal(bodyBytes, &rss); err == nil && len(rss.Channel.Items) > 0 {
		for _, item := range rss.Channel.Items {
			publishedAt := parseRSSDate(item.PubDate)
			id := CandidateID("rss", item.Link)
			score := recencyScore(publishedAt)

			candidates = append(candidates, artifacts.TopicCandidate{
				ID:        id,
				Source:    "rss",
				Title:     item.Title,
				URL:       item.Link,
				Score:     score,
				CreatedAt: time.Now().UTC(),
			})
		}
		return candidates, nil
	}

	// Try Atom
	var atom atomFeed
	if err := xml.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&atom); err == nil && len(atom.Entries) > 0 {
		for _, entry := range atom.Entries {
			publishedAt := parseRSSDate(entry.Updated)
			url := entry.Link.Href
			if url == "" {
				continue
			}
			id := CandidateID("rss", url)
			score := recencyScore(publishedAt)

			candidates = append(candidates, artifacts.TopicCandidate{
				ID:        id,
				Source:    "rss",
				Title:     entry.Title,
				URL:       url,
				Score:     score,
				CreatedAt: time.Now().UTC(),
			})
		}
	}

	return candidates, nil
}

func parseRSSDate(s string) time.Time {
	formats := []string{
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// -- Combined Discovery --

type sourceResult struct {
	candidates []artifacts.TopicCandidate
	err        error
}

// DiscoverAll runs discovery for each configured source and returns deduplicated candidates.
func DiscoverAll(ctx context.Context, sources []string, rssURLs []string, githubBaseURL string) ([]artifacts.TopicCandidate, error) {
	resultCh := make(chan sourceResult, len(sources))

	for _, source := range sources {
		src := source
		go func() {
			var candidates []artifacts.TopicCandidate
			var err error

			switch src {
			case "github_trending":
				candidates, err = DiscoverGitHubTrending(ctx, githubBaseURL)
			case "hackernews":
				candidates, err = DiscoverHackerNews(ctx)
			case "zenn_trending":
				candidates, err = DiscoverZennTrending(ctx)
			case "qiita_trending":
				candidates, err = DiscoverQiitaTrending(ctx)
			case "rss_feeds":
				candidates, err = DiscoverRSS(ctx, rssURLs)
			}

			resultCh <- sourceResult{candidates: candidates, err: err}
		}()
	}

	// Collect results
	var all []artifacts.TopicCandidate
	var errs []error
	for range sources {
		r := <-resultCh
		if r.err != nil {
			errs = append(errs, r.err)
		}
		all = append(all, r.candidates...)
	}

	// Deduplicate by URL, keeping highest score
	seen := make(map[string]int) // URL → index in deduped
	var deduped []artifacts.TopicCandidate
	for _, c := range all {
		if idx, ok := seen[c.URL]; ok {
			if c.Score > deduped[idx].Score {
				deduped[idx] = c
			}
			// Cross-source boost: same URL from multiple sources
			deduped[idx].Score = math.Min(deduped[idx].Score*1.2, 1.0)
		} else {
			seen[c.URL] = len(deduped)
			deduped = append(deduped, c)
		}
	}

	// Sort by score descending
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].Score > deduped[j].Score })

	if len(deduped) == 0 && len(errs) > 0 {
		return deduped, fmt.Errorf("all discovery sources failed: %v", errs)
	}

	return deduped, nil
}
