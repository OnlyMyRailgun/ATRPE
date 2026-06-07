package agents

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/atrpe/internal/artifacts"
)

// VerificationAgent checks experiment results against success criteria.
type VerificationAgent struct {
	checks []string // e.g. ["lint", "vet", "tests", "links"]
}

// NewVerificationAgent creates a verification agent with configured checks.
func NewVerificationAgent(checks []string) *VerificationAgent {
	return &VerificationAgent{checks: checks}
}

// Run verifies the experiment result against the configured checks.
// matchCmd finds a command result by prefix matching against command names.
func matchCmd(cmds []artifacts.CommandResult, prefix string) (artifacts.CommandResult, bool) {
	for _, c := range cmds {
		if strings.HasPrefix(c.Name, prefix) {
			return c, true
		}
	}
	return artifacts.CommandResult{}, false
}

func (a *VerificationAgent) Run(ctx context.Context, brief artifacts.TechnicalBrief, result artifacts.ExperimentResult) (artifacts.VerificationReport, error) {

	report := artifacts.VerificationReport{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:   uuid.New(),
			ArtifactType: "verification_report",
			Version:      1,
			TopicID:      result.TopicID,
			CreatedAt:    time.Now().UTC(),
			Producer:     artifacts.AgentVerification,
			ParentArtifactIDs: []uuid.UUID{result.ArtifactID},
		},
	}

	hasLint := false
	for _, check := range a.checks {
		switch check {
		case "lint":
			hasLint = true
			if cmd, ok := matchCmd(result.Commands, "golangci-lint"); ok {
				report.LintPassed = cmd.ExitCode == 0
				report.CheckedCommands = append(report.CheckedCommands, cmd)
				if !report.LintPassed {
					report.BlockingIssues = append(report.BlockingIssues, fmt.Sprintf("lint failed: %s", cmd.Stderr))
				}
			} else {
				report.Warnings = append(report.Warnings, "golangci-lint not found — skipping lint check")
				report.LintPassed = true
			}
		case "vet":
			if cmd, ok := matchCmd(result.Commands, "go vet"); ok {
				report.VetPassed = cmd.ExitCode == 0
				report.CheckedCommands = append(report.CheckedCommands, cmd)
				if !report.VetPassed {
					report.BlockingIssues = append(report.BlockingIssues, fmt.Sprintf("vet failed: %s", cmd.Stderr))
				}
			} else {
				report.BlockingIssues = append(report.BlockingIssues, "go vet not found")
			}
		case "tests":
			if cmd, ok := matchCmd(result.Commands, "go test"); ok {
				report.TestsPassed = cmd.ExitCode == 0
				report.CheckedCommands = append(report.CheckedCommands, cmd)
				if !report.TestsPassed {
					report.BlockingIssues = append(report.BlockingIssues, fmt.Sprintf("tests failed: %s", cmd.Stderr))
				}
			} else {
				report.BlockingIssues = append(report.BlockingIssues, "go test not found")
			}
		case "links":
			report.LinksPassed = checkLinks(ctx, brief)
			if !report.LinksPassed {
				report.BlockingIssues = append(report.BlockingIssues, "broken links found")
			}
		}
	}

	if !hasLint {
		report.Warnings = append(report.Warnings, "lint check disabled — skipping")
		report.LintPassed = true
	}

	report.OverallPassed = len(report.BlockingIssues) == 0
	return report, nil
}

func checkLinks(ctx context.Context, brief artifacts.TechnicalBrief) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, src := range brief.Sources {
		req, err := http.NewRequestWithContext(ctx, "HEAD", src.URL, nil)
		if err != nil {
			req, err = http.NewRequestWithContext(ctx, "GET", src.URL, nil)
			if err != nil {
				return false
			}
		}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			return false
		}
		resp.Body.Close()
	}
	return true
}
