package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// ExperimentRunner executes commands in a workspace.
type ExperimentRunner interface {
	RunCommands(ctx context.Context, workdir string, testPlan artifacts.TestPlan, lintEnabled bool) ([]artifacts.CommandResult, error)
}

// DefaultExperimentRunner runs go vet, go test, golangci-lint, and test plan commands.
type DefaultExperimentRunner struct{}

func (r *DefaultExperimentRunner) RunCommands(ctx context.Context, workdir string, testPlan artifacts.TestPlan, lintEnabled bool) ([]artifacts.CommandResult, error) {
	var results []artifacts.CommandResult

	results = append(results, runCmd(ctx, workdir, "go", "vet", "./..."))
	results = append(results, runCmd(ctx, workdir, "go", "test", "./..."))

	if lintEnabled {
		results = append(results, runCmd(ctx, workdir, "golangci-lint", "run"))
	}

	seen := map[string]bool{"go vet": true, "go test": true, "golangci-lint": true}
	for _, tc := range testPlan.TestCases {
		if seen[tc.Command] {
			continue
		}
		seen[tc.Command] = true
		parts := strings.Fields(tc.Command)
		results = append(results, runCmd(ctx, workdir, parts[0], parts[1:]...))
	}

	return results, nil
}

func runCmd(ctx context.Context, workdir, name string, args ...string) artifacts.CommandResult {
	displayName := name
	if len(args) > 0 {
		displayName = name + " " + strings.Join(args, " ")
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workdir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return artifacts.CommandResult{
		Name:       displayName,
		Args:       append([]string{name}, args...),
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: elapsed,
	}
}

// ExperimentAgent generates and validates Go code.
type ExperimentAgent struct {
	codeGen       CodeGenerator
	runner        ExperimentRunner
	workspaceRoot string
}

// NewExperimentAgent creates an experiment agent.
func NewExperimentAgent(codeGen CodeGenerator, runner ExperimentRunner, workspaceRoot string) *ExperimentAgent {
	return &ExperimentAgent{codeGen: codeGen, runner: runner, workspaceRoot: workspaceRoot}
}

// NewSandboxedExperimentAgent creates an experiment agent with the sandboxed runner.
func NewSandboxedExperimentAgent(codeGen CodeGenerator, workspaceRoot string) *ExperimentAgent {
	return &ExperimentAgent{
		codeGen:       codeGen,
		runner:        NewSandboxedExperimentRunner(),
		workspaceRoot: workspaceRoot,
	}
}

// Run generates a Go module from the design and runs validation commands.
func (a *ExperimentAgent) Run(ctx context.Context, design artifacts.DesignArtifact) (artifacts.ExperimentResult, error) {
	executionID := uuid.New().String()
	attempt := 1

	mod, err := a.codeGen.GenerateGoModule(ctx, design)
	if err != nil {
		return artifacts.ExperimentResult{}, fmt.Errorf("code generation: %w", err)
	}

	workdir := filepath.Join(a.workspaceRoot, design.TopicID, fmt.Sprintf("attempt-%d", attempt), executionID)
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return artifacts.ExperimentResult{}, fmt.Errorf("create workspace: %w", err)
	}

	var filePaths []string
	for _, f := range mod.Files {
		// E: Reject absolute paths and traversal
		if !pathSafe(f.Path) {
			return artifacts.ExperimentResult{}, fmt.Errorf("unsafe path: %s", f.Path)
		}
		fullPath := filepath.Join(workdir, f.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return artifacts.ExperimentResult{}, fmt.Errorf("mkdir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			return artifacts.ExperimentResult{}, fmt.Errorf("write %s: %w", f.Path, err)
		}
		filePaths = append(filePaths, f.Path)
	}

	commands, err := a.runner.RunCommands(ctx, workdir, design.TestPlan, false)
	if err != nil {
		return artifacts.ExperimentResult{}, fmt.Errorf("run commands: %w", err)
	}

	result := artifacts.ExperimentResult{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:        uuid.New(),
			ArtifactType:      "experiment_result",
			Version:           1,
			TopicID:           design.TopicID,
			CreatedAt:         time.Now().UTC(),
			Producer:          artifacts.AgentExperiment,
			ParentArtifactIDs: []uuid.UUID{design.ArtifactID},
		},
		ExecutionID:        executionID,
		Environment:         artifacts.Environment{Type: "local", Runtime: "go", Workdir: workdir, Attempt: attempt},
		ExperimentLanguage:  "go",
		Entrypoints:         []string{mod.Entrypoint},
		GeneratedFiles:      filePaths,
		Commands:            commands,
	}

	// Capture sandbox file snapshots for audit trail
	if sr, ok := a.runner.(*SandboxedExperimentRunner); ok {
		before := sr.CapturedBefore()
		after := sr.CapturedAfter()
		if len(before) > 0 || len(after) > 0 {
			snapData, _ := json.Marshal(map[string]interface{}{
				"before": before,
				"after":  after,
			})
			result.Commands = append(result.Commands, artifacts.CommandResult{
				Name:    "atrpe-sandbox-snapshot",
				Args:    []string{"snapshot", "workspace"},
				ExitCode: 0,
				Stdout:  string(snapData),
			})
		}
	}

	return result, nil
}

