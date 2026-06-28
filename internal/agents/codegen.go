package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
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

const codegenSystemPromptV2 = `You are a Go programmer writing a minimal, compilable example module.

## Rules
- Generate COMPLETE files: every import, every function body. Nothing unfinished.
- go.mod must specify a real Go version and required dependencies.
- Include at least one _test.go file with a table-driven test (func TestXxx(t *testing.T)).
- The module MUST compile with go build ./... and pass go vet ./...
- Include a README.md with build/run instructions.
- Do NOT use "replace" directives in go.mod.
- Maximum 5 source files total (keep the example focused).

## Output Format
{
  "module_name": "github.com/example/project",
  "entrypoint": "cmd/example/main.go",
  "files": [
    {"path": "go.mod", "content": "module ..."},
    {"path": "cmd/example/main.go", "content": "package main\n..."},
    {"path": "pkg/lib.go", "content": "package lib\n..."},
    {"path": "pkg/lib_test.go", "content": "package lib\n\nimport \"testing\"\n..."},
    {"path": "README.md", "content": "# Example\n..."}
  ]
}

Implement the Components and Interactions from the Design Artifact.
Every file must be valid Go syntax. Tests must use table-driven pattern.`

// GenerateGoModule generates a Go module from a design artifact.
func (g *LLMCodeGenerator) GenerateGoModule(ctx context.Context, design artifacts.DesignArtifact) (artifacts.GeneratedModule, error) {
	designJSON, _ := json.Marshal(design)
	userPrompt := fmt.Sprintf("Generate a Go module for this design:\n%s", string(designJSON))

	resp, err := g.llm.ChatWithTemp(ctx, []ChatMessage{
		{Role: "system", Content: todayPrefix() + " " + codegenSystemPromptV2},
		{Role: "user", Content: userPrompt},
	}, g.llm.config.TempFor("codegen"), 16384)
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
