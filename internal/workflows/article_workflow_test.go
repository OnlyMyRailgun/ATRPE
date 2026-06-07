package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func setupEnv(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterWorkflow(ArticleWorkflow)

	// Mock DiscoverTopics
	env.RegisterActivity(func() (map[string]interface{}, error) {
		return map[string]interface{}{"candidates": []interface{}{}}, nil
	})

	// Mock CreateTopicIssue
	env.RegisterActivity(func(input interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"issue_url": "test://issue"}, nil
	})
}

func TestArticleWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	setupEnv(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("TopicSelectedSignal", TopicSelectedSignal{CandidateID: "abc123"})
	}, time.Millisecond*100)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishApprovalSignal", PublishApprovalSignal{})
	}, time.Millisecond*500)

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
	}, time.Millisecond*100)

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
	}, time.Millisecond*100)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("RequestChangesSignal", RequestChangesSignal{ChangeNotes: "Add more detail"})
	}, time.Millisecond*300)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishApprovalSignal", PublishApprovalSignal{})
	}, time.Millisecond*500)

	env.ExecuteWorkflow(ArticleWorkflow, ArticleWorkflowInput{MaxRemediationAttempts: 3})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
