package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/your-org/atrpe/internal/activities"
	"github.com/your-org/atrpe/internal/config"
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
	w.RegisterActivity(acts.CreateTopicIssue)
	w.RegisterActivity(acts.ResearchTopic)
	w.RegisterActivity(acts.DesignArchitecture)
	w.RegisterActivity(acts.PublishArticle)

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
