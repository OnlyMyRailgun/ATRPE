package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// WriterAgent generates Zenn-formatted technical articles.
type WriterAgent struct {
	llm      *LLMClient
	language string // "ja" or "en"
}

// NewWriterAgent creates a writer agent (default: English).
func NewWriterAgent(llm *LLMClient) *WriterAgent {
	return &WriterAgent{llm: llm, language: "en"}
}

// NewWriterAgentWithLanguage creates a writer agent for a specific language.
func NewWriterAgentWithLanguage(llm *LLMClient, language string) *WriterAgent {
	return &WriterAgent{llm: llm, language: language}
}

const writerSystemPromptV2 = `You are a senior technical writer for Zenn (zenn.dev), a Japanese developer platform known for practical, accurate, and deeply engaging technical articles.

## Audience
Software engineers with 2-5 years of experience who read Japanese and English technical content. They value running code over prose. They will actually type the commands you show.

## Article Structure (use h2 ## for top-level sections)
1. ## はじめに (Background) — Why this matters NOW. 2-3 sentences.
2. ## アーキテクチャ (Architecture) — Visual component diagram described in text. Show data flow.
3. ## 実装 (Implementation) — Step-by-step with REAL code blocks from the experiment results provided. Every code block must come from the experiment's generated files or command outputs.
4. ## 評価 (Evaluation) — What the tests and benchmarks actually showed. Include command output excerpts.
5. ## トラブルシューティング (Troubleshooting) — 3+ common problems encountered during development with solutions.

## Zenn Formatting Conventions
- Use :::message for informational callouts
- Use :::message alert for warnings and important notes
- Use <details><summary>詳細を見る</summary>...content...</details> for expandable sections
- Use ` + "```diff" + ` for code change examples
- Use emoji in section headers to improve scanability
- File paths should be in backticks: ` + "`" + `cmd/api/main.go` + "`" + `
- Command-line instructions should use ` + "```bash" + ` blocks

## Quality Checklist (verify before outputting)
- [ ] Every code block is from the Provided ExperimentResult, not invented.
- [ ] All command outputs match the Provided CommandResults.
- [ ] At least one troubleshooting item matches an actual failed command.
- [ ] No placeholder text like "[TODO]" or "add more here".
- [ ] At least 3 code blocks with real syntax.

## Output Format
Output a JSON object:
{
  "slug": "my-article-slug",
  "title": "Article Title",
  "emoji": "🚀",
  "type": "tech",
  "topics": ["go", "topic"],
  "sections": {
    "background": "Background section in markdown...",
    "architecture": "Architecture section...",
    "implementation": "Implementation section with actual code blocks...",
    "evaluation": "Evaluation section with command outputs...",
    "troubleshooting": "Troubleshooting section with real error messages..."
  }
}

## Language instruction
%s

IMPORTANT: Output ONLY the JSON object. Your entire response must be valid parseable JSON.`

func (a *WriterAgent) languageInstruction() string {
	switch a.language {
	case "ja":
		return "Write the article in Japanese (日本語). Use appropriate technical loanwords (外来語) where standard in the Japanese developer community. Section headers should be in Japanese as shown."
	default:
		return "Write the article in English."
	}
}

// Run generates a Zenn article draft from the full artifact chain.
func (a *WriterAgent) Run(ctx context.Context, brief artifacts.TechnicalBrief, result artifacts.ExperimentResult, report artifacts.VerificationReport, changeNotes string) (artifacts.ArticleDraft, error) {
	input := map[string]any{
		"topic":            brief.CoreConcepts,
		"claims":           brief.SupportedClaims,
		"experiment_files": result.GeneratedFiles,
		"commands":         result.Commands,
		"verification": map[string]any{
			"overall_passed":  report.OverallPassed,
			"blocking_issues": report.BlockingIssues,
			"warnings":        report.Warnings,
		},
		"sources": brief.Sources,
	}
	inputJSON, _ := json.Marshal(input)

	userPrompt := fmt.Sprintf("Write a Zenn article from this verified research and experiment data:\n```json\n%s\n```", string(inputJSON))
	if changeNotes != "" {
		userPrompt += fmt.Sprintf("\n\nRevision notes: %s", changeNotes)
	}
	userPrompt += "\n\nRemember: use only actual code from the experiment. Do not invent code."

	sysPrompt := todayPrefix() + " " + fmt.Sprintf(writerSystemPromptV2, a.languageInstruction())
	resp, err := a.llm.ChatWithTemp(ctx, []ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userPrompt},
	}, a.llm.config.TempFor("writer"), 8192)
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

	body := fmt.Sprintf("# %s\n\n## はじめに\n%s\n\n## アーキテクチャ\n%s\n\n## 実装\n%s\n\n## 評価\n%s\n\n## トラブルシューティング\n%s\n\n---\n*🤖 Generated with [ATRPE](https://github.com/OnlyMyRailgun/ATRPE)*\n",
		parsed.Title,
		parsed.Sections.Background,
		parsed.Sections.Architecture,
		parsed.Sections.Implementation,
		parsed.Sections.Evaluation,
		parsed.Sections.Troubleshooting,
	)

	valid := NewZennValidator()
	errs := valid.Validate(artifacts.ArticleDraft{
		Title:    parsed.Title,
		Emoji:    parsed.Emoji,
		Type:     parsed.Type,
		Topics:   parsed.Topics,
		Slug:     parsed.Slug,
		Body:     body,
		Sections: parsed.Sections,
	})
	if len(errs) > 0 {
		// Log warnings but don't block — human will review
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		fmt.Printf("⚠️ Zenn validation warnings:\n%s\n", strings.Join(msgs, "\n"))
	}

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
		} else if r == ' ' || r == '_' || unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 50 {
		s = s[:50]
	}
	return strings.Trim(s, "-")
}
