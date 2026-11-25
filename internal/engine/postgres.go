package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/logger"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// Postgres implements the Engine interface for PostgreSQL using pg_dump.
type Postgres struct{}

// Name returns "pg".
func (p *Postgres) Name() string { return "pg" }

// DumpCommand builds the pg_dump (or pg_dumpall) command for the given mode.
func (p *Postgres) DumpCommand(ctx context.Context, cfg *config.Config, mode string, outputDir string) (*exec.Cmd, func(), error) {
	if cfg.BackupAllDatabases {
		return p.dumpAllCommand(ctx, cfg, mode, outputDir)
	}

	var args []string

	switch mode {
	case "stream":
		if cfg.BackupCompress {
			args = append(args, "--format=custom")
		} else {
			args = append(args, "--format=plain")
		}
	case "file":
		if cfg.BackupCompress {
			args = append(args, "--format=custom", fmt.Sprintf("--file=%s", outputDir))
		} else {
			args = append(args, "--format=plain", fmt.Sprintf("--file=%s", outputDir))
		}
	case "directory":
		args = append(args, "--format=directory", fmt.Sprintf("--file=%s", outputDir))
	default:
		return nil, nil, fmt.Errorf("unsupported mode for postgres: %s", mode)
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, config.SplitArgs(cfg.DumpExtraArgs)...)
	}

	// Connection: prefer URI, otherwise libpq env vars
	var env []string
	if cfg.DBURI != "" {
		if dbNameFromURI(cfg.DBURI) == "" {
			return nil, nil, fmt.Errorf("postgres URI has no database name; set DB_NAME, add a database to the URI, or use --all-databases")
		}
		uri, pw := splitPassword(cfg.DBURI)
		args = append(args, uri)
		env = pgEnv(&config.Config{DBPassword: pw})
	} else {
		env = pgEnv(cfg)
		args = append(args, cfg.DBName)
	}

	cmd := proc.Command(ctx, "pg_dump", args...)
	cmd.Stderr = os.Stderr
	withEnv(cmd, env...)
	return cmd, noCleanup, nil
}

// pgEnv maps the DB_* settings to libpq environment variables.
func pgEnv(cfg *config.Config) []string {
	var env []string
	for k, v := range map[string]string{
		"PGHOST": cfg.DBHost, "PGPORT": cfg.DBPort, "PGDATABASE": cfg.DBName,
		"PGUSER": cfg.DBUser, "PGPASSWORD": cfg.DBPassword,
	} {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// dumpAllCommand builds a pg_dumpall command. Only stream mode is supported
// because pg_dumpall outputs plain SQL only (no custom/directory format).
func (p *Postgres) dumpAllCommand(ctx context.Context, cfg *config.Config, mode string, outputFile string) (*exec.Cmd, func(), error) {
	if mode != "stream" && mode != "file" {
		return nil, nil, fmt.Errorf("pg_dumpall only supports stream/file mode (got %q)", mode)
	}
	if cfg.BackupCompress {
		logger.Log.Warn().Str("engine", "pg").Msg("pg_dumpall has no native compression; BACKUP_COMPRESS=true is a no-op")
	}

	var args []string

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, config.SplitArgs(cfg.DumpExtraArgs)...)
	}

	// Connection: prefer URI via -d, otherwise libpq env vars.
	// The database name is stripped; pg_dumpall uses it only to connect.
	var env []string
	if cfg.DBURI != "" {
		uri, pw := splitPassword(stripDBFromURI(cfg.DBURI))
		args = append(args, "-d", uri)
		env = pgEnv(&config.Config{DBPassword: pw})
	} else {
		env = pgEnv(&config.Config{DBHost: cfg.DBHost, DBPort: cfg.DBPort, DBUser: cfg.DBUser, DBPassword: cfg.DBPassword})
	}

	if mode == "file" && outputFile != "" {
		args = append(args, fmt.Sprintf("--file=%s", outputFile))
	}

	cmd := proc.Command(ctx, "pg_dumpall", args...)
	cmd.Stderr = os.Stderr
	withEnv(cmd, env...)
	return cmd, noCleanup, nil
}

// DefaultExtension returns the file extension based on compression.
func (p *Postgres) DefaultExtension(compressed bool) string {
	if compressed {
		return ".dump"
	}
	return ".sql"
}

// SupportsCompression returns true — pg_dump supports custom format.
func (p *Postgres) SupportsCompression() bool { return true }

// ConflictingFlags returns flags incompatible with stream mode.
func (p *Postgres) ConflictingFlags(mode string) []string {
	if mode == "stream" || mode == "file" {
		return []string{"--Fd", "--format=directory", "-Fd", "--file=", "-f"}
	}
	return nil
}
