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

// PublishInput holds all data needed for the publish step.
type PublishInput struct {
	DraftID   string                 `json:"draft_id,omitempty"`
	Draft     artifacts.ArticleDraft `json:"draft,omitempty"`
	AutoMerge bool                   `json:"auto_merge"`
}

// MergePublishInput is accepted by MergePublish.
type MergePublishInput struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// PublishResult holds the outcome of the publish step.
type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// PublishArticle creates a publish PR that flips published:true.
// PO-1: Writes to articles/<slug>.md (same directory as drafts).
// The publish PR is distinguished by its branch name prefix "atrpe-publish/".
// PO-3: Does NOT save PublishedArticle or complete workflow on PR creation.
// PO-4: Uses json.Marshal (never fmt.Sprintf) for all GitHub API payloads.
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

	// Build published:true markdown
	draft.Published = true
	markdown := buildZennMarkdown(draft)

	// Save published markdown to ObjectStore
	if _, err := a.Objects.Put(ctx,
		fmt.Sprintf("published/%s.md", draft.Slug),
		strings.NewReader(markdown),
		"text/markdown",
	); err != nil {
		return nil, fmt.Errorf("save published markdown: %w", err)
	}

	// PO-1: Create publish PR in articles/ dir with "atrpe-publish/" branch prefix
	prURL := ""
	if a.Config != nil && a.Config.GitHubIssueRepo != "" {
		prURL = a.createPublishPR(ctx, draft, markdown)
		if prURL == "" {
			return &PublishResult{Slug: draft.Slug, Escalated: true},
				fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	// PO-3: Do NOT save PublishedArticle or complete workflow now.
	// MergePublish is called later by the real pull_request webhook.

	return &PublishResult{
		Slug:      draft.Slug,
		PRURL:     prURL,
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

// MergePublish records the article as published after the PR actually merged.
// PO-3: Called by the pull_request webhook handler when a publish PR is merged.
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

// PO-4: createPublishPR uses json.Marshal for all GitHub API payloads.
// PO-1: Writes to articles/<slug>.md, branch is "atrpe-publish/<slug>-<ts>".
func (a *Activities) createPublishPR(ctx context.Context, draft artifacts.ArticleDraft, body string) string {
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe-publish/%s-%s", draft.Slug, time.Now().Format("0102-1504"))

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

	// PO-4: json.Marshal based payload (no string concatenation)
	createRefPayload, _ := json.Marshal(map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", branchName),
		"sha": ref.Object.SHA,
	})
	_, _ = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), string(createRefPayload))

	// PO-1: Write to articles/<slug>.md (same directory as drafts)
	filePayload, _ := json.Marshal(map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
	})
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/articles/%s.md", repo, draft.Slug), string(filePayload))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return ""
	}

	// PO-4: json.Marshal for PR payload
	prBody, _ := json.Marshal(map[string]interface{}{
		"title": fmt.Sprintf("📤 Publish: %s", draft.Title),
		"head":  branchName,
		"base":  "main",
		"body":  fmt.Sprintf("Publish PR for **%s**\n\nMerging this sets `published: true` on Zenn.", draft.Title),
	})
	prResp, err := a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), string(prBody))
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
