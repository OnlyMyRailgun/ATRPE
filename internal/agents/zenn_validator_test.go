package agents

import (
	"testing"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/stretchr/testify/require"
)

func validDraft() artifacts.ArticleDraft {
	return artifacts.ArticleDraft{
		Title:  "Test Article",
		Emoji:  "🚀",
		Type:   "tech",
		Topics: []string{"go", "testing"},
		Slug:   "test-article",
		Sections: artifacts.ArticleSections{
			Background:      "This is background content.",
			Architecture:    "This describes the architecture with a diagram.",
			Implementation:  "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n```bash\n$ go run .\nhello\n```\n```diff\n- old\n+ new\n```",
			Evaluation:      "The tests passed with 100% coverage.",
			Troubleshooting: "Common issues: import cycle, nil pointer.",
		},
		Body: "## はじめに\nThis is background content.\n\n## アーキテクチャ\nThis describes the architecture.\n\n## 実装\n```go\nfunc main() {}\n```\n\n```bash\n$ go run .\n```\n\n```diff\n- old\n+ new\n```\n\n## 評価\nThe tests passed.\n\n## トラブルシューティング\nCommon issues.",
	}
}

func TestZennValidator_PassesValidDraft(t *testing.T) {
	v := NewZennValidator()
	draft := validDraft()

	errs := v.Validate(draft)
	require.Empty(t, errs, "valid draft should have no errors")
	require.True(t, v.IsValid(draft))
}

func TestZennValidator_MissingFrontmatter(t *testing.T) {
	v := NewZennValidator()

	tests := []struct {
		name  string
		mut   func(d artifacts.ArticleDraft) artifacts.ArticleDraft
		field string
	}{
		{"missing title", func(d artifacts.ArticleDraft) artifacts.ArticleDraft { d.Title = ""; return d }, "title"},
		{"missing emoji", func(d artifacts.ArticleDraft) artifacts.ArticleDraft { d.Emoji = ""; return d }, "emoji"},
		{"invalid type", func(d artifacts.ArticleDraft) artifacts.ArticleDraft { d.Type = "news"; return d }, "type"},
		{"missing topics", func(d artifacts.ArticleDraft) artifacts.ArticleDraft { d.Topics = nil; return d }, "topics"},
		{"missing slug", func(d artifacts.ArticleDraft) artifacts.ArticleDraft { d.Slug = ""; return d }, "slug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v.Validate(tt.mut(validDraft()))
			require.NotEmpty(t, errs, "should have errors for %s", tt.name)
			found := false
			for _, e := range errs {
				if e.Field == tt.field {
					found = true
				}
			}
			require.True(t, found, "should have error for field %q", tt.field)
		})
	}
}

func TestZennValidator_DetectPlaceholders(t *testing.T) {
	v := NewZennValidator()

	draft := validDraft()
	draft.Body += "\n[TODO] add more content here"

	errs := v.Validate(draft)
	found := false
	for _, e := range errs {
		if e.Field == "body" {
			found = true
		}
	}
	require.True(t, found, "should detect placeholder text")
}

func TestZennValidator_TooFewCodeBlocks(t *testing.T) {
	v := NewZennValidator()

	draft := validDraft()
	draft.Body = "# Title\n\nNo code blocks here. Just text."

	errs := v.Validate(draft)
	found := false
	for _, e := range errs {
		if e.Field == "body" {
			found = true
		}
	}
	require.True(t, found, "should flag too few code blocks")
}

func TestZennValidator_EmptySections(t *testing.T) {
	v := NewZennValidator()

	draft := validDraft()
	draft.Sections.Background = ""

	errs := v.Validate(draft)
	require.NotEmpty(t, errs)
	found := false
	for _, e := range errs {
		if e.Field == "sections.background" {
			found = true
		}
	}
	require.True(t, found, "should detect empty background section")
}
