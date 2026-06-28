package github

import (
	"fmt"
	"strings"
)

// ParsedCommand represents a GitHub issue comment parsed into a Temporal signal.
type ParsedCommand struct {
	Signal  string
	Payload map[string]any
}

// Parse extracts a command from a GitHub issue comment body.
// Returns an error if the text is not a recognized command.
func Parse(body string) (*ParsedCommand, error) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "/") {
		return nil, fmt.Errorf("not a command")
	}

	parts := strings.SplitN(trimmed, " ", 2)
	command := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}

	switch command {
	case "/select":
		if rest == "" {
			return nil, fmt.Errorf("/select requires a candidate_id")
		}
		return &ParsedCommand{
			Signal:  "TopicSelectedSignal",
			Payload: map[string]any{"candidate_id": rest},
		}, nil
	case "/approve":
		return &ParsedCommand{Signal: "PublishApprovalSignal", Payload: map[string]any{}}, nil
	case "/retry":
		return &ParsedCommand{Signal: "RetrySignal", Payload: map[string]any{}}, nil
	case "/abort":
		return &ParsedCommand{Signal: "AbortSignal", Payload: map[string]any{}}, nil
	case "/changes":
		if rest == "" {
			return nil, fmt.Errorf("/changes requires change notes")
		}
		return &ParsedCommand{
			Signal:  "RequestChangesSignal",
			Payload: map[string]any{"change_notes": rest},
		}, nil
	case "/merged":
		return &ParsedCommand{Signal: "PublishMergedSignal", Payload: map[string]any{}}, nil
	default:
		return nil, fmt.Errorf("unknown command: %s", command)
	}
}
