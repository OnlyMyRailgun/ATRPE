package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Comment represents a GitHub issue comment.
type Comment struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Issue represents a GitHub issue for polling.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// IssuePoller polls all open issues for new comments and forwards commands as signals.
type IssuePoller struct {
	client    *AppClient
	repo      string
	sender    TemporalSignalSender
	logger    *slog.Logger
	lastSeen  map[int]int64 // issue number → last seen comment ID
	mu        sync.Mutex
	interval  time.Duration
}

// NewIssuePoller creates a poller that watches all open issues in a repo.
func NewIssuePoller(client *AppClient, repo string, sender TemporalSignalSender, logger *slog.Logger) *IssuePoller {
	if logger == nil {
		logger = slog.Default()
	}
	return &IssuePoller{
		client:   client,
		repo:     repo,
		sender:   sender,
		logger:   logger,
		lastSeen: make(map[int]int64),
		interval: 30 * time.Second,
	}
}

// Start begins polling all open issues. Blocks until ctx is cancelled.
func (p *IssuePoller) Start(ctx context.Context) {
	p.logger.Info("issue poller started", "repo", p.repo, "interval", p.interval)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("issue poller stopped")
			return
		case <-ticker.C:
			p.pollAll(ctx)
		}
	}
}

func (p *IssuePoller) pollAll(ctx context.Context) {
	issues, err := p.fetchOpenIssues(ctx)
	if err != nil {
		p.logger.Error("fetch open issues failed", "error", err)
		return
	}

	for _, issue := range issues {
		p.pollIssue(ctx, issue.Number)
	}
}

func (p *IssuePoller) pollIssue(ctx context.Context, issueNumber int) {
	comments, err := p.fetchComments(ctx, issueNumber)
	if err != nil {
		p.logger.Error("fetch comments failed", "issue", issueNumber, "error", err)
		return
	}

	p.mu.Lock()
	lastSeen := p.lastSeen[issueNumber]
	p.mu.Unlock()

	for _, c := range comments {
		if c.ID <= lastSeen {
			continue
		}

		p.logger.Info("new comment", "issue", issueNumber, "id", c.ID, "user", c.User.Login, "body", c.Body)

		cmd, err := Parse(c.Body)
		if err != nil {
			p.logger.Debug("not a command, skipping", "body", c.Body)
			p.mu.Lock()
			p.lastSeen[issueNumber] = c.ID
			p.mu.Unlock()
			continue
		}

		// Forward as Temporal signal
		if p.sender != nil {
			workflowID := fmt.Sprintf("article-issue-%d", issueNumber)
			if err := p.sender.SendSignal(ctx, workflowID, cmd.Signal, cmd.Payload); err != nil {
				p.logger.Error("send signal failed", "error", err, "workflow", workflowID, "signal", cmd.Signal)
				continue
			}
			p.logger.Info("signal sent", "workflow", workflowID, "issue", issueNumber, "signal", cmd.Signal)
		}

		p.mu.Lock()
		p.lastSeen[issueNumber] = c.ID
		p.mu.Unlock()
	}
}

func (p *IssuePoller) fetchOpenIssues(ctx context.Context) ([]Issue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues?state=open&per_page=30", p.repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, string(body))
	}

	var issues []Issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return issues, nil
}

func (p *IssuePoller) fetchComments(ctx context.Context, issueNumber int) ([]Comment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments?per_page=10", p.repo, issueNumber)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, string(body))
	}

	var comments []Comment
	if err := json.Unmarshal(body, &comments); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return comments, nil
}
