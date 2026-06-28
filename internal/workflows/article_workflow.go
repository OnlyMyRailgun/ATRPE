package workflows

import (
	"fmt"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// WorkflowState enumerates the states of the article workflow.
type WorkflowState string

const (
	StateDiscover            WorkflowState = "DISCOVER"
	StateContentAudit        WorkflowState = "CONTENT_AUDIT"
	StateWaitTopicSelection  WorkflowState = "WAIT_TOPIC_SELECTION"
	StateResearch            WorkflowState = "RESEARCH"
	StateDesign              WorkflowState = "DESIGN"
	StateExperiment          WorkflowState = "EXPERIMENT"
	StateVerify              WorkflowState = "VERIFY"
	StateGenerateArticle     WorkflowState = "GENERATE_ARTICLE"
	StateWaitPublishApproval WorkflowState = "WAIT_PUBLISH_APPROVAL"
	StatePublish             WorkflowState = "PUBLISH"
	StatePatchGeneration     WorkflowState = "PATCH_GENERATION"
	StateDesignUpdate        WorkflowState = "DESIGN_UPDATE"
	StateEscalated           WorkflowState = "ESCALATED"
	StateCompleted           WorkflowState = "COMPLETED"
	StateFailed              WorkflowState = "FAILED"
	StateAborted             WorkflowState = "ABORTED"
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

	// Artifact chain — carried across state transitions.
	Brief              artifacts.TechnicalBrief
	Design             artifacts.DesignArtifact
	ExperimentResult   artifacts.ExperimentResult
	VerificationReport artifacts.VerificationReport
	LastPatch          artifacts.PatchResult
	LastDraft          artifacts.ArticleDraft

	// Content audit results — set after CONTENT_AUDIT stage.
	AuditPasses  map[string]bool   // candidateID → passes
	AuditDetails map[string]string // candidateID → decision sheet detail

	// Discovered candidates — stored temporarily for audit → issue flow.
	cachedCandidates []topicCandidate

	// Experiment workspace path for cleanup on terminal states.
	WorkspacePath string
}

func (s *ArticleWorkflowState) Candidates() []topicCandidate {
	return s.cachedCandidates
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

	for s.State != StateCompleted && s.State != StateFailed && s.State != StateAborted {
		switch s.State {
		case StateDiscover:
			s = runDiscover(ctx, s)
		case StateContentAudit:
			s = runContentAudit(ctx, s)
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

	// Clean up experiment workspace on terminal states
	if s.WorkspacePath != "" {
		outcome := "success"
		if s.State == StateFailed {
			outcome = "failure"
		} else if s.State == StateAborted {
			outcome = "abort"
		}
		ctx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		_ = workflow.ExecuteActivity(ctx, "CleanupWorkspace", map[string]interface{}{
			"workdir": s.WorkspacePath,
			"outcome": outcome,
		}).Get(ctx, nil)
	}

	return nil
}

func setState(ctx workflow.Context, state WorkflowState) {
	//nolint:staticcheck
	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"workflow_state": string(state),
	})
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

// longActivityOptions gives more time for LLM-heavy activities.
func longActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    2,
		},
	}
}

// topicCandidate is a lightweight copy of the activity result for JSON deserialization.
type topicCandidate struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
	URL    string  `json:"url"`
}

