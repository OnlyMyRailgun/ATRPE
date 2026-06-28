package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// EngagementCollectionWorkflow periodically collects article engagement metrics.
// This is invoked by a Temporal Schedule (daily at 09:00 JST).
func EngagementCollectionWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    3,
		},
	})

	// Collect engagement for all published articles
	var result struct {
		Metrics []struct {
			TopicID     string `json:"topic_id"`
			Platform    string `json:"platform"`
			Likes       int    `json:"likes"`
			PublishDate string `json:"publish_date"`
		} `json:"metrics"`
	}

	err := workflow.ExecuteActivity(ctx, "CollectEngagementMetrics", map[string]interface{}{
		// Slugs would be populated from the knowledge store
		// For now, the activity queries all known articles
	}).Get(ctx, &result)
	if err != nil {
		workflow.GetLogger(ctx).Error("CollectEngagementMetrics failed", "error", err)
		return err
	}

	workflow.GetLogger(ctx).Info("engagement collection complete", "articles_checked", len(result.Metrics))
	return nil
}

// RegisterSchedule creates a Temporal Schedule for daily engagement collection.
// Call this during worker startup after the client is connected.
// Schedule: every day at 09:00 JST (00:00 UTC).
func RegisterEngagementSchedule(ctx workflow.Context, taskQueue string) {
	_ = taskQueue // reserved for Schedule registration
	// Schedule registration is done via Temporal SDK client, not in workflow code.
	// See cmd/worker/main.go for the actual Schedule registration.
}
