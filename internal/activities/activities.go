package activities

import (
	"context"

	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/atrpe/internal/agents"
	"github.com/your-org/atrpe/internal/artifacts"
	"github.com/your-org/atrpe/internal/config"
	"github.com/your-org/atrpe/internal/github"
	"github.com/your-org/atrpe/internal/knowledge"
	"github.com/your-org/atrpe/internal/objectstore"
	"github.com/your-org/atrpe/internal/topics"
)

// Activities bundles all Temporal activities with their dependencies.
type Activities struct {
	Config   *config.Settings
	Store    *knowledge.SQLiteStore
	Objects  objectstore.ObjectStore
	LLM      *agents.LLMClient
	GitHub   *github.AppClient
	Research *agents.ResearchAgent
	Design   *agents.DesignAgent
}

// New creates an Activities instance with all dependencies wired.
func New(cfg *config.Settings, store *knowledge.SQLiteStore, objects objectstore.ObjectStore) *Activities {
	llm := agents.NewLLMClient(agents.LLMConfig{
		Provider: cfg.LLMProvider,
		Model:    cfg.LLMModel,
		APIKey:   cfg.LLMAPIKey,
		BaseURL:  cfg.LLMBaseURL,
	})

	var ghClient *github.AppClient
	if cfg.GitHubAppID > 0 && cfg.GitHubAppPrivateKey != "" && cfg.GitHubAppInstallationID > 0 {
		var err error
		ghClient, err = github.NewAppClient(cfg.GitHubAppID, cfg.GitHubAppPrivateKey, cfg.GitHubAppInstallationID)
		if err != nil {
			fmt.Printf("⚠️ GitHub App init failed: %v\n", err)
		}
	}

	// Wire ResearchAgent with snapshot persistence and citation registry
	researchAgent := agents.NewResearchAgent(llm)
	researchAgent.SetSnapshotStore(&objectSnapshotAdapter{objects: objects})
	researchAgent.SetCitationStore(&knowledgeCitationAdapter{store: store})

	return &Activities{
		Config:   cfg,
		Store:    store,
		Objects:  objects,
		LLM:      llm,
		GitHub:   ghClient,
		Research: researchAgent,
		Design:   agents.NewDesignAgent(llm),
	}
}

// -- Discovery --

type DiscoverTopicsResult struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

// ResolveCandidateInput resolves a human-provided selection to a candidate ID.
// Accepts either a 12-char hex candidate_id or a numeric position ("1", "2", etc).
type ResolveCandidateInput struct {
	Selection string `json:"selection"`
}

type ResolveCandidateResult struct {
	CandidateID string `json:"candidate_id"`
}

func (a *Activities) ResolveCandidateID(ctx context.Context, input ResolveCandidateInput) (*ResolveCandidateResult, error) {
	sel := strings.TrimSpace(input.Selection)

	// If it looks like a position number, resolve from stored candidates
	if n, err := strconv.Atoi(sel); err == nil && n > 0 {
		candidates, err := a.Store.ListTopicCandidates(ctx, n)
		if err != nil || len(candidates) < n {
			return nil, fmt.Errorf("invalid position %d: %w", n, err)
		}
		return &ResolveCandidateResult{CandidateID: candidates[n-1].ID}, nil
	}

	// Otherwise treat as a direct candidate_id
	return &ResolveCandidateResult{CandidateID: sel}, nil
}

func (a *Activities) DiscoverTopics(ctx context.Context) (*DiscoverTopicsResult, error) {
	rssURLs := getEnvSliceDefault("RSS_FEED_URLS", "https://go.dev/blog/feed.atom,https://kubernetes.io/feed.xml")
	candidates, err := topics.DiscoverAll(ctx, a.Config.TopicSources, rssURLs, "https://api.github.com")
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	// Store all candidates in SQLite
	for _, c := range candidates {
		a.Store.SaveTopicCandidate(ctx, c)
	}
	// Return top 5
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	return &DiscoverTopicsResult{Candidates: candidates}, nil
}

// -- Create Topic Selection Issue (Decision Sheet) --

type CreateTopicIssueInput struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
	Audits     []ContentAuditResult       `json:"audits,omitempty"` // optional per-candidate audit data
}

type CreateTopicIssueResult struct {
	IssueURL    string `json:"issue_url"`
	IssueNumber int    `json:"issue_number"`
}

