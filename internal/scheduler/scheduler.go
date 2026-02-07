// Package scheduler manages cron-based backup scheduling with lock guarding
// and provides the single-run backup orchestration used by both cron and
// one-time backup modes.
package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/engine"
	"github.com/viperadnan/dbstash/internal/health"
	"github.com/viperadnan/dbstash/internal/hooks"
	"github.com/viperadnan/dbstash/internal/logger"
	"github.com/viperadnan/dbstash/internal/notify"
	"github.com/viperadnan/dbstash/internal/pipeline"
	"github.com/viperadnan/dbstash/internal/retention"
)

// Scheduler wraps cron scheduling with lock guarding.
type Scheduler struct {
	cron    *cron.Cron
	cfg     *config.Config
	eng     engine.Engine
	pipe    pipeline.Pipeline
	tracker *health.Tracker
	running atomic.Bool
	mu      sync.Mutex
}

// New creates a new Scheduler.
func New(cfg *config.Config, eng engine.Engine, pipe pipeline.Pipeline, tracker *health.Tracker) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		eng:     eng,
		pipe:    pipe,
		tracker: tracker,
	}
}

// Start begins the cron scheduler.
func (s *Scheduler) Start() error {
	s.cron = cron.New()

	_, err := s.cron.AddFunc(s.cfg.BackupSchedule, func() {
		s.runWithLock()
	})
	if err != nil {
		return fmt.Errorf("adding cron job: %w", err)
	}

	s.cron.Start()
	logger.Log.Info().Str("schedule", s.cfg.BackupSchedule).Msg("cron scheduler started")
	return nil
}

// Stop gracefully stops the cron scheduler and waits for any in-progress
// backup to complete.
func (s *Scheduler) Stop(timeout time.Duration) {
	if s.cron != nil {
		ctx := s.cron.Stop()

		// Wait for in-progress backup or timeout
		select {
		case <-ctx.Done():
		case <-time.After(timeout):
			logger.Log.Warn().Msg("timeout waiting for in-progress backup to finish")
		}
	}
}

func (s *Scheduler) runWithLock() {
	if s.cfg.BackupLock {
		if !s.running.CompareAndSwap(false, true) {
			logger.Log.Warn().Msg("skipping backup: previous run still in progress")
			return
		}
		defer s.running.Store(false)
	}

	RunOnce(context.Background(), s.cfg, s.eng, s.pipe, s.tracker)
}

// RunOnce executes a single backup run with full orchestration:
// hooks, pipeline, retention, notifications, and logging.
func RunOnce(parentCtx context.Context, cfg *config.Config, eng engine.Engine, pipe pipeline.Pipeline, tracker *health.Tracker) error {
	backupID := uuid.New().String()[:8]
	log := logger.With(eng.Name(), cfg.DBNameOrDefault(), backupID)
	start := time.Now()

	log.Info().Msg("backup started")

	// Create context with timeout if configured
	ctx := parentCtx
	var cancel context.CancelFunc
	if cfg.BackupTimeout > 0 {
		ctx, cancel = context.WithTimeout(parentCtx, cfg.BackupTimeout)
		defer cancel()
	}

	var (
		remotePath string
		fileSize   int64
		status     = "success"
		backupErr  error
	)

	// Pre-backup hook
	if err := hooks.RunPreBackup(ctx, cfg.HookPreBackup); err != nil {
		status = "failure"
		backupErr = fmt.Errorf("pre-backup hook failed: %w", err)
		log.Error().Err(err).Msg("pre-backup hook failed, skipping backup")
		goto notify
	}

	// Execute pipeline
	remotePath, fileSize, backupErr = pipe.Execute(ctx, eng, cfg)
	if backupErr != nil {
		status = "failure"
		log.Error().Err(backupErr).Msg("backup failed")
	} else {
		log.Info().
			Str("remote_path", remotePath).
			Int64("file_size", fileSize).
			Dur("duration", time.Since(start)).
			Msg("backup completed successfully")

		// Retention cleanup (only on success)
		deleted, err := retention.Run(ctx, cfg)
		if err != nil {
			log.Warn().Err(err).Msg("retention cleanup failed")
		} else if deleted > 0 {
			log.Info().Int("deleted", deleted).Msg("retention cleanup completed")
		}
	}

notify:
	// Post-backup hook
	if err := hooks.RunPostBackup(ctx, cfg.HookPostBackup, status, remotePath); err != nil {
		log.Warn().Err(err).Msg("post-backup hook failed")
	}

	// Update health tracker
	if tracker != nil {
		tracker.Update(status)
	}

	// Send notification
	duration := time.Since(start)
	result := notify.Result{
		Status:     status,
		Engine:     eng.Name(),
		Database:   cfg.DBNameOrDefault(),
		RemotePath: remotePath,
		FileSize:   fileSize,
		Duration:   duration,
	}
	if backupErr != nil {
		result.Error = backupErr.Error()
	}
	notify.Send(ctx, cfg.NotifyWebhookURL, cfg.NotifyOn, result)

	// Log summary
	log.Info().
		Str("status", status).
		Str("remote_path", remotePath).
		Int64("file_size", fileSize).
		Dur("duration", duration).
		Msg("backup run completed")

	if backupErr != nil {
		return backupErr
	}
	return nil
}

// CheckConflictingFlags scans DUMP_EXTRA_ARGS against the engine's
// conflicting flags for the current mode and logs warnings.
func CheckConflictingFlags(cfg *config.Config, eng engine.Engine) {
	if cfg.DumpExtraArgs == "" {
		return
	}

	conflicts := eng.ConflictingFlags(cfg.BackupMode)
	if len(conflicts) == 0 {
		return
	}

	for _, flag := range conflicts {
		if strings.Contains(cfg.DumpExtraArgs, flag) {
			logger.Log.Warn().
				Str("engine", eng.Name()).
				Str("mode", cfg.BackupMode).
				Str("flag", flag).
				Msg("DUMP_EXTRA_ARGS contains a flag that conflicts with the current backup mode; the dump tool may fail")
		}
	}
}
