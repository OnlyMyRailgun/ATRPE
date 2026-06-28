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
		return map[string]interface{}{"candidates": []interface{}{
			map[string]interface{}{"id": "abc123", "title": "test-repo", "score": 0.95, "source": "github_trending", "url": "https://example.com"},
		}}, nil
	})

	// Mock AuditTopics
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"audits": []interface{}{
			map[string]interface{}{"candidate_id": "abc123", "passes": true, "recommendation": 0.85, "saturation_level": "low"},
		}}, nil
	})

	// Mock CreateTopicIssue
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"issue_url": "test://issue", "issue_number": 1}, nil
	})

	// Mock ResolveCandidateID
	env.RegisterActivity(func(input map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"candidate_id": "abc123"}, nil
	})

	// Mock ResearchTopic
	env.RegisterActivity(func(input map[string]interface{}) (interface{}, error) {
		return nil, nil
	})

	// Mock PostComment (no-op)
	env.RegisterActivity(func(input map[string]interface{}) error {
		return nil
	})

	// Mock all downstream activities
	env.RegisterActivity(func(input map[string]interface{}) (interface{}, error) { return nil, nil })
}

func TestArticleWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	setupEnv(env)

	// After CONTENT_AUDIT completes, select topic and approve
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("TopicSelectedSignal", TopicSelectedSignal{CandidateID: "abc123"})
	}, time.Millisecond*200)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("PublishApprovalSignal", PublishApprovalSignal{})
	}, time.Millisecond*800)

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

	env.ExecuteWorkflow(ArticleWorkflow, ArticleWorkflowInput{MaxRemediationAttempts: 3})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