func (a *Activities) CreateTopicIssue(ctx context.Context, input CreateTopicIssueInput) (*CreateTopicIssueResult, error) {
	// Build audit lookup
	auditByID := make(map[string]ContentAuditResult, len(input.Audits))
	for _, audit := range input.Audits {
		auditByID[audit.CandidateID] = audit
	}

	hasAudits := len(input.Audits) > 0

	var sb strings.Builder
	sb.WriteString("## 🎯 ATRPE Topic Decision Sheet\n\n")

	if hasAudits {
		sb.WriteString("> Each topic has been audited against Zenn, Qiita, HackerNews, and our own article history.\n\n")
		sb.WriteString("| # | Pass | 📊 Rec | Title | Saturation | Why Now |\n")
		sb.WriteString("|---|------|--------|-------|------------|--------|\n")
		for i, c := range input.Candidates {
			audit, ok := auditByID[c.ID]
			passIcon := "⏳"
			recStr := "—"
			satStr := "—"
			whyNow := "—"
			if ok {
				if audit.Passes {
					passIcon = "✅"
				} else {
					passIcon = "⛔"
				}
				recStr = fmt.Sprintf("%.2f", audit.Recommendation)
				satStr = audit.SaturationLevel
				whyNow = shorten(audit.WhyNow, 60)
			}
			sb.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s |\n",
				i+1, passIcon, recStr, c.Title, satStr, whyNow))
		}
		sb.WriteString("\n---\n\n")
	}

	for i, c := range input.Candidates {
		audit, ok := auditByID[c.ID]

		if ok && !audit.Passes {
			sb.WriteString(fmt.Sprintf("### %d. ⛔ %s *(SKIPPED)*\n\n", i+1, c.Title))
		} else if ok {
			sb.WriteString(fmt.Sprintf("### %d. %s %s\n\n", i+1, audit.EmojiForTopic(), c.Title))
		} else {
			sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, c.Title))
		}

		sb.WriteString(fmt.Sprintf("- **ID**: `%s`\n", c.ID))
		sb.WriteString(fmt.Sprintf("- **URL**: %s\n", c.URL))
		sb.WriteString(fmt.Sprintf("- **Source**: %s\n", c.Source))

		if ok {
			sb.WriteString(fmt.Sprintf("- **推薦指数 (Recommendation)**: %.2f\n", audit.Recommendation))
			sb.WriteString(fmt.Sprintf("- **飽和度 (Saturation)**: %s (%d existing articles found)\n", audit.SaturationLevel, audit.ExistingCount))

			if audit.OwnOverlap {
				sb.WriteString("- ⚠️ **注意**: 過去に類似トピックを公開済みです\n")
			}

			sb.WriteString(fmt.Sprintf("\n#### 🔥 なぜ今書くべきか (Why Now)\n%s\n\n", audit.WhyNow))
			sb.WriteString(fmt.Sprintf("#### 🕳️ 既存コンテンツのギャップ (Content Gap)\n%s\n\n", audit.ExistingGaps))
			sb.WriteString(fmt.Sprintf("#### ✨ 差別化ポイント (Differentiation)\n%s\n\n", audit.Differentiation))
			sb.WriteString(fmt.Sprintf("#### 🧪 コード検証可能な部分 (Testable Part)\n%s\n\n", audit.TestablePart))
			sb.WriteString(fmt.Sprintf("#### ⚠️ リスク (Risks)\n%s\n\n", audit.Risks))

			if audit.SuggestedTitle != "" {
				sb.WriteString(fmt.Sprintf("#### 📝 提案タイトル (Suggested Title)\n> %s\n\n", audit.SuggestedTitle))
			}

			if audit.DontWriteReason != "" {
				sb.WriteString(fmt.Sprintf("#### ❌ 書かない理由 (Don't Write Because)\n%s\n\n", audit.DontWriteReason))
			}
		} else {
			sb.WriteString(fmt.Sprintf("- **スコア (Score)**: %.3f\n\n", c.Score))
		}

		sb.WriteString("---\n\n")
	}

	sb.WriteString("\n## 📋 How to Select\n\n")
	sb.WriteString("Comment on this issue to select a topic:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("/select <candidate_id>    # Start the article workflow\n")
	sb.WriteString("/abort                    # Cancel this workflow\n")
	sb.WriteString("```\n\n")
	sb.WriteString("*🤖 ATRPE Content Audit powered by real-time platform searches*")

	body := sb.String()

	// Try to create a real GitHub issue
	issueURL := "internal://topic-selection"
	if a.Config.GitHubIssueRepo != "" {
		var url string
		var err error
		if a.GitHub != nil {
			url, err = a.createIssueViaApp(ctx, "ATRPE Topic Candidates", body)
		} else if a.Config.GitHubToken != "" {
			url, err = createGitHubIssue(ctx, a.Config.GitHubToken, a.Config.GitHubIssueRepo, "ATRPE Topic Candidates", body)
		}
		if err != nil {
			fmt.Printf("⚠️ GitHub issue creation failed: %v\nFalling back to log output.\n\n%s\n", err, body)
		} else {
			issueURL = url
			fmt.Printf("✅ Topic selection issue created: %s\n", url)
			return &CreateTopicIssueResult{IssueURL: issueURL, IssueNumber: extractIssueNumber(issueURL)}, nil
		}
	}

	fmt.Printf("\n=== TOPIC SELECTION ISSUE ===\n%s=== END ===\n", body)
	return &CreateTopicIssueResult{IssueURL: issueURL, IssueNumber: extractIssueNumber(issueURL)}, nil
}

