package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// Mongo implements the Engine interface for MongoDB using mongodump.
type Mongo struct{}

// Name returns "mongo".
func (m *Mongo) Name() string { return "mongo" }

// DumpCommand builds the mongodump command for the given mode.
func (m *Mongo) DumpCommand(ctx context.Context, cfg *config.Config, mode string, outputDir string) (*exec.Cmd, func(), error) {
	var args []string

	switch mode {
	case "stream":
		args = append(args, "--archive")
		if cfg.BackupCompress {
			args = append(args, "--gzip")
		}
	case "file":
		args = append(args, fmt.Sprintf("--archive=%s", outputDir))
		if cfg.BackupCompress {
			args = append(args, "--gzip")
		}
	case "directory":
		args = append(args, fmt.Sprintf("--out=%s", outputDir))
		if cfg.BackupCompress {
			args = append(args, "--gzip")
		}
	default:
		return nil, nil, fmt.Errorf("unsupported mode for mongo: %s", mode)
	}

	// Connection; credentials go in a --config file
	secrets := map[string]string{}
	if cfg.DBURI != "" {
		uri := cfg.DBURI
		if cfg.BackupAllDatabases {
			uri = stripDBFromURI(uri) // so mongodump dumps all databases
		}
		if _, pw := splitPassword(uri); pw != "" {
			secrets["uri"] = uri
		} else {
			args = append(args, fmt.Sprintf("--uri=%s", uri))
		}
		if !cfg.BackupAllDatabases {
			dbName := dbNameFromURI(cfg.DBURI)
			if dbName == "" {
				return nil, nil, fmt.Errorf("mongo URI has no database name; set DB_NAME, add a database to the URI, or use --all-databases")
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
			secrets["password"] = cfg.DBPassword
		}
		if cfg.DBAuthSource != "" {
			args = append(args, fmt.Sprintf("--authenticationDatabase=%s", cfg.DBAuthSource))
		}
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, config.SplitArgs(cfg.DumpExtraArgs)...)
	}

	cleanup := noCleanup
	if len(secrets) > 0 {
		var yaml []byte
		for k, v := range secrets {
			q, _ := json.Marshal(v) // a JSON string is a valid YAML scalar
			yaml = fmt.Appendf(yaml, "%s: %s\n", k, q)
		}
		path, rm, err := config.WriteTempFile("dbstash-*.yaml", yaml)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--config="+path)
		cleanup = rm
	}
	cmd := proc.Command(ctx, "mongodump", args...)
	cmd.Stderr = os.Stderr
	return cmd, cleanup, nil
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
	if mode == "stream" || mode == "file" {
		return []string{"--out=", "-o"}
	}
	return nil
}
