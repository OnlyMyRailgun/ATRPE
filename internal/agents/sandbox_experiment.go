package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// ── Command allowlist ─────────────────────────────────────

// allowedCommands lists the ONLY executables that experiments can invoke.
// Shell built-ins (cd, echo, export, etc.) are NOT allowed — the sandbox
// runs commands directly, not through a shell.
var allowedCommands = map[string]bool{
	"go":            true,
	"golangci-lint": true,
	"make":          false, // BLOCKED — can execute arbitrary shell
	"git":           false, // BLOCKED — can access git remotes
	"curl":          false, // BLOCKED — network access
	"wget":          false, // BLOCKED — network access
	"ssh":           false, // BLOCKED
	"docker":        false, // BLOCKED
	"podman":        false, // BLOCKED
	"kubectl":       false, // BLOCKED
	"bash":          false, // BLOCKED — no shell escapes
	"sh":            false, // BLOCKED
	"sudo":          false, // BLOCKED
	"pip":           false, // BLOCKED
	"npm":           false, // BLOCKED
	"python":        false, // BLOCKED — Go-only sandbox
	"python3":       false, // BLOCKED
	"node":          false, // BLOCKED
	"rustc":         false, // BLOCKED
	"cargo":         false, // BLOCKED
}

// isAllowed returns true if the command executable is in the allowlist.
func isAllowed(cmdName string) bool {
	allowed, known := allowedCommands[cmdName]
	return known && allowed
}

// ── Sandboxed Runner ──────────────────────────────────────

// SandboxedExperimentRunner wraps DefaultExperimentRunner with:
// - command allowlist (reject non-Go toolchain commands)
// - per-command timeout (default 5 min)
// - network isolation (blocked env)
// - artifact capture (snapshot files before/after command execution)
type SandboxedExperimentRunner struct {
	inner          *DefaultExperimentRunner
	timeoutPerCmd  time.Duration
	artifactSnaps  bool // capture file content for before/after diff
	capturedBefore map[string]string // path → content hash
	capturedAfter  map[string]string
}

// NewSandboxedExperimentRunner creates a sandboxed runner.
func NewSandboxedExperimentRunner() *SandboxedExperimentRunner {
	return &SandboxedExperimentRunner{
		inner:         &DefaultExperimentRunner{},
		timeoutPerCmd: 5 * time.Minute,
		artifactSnaps: true,
	}
}

// RunCommands validates commands against the allowlist, runs them with
// timeout and network isolation, and captures artifact snapshots.
func (r *SandboxedExperimentRunner) RunCommands(ctx context.Context, workdir string, testPlan artifacts.TestPlan, lintEnabled bool) ([]artifacts.CommandResult, error) {
	r.capturedBefore = make(map[string]string)
	r.capturedAfter = make(map[string]string)

	// Build command list, validating against allowlist
	type queuedCmd struct {
		name string
		args []string
	}
	var queue []queuedCmd

	// Default Go toolchain commands
	defaultCmds := []queuedCmd{
		{"go", []string{"vet", "./..."}},
		{"go", []string{"test", "./..."}},
	}
	queue = append(queue, defaultCmds...)

	if lintEnabled {
		queue = append(queue, queuedCmd{"golangci-lint", []string{"run"}})
	}

	seen := map[string]bool{"go vet": true, "go test": true, "golangci-lint": true}
	for _, tc := range testPlan.TestCases {
		parts := strings.Fields(tc.Command)
		if len(parts) == 0 {
			continue
		}
		displayName := strings.Join(parts, " ")
		if seen[displayName] {
			continue
		}
		seen[displayName] = true
		queue = append(queue, queuedCmd{parts[0], parts[1:]})
	}

	// Snapshot files before execution
	if r.artifactSnaps {
		r.snapshotFiles(workdir, r.capturedBefore)
	}

	// Execute each command with sandboxing
	var results []artifacts.CommandResult
	for _, qc := range queue {
		if !isAllowed(qc.name) {
			results = append(results, artifacts.CommandResult{
				Name:     qc.name + " " + strings.Join(qc.args, " "),
				Args:     append([]string{qc.name}, qc.args...),
				ExitCode: -1,
				Stderr:   fmt.Sprintf("BLOCKED: '%s' is not in the sandbox allowlist", qc.name),
			})
			continue
		}

		// Per-command timeout context
		cmdCtx, cmdCancel := context.WithTimeout(ctx, r.timeoutPerCmd)
		result := r.runSandboxedCmd(cmdCtx, workdir, qc.name, qc.args...)
		cmdCancel()
		results = append(results, result)
	}

	// Snapshot files after execution
	if r.artifactSnaps {
		r.snapshotFiles(workdir, r.capturedAfter)
	}

	return results, nil
}

func (r *SandboxedExperimentRunner) runSandboxedCmd(ctx context.Context, workdir, name string, args ...string) artifacts.CommandResult {
	displayName := name
	if len(args) > 0 {
		displayName = name + " " + strings.Join(args, " ")
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workdir

	// D: Network isolation — block all network access.
	// Code must be self-contained; `go vet` and `go test` run on pre-generated code.
	cmd.Env = []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"GOPATH=" + os.Getenv("GOPATH"),
		"GO111MODULE=on",
		"GONOSUMDB=*",
		"GONOSUMCHECK=*",
		"GOPROXY=off",       // block Go module downloads
		"GONOPROXY=*",       // block all proxy access
		"GONOSUMDB=*",
		"no_proxy=*",
		"http_proxy=",
		"https_proxy=",
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_EXEC_PATH=",
		"GIT_SSH_COMMAND=",
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			exitCode = -2 // timeout
			stderr.WriteString(fmt.Sprintf("\n[ATRPE] Command timed out after %v", r.timeoutPerCmd))
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

func (r *SandboxedExperimentRunner) snapshotFiles(workdir string, storage map[string]string) {
	_ = filepath.Walk(workdir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Only capture source files
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, ".sum") || strings.HasSuffix(path, ".md") {
			data, err := os.ReadFile(path)
			if err == nil {
				storage[path] = hashBytes(data)
			}
		}
		return nil
	})
}

// CapturedBefore returns file→hash map from before execution.
func (r *SandboxedExperimentRunner) CapturedBefore() map[string]string { return r.capturedBefore }

// CapturedAfter returns file→hash map from after execution.
func (r *SandboxedExperimentRunner) CapturedAfter() map[string]string { return r.capturedAfter }
