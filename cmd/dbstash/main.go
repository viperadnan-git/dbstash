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

	"github.com/urfave/cli/v3"
	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/engine"
	"github.com/viperadnan/dbstash/internal/health"
	"github.com/viperadnan/dbstash/internal/logger"
	"github.com/viperadnan/dbstash/internal/pipeline"
	"github.com/viperadnan/dbstash/internal/scheduler"
)

var version = "dev"

func main() {
	app := &cli.Command{
		Name:    "dbstash",
		Usage:   "Database backup via rclone",
		Version: version,
		// Legacy env-only mode: no subcommand, reads ENGINE env var
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("configuration error: %s", err)
			}
			return run(cfg)
		},
		Commands: []*cli.Command{
			engineCommand("pg", "PostgreSQL backup"),
			engineCommand("mongo", "MongoDB backup"),
			engineCommand("mysql", "MySQL backup"),
			engineCommand("mariadb", "MariaDB backup"),
			engineCommand("redis", "Redis backup"),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

// engineCommand creates a CLI subcommand for the given database engine.
func engineCommand(engineKey, usage string) *cli.Command {
	flags := commonFlags()
	if engineKey == "mongo" {
		flags = append(flags, &cli.StringFlag{
			Name:    "db-auth-source",
			Usage:   "MongoDB auth database",
			Value:   "admin",
			Sources: cli.EnvVars("DB_AUTH_SOURCE"),
		})
	}

	return &cli.Command{
		Name:  engineKey,
		Usage: usage,
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := configFromCLI(engineKey, cmd)
			if err != nil {
				return fmt.Errorf("configuration error: %s", err)
			}
			return run(cfg)
		},
	}
}

