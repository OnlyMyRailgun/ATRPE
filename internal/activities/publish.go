package activities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
)

// PublishInput holds all data needed for the publish step.
// Supports two modes:
//  1. Draft-based: pass `draft` (ArticleDraft) directly from workflow state.
//  2. ID-based: pass `draft_id` to load from ObjectStore (legacy).
type PublishInput struct {
	DraftID  string                `json:"draft_id,omitempty"` // ID-based mode
	Draft    artifacts.ArticleDraft `json:"draft,omitempty"`    // direct draft mode
	AutoMerge bool                 `json:"auto_merge"`
}

// PublishResult holds the outcome of the publish step.
type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// PublishArticle publishes a Zenn article draft to the Zenn repository.
// Steps: resolve draft → flip published:true → save markdown → create publish PR → record.
// Supports both direct draft (from workflow) and ID-based loading (legacy).
func (a *Activities) PublishArticle(ctx context.Context, input PublishInput) (*PublishResult, error) {
	// 1. Resolve draft — direct or ID-based
	var draft artifacts.ArticleDraft
	if input.Draft.Slug != "" {
		// Direct draft mode (from workflow state)
		draft = input.Draft
	} else if input.DraftID != "" {
		// ID-based mode (load from ObjectStore)
		artifactRepo := artifacts.NewRepository(a.Store, a.Objects)
		if err := artifactRepo.LoadArtifact(ctx, "article_drafts", input.DraftID, &draft); err != nil {
			return nil, fmt.Errorf("load draft by ID %s: %w", input.DraftID, err)
		}
	} else {
		return nil, fmt.Errorf("publish: must provide either draft or draft_id")
	}

	// 2. Flip published: true for the final publish commit
	draft.Published = true
	markdown := buildZennMarkdown(draft)

	// 3. Save published markdown to ObjectStore
	if _, err := a.Objects.Put(ctx,
		fmt.Sprintf("published/%s.md", draft.Slug),
		strings.NewReader(markdown),
		"text/markdown",
	); err != nil {
		return nil, fmt.Errorf("save published markdown: %w", err)
	}

	// 4. Create publish PR via GitHub API if repo is configured
	prURL := ""
	if a.Config != nil && a.Config.GitHubIssueRepo != "" {
		publishBody := fmt.Sprintf("ATRPE publish: **%s**\n\n%s", draft.Title, markdown)
		// Reuse CreateArticlePR's GitHub API flow but with published:true
		prURL = a.createPublishPR(ctx, draft, publishBody)
		if prURL == "" {
			return &PublishResult{
				Slug:      draft.Slug,
				PRURL:     "",
				Escalated: true,
			}, fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	// 5. Record published article in knowledge store
	published := artifacts.PublishedArticle{
		ID:          uuid.New().String(),
		Slug:        draft.Slug,
		Title:       draft.Title,
		PublishedAt: time.Now().UTC(),
		Platform:    "zenn",
		URL:         fmt.Sprintf("https://zenn.dev/articles/%s", draft.Slug),
	}
	if err := a.Store.SavePublishedArticle(ctx, published); err != nil {
		return nil, fmt.Errorf("save published article: %w", err)
	}

	return &PublishResult{
		Slug:      draft.Slug,
		PRURL:     prURL,
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

// createPublishPR creates a PR for the published article.
func (a *Activities) createPublishPR(ctx context.Context, draft artifacts.ArticleDraft, body string) string {
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe-publish/%s-%s", draft.Slug, time.Now().Format("0102-1504"))

	// Create branch via GitHub API
	mainRef, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/ref/heads/main", repo))
	if err != nil {
		fmt.Printf("publish: get main ref failed: %v\n", err)
		return ""
	}
	var ref struct{ Object struct{ SHA string `json:"sha"` } `json:"object"` }
	if json.Unmarshal(mainRef, &ref) != nil {
		return ""
	}

	createRefPayload := fmt.Sprintf(`{"ref":"refs/heads/%s","sha":"%s"}`, branchName, ref.Object.SHA)
	_, _ = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), createRefPayload)

	// Write article with published:true
	filePayload := map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
	}
	fileB, _ := json.Marshal(filePayload)
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/articles/%s.md", repo, draft.Slug), string(fileB))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return ""
	}

	// Create PR
	prBody := fmt.Sprintf("📤 ATRPE publish: **%s**\n\nThis PR sets `published: true` for Zenn.\n\n---\n🤖 Generated with [ATRPE](https://github.com/OnlyMyRailgun/ATRPE)", draft.Title)
	prPayload := fmt.Sprintf(`{"title":"📤 Publish: %s","head":"%s","base":"main","body":"%s"}`, draft.Title, branchName, prBody)
	prResp, err := a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), prPayload)
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

// buildZennMarkdown produces a Zenn-compatible markdown file with frontmatter.
// It respects draft.Published: false for draft PRs, true for final publish.
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
