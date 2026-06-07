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
)

// TemporalSignalSender sends signals to Temporal workflows.
type TemporalSignalSender interface {
	SendSignal(ctx context.Context, workflowID, signal string, payload map[string]any) error
}

// WebhookHandler validates and processes GitHub issue comment webhooks.
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
		return nil // dev mode: skip validation
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
	if event != "issue_comment" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ignored"}`))
		return
	}

	var evt githubCommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		h.logger.Error("unmarshal event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if evt.Action != "created" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ignored"}`))
		return
	}

	cmd, err := Parse(evt.Comment.Body)
	if err != nil {
		h.logger.Debug("not a command", "body", evt.Comment.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"not_a_command"}`))
		return
	}

	h.logger.Info("parsed command", "signal", cmd.Signal, "payload", cmd.Payload)

	workflowID := fmt.Sprintf("article-%d", evt.Issue.Number)
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
	w.Write(resp)
}