// commonFlags returns the flags shared by all engine subcommands.
func commonFlags() []cli.Flag {
	return []cli.Flag{
		// Connection
		&cli.StringFlag{
			Name:    "db-uri",
			Usage:   "Full connection URI",
			Sources: cli.EnvVars("DB_URI"),
		},
		&cli.StringFlag{
			Name:    "db-uri-file",
			Usage:   "Path to file containing connection URI",
			Sources: cli.EnvVars("DB_URI_FILE"),
		},
		&cli.StringFlag{
			Name:    "db-host",
			Usage:   "Database host",
			Sources: cli.EnvVars("DB_HOST"),
		},
		&cli.StringFlag{
			Name:    "db-port",
			Usage:   "Database port",
			Sources: cli.EnvVars("DB_PORT"),
		},
		&cli.StringFlag{
			Name:    "db-name",
			Usage:   "Database name",
			Sources: cli.EnvVars("DB_NAME"),
		},
		&cli.StringFlag{
			Name:    "db-user",
			Usage:   "Database user",
			Sources: cli.EnvVars("DB_USER"),
		},
		&cli.StringFlag{
			Name:    "db-password",
			Usage:   "Database password",
			Sources: cli.EnvVars("DB_PASSWORD"),
		},
		&cli.StringFlag{
			Name:    "db-password-file",
			Usage:   "Path to file containing database password",
			Sources: cli.EnvVars("DB_PASSWORD_FILE"),
		},

		// Rclone
		&cli.StringFlag{
			Name:    "rclone-remote",
			Usage:   "Rclone remote path (e.g. s3:my-bucket/backups)",
			Sources: cli.EnvVars("RCLONE_REMOTE"),
		},
		&cli.StringFlag{
			Name:    "rclone-config",
			Usage:   "Base64-encoded rclone.conf content",
			Sources: cli.EnvVars("RCLONE_CONFIG"),
		},
		&cli.StringFlag{
			Name:    "rclone-config-file",
			Usage:   "Path to rclone config file",
			Sources: cli.EnvVars("RCLONE_CONFIG_FILE"),
		},
		&cli.StringFlag{
			Name:    "rclone-extra-args",
			Usage:   "Additional rclone flags",
			Sources: cli.EnvVars("RCLONE_EXTRA_ARGS"),
		},

		// Schedule & Backup
		&cli.StringFlag{
			Name:    "backup-schedule",
			Usage:   "Cron expression or 'once'",
			Value:   "0 2 * * *",
			Sources: cli.EnvVars("BACKUP_SCHEDULE"),
		},
		&cli.StringFlag{
			Name:    "backup-mode",
			Usage:   "Backup mode: stream, directory, or tar",
			Value:   "stream",
			Sources: cli.EnvVars("BACKUP_MODE"),
		},
		&cli.StringFlag{
			Name:    "backup-name-template",
			Usage:   "Filename template with tokens: {db}, {engine}, {date}, {time}, {ts}, {uuid}",
			Value:   "{db}-{date}-{time}",
			Sources: cli.EnvVars("BACKUP_NAME_TEMPLATE"),
		},
		&cli.BoolFlag{
			Name:    "backup-compress",
			Usage:   "Enable native compression",
			Sources: cli.EnvVars("BACKUP_COMPRESS"),
		},
		&cli.StringFlag{
			Name:    "backup-extension",
			Usage:   "Override file extension",
			Sources: cli.EnvVars("BACKUP_EXTENSION"),
		},
		&cli.BoolFlag{
			Name:    "backup-on-start",
			Usage:   "Run backup immediately on start",
			Sources: cli.EnvVars("BACKUP_ON_START"),
		},
		&cli.StringFlag{
			Name:    "backup-timeout",
			Usage:   "Max duration for a backup (e.g. 1h, 30m)",
			Value:   "0",
			Sources: cli.EnvVars("BACKUP_TIMEOUT"),
		},
		&cli.BoolFlag{
			Name:    "backup-lock",
			Usage:   "Prevent overlapping backup runs",
			Value:   true,
			Sources: cli.EnvVars("BACKUP_LOCK"),
		},
		&cli.StringFlag{
			Name:    "backup-temp-dir",
			Usage:   "Temp directory for directory/tar modes",
			Value:   "/tmp/dbstash-work",
			Sources: cli.EnvVars("BACKUP_TEMP_DIR"),
		},
		&cli.StringFlag{
			Name:    "dump-extra-args",
			Usage:   "Additional flags for the dump tool",
			Sources: cli.EnvVars("DUMP_EXTRA_ARGS"),
		},
		&cli.BoolFlag{
			Name:    "dry-run",
			Usage:   "Log config without executing",
			Sources: cli.EnvVars("DRY_RUN"),
		},
		&cli.StringFlag{
			Name:    "tz",
			Usage:   "Timezone for schedule and filenames",
			Value:   "UTC",
			Sources: cli.EnvVars("TZ"),
		},

		// Retention
		&cli.IntFlag{
			Name:    "retention-max-files",
			Usage:   "Keep at most N backup files (0 = unlimited)",
			Sources: cli.EnvVars("RETENTION_MAX_FILES"),
		},
		&cli.IntFlag{
			Name:    "retention-max-days",
			Usage:   "Delete backups older than N days (0 = unlimited)",
			Sources: cli.EnvVars("RETENTION_MAX_DAYS"),
		},

		// Notifications
		&cli.StringFlag{
			Name:    "notify-webhook-url",
			Usage:   "Slack or Discord webhook URL",
			Sources: cli.EnvVars("NOTIFY_WEBHOOK_URL"),
		},
		&cli.StringFlag{
			Name:    "notify-on",
			Usage:   "When to notify: always, failure, success",
			Value:   "failure",
			Sources: cli.EnvVars("NOTIFY_ON"),
		},

		// Hooks
		&cli.StringFlag{
			Name:    "hook-pre-backup",
			Usage:   "Shell command to run before backup",
			Sources: cli.EnvVars("HOOK_PRE_BACKUP"),
		},
		&cli.StringFlag{
			Name:    "hook-post-backup",
			Usage:   "Shell command to run after backup",
			Sources: cli.EnvVars("HOOK_POST_BACKUP"),
		},

		// Logging
		&cli.StringFlag{
			Name:    "log-level",
			Usage:   "Log verbosity: debug, info, warn, error",
			Value:   "info",
			Sources: cli.EnvVars("LOG_LEVEL"),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Usage:   "Log format: json or text",
			Value:   "text",
			Sources: cli.EnvVars("LOG_FORMAT"),
		},
	}
}

