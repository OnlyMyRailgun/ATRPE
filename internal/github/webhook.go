package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// TemporalSignalSender sends signals to Temporal workflows.
type TemporalSignalSender interface {
	SendSignal(ctx context.Context, workflowID, signal string, payload map[string]any) error
}

// PublishMappingLookup resolves a slug to its original GitHub issue number.
// Implemented by the API server which has access to the knowledge store.
type PublishMappingLookup interface {
	LookupIssueNumber(ctx context.Context, slug string) (int, error)
}

// WebhookHandler validates and processes GitHub webhook events.
type WebhookHandler struct {
	webhookSecret string
	sender        TemporalSignalSender
	logger        *slog.Logger
}

// NewWebhookHandler creates a handler for GitHub webhook events.
func NewWebhookHandler(secret string, sender TemporalSignalSender, logger *slog.Logger) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{webhookSecret: secret, sender: sender, logger: logger}
}

// ValidateSignature checks the HMAC-SHA256 signature of a webhook payload.
func ValidateSignature(body []byte, signature, secret string) error {
	if secret == "" {
		return nil
	}
	if signature == "" {
		return fmt.Errorf("missing signature header")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

type githubCommentEvent struct {
	Action  string `json:"action"`
	Comment struct {
		Body string `json:"body"`
		ID   int64  `json:"id"`
	} `json:"comment"`
	Issue struct {
		Number int `json:"number"`
	} `json:"issue"`
}

// PO-2: pull_request event structure for merge detection.
type githubPullRequestEvent struct {
	Action      string `json:"action"` // "closed" for merge
	PullRequest struct {
		Merged bool   `json:"merged"`
		Title  string `json:"title"`
		Head   struct {
			Ref string `json:"ref"` // branch name
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		h.logger.Error("read body", "error", err)
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if err := ValidateSignature(body, sig, h.webhookSecret); err != nil {
		h.logger.Warn("invalid signature", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")

	switch event {
	case "issue_comment":
		h.handleIssueComment(w, r, body)
	case "pull_request":
		h.handlePullRequest(w, r, body)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ignored"}`))
	}
}

func (h *WebhookHandler) handleIssueComment(w http.ResponseWriter, r *http.Request, body []byte) {
	var evt githubCommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		h.logger.Error("unmarshal comment event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if evt.Action != "created" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ignored"}`))
		return
	}

	cmd, err := Parse(evt.Comment.Body)
	if err != nil {
		h.logger.Debug("not a command", "body", evt.Comment.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"not_a_command"}`))
		return
	}

	h.logger.Info("parsed command", "signal", cmd.Signal, "payload", cmd.Payload)

	workflowID := fmt.Sprintf("article-issue-%d", evt.Issue.Number)
	if h.sender != nil {
		if err := h.sender.SendSignal(r.Context(), workflowID, cmd.Signal, cmd.Payload); err != nil {
			h.logger.Error("send signal", "error", err)
			http.Error(w, "signal error", http.StatusInternalServerError)
			return
		}
	}

	resp, _ := json.Marshal(map[string]string{
		"status":  "ok",
		"signal":  cmd.Signal,
		"message": "signal sent to workflow " + workflowID,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// PO-2: handlePullRequest processes pull_request webhook events.
// When a publish PR (atrpe-publish/* branch) is merged, signals the workflow.
func (h *WebhookHandler) handlePullRequest(w http.ResponseWriter, r *http.Request, body []byte) {
	var evt githubPullRequestEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		h.logger.Error("unmarshal PR event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// PO-2: Must be "closed" and actually merged
	if evt.Action != "closed" || !evt.PullRequest.Merged {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ignored"}`))
		return
	}

	// PO-2: Only react to publish branches (atrpe-publish/*)
	if !strings.HasPrefix(evt.PullRequest.Head.Ref, "atrpe-publish/") {
		h.logger.Debug("PR merge ignored — not a publish branch", "ref", evt.PullRequest.Head.Ref)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"non-publish-branch"}`))
		return
	}

	// PO-2: Extract slug from branch name: atrpe-publish/<slug>-MMDD-HHMM
	branch := evt.PullRequest.Head.Ref
	slug := extractSlugFromPublishBranch(branch)
	if slug == "" {
		h.logger.Warn("cannot extract slug from publish branch", "branch", branch)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"invalid-branch-format"}`))
		return
	}

	h.logger.Info("publish PR merged — signalling workflow",
		"branch", branch, "slug", slug, "sha", evt.PullRequest.Head.SHA)

	// Blocker 2: Route signal to article-issue-N via slug→issueNumber mapping.
	// The publish activity records the mapping; webhook uses it to find the workflow.
	if h.sender != nil {
		issueLookup, ok := h.sender.(PublishMappingLookup)
		workflowID := ""
		if ok && slug != "" {
			if n, err := issueLookup.LookupIssueNumber(r.Context(), slug); err == nil && n > 0 {
				workflowID = fmt.Sprintf("article-issue-%d", n)
			}
		}
		if workflowID == "" {
			// Fallback: try publish-{slug} (legacy stored workflows)
			workflowID = "publish-" + slug
		}
		if err := h.sender.SendSignal(r.Context(), workflowID, "PublishMergedSignal", map[string]any{
			"slug":  slug,
			"title": evt.PullRequest.Title,
		}); err != nil {
			h.logger.Error("send publish merge signal", "error", err, "workflowID", workflowID)
			http.Error(w, "signal error", http.StatusInternalServerError)
			return
		}
	}

	resp, _ := json.Marshal(map[string]string{
		"status": "ok",
		"signal": "PublishMergedSignal",
		"slug":   slug,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// extractSlugFromPublishBranch parses "atrpe-publish/my-article-0615-0930" → "my-article".
func extractSlugFromPublishBranch(branch string) string {
	branch = strings.TrimPrefix(branch, "atrpe-publish/")
	// Remove the trailing timestamp: slug-MMDD-HHMM
	parts := strings.Split(branch, "-")
	if len(parts) < 3 {
		return ""
	}
	// Everything except the last two parts (MMDD and HHMM)
	return strings.Join(parts[:len(parts)-2], "-")
}
