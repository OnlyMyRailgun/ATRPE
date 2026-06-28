package agents

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// VerificationAgent checks experiment results against success criteria.
type VerificationAgent struct {
	checks []string // e.g. ["lint", "vet", "tests", "links", "citations"]
}

// NewVerificationAgent creates a verification agent with configured checks.
func NewVerificationAgent(checks []string) *VerificationAgent {
	return &VerificationAgent{checks: checks}
}

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
		case "citations":
			matched, unmatched := checkClaimCitations(brief)
			report.ClaimsMatched = matched
			report.ClaimsUnmatched = unmatched
			report.CitationsPassed = unmatched == 0
			if !report.CitationsPassed {
				report.BlockingIssues = append(report.BlockingIssues,
					fmt.Sprintf("citation coverage: %d/%d claims backed by source+snapshot", matched, matched+unmatched))
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

// checkClaimCitations verifies that each claim in the TechnicalBrief maps
// to a source that has a fetched snapshot (ContentHash + SnapshotURI).
// Returns (matched, unmatched) counts.
func checkClaimCitations(brief artifacts.TechnicalBrief) (matched, unmatched int) {
	// Extract source references from claim text annotations like "[source #1: https://...]"
	sourcePat := regexp.MustCompile(`\[(?:CERTAIN|LIKELY|NEEDS VERIFICATION)\s*[-–—]\s*source\s*#(\d+):?\s*(https?://[^\]]*)?\]`)

	allClaims := append([]string{}, brief.CoreConcepts...)
	allClaims = append(allClaims, brief.SupportedClaims...)
	allClaims = append(allClaims, brief.CommonPitfalls...)

	// Build source index → SourceRef map from brief.Sources
	sourceByIndex := make(map[int]artifacts.SourceRef)
	for i, src := range brief.Sources {
		sourceByIndex[i+1] = src // 1-based source indexing
	}

	seenSources := make(map[string]bool)

	for _, claim := range allClaims {
		matches := sourcePat.FindStringSubmatch(claim)
		if len(matches) < 2 {
			// Claim has no structured source annotation — treat as unmatched
			unmatched++
			continue
		}

		sourceIdx := 0
		_, _ = fmt.Sscanf(matches[1], "%d", &sourceIdx)

		src, ok := sourceByIndex[sourceIdx]
		if !ok {
			unmatched++
			continue
		}

		// Source must have been fetched and have a content hash
		if !src.Fetched || src.ContentHash == "" {
			unmatched++
			continue
		}

		// Source must have a snapshot URI (or we flag it)
		if src.SnapshotURI == "" {
			unmatched++
			continue
		}

		seenSources[src.URL] = true
		matched++
	}

	return matched, unmatched
}

func checkLinks(ctx context.Context, brief artifacts.TechnicalBrief) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	allPassed := true
	for _, src := range brief.Sources {
		req, err := http.NewRequestWithContext(ctx, "HEAD", src.URL, nil)
		if err != nil {
			req, err = http.NewRequestWithContext(ctx, "GET", src.URL, nil)
			if err != nil {
				allPassed = false
				continue
			}
		}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			allPassed = false
			continue
		}
		resp.Body.Close()
	}
	return allPassed
}