// comment posts a progress update on the GitHub issue. Non-blocking on failure.
func comment(ctx workflow.Context, issueNumber int, body string) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	_ = workflow.ExecuteActivity(ctx, "PostComment", map[string]interface{}{
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

	s.cachedCandidates = discoverResult.Candidates

	workflow.GetLogger(ctx).Info("discovery complete", "candidate_count", len(discoverResult.Candidates))
	s.State = StateContentAudit
	setState(ctx, StateContentAudit)
	return s
}

// runContentAudit runs the content audit activity and creates the decision sheet issue.
func runContentAudit(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())

	workflow.GetLogger(ctx).Info("running content audit")

	var auditResult struct {
		Audits []struct {
			CandidateID     string  `json:"candidate_id"`
			Passes          bool    `json:"passes"`
			FailReason      string  `json:"fail_reason,omitempty"`
			Recommendation  float64 `json:"recommendation"`
			WhyNow          string  `json:"why_now"`
			SaturationLevel string  `json:"saturation_level"`
			Differentiation string  `json:"differentiation"`
			ExistingGaps    string  `json:"existing_gaps"`
			TestablePart    string  `json:"testable_part"`
			Risks           string  `json:"risks"`
			SuggestedTitle  string  `json:"suggested_title"`
			DontWriteReason string  `json:"dont_write_reason"`
			ExistingCount   int     `json:"existing_count"`
			OwnOverlap      bool    `json:"own_overlap"`
		} `json:"audits"`
	}

	err := workflow.ExecuteActivity(ctx, "AuditTopics", map[string]interface{}{
		"candidates": struct{ Candidates []topicCandidate }{
			Candidates: s.Candidates(),
		},
	}).Get(ctx, &auditResult)
	if err != nil {
		workflow.GetLogger(ctx).Warn("AuditTopics failed, proceeding without audit", "error", err)
		// Degrade gracefully — create a basic issue without audit data
		s.State = StateWaitTopicSelection
		setState(ctx, StateWaitTopicSelection)
		return s
	}

	// Create decision sheet issue with audit data
	var issueResult struct {
		IssueURL    string `json:"issue_url"`
		IssueNumber int    `json:"issue_number"`
	}
	err = workflow.ExecuteActivity(ctx, "CreateTopicIssue", map[string]interface{}{
		"candidates": s.Candidates(),
		"audits":     auditResult.Audits,
	}).Get(ctx, &issueResult)

	if issueResult.IssueNumber > 0 {
		s.IssueNumber = issueResult.IssueNumber
	}
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
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())

	// Resolve candidate ID (handles numeric positions like "1")
	var resolved struct{ CandidateID string `json:"candidate_id"` }
	workflow.ExecuteActivity(ctx, "ResolveCandidateID", map[string]interface{}{
		"selection": s.CandidateID,
	}).Get(ctx, &resolved)
	s.CandidateID = resolved.CandidateID

	//nolint:staticcheck
	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"topic_id": s.CandidateID,
	})

	comment(ctx, s.IssueNumber, fmt.Sprintf("🔍 Selected `%s`, starting research...", s.CandidateID))

	var brief artifacts.TechnicalBrief
	err := workflow.ExecuteActivity(ctx, "ResearchTopic", map[string]interface{}{
		"candidate_id": s.CandidateID,
	}).Get(ctx, &brief)
	if err != nil {
		workflow.GetLogger(ctx).Error("Research failed", "error", err)
		s.State = StateFailed
		return s
	}
	s.Brief = brief

	comment(ctx, s.IssueNumber, fmt.Sprintf("✅ Research complete. %d core concepts identified.", len(brief.CoreConcepts)))
	s.State = StateDesign
	setState(ctx, StateDesign)
	return s
}

func runDesign(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())
	comment(ctx, s.IssueNumber, "🏗️ Designing architecture...")

	var design artifacts.DesignArtifact
	err := workflow.ExecuteActivity(ctx, "DesignArchitecture", map[string]interface{}{
		"brief": s.Brief,
	}).Get(ctx, &design)
	if err != nil {
		workflow.GetLogger(ctx).Error("DesignArchitecture failed", "error", err)
		s.State = StateFailed
		return s
	}
	s.Design = design

	names := make([]string, len(design.Components))
	for i, c := range design.Components {
		names[i] = fmt.Sprintf("%s (%s)", c.Name, c.Type)
	}
	comment(ctx, s.IssueNumber, fmt.Sprintf("🏗️ Design ready: %d components — %s\nTest plan: %s",
		len(design.Components),
		strings.Join(names, ", "),
		design.TestPlan.Strategy,
	))
	s.State = StateExperiment
	setState(ctx, StateExperiment)
	return s
}

