package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func setupEnv(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterWorkflow(ArticleWorkflow)
	env.RegisterActivity(func() (map[string]interface{}, error) {
		return map[string]interface{}{"candidates": []interface{}{}}, nil
	})
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"audits": []interface{}{}}, nil
	})
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"issue_url": "test://issue", "issue_number": 1}, nil
	})
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"candidate_id": "abc123"}, nil
	})
	// Research, Design, Experiment, Verify, GenerateDraft, PublishArticle,
	// MergePublish, PostComment — all no-ops
	env.RegisterActivity(func(input map[string]interface{}) (interface{}, error) { return nil, nil })
	env.RegisterActivity(func(input map[string]interface{}) error { return nil })
}

func TestArticleWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	setupEnv(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("TopicSelectedSignal", TopicSelectedSignal{CandidateID: "abc123"})
	}, time.Millisecond*200)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishApprovalSignal", PublishApprovalSignal{})
	}, time.Millisecond*800)

	// C: publish PR created → wait for merge → merged signal
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishMergedSignal", struct{}{})
	}, time.Millisecond*1200)

	env.ExecuteWorkflow(ArticleWorkflow, ArticleWorkflowInput{MaxRemediationAttempts: 3})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestArticleWorkflow_AbortDuringWaitSelection(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	setupEnv(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("AbortSignal", AbortSignal{})
	}, time.Millisecond*200)

	env.ExecuteWorkflow(ArticleWorkflow, ArticleWorkflowInput{MaxRemediationAttempts: 3})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestArticleWorkflow_ChangesDuringApproval(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	setupEnv(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("TopicSelectedSignal", TopicSelectedSignal{CandidateID: "abc123"})
	}, time.Millisecond*200)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("RequestChangesSignal", RequestChangesSignal{ChangeNotes: "Add more detail"})
	}, time.Millisecond*500)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishApprovalSignal", PublishApprovalSignal{})
	}, time.Millisecond*800)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishMergedSignal", struct{}{})
	}, time.Millisecond*1200)

	env.ExecuteWorkflow(ArticleWorkflow, ArticleWorkflowInput{MaxRemediationAttempts: 3})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
