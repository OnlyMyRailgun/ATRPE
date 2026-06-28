package activities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/objectstore"
)

// ── Types ──

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
	Slug         string `json:"slug"`
	ExpectedBody string `json:"expected_body"`
}

type VerifyDraftMergedResult struct {
	Merged bool   `json:"merged"`
	Reason string `json:"reason"`
}

// VerifyPublishMergeInput carries PR metadata for server-side verification.
type VerifyPublishMergeInput struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Slug     string `json:"slug"`
	PRNumber int    `json:"pr_number"`
	HeadRef  string `json:"head_ref"`
	HeadSHA  string `json:"head_sha"`
}

type VerifyPublishMergeResult struct {
	Merged  bool   `json:"merged"`
	PRURL   string `json:"pr_url,omitempty"`
	Message string `json:"message"`
}

type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	PRNumber  int    `json:"pr_number"`
	HeadRef   string `json:"head_ref"`
	HeadSHA   string `json:"head_sha"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// ── PublishArticle ──

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

	// HARD GATE: draft PR must be merged before publish
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
		fmt.Sprintf("pending_publish/%s.md", draft.Slug),
		strings.NewReader(markdown),
		"text/markdown",
	); err != nil {
		return nil, fmt.Errorf("save published markdown: %w", err)
	}

	prURL, prNumber, headRef, headSHA := "", 0, "", ""
	if a.Config != nil && a.Config.GitHubIssueRepo != "" {
		prURL, prNumber, headRef, headSHA = a.createPublishPR(ctx, draft, markdown, input.IssueNumber)
		if prURL == "" {
			return &PublishResult{Slug: draft.Slug, Escalated: true},
				fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	if input.IssueNumber > 0 && a.Store != nil {
		_ = a.Store.SavePublishMapping(ctx, draft.Slug, input.IssueNumber)
	}

	return &PublishResult{
		Slug:      draft.Slug,
		PRURL:     prURL,
		PRNumber:  prNumber,
		HeadRef:   headRef,
		HeadSHA:   headSHA,
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

// ── VerifyDraftMerged ──

func (a *Activities) VerifyDraftMerged(ctx context.Context, input VerifyDraftMergedInput) (*VerifyDraftMergedResult, error) {
	repo := a.Config.GitHubIssueRepo
	if repo == "" {
		return &VerifyDraftMergedResult{
			Merged: false,
			Reason: "GITHUB_ISSUE_REPO not configured — cannot verify draft PR was merged.",
		}, nil
	}

	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/articles/%s.md?ref=main", repo, input.Slug)
	resp, err := a.githubGet(ctx, fileURL)
	if err != nil {
		const notFoundMsg = "Draft article not found on main branch. Merge the draft PR first."
		return &VerifyDraftMergedResult{
			Merged: false,
			Reason: notFoundMsg,
		}, nil
	}

	var fileInfo struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if json.Unmarshal(resp, &fileInfo) != nil {
		return &VerifyDraftMergedResult{Merged: false, Reason: "Cannot parse file info from GitHub"}, nil
	}

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
	// 4: Move markdown from pending_publish → published in ObjectStore
	if a.Objects != nil {
		pendingKey := fmt.Sprintf("pending_publish/%s.md", input.Slug)
		if reader, err := a.Objects.Get(ctx, objectstore.URI(pendingKey)); err == nil {
			defer reader.Close()
			_, _ = a.Objects.Put(ctx, fmt.Sprintf("published/%s.md", input.Slug), reader, "text/markdown")
		}
	}

	return a.Store.SavePublishedArticle(ctx, published)
}

// ── VerifyPublishMerge (direct API — no search, no heuristic) ──

// VerifyPublishMerge uses the PR number and head SHA stored at publish time.
//
// Three-point verification:
//  1. GET /repos/{owner}/{repo}/pulls/{pr_number} → merged==true, head.sha matches
//  2. GET /repos/{owner}/{repo}/pulls/{pr_number}/files → only target article changed
//  3. GET /repos/{owner}/{repo}/contents/articles/{slug}.md?ref=main → published:true
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

	// Point 1: GET /pulls/{number} → verify merged + head SHA matches
	if input.PRNumber <= 0 {
		return &VerifyPublishMergeResult{Merged: false, Message: "no PR number provided — cannot verify"}, nil
	}

	prURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, input.PRNumber)
	prResp, err := a.githubGet(ctx, prURL)
	if err != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: fmt.Sprintf("GET pull request failed: %v", err)}, nil
	}

	var pr struct {
		MergedAt string `json:"merged_at"`
		HTMLURL  string `json:"html_url"`
		State    string `json:"state"`
		Head     struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if json.Unmarshal(prResp, &pr) != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: "cannot parse PR response"}, nil
	}

	if pr.MergedAt == "" {
		return &VerifyPublishMergeResult{Merged: false, Message: "PR exists but is not merged"}, nil
	}

	// 2: Verify head SHA matches what we stored at publish time
	if input.HeadSHA != "" && pr.Head.SHA != input.HeadSHA {
		return &VerifyPublishMergeResult{
			Merged: false,
			Message: fmt.Sprintf("head SHA mismatch: stored %s, API returned %s", input.HeadSHA[:12], pr.Head.SHA[:12]),
		}, nil
	}
	expectedRef := fmt.Sprintf("atrpe-publish/%s-N%d", input.Slug, input.PRNumber)
	if pr.Head.Ref != expectedRef && input.HeadRef != "" && pr.Head.Ref != input.HeadRef {
		return &VerifyPublishMergeResult{
			Merged: false,
			Message: fmt.Sprintf("head ref mismatch: expected %s, got %s", expectedRef, pr.Head.Ref),
		}, nil
	}

	// Point 2: GET /pulls/{number}/files → verify only target article changed
	filesURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/files", owner, repo, input.PRNumber)
	filesResp, err := a.githubGet(ctx, filesURL)
	if err != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: fmt.Sprintf("GET PR files failed: %v", err)}, nil
	}

	var files []struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
	}
	if json.Unmarshal(filesResp, &files) != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: "cannot parse PR files"}, nil
	}

	expectedFile := fmt.Sprintf("articles/%s.md", input.Slug)
	hasExpectedFile := false
	for _, f := range files {
		if f.Filename == expectedFile {
			hasExpectedFile = true
		} else if !strings.HasPrefix(f.Filename, "data/artifacts/manifests/") {
			// Allow manifest files. Any other file is suspicious.
			return &VerifyPublishMergeResult{
				Merged: false,
				Message: fmt.Sprintf("unexpected file changed in publish PR: %s (only %s and manifests allowed)", f.Filename, expectedFile),
			}, nil
		}
	}
	if !hasExpectedFile {
		return &VerifyPublishMergeResult{
			Merged: false,
			Message: fmt.Sprintf("target article %s not found in PR changed files", expectedFile),
		}, nil
	}

	// Point 3: GET /contents/articles/{slug}.md?ref=main → published:true
	contentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/articles/%s.md?ref=main", owner, repo, input.Slug)
	contentsResp, err := a.githubGet(ctx, contentsURL)
	if err != nil {
		return &VerifyPublishMergeResult{
			Merged: false,
			Message: fmt.Sprintf("article not found on main branch: %v", err),
		}, nil
	}

	var contents struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if json.Unmarshal(contentsResp, &contents) != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: "cannot parse file contents"}, nil
	}

	decoded, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(contents.Content, "\n", ""))
	contentStr := string(decoded)
	if !strings.Contains(contentStr, "published: true") && !strings.Contains(contentStr, "published:true") {
		return &VerifyPublishMergeResult{
			Merged: false,
			Message: "article on main does not have published:true",
		}, nil
	}

	return &VerifyPublishMergeResult{
		Merged:  true,
		PRURL:   pr.HTMLURL,
		Message: fmt.Sprintf("✅ Verified: PR #%d merged, head matches, only %s changed, main has published:true", input.PRNumber, expectedFile),
	}, nil
}

// ── createPublishPR ──

// Returns (prURL, prNumber, headRef, headSHA).
func (a *Activities) createPublishPR(ctx context.Context, draft artifacts.ArticleDraft, _ string, issueNumber int) (string, int, string, string) {
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe-publish/%s-N%d", draft.Slug, issueNumber)

	mainRef, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/ref/heads/main", repo))
	if err != nil {
		fmt.Printf("publish: get main ref failed: %v\n", err)
		return "", 0, "", ""
	}
	var ref struct{ Object struct{ SHA string `json:"sha"` } `json:"object"` }
	if json.Unmarshal(mainRef, &ref) != nil {
		return "", 0, "", ""
	}

	createRefPayload, _ := json.Marshal(map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", branchName),
		"sha": ref.Object.SHA,
	})
	_, _ = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), string(createRefPayload))

	// ── Read ACTUAL content from main (preserves human edits) ──
	filePath := fmt.Sprintf("articles/%s.md", draft.Slug)
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=main", repo, filePath)
	existingResp, err := a.githubGet(ctx, fileURL)
	if err != nil {
		fmt.Printf("publish: file not found on main (draft not merged?): %v\n", err)
		return "", 0, "", ""
	}
	var existing struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if json.Unmarshal(existingResp, &existing) != nil {
		return "", 0, "", ""
	}
	if existing.Content == "" {
		fmt.Printf("publish: empty content on main\n")
		return "", 0, "", ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(existing.Content, "\n", ""))
	if err != nil {
		fmt.Printf("publish: cannot decode main content: %v\n", err)
		return "", 0, "", ""
	}
	mainContent := string(decoded)

	// Guard: already published?
	if strings.Contains(mainContent, "published: true") || strings.Contains(mainContent, "published:true") {
		fmt.Printf("publish: file on main already has published:true\n")
		return "", 0, "", ""
	}

		// ── 1: YAML frontmatter parser — only touches the --- delimited block ──
		// This prevents false matches if body text contains "published: false".
		publishContent, flipped := flipFrontmatterPublished(mainContent)
		if !flipped {
			fmt.Printf("publish: could not flip published:false->true in frontmatter\n")
			return "", 0, "", ""
		}

	filePayload, _ := json.Marshal(map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(publishContent)),
		"branch":  branchName,
		"sha":     existing.SHA,
	})
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, filePath), string(filePayload))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return "", 0, "", ""
	}

	prPayload, _ := json.Marshal(map[string]interface{}{
		"title": fmt.Sprintf("📤 Publish: %s", draft.Title),
		"head":  branchName,
		"base":  "main",
		"body":  fmt.Sprintf("Publish PR for **%s**\n\nOnly change: `published: false` → `published: true`.\nArticle body preserved from main branch (includes human edits).", draft.Title),
	})
	prResp, err := a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), string(prPayload))
	if err != nil {
		fmt.Printf("publish: create PR failed: %v\n", err)
		return "", 0, "", ""
	}

	var prResult struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
		Head    struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if json.Unmarshal(prResp, &prResult) != nil {
		return "", 0, "", ""
	}

	return prResult.HTMLURL, prResult.Number, prResult.Head.Ref, prResult.Head.SHA
}

// flipFrontmatterPublished finds the YAML frontmatter block (delimited by ---)
// and flips "published: false" → "published: true" within that block only.
// Returns (modified, true) on success; (original, false) if not found or already true.
func flipFrontmatterPublished(md string) (string, bool) {
	if !strings.HasPrefix(md, "---") {
		return md, false
	}
	end := strings.Index(md[3:], "\n---")
	if end < 0 {
		return md, false
	}
	fmBlock := md[3 : 3+end]
	rest := md[3+end:]

	if strings.Contains(fmBlock, "published: true") || strings.Contains(fmBlock, "published:true") {
		return md, false
	}

	newFM := strings.Replace(fmBlock, "published: false", "published: true", 1)
	if newFM == fmBlock {
		newFM = strings.Replace(fmBlock, "published:false", "published:true", 1)
	}
	if newFM == fmBlock {
		return md, false
	}

	return "---" + newFM + rest, true
}

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