// runExperiment calls RunExperiment activity, stores the result, and transitions to VERIFY.
func runExperiment(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())
	comment(ctx, s.IssueNumber, "🧪 Running experiment — generating and validating code...")

	var result artifacts.ExperimentResult
	err := workflow.ExecuteActivity(ctx, "RunExperiment", map[string]interface{}{
		"design": s.Design,
	}).Get(ctx, &result)
	if err != nil {
		workflow.GetLogger(ctx).Error("RunExperiment failed", "error", err)
		comment(ctx, s.IssueNumber, fmt.Sprintf("❌ Experiment failed: %v", err))
		s.State = StateFailed
		return s
	}
	s.ExperimentResult = result
	s.WorkspacePath = result.Environment.Workdir

	// Report experiment outcomes
	passCount, failCount := 0, 0
	for _, cmd := range result.Commands {
		if cmd.ExitCode == 0 {
			passCount++
		} else {
			failCount++
		}
	}
	comment(ctx, s.IssueNumber, fmt.Sprintf(
		"🧪 Experiment complete: %d commands run (%d pass, %d fail, %dms).",
		len(result.Commands), passCount, failCount,
		result.Commands[len(result.Commands)-1].DurationMS,
	))

	s.State = StateVerify
	setState(ctx, StateVerify)
	return s
}

// runVerify checks the experiment result and branches to GENERATE_ARTICLE or remediation.
func runVerify(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	comment(ctx, s.IssueNumber, "🔬 Verifying experiment results...")

	var report artifacts.VerificationReport
	err := workflow.ExecuteActivity(ctx, "VerifyExperiment", map[string]interface{}{
		"brief":  s.Brief,
		"result": s.ExperimentResult,
	}).Get(ctx, &report)
	if err != nil {
		workflow.GetLogger(ctx).Error("VerifyExperiment failed", "error", err)
		s.State = StateFailed
		return s
	}
	s.VerificationReport = report

	if report.OverallPassed {
		comment(ctx, s.IssueNumber, "✅ Verification PASSED — proceeding to article generation.")
		s.State = StateGenerateArticle
		setState(ctx, StateGenerateArticle)
		return s
	}

	// Verification failed — check remediation budget
	s.RemediationCount++
	if s.RemediationCount <= s.MaxRemediation {
		comment(ctx, s.IssueNumber, fmt.Sprintf(
			"⚠️ Verification FAILED (attempt %d/%d).\nBlocking issues:\n%s\n\nEntering remediation loop...",
			s.RemediationCount, s.MaxRemediation,
			formatIssues(report.BlockingIssues),
		))
		s.State = StatePatchGeneration
		setState(ctx, StatePatchGeneration)
	} else {
		comment(ctx, s.IssueNumber, fmt.Sprintf(
			"🚨 Verification FAILED after %d attempts.\nBlocking issues:\n%s\n\nWorkflow ESCALATED — manual intervention required.",
			s.MaxRemediation, formatIssues(report.BlockingIssues),
		))
		s.State = StateEscalated
		setState(ctx, StateEscalated)
	}
	return s
}

// runPatchGeneration calls ExperimentAgent.Patch to fix code issues.
func runPatchGeneration(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())
	comment(ctx, s.IssueNumber, fmt.Sprintf("🔧 Generating patch (attempt %d/%d)...", s.RemediationCount, s.MaxRemediation))

	var patch artifacts.PatchResult
	err := workflow.ExecuteActivity(ctx, "PatchExperiment", map[string]interface{}{
		"design": s.Design,
		"result": s.ExperimentResult,
	}).Get(ctx, &patch)
	if err != nil {
		workflow.GetLogger(ctx).Error("PatchExperiment failed", "error", err)
		comment(ctx, s.IssueNumber, fmt.Sprintf("❌ Patch generation failed: %v", err))
		s.State = StateFailed
		return s
	}
	s.LastPatch = patch

	patchedNames := make([]string, len(patch.PatchedFiles))
	for i, f := range patch.PatchedFiles {
		patchedNames[i] = f.Path
	}
	comment(ctx, s.IssueNumber, fmt.Sprintf(
		"🔧 Patch generated: %d files changed — %s",
		len(patch.PatchedFiles), strings.Join(patchedNames, ", "),
	))
	s.State = StateDesignUpdate
	setState(ctx, StateDesignUpdate)
	return s
}

