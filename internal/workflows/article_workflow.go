package workflows

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// WorkflowState enumerates the states of the article workflow.
type WorkflowState string

const (
	StateDiscover           WorkflowState = "DISCOVER"
	StateWaitTopicSelection WorkflowState = "WAIT_TOPIC_SELECTION"
	StateResearch           WorkflowState = "RESEARCH"
	StateDesign             WorkflowState = "DESIGN"
	StateExperiment         WorkflowState = "EXPERIMENT"
	StateVerify             WorkflowState = "VERIFY"
	StateGenerateArticle    WorkflowState = "GENERATE_ARTICLE"
	StateWaitPublishApproval WorkflowState = "WAIT_PUBLISH_APPROVAL"
	StatePublish            WorkflowState = "PUBLISH"
	StatePatchGeneration    WorkflowState = "PATCH_GENERATION"
	StateDesignUpdate       WorkflowState = "DESIGN_UPDATE"
	StateEscalated          WorkflowState = "ESCALATED"
	StateCompleted          WorkflowState = "COMPLETED"
	StateFailed             WorkflowState = "FAILED"
	StateAborted            WorkflowState = "ABORTED"
)

// Signal types

type TopicSelectedSignal struct {
	CandidateID string `json:"candidate_id"`
}
type PublishApprovalSignal struct{}
type RetrySignal struct{}
type AbortSignal struct{}
type RequestChangesSignal struct {
	ChangeNotes string `json:"change_notes"`
}

// ArticleWorkflowInput is the input to the article workflow.
type ArticleWorkflowInput struct {
	MaxRemediationAttempts int
	IssueNumber            int // GitHub issue number for posting comments
}

// ArticleWorkflowState is the durable state carried through the workflow.
type ArticleWorkflowState struct {
	State            WorkflowState
	CandidateID      string
	IssueNumber      int
	ChangeNotes      string
	RemediationCount int
	MaxRemediation   int
}

// ArticleWorkflow orchestrates the full article lifecycle.
func ArticleWorkflow(ctx workflow.Context, input ArticleWorkflowInput) error {
	s := ArticleWorkflowState{
		State:          StateDiscover,
		IssueNumber:    input.IssueNumber,
		MaxRemediation: input.MaxRemediationAttempts,
	}
	if s.MaxRemediation <= 0 {
		s.MaxRemediation = 3
	}

	// Search attributes deferred (needs Temporal namespace config)

	for s.State != StateCompleted && s.State != StateFailed && s.State != StateAborted {
		switch s.State {
		case StateDiscover:
			s = runDiscover(ctx, s)
		case StateWaitTopicSelection:
			s = runWaitTopicSelection(ctx, s)
		case StateResearch:
			s = runResearch(ctx, s)
		case StateDesign:
			s = runDesign(ctx, s)
		case StateExperiment:
			s = runExperiment(ctx, s)
		case StateVerify:
			s = runVerify(ctx, s)
		case StateGenerateArticle:
			s = runGenerateArticle(ctx, s)
		case StateWaitPublishApproval:
			s = runWaitPublishApproval(ctx, s)
		case StatePublish:
			s = runPublish(ctx, s)
		case StatePatchGeneration:
			s = runPatchGeneration(ctx, s)
		case StateDesignUpdate:
			s = runDesignUpdate(ctx, s)
		case StateEscalated:
			s = runEscalated(ctx, s)
		}
	}

	return nil
}

func setState(ctx workflow.Context, state WorkflowState) {
	// Search attributes deferred — register in Temporal namespace first
}

func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
}

// State handler stubs — to be wired to activities in Week 2

// topicCandidate is a lightweight copy of the activity result for JSON deserialization.
type topicCandidate struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
	URL    string  `json:"url"`
}

type topicCandidateBrief struct {
	CoreConcepts []string `json:"core_concepts"`
}

// comment posts a progress update on the GitHub issue. Non-blocking on failure.
func comment(ctx workflow.Context, issueNumber int, body string) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	workflow.ExecuteActivity(ctx, "PostComment", map[string]interface{}{
		"issue_number": issueNumber,
		"body":         body,
	}).Get(ctx, nil)
}

func runDiscover(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var discoverResult struct {
		Candidates []topicCandidate `json:"candidates"`
	}
	err := workflow.ExecuteActivity(ctx, "DiscoverTopics").Get(ctx, &discoverResult)
	if err != nil {
		workflow.GetLogger(ctx).Error("DiscoverTopics failed", "error", err)
		s.State = StateFailed
		return s
	}

	err = workflow.ExecuteActivity(ctx, "CreateTopicIssue", map[string]interface{}{
		"candidates": discoverResult.Candidates,
	}).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("CreateTopicIssue failed", "error", err)
	}

	s.State = StateWaitTopicSelection
	setState(ctx, StateWaitTopicSelection)
	return s
}

func runWaitTopicSelection(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(workflow.GetSignalChannel(ctx, "TopicSelectedSignal"), func(c workflow.ReceiveChannel, more bool) {
		var sig TopicSelectedSignal
		c.Receive(ctx, &sig)
		s.CandidateID = sig.CandidateID
		s.State = StateResearch
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "AbortSignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &AbortSignal{})
		s.State = StateAborted
	})
	sel.Select(ctx)
	return s
}

