package workflows

import (
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
}

// ArticleWorkflowState is the durable state carried through the workflow.
type ArticleWorkflowState struct {
	State            WorkflowState
	CandidateID      string
	ChangeNotes      string
	RemediationCount int
	MaxRemediation   int
}

// ArticleWorkflow orchestrates the full article lifecycle.
func ArticleWorkflow(ctx workflow.Context, input ArticleWorkflowInput) error {
	s := ArticleWorkflowState{
		State:          StateDiscover,
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

func runDiscover(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — DiscoverTopics
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
		// topic_id = s.CandidateID (search attribute deferred)
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "AbortSignal"), func(c workflow.ReceiveChannel, more bool) {
		var sig AbortSignal
		c.Receive(ctx, &sig)
		s.State = StateAborted
	})

	sel.Select(ctx)
	return s
}

func runResearch(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — ResearchTopic
	s.State = StateDesign
	setState(ctx, StateDesign)
	return s
}

func runDesign(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — DesignArchitecture
	s.State = StateExperiment
	setState(ctx, StateExperiment)
	return s
}

func runExperiment(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — RunExperiment
	s.State = StateVerify
	setState(ctx, StateVerify)
	return s
}

func runVerify(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — VerifyResults
	passed := true // placeholder
	if passed {
		s.State = StateGenerateArticle
	} else if s.RemediationCount < s.MaxRemediation {
		s.State = StatePatchGeneration
	} else {
		s.State = StateEscalated
	}
	setState(ctx, s.State)
	return s
}

func runGenerateArticle(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — GenerateDraft
	s.State = StateWaitPublishApproval
	setState(ctx, StateWaitPublishApproval)
	return s
}

func runWaitPublishApproval(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	sel := workflow.NewSelector(ctx)

	sel.AddReceive(workflow.GetSignalChannel(ctx, "PublishApprovalSignal"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &PublishApprovalSignal{})
		s.State = StatePublish
	})
	sel.AddReceive(workflow.GetSignalChannel(ctx, "RequestChangesSignal"), func(c workflow.ReceiveChannel, more bool) {
		var sig RequestChangesSignal
		c.Receive(ctx, &sig)
		s.ChangeNotes = sig.ChangeNotes
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
	// TODO: activity call — PublishArticle
	s.State = StateCompleted
	setState(ctx, StateCompleted)
	return s
}

func runPatchGeneration(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	s.RemediationCount++
	// TODO: activity call — PatchExperiment
	s.State = StateDesignUpdate
	setState(ctx, StateDesignUpdate)
	return s
}

func runDesignUpdate(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
	// TODO: activity call — UpdateDesign
	s.State = StateExperiment
	setState(ctx, StateExperiment)
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
	setState(ctx, s.State)
	return s
}
