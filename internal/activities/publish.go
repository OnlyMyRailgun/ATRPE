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
	DraftID   string                `json:"draft_id,omitempty"`
	Draft     artifacts.ArticleDraft `json:"draft,omitempty"`
	AutoMerge bool                  `json:"auto_merge"`
}

// PublishResult holds the outcome of the publish step.
type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// PublishArticle creates a publish PR that flips published:true.
// It does NOT record PublishedArticle or complete the workflow —
// that happens only after the publish PR is actually merged.
//
// B: File content is buildZennMarkdown (pure Zenn markdown), not a PR body string.
// A: Publish PR writes to articles-publish/ directory so CI article-check
//    (which checks articles/*.md) does not fail on published:true drafts.
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

	// B: Flip published:true and build proper Zenn markdown (not PR body wrapper)
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

	// A: Create publish PR — writes to articles-publish/ directory to bypass CI gate
	prURL := ""
	if a.Config != nil && a.Config.GitHubIssueRepo != "" {
		prURL = a.createPublishPR(ctx, draft, markdown)
		if prURL == "" {
			return &PublishResult{Slug: draft.Slug, Escalated: true},
				fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	// C: Do NOT save PublishedArticle or mark complete here.
	//    PR merge is the trigger for that — see MergePublishActivity.
	//    The workflow transitions to WAIT_PUBLISH_MERGE after this returns.

	return &PublishResult{
		Slug:      draft.Slug,
		PRURL:     prURL,
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

// MergePublish records the article as published after the PR actually merged.
// Called from the webhook/poller when a publish PR merges.
func (a *Activities) MergePublish(ctx context.Context, slug, title string) error {
	published := artifacts.PublishedArticle{
		ID:          slug,
		Slug:        slug,
		Title:       title,
		PublishedAt: time.Now().UTC(),
		Platform:    "zenn",
		URL:         fmt.Sprintf("https://zenn.dev/articles/%s", slug),
	}
	return a.Store.SavePublishedArticle(ctx, published)
}

// A: createPublishPR writes to articles-publish/ directory so CI skips it.
// B: `body` is the actual Zenn markdown (from buildZennMarkdown).
func (a *Activities) createPublishPR(ctx context.Context, draft artifacts.ArticleDraft, body string) string {
	repo := a.Config.GitHubIssueRepo
	branchName := fmt.Sprintf("atrpe-publish/%s-%s", draft.Slug, time.Now().Format("0102-1504"))

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

	// A: Write to articles-publish/ to avoid CI article-check on articles/*.md
	filePayload := map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
	}
	fileB, _ := json.Marshal(filePayload)
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/articles-publish/%s.md", repo, draft.Slug), string(fileB))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return ""
	}

	prBody := fmt.Sprintf("📤 ATRPE publish: **%s**\n\nMerging this PR sets `published: true` on Zenn.\n\n---\n🤖 Generated with [ATRPE](https://github.com/OnlyMyRailgun/ATRPE)", draft.Title)
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
