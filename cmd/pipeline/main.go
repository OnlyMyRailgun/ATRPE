package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/your-org/atrpe/internal/agents"
	"github.com/your-org/atrpe/internal/artifacts"
	"github.com/your-org/atrpe/internal/config"
	"github.com/your-org/atrpe/internal/knowledge"
	"github.com/your-org/atrpe/internal/objectstore"
	"github.com/your-org/atrpe/internal/topics"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	step := 0
	start := time.Now()

	// ── Load config ──
	cfg, err := config.Load()
	fatalIf(err, "config")
	log("╔══════════════════════════════════════════╗")
	log("║  ATRPE Pipeline — Discovery to Article   ║")
	log("╚══════════════════════════════════════════╝")
	log("Provider: %s | Model: %s", cfg.LLMProvider, cfg.LLMModel)

	// ── Init stores ──
	store, err := knowledge.NewSQLiteStore(":memory:")
	fatalIf(err, "sqlite")
	defer store.Close()

	objects := objectstore.NewLocalObjectStore(os.TempDir() + "/atrpe-pipeline")
	repo := artifacts.NewRepository(store, objects)

	// ── Init LLM & agents ──
	llm := agents.NewLLMClient(agents.LLMConfig{
		Provider: cfg.LLMProvider, Model: cfg.LLMModel,
		APIKey: cfg.LLMAPIKey, BaseURL: cfg.LLMBaseURL,
	})
	researchAgent := agents.NewResearchAgent(llm)
	designAgent := agents.NewDesignAgent(llm)
	codeGen := agents.NewLLMCodeGenerator(llm)
	expAgent := agents.NewExperimentAgent(codeGen, &agents.DefaultExperimentRunner{}, "/tmp/atrpe-workspaces")
	verifyAgent := agents.NewVerificationAgent(cfg.VerificationChecks)
	writerAgent := agents.NewWriterAgent(llm)
	log("All agents ready\n")

	// ═══ STEP 1: DISCOVER ═══
	step++
	logSection(step, "DISCOVERY — finding writable Go projects")
	candidates, err := topics.DiscoverGitHubTrending(ctx, "https://api.github.com")
	fatalIf(err, "discovery")
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	log("  Found %d candidates. Top 3:", len(candidates))
	for i := 0; i < 3 && i < len(candidates); i++ {
		log("    %d. %s (%.3f)", i+1, candidates[i].Title, candidates[i].Score)
	}
	top := candidates[0]
	log("  ▶ Selected: %s\n", top.Title)

	// ═══ STEP 2: RESEARCH ═══
	step++
	logSection(step, "RESEARCH — LLM analyzing %s", top.Title)
	brief, err := researchAgent.Run(ctx, top)
	fatalIf(err, "research")
	log("  Concepts:")
	for i, c := range brief.CoreConcepts {
		log("    %d. %s", i+1, c)
	}
	log("  Sources: %d", len(brief.Sources))
	for _, s := range brief.Sources {
		log("    • %s", s.Title)
	}
	repo.SaveArtifact(ctx, "technical_briefs", brief.ArtifactID.String(), brief.TopicID, brief)

	// ═══ STEP 3: DESIGN ═══
	step++
	logSection(step, "DESIGN — LLM architecting example")
	design, err := designAgent.Run(ctx, brief)
	fatalIf(err, "design")
	log("  Components:")
	for _, c := range design.Components {
		log("    • %s (%s/%s)", c.Name, c.Type, c.Technology)
	}
	log("  TestPlan: %s — %d cases", design.TestPlan.Strategy, len(design.TestPlan.TestCases))
	for _, tc := range design.TestPlan.TestCases {
		log("    • %s: %s", tc.Name, tc.Command)
	}
	repo.SaveArtifact(ctx, "design_artifacts", design.ArtifactID.String(), design.TopicID, design)

	// ═══ STEP 4: EXPERIMENT ═══
	step++
	logSection(step, "EXPERIMENT — code generation + go test/vet/lint")
	result, err := expAgent.Run(ctx, design)
	fatalIf(err, "experiment")
	log("  Workspace: %s", result.Environment.Workdir)
	log("  Files (%d):", len(result.GeneratedFiles))
	for _, f := range result.GeneratedFiles {
		log("    • %s", f)
	}
	log("  Commands:")
	for _, c := range result.Commands {
		status := "✅"
		if c.ExitCode != 0 {
			status = "❌"
		}
		stderr := ""
		if c.Stderr != "" && c.ExitCode != 0 {
			stderr = " | " + strings.Split(c.Stderr, "\n")[0]
		}
		log("    %s %s (%dms)%s", status, c.Name, c.DurationMS, stderr)
	}
	repo.SaveArtifact(ctx, "experiment_results", result.ArtifactID.String(), result.TopicID, result)

	// ═══ STEP 5: VERIFY ═══
	step++
	logSection(step, "VERIFICATION — checking pass/fail")
	report, err := verifyAgent.Run(ctx, brief, result)
	fatalIf(err, "verification")
	log("  Lint:  %s | Vet: %s | Tests: %s | Links: %s",
		check(report.LintPassed), check(report.VetPassed), check(report.TestsPassed), check(report.LinksPassed))
	log("  Overall: %s", check(report.OverallPassed))
	for _, w := range report.Warnings {
		log("  ⚠️  %s", w)
	}
	for _, issue := range report.BlockingIssues {
		log("  🔴 %s", issue)
	}

	// ═══ STEP 6: WRITER ═══
	step++
	logSection(step, "WRITER — generating Zenn article")
	draft, err := writerAgent.Run(ctx, brief, result, report, "")
	fatalIf(err, "writer")
	log("  📝 %s", draft.Title)
	log("  🔗 slug: %s", draft.Slug)
	log("  %s | type=%s | topics=%v", draft.Emoji, draft.Type, draft.Topics)
	log("  Sections: bg=%d arch=%d impl=%d eval=%d troubleshoot=%d chars",
		len(draft.Sections.Background), len(draft.Sections.Architecture),
		len(draft.Sections.Implementation), len(draft.Sections.Evaluation),
		len(draft.Sections.Troubleshooting))

	// ── Save article ──
	outPath := fmt.Sprintf("/tmp/atrpe-article-%s.md", draft.Slug)
	os.WriteFile(outPath, []byte(draft.Body), 0644)

	// ═══ DONE ═══
	elapsed := time.Since(start).Round(time.Second)
	log("\n" + strings.Repeat("═", 60))
	log("✅ Pipeline finished in %s", elapsed)
	log("📄 Article: %s", outPath)
	log(strings.Repeat("═", 60))
}

func log(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}

func logSection(n int, format string, args ...interface{}) {
	header := fmt.Sprintf(format, args...)
	fmt.Printf("\n── STEP %d ── %s\n", n, header)
}

func fatalIf(err error, step string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ FAILED [%s]: %v\n", step, err)
		os.Exit(1)
	}
}

func check(b bool) string {
	if b { return "✅" }
	return "❌"
}
