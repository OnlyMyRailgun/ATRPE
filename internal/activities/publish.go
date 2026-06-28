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
	DraftID     string                 `json:"draft_id,omitempty"`
	Draft       artifacts.ArticleDraft `json:"draft,omitempty"`
	IssueNumber int                    `json:"issue_number"` // for slug→workflowID mapping
	AutoMerge   bool                   `json:"auto_merge"`
}

// MergePublishInput is accepted by MergePublish.
type MergePublishInput struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// VerifyPublishMergeInput is accepted by the /merged fallback handler.
type VerifyPublishMergeInput struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Slug  string `json:"slug"`
}

// VerifyPublishMergeResult reports whether a publish PR was actually merged.
type VerifyPublishMergeResult struct {
	Merged   bool   `json:"merged"`
	PRURL    string `json:"pr_url,omitempty"`
	Message  string `json:"message"`
}

// PublishResult holds the outcome of the publish step.
type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// PublishArticle creates a publish PR that flips published:true.
// Blocker 1: GETs existing file SHA before PUT to avoid 422.
// Blocker 6: Records slug→issueNumber mapping so webhook can route signals.
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
		prURL = a.createPublishPR(ctx, draft, markdown)
		if prURL == "" {
			return &PublishResult{Slug: draft.Slug, Escalated: true},
				fmt.Errorf("publish PR creation failed for %s", draft.Slug)
		}
	}

	// Blocker 6: Record slug→issueNumber mapping so webhook can route PublishMergedSignal
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

// MergePublish records the article as published after the PR actually merged.
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

// Blocker 3: VerifyPublishMerge checks GitHub API to confirm a publish PR was actually merged.
// Used by /merged command fallback to prevent bypassing real merge verification.
func (a *Activities) VerifyPublishMerge(ctx context.Context, input VerifyPublishMergeInput) (*VerifyPublishMergeResult, error) {
	owner := input.Owner
	repo := input.Repo
	if owner == "" || repo == "" {
		parts := strings.SplitN(a.Config.GitHubIssueRepo, "/", 2)
		if len(parts) == 2 {
			owner, repo = parts[0], parts[1]
		}
	}
	if owner == "" || repo == "" {
		return &VerifyPublishMergeResult{Merged: false, Message: "cannot determine repo"}, nil
	}

	// Search for merged PRs with the publish branch prefix
	searchURL := fmt.Sprintf(
		"https://api.github.com/search/issues?q=repo:%s/%s+is:pr+is:merged+head:atrpe-publish/%s+in:title",
		owner, repo, input.Slug,
	)

	resp, err := a.githubGet(ctx, searchURL)
	if err != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: fmt.Sprintf("API error: %v", err)}, nil
	}

	var searchResult struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			State   string `json:"state"`
		} `json:"items"`
	}
	if json.Unmarshal(resp, &searchResult) != nil {
		return &VerifyPublishMergeResult{Merged: false, Message: "parse error"}, nil
	}

	if searchResult.TotalCount > 0 {
		item := searchResult.Items[0]
		return &VerifyPublishMergeResult{
			Merged: true,
			PRURL:  item.HTMLURL,
			Message: fmt.Sprintf("✅ Merged: %s", item.HTMLURL),
		}, nil
	}

	return &VerifyPublishMergeResult{
		Merged:  false,
		Message: fmt.Sprintf("No merged publish PR found for slug '%s'", input.Slug),
	}, nil
}

// Blocker 1: createPublishPR GETs existing file SHA before PUT to avoid 422.
// GitHub Content API requires the SHA of the file being replaced when updating.
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

	// Create branch
	createRefPayload, _ := json.Marshal(map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", branchName),
		"sha": ref.Object.SHA,
	})
	_, _ = a.githubPost(ctx, fmt.Sprintf("https://api.github.com/repos/%s/git/refs", repo), string(createRefPayload))

	// Blocker 1: GET existing file SHA from main before PUT
	filePath := fmt.Sprintf("articles/%s.md", draft.Slug)
	existingSHA := ""
	existingResp, err := a.githubGet(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=main", repo, filePath))
	if err == nil {
		var existing struct {
			SHA string `json:"sha"`
		}
		if json.Unmarshal(existingResp, &existing) == nil {
			existingSHA = existing.SHA
		}
	}

	// PUT with SHA when updating existing file (avoids 422)
	filePayloadMap := map[string]string{
		"message": fmt.Sprintf("ATRPE: publish %s", draft.Title),
		"content": base64.StdEncoding.EncodeToString([]byte(body)),
		"branch":  branchName,
	}
	if existingSHA != "" {
		filePayloadMap["sha"] = existingSHA
	}
	filePayload, _ := json.Marshal(filePayloadMap)
	_, err = a.githubPut(ctx, fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, filePath), string(filePayload))
	if err != nil {
		fmt.Printf("publish: write file failed: %v\n", err)
		return ""
	}

	// Create PR
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