// configFromCLI builds a Config from parsed CLI flags with env var fallback.
func configFromCLI(engineKey string, cmd *cli.Command) (*config.Config, error) {
	cfg := &config.Config{}
	cfg.Engine = engineKey

	// Connection — resolve _FILE variants
	cfg.DBURI = cmd.String("db-uri")
	if cfg.DBURI == "" {
		cfg.DBURI = config.ResolveFileValue(cmd.String("db-uri-file"))
	}
	cfg.DBHost = cmd.String("db-host")
	cfg.DBPort = cmd.String("db-port")
	cfg.DBName = cmd.String("db-name")
	cfg.DBUser = cmd.String("db-user")
	cfg.DBPassword = cmd.String("db-password")
	if cfg.DBPassword == "" {
		cfg.DBPassword = config.ResolveFileValue(cmd.String("db-password-file"))
	}
	cfg.DBAuthSource = cmd.String("db-auth-source")
	if cfg.DBAuthSource == "" {
		cfg.DBAuthSource = "admin"
	}

	// Rclone
	cfg.RcloneRemote = cmd.String("rclone-remote")
	cfg.RcloneExtraArgs = cmd.String("rclone-extra-args")

	rcloneConfigFile, err := config.ResolveRcloneConfig(
		cmd.String("rclone-config-file"),
		cmd.String("rclone-config"),
	)
	if err != nil {
		return nil, err
	}
	cfg.RcloneConfigFile = rcloneConfigFile

	// Schedule & Backup
	cfg.BackupSchedule = cmd.String("backup-schedule")
	cfg.BackupMode = cmd.String("backup-mode")
	cfg.BackupNameTemplate = cmd.String("backup-name-template")
	cfg.BackupCompress = cmd.Bool("backup-compress")
	cfg.BackupExtension = cmd.String("backup-extension")
	cfg.BackupOnStart = cmd.Bool("backup-on-start")
	cfg.DumpExtraArgs = cmd.String("dump-extra-args")
	cfg.Timezone = cmd.String("tz")
	cfg.BackupTempDir = cmd.String("backup-temp-dir")

	// Timeout
	timeoutStr := cmd.String("backup-timeout")
	if timeoutStr != "0" && timeoutStr != "" {
		d, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid --backup-timeout %q: %w", timeoutStr, err)
		}
		cfg.BackupTimeout = d
	}

	cfg.BackupLock = cmd.Bool("backup-lock")
	cfg.DryRun = cmd.Bool("dry-run")

	// Retention
	cfg.RetentionMaxFiles = int(cmd.Int("retention-max-files"))
	cfg.RetentionMaxDays = int(cmd.Int("retention-max-days"))

	// Notifications
	cfg.NotifyWebhookURL = cmd.String("notify-webhook-url")
	cfg.NotifyOn = cmd.String("notify-on")

	// Hooks
	cfg.HookPreBackup = cmd.String("hook-pre-backup")
	cfg.HookPostBackup = cmd.String("hook-post-backup")

	// Logging
	cfg.LogLevel = cmd.String("log-level")
	cfg.LogFormat = cmd.String("log-format")

	// Validate
	if err := cfg.Prepare(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// run executes the backup workflow with a validated Config.
func run(cfg *config.Config) error {
	// Initialize logger
	logger.Init(cfg.LogLevel, cfg.LogFormat)
	log := logger.With(cfg.Engine, cfg.DBNameOrDefault(), "")

	log.Info().
		Str("engine", cfg.Engine).
		Str("database", cfg.DBNameOrDefault()).
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
		return nil
	}

	// One-time backup mode
	if cfg.ScheduleOnce {
		log.Info().Msg("running one-time backup")
		if err := scheduler.RunOnce(context.Background(), cfg, eng, pipe, nil); err != nil {
			log.Error().Err(err).Msg("one-time backup failed")
			os.Exit(1)
		}
		return nil
	}

	// Health tracker
	tracker := health.NewTracker(eng.Name())

	// Start health server
	healthServer := health.StartServer(":8080", tracker)
	defer healthServer.Close()

	// Run backup on start if configured
	if cfg.BackupOnStart {
		log.Info().Msg("running backup on start")
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
	return nil
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
	parts := strings.SplitN(uri, "@", 2)
	if len(parts) != 2 {
		return uri
	}
	prefix := parts[0]
	idx := strings.LastIndex(prefix, ":")
	if idx < 0 {
		return uri
	}
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
