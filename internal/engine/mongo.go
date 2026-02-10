package engine

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/config"
)

// Mongo implements the Engine interface for MongoDB using mongodump.
type Mongo struct{}

// Name returns "mongo".
func (m *Mongo) Name() string { return "mongo" }

// DumpCommand builds the mongodump command for the given mode.
func (m *Mongo) DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error) {
	var args []string

	switch mode {
	case "stream":
		args = append(args, "--archive")
		if cfg.BackupCompress {
			args = append(args, "--gzip")
		}
	case "directory":
		args = append(args, fmt.Sprintf("--out=%s", outputDir))
		if cfg.BackupCompress {
			args = append(args, "--gzip")
		}
	default:
		return nil, fmt.Errorf("unsupported mode for mongo: %s", mode)
	}

	// Connection
	if cfg.DBURI != "" {
		if cfg.BackupAllDatabases {
			// Strip database name from URI so mongodump dumps all databases
			args = append(args, fmt.Sprintf("--uri=%s", stripDBFromURI(cfg.DBURI)))
		} else {
			args = append(args, fmt.Sprintf("--uri=%s", cfg.DBURI))
			dbName := dbNameFromURI(cfg.DBURI)
			if dbName == "" {
				return nil, fmt.Errorf("mongo URI has no database name; set DB_NAME, add a database to the URI, or use --all-databases")
			}
			args = append(args, fmt.Sprintf("--db=%s", dbName))
		}
	} else {
		if cfg.DBHost != "" {
			args = append(args, fmt.Sprintf("--host=%s", cfg.DBHost))
		}
		if cfg.DBPort != "" {
			args = append(args, fmt.Sprintf("--port=%s", cfg.DBPort))
		}
		if cfg.DBName != "" && !cfg.BackupAllDatabases {
			args = append(args, fmt.Sprintf("--db=%s", cfg.DBName))
		}
		if cfg.DBUser != "" {
			args = append(args, fmt.Sprintf("--username=%s", cfg.DBUser))
		}
		if cfg.DBPassword != "" {
			args = append(args, fmt.Sprintf("--password=%s", cfg.DBPassword))
		}
		if cfg.DBAuthSource != "" {
			args = append(args, fmt.Sprintf("--authenticationDatabase=%s", cfg.DBAuthSource))
		}
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, shellSplit(cfg.DumpExtraArgs)...)
	}

	cmd := exec.Command("mongodump", args...)
	cmd.Stderr = os.Stderr
	return cmd, nil
}

// DefaultExtension returns the file extension based on compression.
func (m *Mongo) DefaultExtension(compressed bool) string {
	if compressed {
		return ".archive.gz"
	}
	return ".archive"
}

// SupportsCompression returns true — mongodump supports --gzip.
func (m *Mongo) SupportsCompression() bool { return true }

// ConflictingFlags returns flags incompatible with stream mode.
func (m *Mongo) ConflictingFlags(mode string) []string {
	if mode == "stream" {
		return []string{"--out=", "-o"}
	}
	return nil
}
