package topics

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"
)

// CandidateInput contains the raw signals used for scoring.
type CandidateInput struct {
	RepoName             string
	Description          string
	JapaneseArticleCount int
	GithubStars          int
	PublishedAt          time.Time
}

// ScoreCandidate computes a 0..1 composite score.
// Weights: novelty 0.25, practicality 0.25, timing 0.15, specificity 0.35
// Specificity is weighted highest because broad topics (kubernetes, go) produce bad articles.
func ScoreCandidate(c CandidateInput) float64 {
	novelty := 1.0 - math.Min(float64(c.JapaneseArticleCount)/50.0, 1.0)

	practicality := math.Min(float64(c.GithubStars)/10000.0, 1.0)
	if c.GithubStars == 0 {
		practicality = 0.5 // neutral default
	}

	timing := recencyScore(c.PublishedAt)
	specificity := specificityScore(c.RepoName, c.Description)

	return 0.25*novelty + 0.25*practicality + 0.15*timing + 0.35*specificity
}

// specificityScore penalizes generic/overbroad topics and rewards narrow, implementable ones.
func specificityScore(repoName, description string) float64 {
	score := 0.5 // neutral start

	// Penalize single-word names (e.g. "go", "kubernetes", "ollama")
	parts := strings.Split(repoName, "/")
	name := repoName
	if len(parts) == 2 {
		name = parts[1] // just the repo part
	}
	wordCount := len(strings.Split(name, "-"))
	switch {
	case wordCount == 1:
		score -= 0.25 // "ollama" → too broad
	case wordCount == 2:
		score += 0.05 // "awesome-go" → somewhat specific
	case wordCount >= 3:
		score += 0.15 // "go-k8s-operator-testing" → specific
	}

	// Reward repos with descriptive taglines (length > 40 chars)
	if len(description) > 40 {
		score += 0.10
	}
	if len(description) > 80 {
		score += 0.05
	}

	// Penalize ultra-generic names
	genericNames := map[string]bool{
		"go": true, "golang": true, "rust": true, "python": true,
		"kubernetes": true, "docker": true, "linux": true, "awesome-go": true,
	}
	if genericNames[name] {
		score -= 0.20
	}

	// Reward names that mention specific tech stacks
	specificIndicators := []string{
		"operator", "plugin", "driver", "middleware", "codec",
		"compiler", "parser", "serializer", "crypto", "fuzzer",
		"proxy", "bridge", "adapter", "migrat", "scaffold",
	}
	for _, indicator := range specificIndicators {
		if strings.Contains(strings.ToLower(description), indicator) ||
			strings.Contains(strings.ToLower(name), indicator) {
			score += 0.10
			break
		}
	}

	return math.Max(0.0, math.Min(1.0, score))
}

func recencyScore(publishedAt time.Time) float64 {
	if publishedAt.IsZero() {
		return 0.0
	}
	days := time.Since(publishedAt).Hours() / 24
	switch {
	case days <= 7:
		return 1.0
	case days <= 30:
		return 0.5
	case days <= 90:
		return 0.2
	default:
		return 0.0
	}
}

// CandidateID generates a stable, deterministic 12-char hex ID from source and URL.
func CandidateID(source, url string) string {
	h := sha256.Sum256([]byte(source + "|" + url))
	return hex.EncodeToString(h[:])[:12]
}
