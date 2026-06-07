package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/your-org/atrpe/internal/artifacts"
)

type WriterAgent struct {
	llm *LLMClient
}

func NewWriterAgent(llm *LLMClient) *WriterAgent {
	return &WriterAgent{llm: llm}
}

const writerSystemPrompt = `You are a Zenn technical article writer. Write a comprehensive technical article in Zenn markdown format.

Output a JSON object:
{
  "slug": "my-article-slug",
  "title": "Article Title",
  "emoji": "🚀",
  "type": "tech",
  "topics": ["go", "topic"],
  "sections": {
    "background": "Background section in markdown...",
    "architecture": "Architecture section in markdown...",
    "implementation": "Implementation section with actual code blocks...",
    "evaluation": "Evaluation section in markdown...",
    "troubleshooting": "Troubleshooting section..."
  }
}

IMPORTANT: Output ONLY the JSON object, no other text. Your entire response must be valid parseable JSON.
Each section must be complete markdown with code blocks, not placeholders.
Include actual code snippets from the experiment in the implementation section.`

func (a *WriterAgent) Run(ctx context.Context, brief artifacts.TechnicalBrief, result artifacts.ExperimentResult, report artifacts.VerificationReport, changeNotes string) (artifacts.ArticleDraft, error) {
	input := map[string]any{
		"brief":  brief,
		"result": result,
		"report": report,
	}
	inputJSON, _ := json.Marshal(input)

	userPrompt := fmt.Sprintf("Write a Zenn article from this research:\n%s", string(inputJSON))
	if changeNotes != "" {
		userPrompt += fmt.Sprintf("\n\nRevision notes: %s", changeNotes)
	}

	resp, err := a.llm.ChatWithMaxTokens(ctx, []ChatMessage{
		{Role: "system", Content: todayPrefix() + " " + writerSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, 8192)
	if err != nil {
		return artifacts.ArticleDraft{}, fmt.Errorf("writer llm call: %w", err)
	}

	// Try to parse as JSON
	jsonStr := extractJSON(resp)
	var parsed struct {
		Slug     string                    `json:"slug"`
		Title    string                    `json:"title"`
		Emoji    string                    `json:"emoji"`
		Type     string                    `json:"type"`
		Topics   []string                  `json:"topics"`
		Sections artifacts.ArticleSections `json:"sections"`
	}

	title := "ATRPE Article"
	if len(brief.CoreConcepts) > 0 {
		title = brief.CoreConcepts[0]
	}
	if t := extractFirstHeading(resp); t != "" {
		title = t
	}

	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		// Fallback: LLM returned raw markdown
		return artifacts.ArticleDraft{
			BaseArtifact: artifacts.BaseArtifact{
				ArtifactID:        uuid.New(),
				ArtifactType:      "article_draft",
				Version:           1,
				TopicID:           brief.TopicID,
				CreatedAt:         time.Now().UTC(),
				Producer:          artifacts.AgentWriter,
				ParentArtifactIDs: []uuid.UUID{brief.ArtifactID, result.ArtifactID, report.ArtifactID},
			},
			Slug:      slugify(title),
			Title:     title,
			Emoji:     "📝",
			Type:      "tech",
			Topics:    []string{"go"},
			Published: false,
			Body:      resp,
		}, nil
	}

	body := fmt.Sprintf("# %s\n\n## Background\n%s\n\n## Architecture\n%s\n\n## Implementation\n%s\n\n## Evaluation\n%s\n\n## Troubleshooting\n%s",
		parsed.Title,
		parsed.Sections.Background,
		parsed.Sections.Architecture,
		parsed.Sections.Implementation,
		parsed.Sections.Evaluation,
		parsed.Sections.Troubleshooting,
	)

	return artifacts.ArticleDraft{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:        uuid.New(),
			ArtifactType:      "article_draft",
			Version:           1,
			TopicID:           brief.TopicID,
			CreatedAt:         time.Now().UTC(),
			Producer:          artifacts.AgentWriter,
			ParentArtifactIDs: []uuid.UUID{brief.ArtifactID, result.ArtifactID, report.ArtifactID},
		},
		Slug:      parsed.Slug,
		Title:     parsed.Title,
		Emoji:     parsed.Emoji,
		Type:      parsed.Type,
		Topics:    parsed.Topics,
		Published: false,
		Sections:  parsed.Sections,
		Body:      body,
	}, nil
}

func extractFirstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

func slugify(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' {
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 50 {
		s = s[:50]
	}
	return strings.Trim(s, "-")
}
