package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/OnlyMyRailgun/ATRPE/internal/config"
	"github.com/OnlyMyRailgun/ATRPE/internal/github"
	"github.com/OnlyMyRailgun/ATRPE/internal/knowledge"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Open knowledge store for publish mapping lookups
	store, err := knowledge.NewSQLiteStore("data/knowledge.db")
	if err != nil {
		logger.Error("sqlite store init", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Connect to Temporal for sending signals from webhooks
	var sender github.TemporalSignalSender
	if cfg.TemporalHostPort != "" {
		c, err := client.Dial(client.Options{
			HostPort:  cfg.TemporalHostPort,
			Namespace: cfg.TemporalNamespace,
			Logger:    log.NewStructuredLogger(logger),
		})
		if err != nil {
			logger.Warn("temporal client unavailable — webhook signals disabled", "error", err)
		} else {
			defer c.Close()
			// Blocker 2: Sender carries slug→issueNumber lookup for webhook routing
			sender = &publishAwareSender{
				client: c,
				store:  store,
			}
			logger.Info("temporal client connected", "host", cfg.TemporalHostPort)
		}
	} else {
		logger.Warn("TEMPORAL_HOST_PORT not set — webhook signal forwarding disabled")
	}

	mux := http.NewServeMux()

	webhook := github.NewWebhookHandler(cfg.GitHubWebhookSecret, sender, logger)
	mux.Handle("/webhook", webhook)

	internalSig := github.NewInternalSignalHandler(cfg.InternalSignalToken, sender, logger)
	mux.Handle("/internal/workflows/", internalSig)

	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler)

	addr := ":8080"
	logger.Info("API server starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]bool{"ok": true}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Blocker 2 + 6: publishAwareSender implements both TemporalSignalSender and PublishMappingLookup.
// This lets the webhook handler resolve slug→issueNumber for routing PublishMergedSignal.
type publishAwareSender struct {
	client client.Client
	store  *knowledge.SQLiteStore
}

func (s *publishAwareSender) SendSignal(ctx context.Context, workflowID, signal string, payload map[string]any) error {
	return s.client.SignalWorkflow(ctx, workflowID, "", signal, payload)
}

func (s *publishAwareSender) LookupIssueNumber(ctx context.Context, slug string) (int, error) {
	return s.store.GetPublishMapping(ctx, slug)
}
