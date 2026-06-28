package github

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

var validSignals = map[string]bool{
	"TopicSelectedSignal":   true,
	"PublishApprovalSignal": true,
	"RetrySignal":           true,
	"AbortSignal":           true,
	"RequestChangesSignal":  true,
}

// InternalSignalRequest is the JSON body for the internal fallback endpoint.
type InternalSignalRequest struct {
	Signal  string         `json:"signal"`
	Payload map[string]any `json:"payload"`
}

// InternalSignalHandler serves the operator-only internal signal endpoint.
type InternalSignalHandler struct {
	authToken string
	sender    TemporalSignalSender
	logger    *slog.Logger
}

// NewInternalSignalHandler creates a handler for internal signal delivery.
func NewInternalSignalHandler(token string, sender TemporalSignalSender, logger *slog.Logger) *InternalSignalHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &InternalSignalHandler{authToken: token, sender: sender, logger: logger}
}

func (h *InternalSignalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if h.authToken != "" && (!strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != h.authToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/workflows/"), "/")
	if len(pathParts) < 2 || pathParts[1] != "signal" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	workflowID := pathParts[0]

	var req InternalSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if !validSignals[req.Signal] {
		http.Error(w, fmt.Sprintf("unknown signal: %s", req.Signal), http.StatusBadRequest)
		return
	}

	if h.sender != nil {
		if err := h.sender.SendSignal(r.Context(), workflowID, req.Signal, req.Payload); err != nil {
			h.logger.Error("send internal signal", "error", err)
			http.Error(w, "signal error", http.StatusInternalServerError)
			return
		}
	}

	resp, _ := json.Marshal(map[string]string{"status": "ok"})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}
