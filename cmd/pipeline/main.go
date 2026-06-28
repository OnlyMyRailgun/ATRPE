package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/activities"
	"github.com/OnlyMyRailgun/ATRPE/internal/agents"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/config"
	"github.com/OnlyMyRailgun/ATRPE/internal/knowledge"
	"github.com/OnlyMyRailgun/ATRPE/internal/objectstore"
	"github.com/OnlyMyRailgun/ATRPE/internal/topics"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	start := time.Now()

	// ── Load config ──
	cfg, err := config.Load()
	fatalIf(err, "config")
	log("╔══════════════════════════════════════════╗")
	log("║  ATRPE Pipeline — Phase 2 Full Chain     ║")
	log("╚══════════════════════════════════════════╝")
	log("Provider: %s | Model: %s", cfg.LLMProvider, cfg.LLMModel)
	log("Article language: %s | Bilingual: %v",
		getEnvDefault("DEFAULT_ARTICLE_LANGUAGE", "ja"),
		getEnvBool("BILINGUAL_ARTICLES", false))

	// ── Init stores ──
	store, err := knowledge.NewSQLiteStore("data/knowledge.db")
	fatalIf(err, "sqlite")
	defer store.Close()

	objects := objectstore.NewLocalObjectStore(os.TempDir() + "/atrpe-pipeline")
	repo := artifacts.NewRepository(store, objects)

	// ── Init LLM & agents with per-agent temperatures ──
	llmConfig := agents.LLMConfig{
		Provider:        cfg.LLMProvider,
		Model:           cfg.LLMModel,
		APIKey:          cfg.LLMAPIKey,
		BaseURL:         cfg.LLMBaseURL,
		TempResearch:    0.1,
		TempDesign:      0.3,
		TempCodeGen:     0.2,
		TempVerification: 0.0,
		TempWriter:      0.5,
	}
	llm := agents.NewLLMClient(llmConfig)

	researchAgent := agents.NewResearchAgent(llm)
	researchAgent.SetSnapshotStore(&objSnapshotAdapter{objects})
	researchAgent.SetCitationStore(&storeCitationAdapter{store})

	designAgent := agents.NewDesignAgent(llm)
	codeGen := agents.NewLLMCodeGenerator(llm)
	expAgent := agents.NewExperimentAgent(codeGen, &agents.DefaultExperimentRunner{}, "/tmp/atrpe-workspaces")
	verifyAgent := agents.NewVerificationAgent(cfg.VerificationChecks)
	writerAgent := agents.NewWriterAgent(llm)
	log("All agents ready with per-agent temperatures\n")

	// ═══ STEP 1: DISCOVER (all sources) ═══
	logSection(1, "DISCOVERY — searching all configured sources")
	rssURLs := getEnvSliceDefault("RSS_FEED_URLS", "https://go.dev/blog/feed.atom,https://kubernetes.io/feed.xml")
	candidates, err := topics.DiscoverAll(ctx, cfg.TopicSources, rssURLs, "https://api.github.com")
	fatalIf(err, "discovery")
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	log("  Found %d candidates across %s", len(candidates), strings.Join(cfg.TopicSources, ", "))
	for i := 0; i < 5 && i < len(candidates); i++ {
		log("    %d. [%s] %s (%.3f)", i+1, candidates[i].Source, candidates[i].Title, candidates[i].Score)
	}
	top := candidates[0]
	log("  ▶ Selected: %s\n", top.Title)

	// ═══ STEP 2: CONTENT AUDIT ═══
	logSection(2, "CONTENT AUDIT — checking existing coverage")
	acts := &activities.Activities{
		Config: cfg, Store: store, Objects: objects, LLM: llm,
		Research: researchAgent, Design: designAgent,
	}
	auditResult, err := acts.AuditTopics(ctx, activities.AuditTopicsInput{Candidates: candidates[:min(5, len(candidates))]})
	if err != nil {
		log("  ⚠️ Audit failed (%v) — proceeding anyway", err)
	} else {
		for _, a := range auditResult.Audits {
			passIcon := "✅"
			if !a.Passes {
				passIcon = "⛔"
			}
			log("  %s %s | rec=%.2f | sat=%s | existing=%d",
				passIcon, a.CandidateID[:8], a.Recommendation,
				a.SaturationLevel, a.ExistingCount)
		}
	}

	// ═══ STEP 3: RESEARCH (two-phase web-backed) ═══
	logSection(3, "RESEARCH — URL discovery → fetch → synthesize")
	brief, err := researchAgent.Run(ctx, top)
	fatalIf(err, "research")
	log("  Core concepts: %d | Claims: %d | Pitfalls: %d",
		len(brief.CoreConcepts), len(brief.SupportedClaims),
		len(brief.CommonPitfalls))
	log("  Sources (%d):", len(brief.Sources))
	for _, s := range brief.Sources {
		icon := "🌐"
		if !s.Fetched {
			icon = "⚠️fallback"
		}
		snap := ""
		if s.SnapshotURI != "" {
			snap = fmt.Sprintf(" [snap:%s]", s.SnapshotURI)
		}
		log("    %s %s (hash=%s)%s", icon, s.Title, s.ContentHash[:12], snap)
	}
	repo.SaveArtifact(ctx, "technical_briefs", brief.ArtifactID.String(), brief.TopicID, brief)

	// ═══ STEP 4: DESIGN ═══
	logSection(4, "DESIGN — LLM architecting example")
	design, err := designAgent.Run(ctx, brief)
	fatalIf(err, "design")
	log("  Components:")
	for _, c := range design.Components {
		log("    • %s (%s/%s)", c.Name, c.Type, c.Technology)
	}
	log("  TestPlan: %s — %d cases", design.TestPlan.Strategy, len(design.TestPlan.TestCases))
	repo.SaveArtifact(ctx, "design_artifacts", design.ArtifactID.String(), design.TopicID, design)

	// ═══ STEP 5: EXPERIMENT ═══
	logSection(5, "EXPERIMENT — code generation + go test/vet")
	result, err := expAgent.Run(ctx, design)
	fatalIf(err, "experiment")
	log("  Workspace: %s", result.Environment.Workdir)
	log("  Files (%d):", len(result.GeneratedFiles))
	for _, f := range result.GeneratedFiles {
		log("    • %s", f)
	}
	passCount, failCount := 0, 0
	for _, c := range result.Commands {
		status := "✅"
		if c.ExitCode != 0 {
			status = "❌"
			failCount++
		} else {
			passCount++
		}
		stderr := ""
		if c.Stderr != "" && c.ExitCode != 0 {
			stderr = " | " + strings.Split(c.Stderr, "\n")[0]
		}
		log("    %s %s (%dms)%s", status, c.Name, c.DurationMS, stderr)
	}
	log("  Summary: %d✅ %d❌", passCount, failCount)
	repo.SaveArtifact(ctx, "experiment_results", result.ArtifactID.String(), result.TopicID, result)

	// ═══ STEP 6: VERIFY ═══
	logSection(6, "VERIFICATION — checking pass/fail")
	report, err := verifyAgent.Run(ctx, brief, result)
	fatalIf(err, "verification")
	log("  Lint: %s | Vet: %s | Tests: %s | Links: %s",
		check(report.LintPassed), check(report.VetPassed),
		check(report.TestsPassed), check(report.LinksPassed))
	log("  Overall: %s", check(report.OverallPassed))
	for _, w := range report.Warnings {
		log("  ⚠️  %s", w)
	}
	for _, issue := range report.BlockingIssues {
		log("  🔴 %s", issue)
	}
	repo.SaveArtifact(ctx, "verification_reports", report.ArtifactID.String(), report.TopicID, report)

	// ═══ STEP 7: WRITER (with Zenn validation) ═══
	logSection(7, "WRITER — generating Zenn article")
	draft, err := writerAgent.Run(ctx, brief, result, report, "")
	fatalIf(err, "writer")
	log("  📝 %s", draft.Title)
	log("  🔗 slug: %s", draft.Slug)
	log("  %s | type=%s | topics=%v", draft.Emoji, draft.Type, draft.Topics)
	log("  Sections: bg=%d arch=%d impl=%d eval=%d troubleshoot=%d chars",
		len(draft.Sections.Background), len(draft.Sections.Architecture),
		len(draft.Sections.Implementation), len(draft.Sections.Evaluation),
		len(draft.Sections.Troubleshooting))

	validator := agents.NewZennValidator()
	errs := validator.Validate(draft)
	if len(errs) > 0 {
		log("  ⚠️  Zenn validation issues:")
		for _, e := range errs {
			log("    • %s", e.Error())
		}
	} else {
		log("  ✅ Zenn validation PASSED")
	}
	repo.SaveArtifact(ctx, "article_drafts", draft.ArtifactID.String(), draft.TopicID, draft)

	// ── Save article ──
	outPath := fmt.Sprintf("/tmp/atrpe-article-%s.md", draft.Slug)
	_ = os.WriteFile(outPath, []byte(draft.Body), 0644)

	// ═══ CLEANUP ═══
	if result.Environment.Workdir != "" {
		logSection(8, "CLEANUP — workspace retained for debugging")
		log("  Path: %s (retained 24h in full workflow)", result.Environment.Workdir)
	}

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
	if b {
		return "✅"
	}
	return "❌"
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1"
}

func getEnvSliceDefault(key, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = fallback
	}
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// ── Local adapters for standalone CLI use ──

type objSnapshotAdapter struct{ objects objectstore.ObjectStore }

func (a *objSnapshotAdapter) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := a.objects.Put(ctx, key, body, contentType)
	return err
}

type storeCitationAdapter struct{ store *knowledge.SQLiteStore }

func (a *storeCitationAdapter) RegisterCitation(ctx context.Context, url, contentHash, retrievedAt string) error {
	return a.store.RegisterCitation(ctx, artifacts.CitationRecord{
		ID:            contentHash[:12],
		SourceURL:     url,
		ContentHash:   contentHash,
		HashAlgorithm: "sha256",
		RetrievedAt:   retrievedAt,
	})
}
