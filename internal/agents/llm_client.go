package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMConfig holds the provider connection details.
type LLMConfig struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
	// Per-agent temperature overrides. 0 means use per-call default.
	TempResearch     float64
	TempDesign       float64
	TempCodeGen      float64
	TempVerification float64
	TempWriter       float64
}

// TempFor returns the temperature for a given agent name.
func (c LLMConfig) TempFor(agent string) float64 {
	switch agent {
	case "research":
		return firstNonZero(c.TempResearch, 0.1)
	case "design":
		return firstNonZero(c.TempDesign, 0.3)
	case "codegen":
		return firstNonZero(c.TempCodeGen, 0.2)
	case "verification":
		return firstNonZero(c.TempVerification, 0.0)
	case "writer":
		return firstNonZero(c.TempWriter, 0.5)
	default:
		return 0.3
	}
}

func firstNonZero(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0.3
}

// ChatMessage represents a single message in an LLM conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the OpenAI-compatible chat completion response.
type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

// LLMClient is a minimal OpenAI-compatible HTTP client for LLM calls.
type LLMClient struct {
	config LLMConfig
	http   *http.Client
}

// NewLLMClient creates an LLM client with the given config.
func NewLLMClient(config LLMConfig) *LLMClient {
	return &LLMClient{
		config: config,
		http:   &http.Client{Timeout: 120 * time.Second},
	}
}

// Chat sends a conversation to the LLM and returns the assistant's reply.
func (c *LLMClient) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	return c.ChatWithMaxTokens(ctx, messages, 8192)
}

// ChatWithAgent sends a conversation with the per-agent temperature.
func (c *LLMClient) ChatWithAgent(ctx context.Context, messages []ChatMessage, agent string) (string, error) {
	return c.ChatWithTemp(ctx, messages, c.config.TempFor(agent), 8192)
}

// ChatWithMaxTokens allows configuring the max output tokens.
func (c *LLMClient) ChatWithMaxTokens(ctx context.Context, messages []ChatMessage, maxTokens int) (string, error) {
	return c.ChatWithTemp(ctx, messages, 0.3, maxTokens)
}

// ChatWithTemp sends a conversation with a specific temperature and max tokens.
func (c *LLMClient) ChatWithTemp(ctx context.Context, messages []ChatMessage, temperature float64, maxTokens int) (string, error) {
	reqBody := map[string]interface{}{
		"model":       c.config.Model,
		"messages":    messages,
		"temperature": temperature,
	}
	if maxTokens > 0 {
		reqBody["max_tokens"] = maxTokens
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// extractJSON finds the first JSON object in a string, stripping markdown code blocks.
func extractJSON(s string) string {
	return ExtractJSON(s)
}

// ExtractJSON finds the first JSON object in a string, stripping markdown code blocks.
func ExtractJSON(s string) string {
	// Strip markdown code blocks: ```json ... ``` or ``` ... ```
	if idx := strings.Index(s, "```json"); idx >= 0 {
		s = s[idx+7:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	} else if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx+3:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	start, end := -1, -1
	for i, c := range s {
		if c == '{' && start == -1 {
			start = i
		}
		if c == '}' {
			end = i + 1
		}
	}
	if start >= 0 && end > start {
		return s[start:end]
	}
	return s
}

// truncate cuts a string to n characters for error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// todayPrefix returns "Today's date is YYYY-MM-DD." for prompt injection.
func todayPrefix() string {
	return "Today's date is " + time.Now().Format("2006-01-02") + "."
}
