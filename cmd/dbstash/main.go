// Package main is the entrypoint for the dbstash backup tool.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/engine"
	"github.com/viperadnan/dbstash/internal/health"
	"github.com/viperadnan/dbstash/internal/logger"
	"github.com/viperadnan/dbstash/internal/pipeline"
	"github.com/viperadnan/dbstash/internal/scheduler"
)

// TODO(v0.7): Add restore command support (dbstash restore)
// TODO(v0.7): Add email notifications (EmailNotifier interface)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %s\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(cfg.LogLevel, cfg.LogFormat)
	log := logger.With(cfg.Engine, cfg.DBNameOrDefault(), "")

	log.Info().
		Str("engine", cfg.Engine).
		Str("mode", cfg.BackupMode).
		Str("schedule", cfg.BackupSchedule).
		Str("remote", cfg.RcloneRemote).
		Msg("dbstash starting")

	// Initialize engine
	eng, err := engine.New(cfg.Engine)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize engine")
	}

	// Check for conflicting DUMP_EXTRA_ARGS
	scheduler.CheckConflictingFlags(cfg, eng)

	// Warn if BACKUP_COMPRESS is set but engine doesn't support it
	if cfg.BackupCompress && !eng.SupportsCompression() {
		log.Warn().
			Str("engine", eng.Name()).
			Msg("BACKUP_COMPRESS=true but this engine has no native compression; ignoring")
	}

	// Initialize pipeline
	pipe, err := pipeline.New(cfg.BackupMode)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize pipeline")
	}

	// Dry run mode
	if cfg.DryRun {
		dryRun(cfg, eng)
		os.Exit(0)
	}

	// One-time backup mode
	if cfg.ScheduleOnce {
		log.Info().Msg("running one-time backup (BACKUP_SCHEDULE=once)")
		if err := scheduler.RunOnce(context.Background(), cfg, eng, pipe, nil); err != nil {
			log.Error().Err(err).Msg("one-time backup failed")
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Health tracker
	tracker := health.NewTracker(eng.Name())

	// Start health server
	healthServer := health.StartServer(":8080", tracker)
	defer healthServer.Close()

	// Run backup on start if configured
	if cfg.BackupOnStart {
		log.Info().Msg("running backup on start (BACKUP_ON_START=true)")
		go scheduler.RunOnce(context.Background(), cfg, eng, pipe, tracker)
	}

	// Start cron scheduler
	sched := scheduler.New(cfg, eng, pipe, tracker)
	if err := sched.Start(); err != nil {
		log.Fatal().Err(err).Msg("failed to start scheduler")
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	log.Info().Str("signal", sig.String()).Msg("shutting down")
	sched.Stop(30 * time.Second)
	log.Info().Msg("shutdown complete")
}

func dryRun(cfg *config.Config, eng engine.Engine) {
	log := logger.Log
	log.Info().Msg("=== DRY RUN MODE ===")
	log.Info().Str("engine", cfg.Engine).Msg("engine")
	log.Info().Str("mode", cfg.BackupMode).Msg("backup mode")
	log.Info().Str("schedule", cfg.BackupSchedule).Msg("schedule")
	log.Info().Str("remote", cfg.RcloneRemote).Msg("rclone remote")
	log.Info().Str("template", cfg.BackupNameTemplate).Msg("name template")
	log.Info().Bool("compress", cfg.BackupCompress).Msg("compression")

	// Connection info (masked)
	if cfg.DBURI != "" {
		log.Info().Str("db_uri", maskURI(cfg.DBURI)).Msg("connection")
	} else {
		log.Info().
			Str("host", cfg.DBHost).
			Str("port", cfg.DBPort).
			Str("name", cfg.DBName).
			Str("user", cfg.DBUser).
			Str("password", maskPassword(cfg.DBPassword)).
			Msg("connection")
	}

	// Build a sample dump command
	cmd, err := eng.DumpCommand(cfg, cfg.BackupMode, "/tmp/dbstash-dry-run")
	if err != nil {
		log.Warn().Err(err).Msg("could not build dump command for dry run")
	} else {
		log.Info().Str("command", cmd.String()).Msg("dump command")
	}

	log.Info().Msg("=== END DRY RUN ===")
}

func maskURI(uri string) string {
	// Mask password in URI: scheme://user:****@host
	parts := strings.SplitN(uri, "@", 2)
	if len(parts) != 2 {
		return uri
	}
	prefix := parts[0]
	idx := strings.LastIndex(prefix, ":")
	if idx < 0 {
		return uri
	}
	// Check if this colon is part of scheme://
	schemeEnd := strings.Index(prefix, "://")
	if schemeEnd >= 0 && idx <= schemeEnd+2 {
		return uri
	}
	return prefix[:idx+1] + "****@" + parts[1]
}

func maskPassword(pw string) string {
	if pw == "" {
		return ""
	}
	return "****"
}
