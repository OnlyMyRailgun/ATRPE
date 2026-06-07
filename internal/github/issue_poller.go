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

// IssuePoller polls a GitHub issue for new comments and forwards commands as signals.
type IssuePoller struct {
	client       *AppClient
	repo         string
	issueNumber  int
	sender       TemporalSignalSender
	logger       *slog.Logger
	lastSeenID   int64
	mu           sync.Mutex
	interval     time.Duration
}

// NewIssuePoller creates a poller for a specific GitHub issue.
func NewIssuePoller(client *AppClient, repo string, issueNumber int, sender TemporalSignalSender, logger *slog.Logger) *IssuePoller {
	if logger == nil {
		logger = slog.Default()
	}
	return &IssuePoller{
		client:      client,
		repo:        repo,
		issueNumber: issueNumber,
		sender:      sender,
		logger:      logger,
		interval:    30 * time.Second,
	}
}

// Start begins polling. Blocks until ctx is cancelled.
func (p *IssuePoller) Start(ctx context.Context) {
	p.logger.Info("issue poller started", "repo", p.repo, "issue", p.issueNumber, "interval", p.interval)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("issue poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *IssuePoller) poll(ctx context.Context) {
	comments, err := p.fetchComments(ctx)
	if err != nil {
		p.logger.Error("fetch comments failed", "error", err)
		return
	}

	p.mu.Lock()
	lastSeen := p.lastSeenID
	p.mu.Unlock()

	for _, c := range comments {
		if c.ID <= lastSeen {
			continue
		}

		p.logger.Info("new comment", "id", c.ID, "user", c.User.Login, "body", c.Body)

		cmd, err := Parse(c.Body)
		if err != nil {
			p.logger.Debug("not a command, skipping", "body", c.Body)
			p.mu.Lock()
			p.lastSeenID = c.ID
			p.mu.Unlock()
			continue
		}

		// Send signal to Temporal
		if p.sender != nil {
			// Workflow ID derived from issue number
			workflowID := fmt.Sprintf("article-issue-%d", p.issueNumber)
			if err := p.sender.SendSignal(ctx, workflowID, cmd.Signal, cmd.Payload); err != nil {
				p.logger.Error("send signal failed", "error", err, "signal", cmd.Signal)
				continue
			}
			p.logger.Info("signal sent", "workflow", workflowID, "signal", cmd.Signal)
		}

		p.mu.Lock()
		p.lastSeenID = c.ID
		p.mu.Unlock()
	}
}

func (p *IssuePoller) fetchComments(ctx context.Context) ([]Comment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments?per_page=10", p.repo, p.issueNumber)
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