// Patch re-generates code after a failure and returns the patch.
func (a *ExperimentAgent) Patch(ctx context.Context, design artifacts.DesignArtifact, result artifacts.ExperimentResult) (artifacts.PatchResult, error) {
	var failedCmds []artifacts.CommandResult
	for _, c := range result.Commands {
		if c.ExitCode != 0 {
			failedCmds = append(failedCmds, c)
		}
	}

	mod, err := a.codeGen.GenerateGoModule(ctx, design)
	if err != nil {
		return artifacts.PatchResult{}, fmt.Errorf("patch codegen: %w", err)
	}

	var patchedFiles []artifacts.PatchedFile
	for _, f := range mod.Files {
		// PO-5: path safety check in Patch stage too
		if !pathSafe(f.Path) {
			continue // skip unsafe paths
		}
		fullPath, err := safeJoin(result.Environment.Workdir, f.Path)
		if err != nil {
			continue
		}
		oldHash := ""
		if old, err := os.ReadFile(fullPath); err == nil {
			oldHash = hashBytes(old)
		}
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, []byte(f.Content), 0644)
		newHash := hashBytes([]byte(f.Content))
		patchedFiles = append(patchedFiles, artifacts.PatchedFile{
			Path: f.Path, OldHash: oldHash, NewHash: newHash,
		})
	}

	return artifacts.PatchResult{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:  uuid.New(),
			ArtifactType: "patch_result",
			Version:      1,
			TopicID:      design.TopicID,
			CreatedAt:    time.Now().UTC(),
			Producer:     artifacts.AgentExperiment,
			ParentArtifactIDs: []uuid.UUID{result.ArtifactID},
		},
		OriginalArtifactID: result.ArtifactID,
		PatchedFiles:       patchedFiles,
		FailedCommands:     failedCmds,
		RemediationReason:  fmt.Sprintf("%d commands failed", len(failedCmds)),
	}, nil
}

// E/P-11: safeJoin validates and joins a root dir with a relative path.
// Rejects absolute paths, traversal (../), and cleaned results escaping root.
func safeJoin(root, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(rel) || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths not allowed: %s", rel)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal rejected: %s", rel)
	}
	joined := filepath.Join(root, cleaned)
	// Verify the result still has root as prefix
	if !strings.HasPrefix(filepath.Clean(joined), filepath.Clean(root)) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	return joined, nil
}

// E: pathSafe rejects absolute paths and traversal attempts (kept as alias).
func pathSafe(p string) bool {
	_, err := safeJoin("/safe/phony", p)
	return err == nil
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:12]
}
