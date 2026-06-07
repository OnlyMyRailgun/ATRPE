package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateSignature_Valid(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"created","comment":{"body":"/approve"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if err := ValidateSignature(body, sig, secret); err != nil {
		t.Errorf("expected valid signature, got error: %v", err)
	}
}

func TestValidateSignature_Invalid(t *testing.T) {
	if err := ValidateSignature([]byte("body"), "sha256=badsig", "secret"); err == nil {
		t.Error("expected error for invalid signature")
	}
}

func TestValidateSignature_EmptySecret(t *testing.T) {
	if err := ValidateSignature([]byte("body"), "sha256=anything", ""); err != nil {
		t.Errorf("expected no error with empty secret, got: %v", err)
	}
}

func TestWebhookHandler_ValidCommand(t *testing.T) {
	handler := NewWebhookHandler("test-secret", nil, nil)
	body := `{"action":"created","comment":{"body":"/approve"}}`
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "issue_comment")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	handler := NewWebhookHandler("test-secret", nil, nil)
	body := `{"action":"created","comment":{"body":"/approve"}}`

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=badsig")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
