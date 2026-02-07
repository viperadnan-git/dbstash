package engine

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan/dbstash/internal/config"
)

// Postgres implements the Engine interface for PostgreSQL using pg_dump.
type Postgres struct{}

// Name returns "pg".
func (p *Postgres) Name() string { return "pg" }

// DumpCommand builds the pg_dump command for the given mode.
func (p *Postgres) DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error) {
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
