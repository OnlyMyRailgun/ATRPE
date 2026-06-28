package agents

import (
	"fmt"
	"strings"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// ValidationError describes a problem with an article draft.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ZennValidator checks an ArticleDraft for completeness and Zenn compatibility.
type ZennValidator struct{}

// NewZennValidator creates a Zenn article validator.
func NewZennValidator() *ZennValidator {
	return &ZennValidator{}
}

// placeholderPatterns are strings that indicate incomplete content.
var placeholderPatterns = []string{
	"[TODO]", "[WIP]", "[TBD]", "TODO:", "Lorem ipsum",
	"add more here", "coming soon", "work in progress",
	"<!-- TODO", "<!-- FIXME", "{placeholder}",
}

// Validate checks a draft and returns all validation errors.
func (v *ZennValidator) Validate(draft artifacts.ArticleDraft) []ValidationError {
	var errs []ValidationError

	// 1. Frontmatter completeness
	if draft.Title == "" {
		errs = append(errs, ValidationError{Field: "title", Message: "missing"})
	}
	if draft.Emoji == "" {
		errs = append(errs, ValidationError{Field: "emoji", Message: "missing"})
	}
	if draft.Type != "tech" && draft.Type != "idea" {
		errs = append(errs, ValidationError{Field: "type", Message: fmt.Sprintf("must be 'tech' or 'idea', got '%s'", draft.Type)})
	}
	if len(draft.Topics) == 0 {
		errs = append(errs, ValidationError{Field: "topics", Message: "at least one topic tag required"})
	}
	if draft.Slug == "" {
		errs = append(errs, ValidationError{Field: "slug", Message: "missing"})
	}

	// 2. Required sections
	requiredSections := map[string]string{
		"sections.background":      draft.Sections.Background,
		"sections.architecture":    draft.Sections.Architecture,
		"sections.implementation":  draft.Sections.Implementation,
		"sections.evaluation":      draft.Sections.Evaluation,
		"sections.troubleshooting": draft.Sections.Troubleshooting,
	}
	for name, content := range requiredSections {
		if strings.TrimSpace(content) == "" {
			errs = append(errs, ValidationError{Field: name, Message: "section is empty"})
		}
	}

	// 3. Code block count — at least 3 expected
	codeBlockCount := strings.Count(draft.Body, "```")
	if codeBlockCount < 6 { // 3 pairs of opening/closing
		errs = append(errs, ValidationError{
			Field:   "body",
			Message: fmt.Sprintf("only %d code fences found — expected at least 6 (3 code blocks)", codeBlockCount),
		})
	}

	// 4. Placeholder detection
	bodyLower := strings.ToLower(draft.Body)
	for _, pattern := range placeholderPatterns {
		if strings.Contains(draft.Body, pattern) || strings.Contains(bodyLower, strings.ToLower(pattern)) {
			errs = append(errs, ValidationError{
				Field:   "body",
				Message: fmt.Sprintf("placeholder text detected: %q", pattern),
			})
		}
	}

	// 5. Frontmatter format validation
	if !strings.Contains(draft.Body, "##") {
		errs = append(errs, ValidationError{
			Field:   "body",
			Message: "no markdown headings (##) found — article may be unstructured",
		})
	}

	return errs
}

// IsValid returns true if the draft passes all checks.
func (v *ZennValidator) IsValid(draft artifacts.ArticleDraft) bool {
	return len(v.Validate(draft)) == 0
}
