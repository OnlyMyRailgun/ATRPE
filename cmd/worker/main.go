package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/your-org/atrpe/internal/activities"
	"github.com/your-org/atrpe/internal/config"
	"github.com/your-org/atrpe/internal/github"
	"github.com/your-org/atrpe/internal/knowledge"
	"github.com/your-org/atrpe/internal/objectstore"
	"github.com/your-org/atrpe/internal/workflows"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	sdkworker "go.temporal.io/sdk/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "error", err)
		os.Exit(1)
	}

	// Initialize stores
	store, err := knowledge.NewSQLiteStore("data/knowledge.db")
	if err != nil {
		logger.Error("sqlite store init failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	objects := objectstore.NewObjectStore(
		cfg.ArtifactStoreType, cfg.LocalArtifactDir,
		cfg.R2Endpoint, cfg.R2Bucket, cfg.R2AccessKeyID, cfg.R2SecretAccessKey,
	)

	// Initialize activities
	acts := activities.New(cfg, store, objects)

	// Connect to Temporal
	c, err := client.Dial(client.Options{
		HostPort:  cfg.TemporalHostPort,
		Namespace: cfg.TemporalNamespace,
		Logger:    log.NewStructuredLogger(logger),
	})
	if err != nil {
		logger.Error("temporal client dial failed", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	// Create worker
	w := sdkworker.New(c, cfg.TemporalTaskQueue, sdkworker.Options{
		WorkerStopTimeout: 30 * time.Second,
	})

	w.RegisterWorkflow(workflows.ArticleWorkflow)
	w.RegisterActivity(acts.DiscoverTopics)
	w.RegisterActivity(acts.AuditTopics)
	w.RegisterActivity(acts.CreateTopicIssue)
	w.RegisterActivity(acts.PostComment)
	w.RegisterActivity(acts.ResolveCandidateID)
	w.RegisterActivity(acts.ResearchTopic)
	w.RegisterActivity(acts.DesignArchitecture)
	w.RegisterActivity(acts.RunExperiment)
	w.RegisterActivity(acts.VerifyExperiment)
	w.RegisterActivity(acts.GenerateDraft)
	w.RegisterActivity(acts.CreateArticlePR)
	w.RegisterActivity(acts.PatchExperiment)
	w.RegisterActivity(acts.UpdateDesign)
	w.RegisterActivity(acts.CleanupWorkspace)
	w.RegisterActivity(acts.CollectEngagementMetrics)
	w.RegisterActivity(acts.PublishArticle)

	// Start issue poller if GitHub App is configured
	if acts.GitHub != nil && cfg.GitHubIssueRepo != "" {
		sender := &temporalSender{client: c}
		poller := github.NewIssuePoller(acts.GitHub, cfg.GitHubIssueRepo, sender, logger)
		go poller.Start(context.Background())
		logger.Info("issue poller started — watching all open issues")
	}

	if err := w.Start(); err != nil {
		logger.Error("worker start failed", "error", err)
		os.Exit(1)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("worker started", "queue", cfg.TemporalTaskQueue)
	<-quit

	logger.Info("worker shutting down")
	w.Stop()
}

// temporalSender implements github.TemporalSignalSender using the Temporal client.
type temporalSender struct {
	client client.Client
}

func (s *temporalSender) SendSignal(ctx context.Context, workflowID, signal string, payload map[string]any) error {
	return s.client.SignalWorkflow(ctx, workflowID, "", signal, payload)
}
