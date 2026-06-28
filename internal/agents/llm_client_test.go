package agents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLLMConfig_TempFor(t *testing.T) {
	cfg := LLMConfig{
		Provider:        "deepseek",
		Model:           "deepseek-chat",
		TempResearch:    0.1,
		TempDesign:      0.3,
		TempCodeGen:     0.2,
		TempVerification: 0.0,
		TempWriter:      0.5,
	}

	require.Equal(t, 0.1, cfg.TempFor("research"))
	require.Equal(t, 0.3, cfg.TempFor("design"))
	require.Equal(t, 0.2, cfg.TempFor("codegen"))
	require.Equal(t, 0.0, cfg.TempFor("verification"))
	require.Equal(t, 0.5, cfg.TempFor("writer"))
	require.Equal(t, 0.3, cfg.TempFor("unknown"))
}

func TestLLMConfig_TempFor_Defaults(t *testing.T) {
	// When no per-agent temps are set, should use sensible defaults
	cfg := LLMConfig{
		Provider: "openai",
		Model:    "gpt-4",
	}

	// All zero → firstNonZero returns defaults
	require.Equal(t, 0.1, cfg.TempFor("research"))
	require.Equal(t, 0.3, cfg.TempFor("design"))
	require.Equal(t, 0.2, cfg.TempFor("codegen"))
	require.Equal(t, 0.0, cfg.TempFor("verification"))
	require.Equal(t, 0.5, cfg.TempFor("writer"))
}

func TestExtractJSON_StripsCodeBlock(t *testing.T) {
	input := "```json\n{\"key\": \"value\"}\n```"
	result := ExtractJSON(input)
	require.Equal(t, `{"key": "value"}`, result)
}

func TestExtractJSON_PlainJSON(t *testing.T) {
	input := `{"hello": "world"}`
	result := ExtractJSON(input)
	require.Equal(t, `{"hello": "world"}`, result)
}

func TestExtractJSON_JSONWithText(t *testing.T) {
	input := "Here is the output:\n{\"result\": 42}\nHope that helps."
	result := ExtractJSON(input)
	require.Equal(t, `{"result": 42}`, result)
}

func TestExtractJSON_NestedBraces(t *testing.T) {
	input := `{"outer": {"inner": "value"}}`
	result := ExtractJSON(input)
	require.Equal(t, `{"outer": {"inner": "value"}}`, result)
}

func TestExtractJSON_NoJSON(t *testing.T) {
	input := "Just plain text, no JSON here."
	result := ExtractJSON(input)
	require.Equal(t, "Just plain text, no JSON here.", result)
}

func TestTodayPrefix(t *testing.T) {
	result := todayPrefix()
	require.Contains(t, result, "Today's date is ")
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "hello", truncate("hello", 10))
	require.Equal(t, "hello wo...", truncate("hello world this is long", 8))
}