// runDesignUpdate calls DesignAgent.Update to align design with patched code.
func runDesignUpdate(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())
	comment(ctx, s.IssueNumber, "📐 Updating design from patch feedback...")

	var updated artifacts.DesignArtifact
	err := workflow.ExecuteActivity(ctx, "UpdateDesign", map[string]interface{}{
		"design": s.Design,
		"patch":  s.LastPatch,
	}).Get(ctx, &updated)
	if err != nil {
		workflow.GetLogger(ctx).Warn("UpdateDesign failed, reusing current design", "error", err)
		// Non-fatal: continue with existing design
	} else {
		s.Design = updated
	}

	comment(ctx, s.IssueNumber, "📐 Design updated — re-running experiment with fixes.")
	s.State = StateExperiment
	setState(ctx, StateExperiment)
	return s
}

// runGenerateArticle generates the article draft with the full artifact chain.
func runGenerateArticle(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	ctx = workflow.WithActivityOptions(ctx, longActivityOptions())
	comment(ctx, s.IssueNumber, "✍️ Writing article draft from verified experiment...")

	var draft artifacts.ArticleDraft
	err := workflow.ExecuteActivity(ctx, "GenerateDraft", map[string]interface{}{
		"brief":        s.Brief,
		"result":       s.ExperimentResult,
		"report":       s.VerificationReport,
		"change_notes": s.ChangeNotes,
	}).Get(ctx, &draft)
	if err != nil {
		workflow.GetLogger(ctx).Error("GenerateDraft failed", "error", err)
		s.State = StateFailed
		return s
	}
	s.LastDraft = draft

	//nolint:staticcheck
	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
		"article_slug": draft.Slug,
	})

	// Create PR
	comment(ctx, s.IssueNumber, fmt.Sprintf("📝 Article draft generated: **%s**\n\nCreating PR for review...", draft.Title))

	var prResult struct{ PRURL string `json:"pr_url"` }
	err = workflow.ExecuteActivity(ctx, "CreateArticlePR", map[string]interface{}{
		"draft":        draft,
		"issue_number": s.IssueNumber,
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
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	comment(ctx, s.IssueNumber, "🚀 Publishing article to Zenn...")

	var pubResult struct {
		Slug      string `json:"slug"`
		PRURL     string `json:"pr_url"`
		Merged    bool   `json:"merged"`
		Escalated bool   `json:"escalated"`
	}
	err := workflow.ExecuteActivity(ctx, "PublishArticle", map[string]interface{}{
		"draft": s.LastDraft,
	}).Get(ctx, &pubResult)
	if err != nil {
		workflow.GetLogger(ctx).Error("PublishArticle failed", "error", err)
		comment(ctx, s.IssueNumber, fmt.Sprintf("❌ Publish failed: %v", err))
		s.State = StateEscalated
		setState(ctx, StateEscalated)
		return s
	}

	if pubResult.Escalated {
		comment(ctx, s.IssueNumber, fmt.Sprintf("⚠️ PR created but not merged: %s\nManual merge required. Reply `/retry` after merging.", pubResult.PRURL))
		s.State = StateEscalated
		setState(ctx, StateEscalated)
	} else if pubResult.Merged {
		comment(ctx, s.IssueNumber, fmt.Sprintf("🎉 Published! https://zenn.dev/articles/%s", pubResult.Slug))
		s.State = StateCompleted
		setState(ctx, StateCompleted)
	} else {
		comment(ctx, s.IssueNumber, fmt.Sprintf("📤 PR submitted: %s\nAwaiting merge.", pubResult.PRURL))
		s.State = StateCompleted
		setState(ctx, StateCompleted)
	}
	return s
}

func runEscalated(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	comment(ctx, s.IssueNumber, "⏸️ Workflow escalated — waiting for manual intervention.\nReply `/retry` to retry, or `/abort` to abort.")
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

func formatIssues(issues []string) string {
	var sb strings.Builder
	for _, issue := range issues {
		sb.WriteString(fmt.Sprintf("- %s\n", issue))
	}
	return sb.String()
}
