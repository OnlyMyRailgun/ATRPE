package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/your-org/atrpe/internal/artifacts"
)

// CodeGenerator generates Go modules from design artifacts.
type CodeGenerator interface {
	GenerateGoModule(ctx context.Context, design artifacts.DesignArtifact) (artifacts.GeneratedModule, error)
}

// LLMCodeGenerator implements CodeGenerator using an LLM.
type LLMCodeGenerator struct {
	llm *LLMClient
}

// NewLLMCodeGenerator creates an LLM-backed code generator.
func NewLLMCodeGenerator(llm *LLMClient) *LLMCodeGenerator {
	return &LLMCodeGenerator{llm: llm}
}

const codegenSystemPrompt = `You are a Go code generator. Given a design artifact, produce a complete, buildable Go module.

Output a JSON object:
{
  "module_name": "example",
  "entrypoint": "cmd/example/main.go",
  "files": [
    {"path": "go.mod", "content": "module example\n\ngo 1.23"},
    {"path": "cmd/example/main.go", "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}"},
    {"path": "example_test.go", "content": "package main\n\nimport \"testing\"\n\nfunc TestExample(t *testing.T) {\n\t// test\n}"}
  ]
}

Rules:
- go.mod must have a valid module path and go 1.23
- Every .go file must be compilable (correct package, imports, syntax)
- Include at least one test file with a real test
- Keep it minimal: 3-6 files total
- Implement the components and interactions from the design
- All files must be complete, never truncated`

// GenerateGoModule generates a Go module from a design artifact.
func (g *LLMCodeGenerator) GenerateGoModule(ctx context.Context, design artifacts.DesignArtifact) (artifacts.GeneratedModule, error) {
	designJSON, _ := json.Marshal(design)
	userPrompt := fmt.Sprintf("Generate a Go module for this design:\n%s", string(designJSON))

	resp, err := g.llm.ChatWithMaxTokens(ctx, []ChatMessage{
		{Role: "system", Content: codegenSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, 16384)
	if err != nil {
		return artifacts.GeneratedModule{}, fmt.Errorf("codegen llm call: %w", err)
	}

	resp = extractJSON(resp)

	var result struct {
		ModuleName string                    `json:"module_name"`
		Entrypoint string                    `json:"entrypoint"`
		Files      []artifacts.GeneratedFile `json:"files"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.GeneratedModule{}, fmt.Errorf("parse codegen output: %w", err)
	}

	// Validate paths
	for _, f := range result.Files {
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") {
			return artifacts.GeneratedModule{}, fmt.Errorf("invalid file path: %s", f.Path)
		}
		if f.Content == "" {
			return artifacts.GeneratedModule{}, fmt.Errorf("empty file content: %s", f.Path)
		}
	}

	return artifacts.GeneratedModule{
		ModuleName: result.ModuleName,
		Files:      result.Files,
		Entrypoint: result.Entrypoint,
	}, nil
}