// -- Post Comment --

type PostCommentInput struct {
	IssueNumber int    `json:"issue_number"`
	Body        string `json:"body"`
}

func (a *Activities) PostComment(ctx context.Context, input PostCommentInput) error {
	if a.GitHub == nil {
		return fmt.Errorf("GitHub App not configured")
	}
	return postCommentViaApp(ctx, a.GitHub, a.Config.GitHubIssueRepo, input.IssueNumber, input.Body)
}

func postCommentViaApp(ctx context.Context, client *github.AppClient, repo string, issueNumber int, body string) error {
	payload := map[string]string{"body": body}
	b, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", repo, issueNumber)
	resp, err := client.PostJSON(url, string(b))
	if err != nil {
		return fmt.Errorf("post comment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// -- Experiment --

type ExperimentInput struct {
	Design artifacts.DesignArtifact `json:"design"`
}

type PatchExperimentInput struct {
	Design artifacts.DesignArtifact  `json:"design"`
	Result artifacts.ExperimentResult `json:"result"`
}

type UpdateDesignInput struct {
	Design artifacts.DesignArtifact `json:"design"`
	Patch  artifacts.PatchResult    `json:"patch"`
}

func (a *Activities) RunExperiment(ctx context.Context, input ExperimentInput) (*artifacts.ExperimentResult, error) {
	design := input.Design
	agent := agents.NewExperimentAgent(
		agents.NewLLMCodeGenerator(a.LLM),
		&agents.DefaultExperimentRunner{},
		"/tmp/atrpe-workspaces",
	)
	result, err := agent.Run(ctx, design)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	repo.SaveArtifact(ctx, "experiment_results", result.ArtifactID.String(), result.TopicID, result)
	return &result, nil
}

// -- Verify --

type VerifyInput struct {
	Brief  artifacts.TechnicalBrief  `json:"brief"`
	Result artifacts.ExperimentResult `json:"result"`
}

func (a *Activities) VerifyExperiment(ctx context.Context, input VerifyInput) (*artifacts.VerificationReport, error) {
	agent := agents.NewVerificationAgent(a.Config.VerificationChecks)
	report, err := agent.Run(ctx, input.Brief, input.Result)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	repo.SaveArtifact(ctx, "verification_reports", report.ArtifactID.String(), report.TopicID, report)
	return &report, nil
}

// -- Patch Experiment --

func (a *Activities) PatchExperiment(ctx context.Context, input PatchExperimentInput) (*artifacts.PatchResult, error) {
	agent := agents.NewExperimentAgent(
		agents.NewLLMCodeGenerator(a.LLM),
		&agents.DefaultExperimentRunner{},
		"/tmp/atrpe-workspaces",
	)
	patch, err := agent.Patch(ctx, input.Design, input.Result)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	repo.SaveArtifact(ctx, "patch_results", patch.ArtifactID.String(), patch.TopicID, patch)
	return &patch, nil
}

// -- Update Design --

func (a *Activities) UpdateDesign(ctx context.Context, input UpdateDesignInput) (*artifacts.DesignArtifact, error) {
	updated, err := a.Design.Update(ctx, input.Design, input.Patch)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	repo.SaveArtifact(ctx, "design_artifacts", updated.ArtifactID.String(), updated.TopicID, updated)
	return &updated, nil
}

// -- Generate Draft --

type GenerateDraftInput struct {
	Brief       artifacts.TechnicalBrief    `json:"brief"`
	Result      artifacts.ExperimentResult  `json:"result"`
	Report      artifacts.VerificationReport `json:"report"`
	ChangeNotes string                      `json:"change_notes"`
}

func (a *Activities) GenerateDraft(ctx context.Context, input GenerateDraftInput) (*artifacts.ArticleDraft, error) {
	// Default article language from config (env: DEFAULT_ARTICLE_LANGUAGE)
	lang := getEnvDefault("DEFAULT_ARTICLE_LANGUAGE", "ja")

	// Generate primary language draft
	primaryAgent := agents.NewWriterAgentWithLanguage(a.LLM, lang)
	draft, err := primaryAgent.Run(ctx, input.Brief, input.Result, input.Report, input.ChangeNotes)
	if err != nil {
		return nil, err
	}

	// Validate the draft against Zenn conventions
	validator := agents.NewZennValidator()
	if errs := validator.Validate(draft); len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		fmt.Printf("⚠️ Zenn validation warnings for draft %s:\n  %s\n", draft.Slug, strings.Join(msgs, "\n  "))
	}

	repo := artifacts.NewRepository(a.Store, a.Objects)
	repo.SaveArtifact(ctx, "article_drafts", draft.ArtifactID.String(), draft.TopicID, draft)

	// Generate secondary language draft if bilingual is enabled
	if getEnvBool("BILINGUAL_ARTICLES", false) {
		secondaryLang := "en"
		if lang == "en" {
			secondaryLang = "ja"
		}
		secondaryAgent := agents.NewWriterAgentWithLanguage(a.LLM, secondaryLang)
		enDraft, enErr := secondaryAgent.Run(ctx, input.Brief, input.Result, input.Report, input.ChangeNotes)
		if enErr == nil {
			repo.SaveArtifact(ctx, "article_drafts", enDraft.ArtifactID.String(), enDraft.TopicID, enDraft)
			fmt.Printf("✅ Generated %s secondary draft: %s\n", secondaryLang, enDraft.Slug)
		} else {
			fmt.Printf("⚠️ Secondary language (%s) generation failed: %v\n", secondaryLang, enErr)
		}
	}

	return &draft, nil
}

// -- Create Article PR --

type CreateArticlePRInput struct {
	Draft       artifacts.ArticleDraft `json:"draft"`
	IssueNumber int                    `json:"issue_number"`
}

type CreateArticlePRResult struct {
	PRURL string `json:"pr_url"`
}

func (a *Activities) CreateArticlePR(ctx context.Context, input CreateArticlePRInput) (*CreateArticlePRResult, error) {
	draft := input.Draft
	body := buildZennMarkdown(draft)
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe/%s-%s", draft.Slug, time.Now().Format("0102-1504"))

	// 1. Get main HEAD SHA
	mainRef, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/ref/heads/main", repo))
	if err != nil {
		return nil, fmt.Errorf("get main ref: %w", err)
	}
	var ref struct{ Object struct{ SHA string `json:"sha"` } `json:"object"` }
	json.Unmarshal(mainRef, &ref)

	// 2. Create branch from main
	createRefPayload := fmt.Sprintf(`{"ref":"refs/heads/%s","sha":"%s"}`, branchName, ref.Object.SHA)
	_, err = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), createRefPayload)
	if err != nil {
		// Branch might already exist — that's ok
		fmt.Printf("Branch creation note: %v\n", err)
	}

	// 3. Write article file
	filePayload := map[string]string{
		"message": fmt.Sprintf("ATRPE: %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
	}
	fileB, _ := json.Marshal(filePayload)
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/articles/%s.md", repo, draft.Slug), string(fileB))
	if err != nil {
		return nil, fmt.Errorf("write article file: %w", err)
	}

	// 4. Create PR
	prBody := fmt.Sprintf("ATRPE generated article: **%s**\\n\\nReview and merge to publish on Zenn.\\n\\nCloses #%d\\n\\n---\\n🤖 Generated with [ATRPE](https://github.com/OnlyMyRailgun/ATRPE)", draft.Title, input.IssueNumber)
	prPayload := fmt.Sprintf(`{"title":"📝 %s","head":"%s","base":"main","body":"%s"}`, draft.Title, branchName, prBody)
	prResp, err := a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), prPayload)
	if err != nil {
		return nil, fmt.Errorf("create PR: %w", err)
	}

	var prResult struct{ HTMLURL string `json:"html_url"` }
	json.Unmarshal(prResp, &prResult)

	return &CreateArticlePRResult{PRURL: prResult.HTMLURL}, nil
}

