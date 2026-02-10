package engine

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/logger"
)

// Postgres implements the Engine interface for PostgreSQL using pg_dump.
type Postgres struct{}

// Name returns "pg".
func (p *Postgres) Name() string { return "pg" }

// DumpCommand builds the pg_dump (or pg_dumpall) command for the given mode.
func (p *Postgres) DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error) {
	if cfg.BackupAllDatabases {
		return p.dumpAllCommand(cfg, mode)
	}

	var args []string

	switch mode {
	case "stream":
		if cfg.BackupCompress {
			args = append(args, "--format=custom")
		} else {
			args = append(args, "--format=plain")
		}
	case "directory":
		args = append(args, "--format=directory", fmt.Sprintf("--file=%s", outputDir))
	default:
		return nil, fmt.Errorf("unsupported mode for postgres: %s", mode)
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, shellSplit(cfg.DumpExtraArgs)...)
	}

	// Connection: prefer URI, otherwise set env vars for pg_dump
	if cfg.DBURI != "" {
		if dbNameFromURI(cfg.DBURI) == "" {
			return nil, fmt.Errorf("postgres URI has no database name; set DB_NAME, add a database to the URI, or use --all-databases")
		}
		args = append(args, cfg.DBURI)
	} else {
		// pg_dump reads PGHOST, PGPORT, etc. from environment
		if cfg.DBHost != "" {
			os.Setenv("PGHOST", cfg.DBHost)
		}
		if cfg.DBPort != "" {
			os.Setenv("PGPORT", cfg.DBPort)
		}
		if cfg.DBName != "" {
			os.Setenv("PGDATABASE", cfg.DBName)
		}
		if cfg.DBUser != "" {
			os.Setenv("PGUSER", cfg.DBUser)
		}
		if cfg.DBPassword != "" {
			os.Setenv("PGPASSWORD", cfg.DBPassword)
		}
		args = append(args, cfg.DBName)
	}

	cmd := exec.Command("pg_dump", args...)
	cmd.Stderr = os.Stderr
	return cmd, nil
}

// dumpAllCommand builds a pg_dumpall command. Only stream mode is supported
// because pg_dumpall outputs plain SQL only (no custom/directory format).
func (p *Postgres) dumpAllCommand(cfg *config.Config, mode string) (*exec.Cmd, error) {
	if mode != "stream" {
		return nil, fmt.Errorf("pg_dumpall only supports stream mode (got %q)", mode)
	}
	if cfg.BackupCompress {
		logger.Log.Warn().Str("engine", "pg").Msg("pg_dumpall has no native compression; BACKUP_COMPRESS=true is a no-op")
	}

	var args []string

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, shellSplit(cfg.DumpExtraArgs)...)
	}

	// Connection: prefer URI via -d, otherwise env vars
	// Strip database name from URI — pg_dumpall uses it only for connection
	if cfg.DBURI != "" {
		args = append(args, "-d", stripDBFromURI(cfg.DBURI))
	} else {
		if cfg.DBHost != "" {
			os.Setenv("PGHOST", cfg.DBHost)
		}
		if cfg.DBPort != "" {
			os.Setenv("PGPORT", cfg.DBPort)
		}
		if cfg.DBUser != "" {
			os.Setenv("PGUSER", cfg.DBUser)
		}
		if cfg.DBPassword != "" {
			os.Setenv("PGPASSWORD", cfg.DBPassword)
		}
	}

	cmd := exec.Command("pg_dumpall", args...)
	cmd.Stderr = os.Stderr
	return cmd, nil
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
	if mode == "stream" {
		return []string{"--Fd", "--format=directory", "-Fd", "--file=", "-f"}
	}
	return nil
}
