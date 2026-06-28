package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/OnlyMyRailgun/ATRPE/internal/agents"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/topics"
)

// RefineTopicsInput is the input for topic refinement.
type RefineTopicsInput struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

// RefineTopicsResult contains scored candidates plus LLM-refined angles.
type RefineTopicsResult struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
	Angles     []artifacts.ArticleAngle   `json:"angles"`
}

const refinePrompt = `You are a technical editor for Zenn (Japanese tech blog). Given a list of GitHub projects, suggest SPECIFIC article angles that would make good 2000-word Zenn articles.

Rules:
- Skip projects that are too generic/broad (e.g. "kubernetes", "golang", "awesome-go")
- For each worthwhile project, give 1-3 specific angles focusing on implementation details
- Angles should be concrete enough to write code examples for
- Output raw JSON only, no markdown wrapping

Output format:
{
  "angles": [
    {
      "project_name": "ollama/ollama",
      "project_url": "https://github.com/ollama/ollama",
      "angles": [
        "Ollama Go SDK を使ったローカル LLM のコード生成 CLI の作り方",
        "Ollama の Modelfile をプログラムから生成して CI でモデル配布する仕組み"
      ]
    }
  ]
}`

// RefineTopics takes raw candidates and returns LLM-refined article angles sorted by score.
func (a *Activities) RefineTopics(ctx context.Context, input RefineTopicsInput) (*RefineTopicsResult, error) {
	// Filter to top 6 by score
	candidates := input.Candidates
	if len(candidates) > 6 {
		// Sort by score descending
		sortByScore(candidates)
		candidates = candidates[:6]
	}

	// Build prompt
	var sb strings.Builder
	sb.WriteString("Projects:\n\n")
	for i, c := range candidates {
		sb.WriteString(fmt.Sprintf("%d. %s (stars: fetch from URL, score: %.2f)\n   URL: %s\n\n",
			i+1, c.Title, c.Score, c.URL))
	}

	resp, err := a.LLM.Chat(ctx, []agents.ChatMessage{
		{Role: "system", Content: refinePrompt},
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		// If LLM fails, return scored candidates without angles
		return &RefineTopicsResult{Candidates: candidates}, nil
	}

	// Extract JSON from response
	resp = extractJSONStr(resp)

	var parsed struct {
		Angles []struct {
			ProjectName string   `json:"project_name"`
			ProjectURL  string   `json:"project_url"`
			Angles      []string `json:"angles"`
		} `json:"angles"`
	}

	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		// Fall back to raw candidates if parsing fails
		return &RefineTopicsResult{Candidates: candidates}, nil
	}

	var angles []artifacts.ArticleAngle
	for _, a := range parsed.Angles {
		angles = append(angles, artifacts.ArticleAngle{
			CandidateID: topics.CandidateID("github_trending", a.ProjectURL),
			ProjectName: a.ProjectName,
			ProjectURL:  a.ProjectURL,
			Angles:      a.Angles,
		})
	}

	return &RefineTopicsResult{
		Candidates: candidates,
		Angles:     angles,
	}, nil
}

func sortByScore(candidates []artifacts.TopicCandidate) {
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Score > candidates[i].Score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
}

func extractJSONStr(s string) string {
	// Find first { and last }
	start, end := -1, -1
	for i, c := range s {
		if c == '{' && start == -1 {
			start = i
		}
		if c == '}' {
			end = i + 1
		}
	}
	if start >= 0 && end > start {
		return s[start:end]
	}
	return s
}
