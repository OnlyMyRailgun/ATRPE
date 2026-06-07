package github

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalSignalHandler_Valid(t *testing.T) {
	handler := NewInternalSignalHandler("test-token", nil, nil)
	payload := InternalSignalRequest{Signal: "TopicSelectedSignal", Payload: map[string]any{"candidate_id": "abc123"}}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: body=%s", rec.Code, rec.Body.String())
	}
}

func TestInternalSignalHandler_InvalidToken(t *testing.T) {
	handler := NewInternalSignalHandler("test-token", nil, nil)
	payload := InternalSignalRequest{Signal: "TopicSelectedSignal"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestInternalSignalHandler_UnknownSignal(t *testing.T) {
	handler := NewInternalSignalHandler("test-token", nil, nil)
	payload := InternalSignalRequest{Signal: "UnknownSignal"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