func (a *Activities) githubGet(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := a.GitHub.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub GET error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (a *Activities) githubPost(ctx context.Context, url, payload string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.GitHub.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub POST error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (a *Activities) githubPut(ctx context.Context, url, payload string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.GitHub.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub PUT error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func extractIssueNumber(url string) int {
	// URL format: https://github.com/owner/repo/issues/N
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if n, err := strconv.Atoi(last); err == nil {
			return n
		}
	}
	return 0
}

// createIssueViaApp creates an issue using the GitHub App client.
func (a *Activities) createIssueViaApp(ctx context.Context, title, body string) (string, error) {
	return createGitHubIssueViaClient(ctx, a.GitHub, a.Config.GitHubIssueRepo, title, body)
}

// createGitHubIssueViaClient uses an AppClient to create a GitHub issue.
func createGitHubIssueViaClient(ctx context.Context, client *github.AppClient, repo, title, body string) (string, error) {
	payload := map[string]string{"title": title, "body": body}
	b, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.github.com/repos/%s/issues", repo)
	resp, err := client.PostJSON(url, string(b))
	if err != nil {
		return "", fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return result.HTMLURL, nil
}

// createGitHubIssue calls the GitHub API to create an issue (PAT fallback).
func createGitHubIssue(ctx context.Context, token, repo, title, body string) (string, error) {
	payload := map[string]string{"title": title, "body": body}
	b, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.github.com/repos/%s/issues", repo)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return result.HTMLURL, nil
}

// -- Research --

type ResearchInput struct {
	CandidateID string `json:"candidate_id"`
}

func (a *Activities) ResearchTopic(ctx context.Context, input ResearchInput) (*artifacts.TechnicalBrief, error) {
	candidate, err := a.Store.GetTopicCandidate(ctx, input.CandidateID)
	if err != nil {
		return nil, err
	}
	brief, err := a.Research.Run(ctx, candidate)
	if err != nil {
		return nil, err
	}
	// Save brief to object store + SQLite
	repo := artifacts.NewRepository(a.Store, a.Objects)
	if _, err := repo.SaveArtifact(ctx, "technical_briefs", brief.ArtifactID.String(), brief.TopicID, brief); err != nil {
		return nil, err
	}
	return &brief, nil
}

// -- Design --

type DesignInput struct {
	Brief artifacts.TechnicalBrief `json:"brief"`
}

func (a *Activities) DesignArchitecture(ctx context.Context, input DesignInput) (*artifacts.DesignArtifact, error) {
	design, err := a.Design.Run(ctx, input.Brief)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	if _, err := repo.SaveArtifact(ctx, "design_artifacts", design.ArtifactID.String(), design.TopicID, design); err != nil {
		return nil, err
	}
	return &design, nil
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

// getEnvSliceDefault reads an env var or falls back to a default, splitting on comma.
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


// ── Adapters for agent persistence interfaces ──────────────────────────

// objectSnapshotAdapter adapts objectstore.ObjectStore to agents.SnapshotStore.
type objectSnapshotAdapter struct {
	objects objectstore.ObjectStore
}

func (a *objectSnapshotAdapter) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := a.objects.Put(ctx, key, body, contentType)
	return err
}

// knowledgeCitationAdapter adapts knowledge.SQLiteStore to agents.CitationStore.
type knowledgeCitationAdapter struct {
	store *knowledge.SQLiteStore
}

func (a *knowledgeCitationAdapter) RegisterCitation(ctx context.Context, url, contentHash, retrievedAt string) error {
	return a.store.RegisterCitation(ctx, artifacts.CitationRecord{
		ID:            contentHash[:12],
		SourceURL:     url,
		ContentHash:   contentHash,
		HashAlgorithm: "sha256",
		RetrievedAt:   retrievedAt,
	})
}
