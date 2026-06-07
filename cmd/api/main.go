package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/your-org/atrpe/internal/config"
	"github.com/your-org/atrpe/internal/github"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Temporal signal sender will be wired in Week 2
	var sender github.TemporalSignalSender

	mux := http.NewServeMux()

	webhook := github.NewWebhookHandler(cfg.GitHubWebhookSecret, sender, logger)
	mux.Handle("/webhook", webhook)

	internalSig := github.NewInternalSignalHandler(cfg.InternalSignalToken, sender, logger)
	mux.Handle("/internal/workflows/", internalSig)

	addr := ":8080"
	logger.Info("API server starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
