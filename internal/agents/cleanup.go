package agents

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// CleanupPolicy controls experiment workspace retention.
type CleanupPolicy struct {
	OnSuccessRetainHours int
	OnFailureRetainHours int
	OnAbortImmediate     bool
}

// DefaultCleanupPolicy returns the MVP defaults: 24h success, 72h failure, immediate abort.
func DefaultCleanupPolicy() CleanupPolicy {
	return CleanupPolicy{
		OnSuccessRetainHours: 24,
		OnFailureRetainHours: 72,
		OnAbortImmediate:     true,
	}
}

// ScheduleCleanup deletes a workspace after the configured retention period.
func ScheduleCleanup(logger *slog.Logger, workdir string, policy CleanupPolicy, passed bool) {
	if passed {
		logger.Info("scheduling cleanup", "workdir", workdir, "retain_hours", policy.OnSuccessRetainHours)
		go func() {
			time.Sleep(time.Duration(policy.OnSuccessRetainHours) * time.Hour)
			os.RemoveAll(workdir)
			logger.Debug("workspace cleaned up", "workdir", workdir)
		}()
	} else {
		logger.Info("scheduling cleanup", "workdir", workdir, "retain_hours", policy.OnFailureRetainHours)
		go func() {
			time.Sleep(time.Duration(policy.OnFailureRetainHours) * time.Hour)
			os.RemoveAll(workdir)
			logger.Debug("workspace cleaned up", "workdir", workdir)
		}()
	}
}

// ImmediateCleanup removes a workspace immediately (for aborted/failed workflows).
func ImmediateCleanup(ctx context.Context, workdir string) error {
	return os.RemoveAll(workdir)
}
