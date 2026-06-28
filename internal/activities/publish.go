package activities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// ── Input / Result types ──

type PublishInput struct {
	DraftID     string                 `json:"draft_id,omitempty"`
	Draft       artifacts.ArticleDraft `json:"draft,omitempty"`
	IssueNumber int                    `json:"issue_number"`
	AutoMerge   bool                   `json:"auto_merge"`
}

type MergePublishInput struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type VerifyDraftMergedInput struct {
	Slug        string `json:"slug"`
	ExpectedBody string `json:"expected_body"` // hash of draft content
}

type VerifyDraftMergedResult struct {
	Merged  bool   `json:"merged"`
	Reason  string `json:"reason"`
}

type VerifyPublishMergeInput struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Slug  string `json:"slug"`
}

type VerifyPublishMergeResult struct {
	Merged  bool   `json:"merged"`
	PRURL   string `json:"pr_url,omitempty"`
	Message string `json:"message"`
}

type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// ── PublishArticle ──

// PublishArticle creates a publish PR that flips published:true.
// Fix 1: Before creating the publish PR, verifies the draft was actually
// merged into main by checking articles/<slug>.md exists on main with published:false.
// Fix 3: Encodes issue number into branch name so webhook can route without SQLite.
func (a *Activities) PublishArticle(ctx context.Context, input PublishInput) (*PublishResult, error) {
	var draft artifacts.ArticleDraft
	if input.Draft.Slug != "" {
		draft = input.Draft
	} else if input.DraftID != "" {
		artifactRepo := artifacts.NewRepository(a.Store, a.Objects)
		if err := artifactRepo.LoadArtifact(ctx, "article_drafts", input.DraftID, &draft); err != nil {
			return nil, fmt.Errorf("load draft by ID %s: %w", input.DraftID, err)
		}
	} else {
		return nil, fmt.Errorf("publish: must provide either draft or draft_id")
	}

	// Fix 1: Always verify draft PR actually merged before creating publish PR.
	// No bypass via AutoMerge. No bypass via empty repo. Hard gate.
	verified, err := a.VerifyDraftMerged(ctx, VerifyDraftMergedInput{
		Slug:         draft.Slug,
		ExpectedBody: draft.Body,
	})
	if err != nil || !verified.Merged {
		return nil, fmt.Errorf("draft PR not merged: %s", verified.Reason)
	}

	draft.Published = true
	markdown := buildZennMarkdown(draft)

	if _, err := a.Objects.Put(ctx,
		fmt.Sprintf("published/%s.md", draft.Slug),
		strings.NewReader(markdown),
		"text/markdown",
	); err != nil {
		return nil, fmt.Errorf("save published markdown: %w", err)
	}

	prURL := ""
	if a.Config != nil && a.Config.GitHubIssueRepo != "" {
		// Fix 3: Encode issue number in publish branch name
		prURL = a.createPublishPR(ctx, draft, markdown, input.IssueNumber)
		if prURL == "" {
			return &PublishResult{Slug: draft.Slug, Escalated: true},
				fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	// Record slug→issueNumber mapping for webhook routing
	if input.IssueNumber > 0 && a.Store != nil {
		_ = a.Store.SavePublishMapping(ctx, draft.Slug, input.IssueNumber)
	}

	return &PublishResult{
		Slug:      draft.Slug,
		PRURL:     prURL,
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

// ── VerifyDraftMerged ──

// Fix 1: VerifyDraftMerged checks GitHub API to confirm the draft PR was merged.
// Checks that articles/<slug>.md exists on main with published:false.
func (a *Activities) VerifyDraftMerged(ctx context.Context, input VerifyDraftMergedInput) (*VerifyDraftMergedResult, error) {
	repo := a.Config.GitHubIssueRepo
	if repo == "" {
		return &VerifyDraftMergedResult{
			Merged: false,
			Reason: "GITHUB_ISSUE_REPO not configured — cannot verify draft PR was merged. Set the env var.",
		}, nil
	}

	// Check main branch for articles/<slug>.md
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/articles/%s.md?ref=main", repo, input.Slug)
	resp, err := a.githubGet(ctx, fileURL)
	if err != nil {
		return &VerifyDraftMergedResult{
			Merged: false,
			Reason: fmt.Sprintf("Draft article not found on main branch (%s). Merge the draft PR first.", fileURL),
		}, nil
	}

	var fileInfo struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if json.Unmarshal(resp, &fileInfo) != nil {
		return &VerifyDraftMergedResult{Merged: false, Reason: "Cannot parse file info from GitHub"}, nil
	}

	// Decode and check it has published:false
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(fileInfo.Content, "\n", ""))
	if err != nil {
		return &VerifyDraftMergedResult{Merged: false, Reason: "Cannot decode file content"}, nil
	}

	content := string(decoded)
	if !strings.Contains(content, "published: false") && !strings.Contains(content, "published:false") {
		return &VerifyDraftMergedResult{
			Merged: false,
			Reason: "File on main does not have published:false — has the draft PR been merged?",
		}, nil
	}

	return &VerifyDraftMergedResult{
		Merged: true,
		Reason: fmt.Sprintf("✅ Draft PR merged — articles/%s.md found on main with published:false", input.Slug),
	}, nil
}

// ── MergePublish ──

func (a *Activities) MergePublish(ctx context.Context, input MergePublishInput) error {
	published := artifacts.PublishedArticle{
		ID:          input.Slug,
		Slug:        input.Slug,
		Title:       input.Title,
		PublishedAt: time.Now().UTC(),
		Platform:    "zenn",
		URL:         fmt.Sprintf("https://zenn.dev/articles/%s", input.Slug),
	}
	return a.Store.SavePublishedArticle(ctx, published)
}

// ── VerifyPublishMerge (Fix 7: direct API) ──

// Fix 7: Uses direct GitHub API (GET /pulls?head=...) not Search API.
func (a *Activities) VerifyPublishMerge(ctx context.Context, input VerifyPublishMergeInput) (*VerifyPublishMergeResult, error) {
	owner, repo := input.Owner, input.Repo
	if owner == "" || repo == "" {
		parts := strings.SplitN(a.Config.GitHubIssueRepo, "/", 2)
		if len(parts) == 2 {
			owner, repo = parts[0], parts[1]
		}
	}
	if owner == "" || repo == "" {
		return &VerifyPublishMergeResult{Merged: false, Message: "cannot determine repo"}, nil
	}

	// Fix 7: List PRs for this branch head pattern (not Search API)
	// The publish branch is atrpe-publish/<slug>-N<issueNumber>
	branchPrefix := fmt.Sprintf("atrpe-publish/%s", input.Slug)
	listURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=closed&sort=updated&direction=desc&per_page=10&head=%s",
		owner, repo, owner+":"+branchPrefix)

	resp, err := a.githubGet(ctx, listURL)
	if err != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: fmt.Sprintf("API error: %v", err)}, nil
	}

	var prs []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Merged  bool   `json:"merged_at"` // non-null if merged
	}
	if json.Unmarshal(resp, &prs) != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: "parse error"}, nil
	}

	for _, pr := range prs {
		// Verify the PR is actually merged (merged_at != nil)
		// Do a direct GET to check merged status
		prDetail, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, pr.Number))
		if err != nil {
			continue
		}
		var detail struct {
			MergedAt string `json:"merged_at"`
			HTMLURL  string `json:"html_url"`
		}
		if json.Unmarshal(prDetail, &detail) != nil {
			continue
		}
		if detail.MergedAt != "" {
			// Confirm main branch has published:true
			fileURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/articles/%s.md?ref=main", owner, repo, input.Slug)
			fileResp, err := a.githubGet(ctx, fileURL)
			if err == nil {
				if strings.Contains(string(fileResp), "published: true") || strings.Contains(string(fileResp), "published:true") {
					// Everything checks out
					if detail.HTMLURL == "" {
						detail.HTMLURL = pr.HTMLURL
					}
					return &VerifyPublishMergeResult{
						Merged:  true,
						PRURL:   detail.HTMLURL,
						Message: fmt.Sprintf("✅ Merged: %s (main has published:true)", detail.HTMLURL),
					}, nil
				}
			}
		}
	}

	return &VerifyPublishMergeResult{
		Merged:  false,
		Message: fmt.Sprintf("No merged publish PR found for slug '%s'", input.Slug),
	}, nil
}

