package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/OnlyMyRailgun/ATRPE/internal/config"
	"github.com/OnlyMyRailgun/ATRPE/internal/github"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var sender github.TemporalSignalSender

	mux := http.NewServeMux()

	webhook := github.NewWebhookHandler(cfg.GitHubWebhookSecret, sender, logger)
	mux.Handle("/webhook", webhook)

	internalSig := github.NewInternalSignalHandler(cfg.InternalSignalToken, sender, logger)
	mux.Handle("/internal/workflows/", internalSig)

	// Observability endpoints
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
	// TODO: check Temporal connection + SQLite reachability for real health
	resp := map[string]bool{"ok": true}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
