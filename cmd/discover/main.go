package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/your-org/atrpe/internal/topics"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := topics.DiscoverGitHubTrending(ctx, "https://api.github.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "GitHub API error: %v\n", err)
		os.Exit(1)
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	fmt.Printf("Top candidates (scored with specificity):\n\n")
	for i, c := range candidates {
		if i >= 8 {
			break
		}
		fmt.Printf("%d. [%s] %s\n", i+1, c.ID, c.Title)
		fmt.Printf("   Score: %.3f | URL: %s\n\n", c.Score, c.URL)
	}

	b, _ := json.MarshalIndent(candidates, "", "  ")
	fmt.Println(string(b))
}
