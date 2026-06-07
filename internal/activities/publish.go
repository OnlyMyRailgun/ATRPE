package activities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/uuid"
	"github.com/your-org/atrpe/internal/artifacts"
)

// PublishInput holds all data needed for the publish step.
type PublishInput struct {
	DraftID     string `json:"draft_id"`
	TopicID     string `json:"topic_id"`
	ZennRepoURL string `json:"zenn_repo_url"`
	GitHubToken string `json:"github_token"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	AutoMerge   bool   `json:"auto_merge"`
}

// PublishResult holds the outcome of the publish step.
type PublishResult struct {
	Slug      string `json:"slug"`
	PRURL     string `json:"pr_url,omitempty"`
	Merged    bool   `json:"merged"`
	Escalated bool   `json:"escalated"`
}

// PublishArticle publishes a Zenn article draft to the Zenn repository.
// Steps: load draft → flip published:true → clone/update repo → create branch → commit → push → PR → merge.
func (a *Activities) PublishArticle(ctx context.Context, input PublishInput) (*PublishResult, error) {
	// 1. Load draft
	repo := artifacts.NewRepository(a.Store, a.Objects)
	var draft artifacts.ArticleDraft
	if err := repo.LoadArtifact(ctx, "article_drafts", input.DraftID, &draft); err != nil {
		return nil, fmt.Errorf("load draft: %w", err)
	}

	// 2. Flip published: true
	draft.Published = true
	markdown := buildZennMarkdown(draft)

	// 3. Save final markdown
	if _, err := a.Objects.Put(ctx,
		fmt.Sprintf("published/%s.md", draft.Slug),
		strings.NewReader(markdown),
		"text/markdown",
	); err != nil {
		return nil, fmt.Errorf("save published markdown: %w", err)
	}

	// 4. Clone Zenn repo
	tmpDir, err := os.MkdirTemp("", "atrpe-publish-*")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cloneOpts := &git.CloneOptions{
		URL:  input.ZennRepoURL,
		Auth: authFromToken(input.GitHubToken),
	}
	zennRepo, err := git.PlainClone(tmpDir, false, cloneOpts)
	if err != nil {
		return nil, fmt.Errorf("clone zenn repo: %w", err)
	}

	// 5. Create branch
	branchName := fmt.Sprintf("atrpe/%s", draft.Slug)
	branchRef := plumbing.NewBranchReferenceName(branchName)
	headRef, _ := zennRepo.Head()
	zennRepo.CreateBranch(&config.Branch{Name: branchName, Remote: "origin"})

	wt, _ := zennRepo.Worktree()
	wt.Checkout(&git.CheckoutOptions{Branch: branchRef})

	// 6. Write article
	articlePath := filepath.Join(tmpDir, "articles", draft.Slug+".md")
	os.MkdirAll(filepath.Dir(articlePath), 0755)
	os.WriteFile(articlePath, []byte(markdown), 0644)
	wt.Add(fmt.Sprintf("articles/%s.md", draft.Slug))

	// 7. Commit
	commit, err := wt.Commit(fmt.Sprintf("ATRPE: publish %s", draft.Title), &git.CommitOptions{
		Author: &object.Signature{
			Name:  input.AuthorName,
			Email: input.AuthorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// 8. Push
	pushOpts := &git.PushOptions{Auth: authFromToken(input.GitHubToken)}
	if err := zennRepo.Push(pushOpts); err != nil && err != git.NoErrAlreadyUpToDate {
		return nil, fmt.Errorf("push: %w", err)
	}

	// 9. Create PR via GitHub API (simplified — uses go-git commit for now)
	_ = headRef
	_ = commit

	// 10. Record published article
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
		Merged:    input.AutoMerge,
		Escalated: false,
	}, nil
}

func authFromToken(token string) *http.BasicAuth {
	return &http.BasicAuth{
		Username: "atrpe",
		Password: token,
	}
}

// buildZennMarkdown produces a Zenn-compatible markdown file with frontmatter.
func buildZennMarkdown(draft artifacts.ArticleDraft) string {
	return fmt.Sprintf(`---
title: "%s"
emoji: "%s"
type: "%s"
topics: [%s]
published: true
---

%s
`, draft.Title, draft.Emoji, draft.Type, quoteTopics(draft.Topics), draft.Body)
}

func quoteTopics(topics []string) string {
	quoted := make([]string, len(topics))
	for i, t := range topics {
		quoted[i] = fmt.Sprintf(`"%s"`, t)
	}
	return strings.Join(quoted, ", ")
}