func runResearch(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Resolve candidate ID (handles numeric positions like "1")
	var resolved struct{ CandidateID string `json:"candidate_id"` }
	workflow.ExecuteActivity(ctx, "ResolveCandidateID", map[string]interface{}{
		"selection": s.CandidateID,
	}).Get(ctx, &resolved)
	s.CandidateID = resolved.CandidateID

	comment(ctx, s.IssueNumber, fmt.Sprintf("🔍 Selected `%s`, starting research...", s.CandidateID))

	var brief topicCandidateBrief
	err := workflow.ExecuteActivity(ctx, "ResearchTopic", map[string]interface{}{
		"candidate_id": s.CandidateID,
	}).Get(ctx, &brief)
	if err != nil {
		workflow.GetLogger(ctx).Error("Research failed", "error", err)
		s.State = StateFailed
		return s
	}

	comment(ctx, s.IssueNumber, fmt.Sprintf("✅ Research complete. %d core concepts identified.", len(brief.CoreConcepts)))
	s.State = StateDesign
	setState(ctx, StateDesign)
	return s
}

func runDesign(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	comment(ctx, s.IssueNumber, "🏗️ Designing architecture...")

	var design struct {
		Components []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"components"`
	}
	err := workflow.ExecuteActivity(ctx, "DesignArchitecture", map[string]interface{}{
		"brief": map[string]interface{}{"topic_id": s.CandidateID},
	}).Get(ctx, &design)
	if err != nil {
		workflow.GetLogger(ctx).Error("DesignArchitecture failed", "error", err)
		s.State = StateFailed
		return s
	}

	names := make([]string, len(design.Components))
	for i, c := range design.Components {
		names[i] = fmt.Sprintf("%s (%s)", c.Name, c.Type)
	}
	comment(ctx, s.IssueNumber, fmt.Sprintf("🏗️ Design ready: %d components — %s", len(design.Components), strings.Join(names, ", ")))
	s.State = StateExperiment
	setState(ctx, StateExperiment)
	return s
}

func runExperiment(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// MVP: skip experiment, go straight to article generation
	comment(ctx, s.IssueNumber, "🧪 Experiment skipped (MVP), proceeding to article generation...")
	s.State = StateGenerateArticle
	setState(ctx, StateGenerateArticle)
	return s
}

func runVerify(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	s.State = StateGenerateArticle
	return s
}

func runGenerateArticle(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	comment(ctx, s.IssueNumber, "✍️ Writing article draft...")

	var draft struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	err := workflow.ExecuteActivity(ctx, "GenerateDraft", map[string]interface{}{
		"change_notes": s.ChangeNotes,
	}).Get(ctx, &draft)
	if err != nil {
		workflow.GetLogger(ctx).Error("GenerateDraft failed", "error", err)
		s.State = StateFailed
		return s
	}

	// Create PR
	comment(ctx, s.IssueNumber, fmt.Sprintf("📝 Article draft generated: **%s**\n\nCreating PR for review...", draft.Title))

	var prResult struct{ PRURL string `json:"pr_url"` }
	err = workflow.ExecuteActivity(ctx, "CreateArticlePR", map[string]interface{}{
		"draft": map[string]interface{}{
			"slug":  draft.Slug,
			"title": draft.Title,
			"body":  draft.Body,
		},
	}).Get(ctx, &prResult)
	if err != nil {
		workflow.GetLogger(ctx).Error("CreateArticlePR failed", "error", err)
		comment(ctx, s.IssueNumber, fmt.Sprintf("⚠️ PR creation failed: %v", err))
		s.State = StateFailed
		return s
	}

	comment(ctx, s.IssueNumber, fmt.Sprintf("🔀 PR created: %s\n\nReview and merge to publish on Zenn. Reply `/approve` to confirm, or `/changes <notes>` for revisions.", prResult.PRURL))

	s.State = StateWaitPublishApproval
	setState(ctx, StateWaitPublishApproval)
	return s
}

func runWaitPublishApproval(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(workflow.GetSignalChannel(ctx, "PublishApprovalSignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &PublishApprovalSignal{})
		comment(ctx, s.IssueNumber, "✅ Approved! Merge the PR to publish on Zenn.")
		s.State = StateCompleted
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "RequestChangesSignal"), func(c workflow.ReceiveChannel, more bool) {
		var sig RequestChangesSignal
		c.Receive(ctx, &sig)
		s.ChangeNotes = sig.ChangeNotes
		comment(ctx, s.IssueNumber, fmt.Sprintf("📝 Changes requested: %s\nRegenerating draft...", sig.ChangeNotes))
		s.State = StateGenerateArticle
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "AbortSignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &AbortSignal{})
		s.State = StateAborted
	})
	sel.Select(ctx)
	setState(ctx, s.State)
	return s
}

func runPublish(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	s.State = StateCompleted
	return s
}

func runPatchGeneration(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	s.RemediationCount++
	s.State = StateDesignUpdate
	return s
}

func runDesignUpdate(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	s.State = StateExperiment
	return s
}

func runEscalated(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(workflow.GetSignalChannel(ctx, "RetrySignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &RetrySignal{})
		s.State = StatePublish
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "AbortSignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &AbortSignal{})
		s.State = StateAborted
	})
	sel.Select(ctx)
	return s
}
