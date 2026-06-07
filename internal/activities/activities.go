package activities

import (
	"context"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/your-org/atrpe/internal/agents"
	"github.com/your-org/atrpe/internal/artifacts"
	"github.com/your-org/atrpe/internal/config"
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
	return &Activities{
		Config:   cfg,
		Store:    store,
		Objects:  objects,
		LLM:      llm,
		Research: agents.NewResearchAgent(llm),
		Design:   agents.NewDesignAgent(llm),
	}
}

// -- Discovery --

type DiscoverTopicsResult struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

func (a *Activities) DiscoverTopics(ctx context.Context) (*DiscoverTopicsResult, error) {
	candidates, err := topics.DiscoverGitHubTrending(ctx, "https://api.github.com")
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

// -- Create Topic Selection Issue --

type CreateTopicIssueInput struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

type CreateTopicIssueResult struct {
	IssueURL string `json:"issue_url"`
}

func (a *Activities) CreateTopicIssue(ctx context.Context, input CreateTopicIssueInput) (*CreateTopicIssueResult, error) {
	var sb strings.Builder
	sb.WriteString("## 🎯 ATRPE Topic Candidates\n\n")
	sb.WriteString("Select a topic by commenting `/select <candidate_id>`:\n\n")

	for i, c := range input.Candidates {
		sb.WriteString(fmt.Sprintf("### %d. %s\n", i+1, c.Title))
		sb.WriteString(fmt.Sprintf("- **ID**: `%s`\n", c.ID))
		sb.WriteString(fmt.Sprintf("- **Score**: %.3f\n", c.Score))
		sb.WriteString(fmt.Sprintf("- **Source**: %s\n", c.Source))
		sb.WriteString(fmt.Sprintf("- **URL**: %s\n\n", c.URL))
	}
	sb.WriteString("\n---\n*ATRPE automated topic discovery*")

	body := sb.String()

	// Try to create a real GitHub issue if token and repo are configured
	issueURL := "internal://topic-selection"
	if a.Config.GitHubToken != "" && a.Config.GitHubIssueRepo != "" {
		url, err := createGitHubIssue(ctx, a.Config.GitHubToken, a.Config.GitHubIssueRepo, "ATRPE Topic Candidates", body)
		if err != nil {
			fmt.Printf("⚠️ GitHub issue creation failed: %v\nFalling back to log output.\n\n%s\n", err, body)
		} else {
			issueURL = url
			fmt.Printf("✅ Topic selection issue created: %s\n", url)
			return &CreateTopicIssueResult{IssueURL: issueURL}, nil
		}
	}

	fmt.Printf("\n=== TOPIC SELECTION ISSUE ===\n%s=== END ===\n", body)
	return &CreateTopicIssueResult{IssueURL: issueURL}, nil
}

// createGitHubIssue calls the GitHub API to create an issue.
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
