package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/topics"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Sources from env or defaults
	sources := getEnvSlice("TOPIC_SOURCES", "github_trending,hackernews,zenn_trending,qiita_trending,rss_feeds")
	rssURLs := getEnvSlice("RSS_FEED_URLS", "https://go.dev/blog/feed.atom,https://kubernetes.io/feed.xml")

	fmt.Printf("ATRPE Discovery — searching %d sources...\n\n", len(sources))

	candidates, err := topics.DiscoverAll(ctx, sources, rssURLs, "https://api.github.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Discovery error: %v\n", err)
		os.Exit(1)
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Count per source
	sourceCounts := make(map[string]int)
	for _, c := range candidates {
		sourceCounts[c.Source]++
	}

	fmt.Printf("Sources breakdown:\n")
	for _, s := range sources {
		fmt.Printf("  %-20s: %d candidates\n", s, sourceCounts[s])
	}
	fmt.Printf("  %-20s: %d total\n\n", "──", len(candidates))

	if len(candidates) == 0 {
		fmt.Println("No candidates found.")
		os.Exit(0)
	}

	fmt.Printf("Top candidates:\n\n")
	limit := min(10, len(candidates))
	for i := 0; i < limit; i++ {
		c := candidates[i]
		fmt.Printf("%d. [%s] %s\n", i+1, c.Source, c.Title)
		fmt.Printf("   ID: %s | Score: %.3f | URL: %s\n\n", c.ID, c.Score, c.URL)
	}

	b, _ := json.MarshalIndent(candidates[:limit], "", "  ")
	fmt.Println(string(b))
}

func getEnvSlice(key, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = fallback
	}
	result := make([]string, 0)
	for _, p := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
