package github

import "testing"

func TestParseSelect(t *testing.T) {
	cmd, err := Parse("/select abc123def456")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cmd.Signal != "TopicSelectedSignal" {
		t.Errorf("expected TopicSelectedSignal, got %s", cmd.Signal)
	}
	if cmd.Payload["candidate_id"] != "abc123def456" {
		t.Errorf("expected candidate_id=abc123def456, got %v", cmd.Payload["candidate_id"])
	}
}

func TestParseSelect_NoID(t *testing.T) {
	_, err := Parse("/select")
	if err == nil {
		t.Error("expected error for /select without candidate_id")
	}
}

func TestParseApprove(t *testing.T) {
	cmd, err := Parse("/approve")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cmd.Signal != "PublishApprovalSignal" {
		t.Errorf("expected PublishApprovalSignal, got %s", cmd.Signal)
	}
}

func TestParseRetry(t *testing.T) {
	cmd, err := Parse("/retry")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cmd.Signal != "RetrySignal" {
		t.Errorf("expected RetrySignal, got %s", cmd.Signal)
	}
}

func TestParseAbort(t *testing.T) {
	cmd, err := Parse("/abort")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cmd.Signal != "AbortSignal" {
		t.Errorf("expected AbortSignal, got %s", cmd.Signal)
	}
}

func TestParseChanges(t *testing.T) {
	cmd, err := Parse("/changes Make it more detailed")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cmd.Signal != "RequestChangesSignal" {
		t.Errorf("expected RequestChangesSignal, got %s", cmd.Signal)
	}
	notes, ok := cmd.Payload["change_notes"].(string)
	if !ok || notes != "Make it more detailed" {
		t.Errorf("expected change_notes, got %v", cmd.Payload["change_notes"])
	}
}

func TestParseChanges_NoNotes(t *testing.T) {
	_, err := Parse("/changes")
	if err == nil {
		t.Error("expected error for /changes without notes")
	}
}

func TestParse_UnknownCommand(t *testing.T) {
	_, err := Parse("/unknown")
	if err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestParse_NotACommand(t *testing.T) {
	_, err := Parse("just a regular comment")
	if err == nil {
		t.Error("expected error for non-command text")
	}
}