// ── createPublishPR ──

// Fix 3: Branch name = atrpe-publish/<slug>-N<issueNumber>
// This encodes the issue number so the webhook can extract it without SQLite.
func (a *Activities) createPublishPR(ctx context.Context, draft artifacts.ArticleDraft, body string, issueNumber int) string {
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe-publish/%s-N%d", draft.Slug, issueNumber)

	// Get main HEAD SHA
	mainRef, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/ref/heads/main", repo))
	if err != nil {
		fmt.Printf("publish: get main ref failed: %v\n", err)
		return ""
	}
	var ref struct{ Object struct{ SHA string `json:"sha"` } `json:"object"` }
	if json.Unmarshal(mainRef, &ref) != nil {
		return ""
	}

	createRefPayload, _ := json.Marshal(map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", branchName),
		"sha": ref.Object.SHA,
	})
	_, _ = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), string(createRefPayload))

	// GET existing file SHA from main — MUST exist (draft was merged).
	// If the file isn't on main, the draft PR was never merged — hard fail.
	filePath := fmt.Sprintf("articles/%s.md", draft.Slug)
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=main", repo, filePath)
	existingResp, err := a.githubGet(ctx, fileURL)
	if err != nil {
		fmt.Printf("publish: file not found on main (draft not merged?): %v\n", err)
		return ""
	}
	var existing struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if json.Unmarshal(existingResp, &existing) != nil {
		return ""
	}

	// Double-check: file on main must still have published:false
	if existing.Content != "" {
		decoded, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(existing.Content, "\n", ""))
		if strings.Contains(string(decoded), "published: true") || strings.Contains(string(decoded), "published:true") {
			fmt.Printf("publish: file on main already has published:true — duplicate publish?\n")
			return ""
		}
	}

	filePayloadMap := map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
		"sha":     existing.SHA, // always set SHA — file must exist on main
	}
	filePayload, _ := json.Marshal(filePayloadMap)
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, filePath), string(filePayload))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return ""
	}

	prPayload, _ := json.Marshal(map[string]interface{}{
		"title": fmt.Sprintf("📤 Publish: %s", draft.Title),
		"head":  branchName,
		"base":  "main",
		"body":  fmt.Sprintf("Publish PR for **%s**\n\nMerging this sets `published: true` on Zenn.", draft.Title),
	})
	prResp, err := a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), string(prPayload))
	if err != nil {
		fmt.Printf("publish: create PR failed: %v\n", err)
		return ""
	}

	var prResult struct{ HTMLURL string `json:"html_url"` }
	if json.Unmarshal(prResp, &prResult) != nil {
		return ""
	}
	return prResult.HTMLURL
}

// ── Helpers ──

func buildZennMarkdown(draft artifacts.ArticleDraft) string {
	publishedStr := "false"
	if draft.Published {
		publishedStr = "true"
	}
	return fmt.Sprintf(`---
title: "%s"
emoji: "%s"
type: "%s"
topics: [%s]
published: %s
---

%s
`, draft.Title, draft.Emoji, draft.Type, quoteTopics(draft.Topics), publishedStr, draft.Body)
}

func quoteTopics(topics []string) string {
	quoted := make([]string, len(topics))
	for i, t := range topics {
		quoted[i] = fmt.Sprintf(`"%s"`, t)
	}
	return strings.Join(quoted, ", ")
}
