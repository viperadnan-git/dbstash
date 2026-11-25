package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// Redis implements the Engine interface for Redis using redis-cli --rdb.
type Redis struct{}

// Name returns "redis".
func (r *Redis) Name() string { return "redis" }

// DumpCommand builds the redis-cli command. Only stream mode is supported.
func (r *Redis) DumpCommand(ctx context.Context, cfg *config.Config, mode string, _ string) (*exec.Cmd, func(), error) {
	if mode != "stream" && mode != "file" {
		return nil, nil, fmt.Errorf("redis only supports stream/file mode (got %q)", mode)
	}

	var args []string
	host, port, password := r.resolveConnection(cfg)

	if host != "" {
		args = append(args, "-h", host)
	}
	if port != "" {
		args = append(args, "-p", port)
	}

	args = append(args, "--rdb", "-")

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, config.SplitArgs(cfg.DumpExtraArgs)...)
	}

	cmd := proc.Command(ctx, "redis-cli", args...)
	cmd.Stderr = os.Stderr
	if password != "" {
		withEnv(cmd, "REDISCLI_AUTH="+password)
	}
	return cmd, noCleanup, nil
}

// resolveConnection parses redis://... URI or uses individual vars.
func (r *Redis) resolveConnection(cfg *config.Config) (host, port, password string) {
	if cfg.DBURI != "" {
		u, err := url.Parse(cfg.DBURI)
		if err == nil {
			host = u.Hostname()
			port = u.Port()
			password, _ = u.User.Password()
			if password == "" && u.User != nil {
				// redis://:password@host format
				password = u.User.Username()
			}
			return
		}
	}
	return cfg.DBHost, cfg.DBPort, cfg.DBPassword
}

// DefaultExtension returns ".rdb".
func (r *Redis) DefaultExtension(_ bool) string { return ".rdb" }

// SupportsCompression returns false — RDB is already compact.
func (r *Redis) SupportsCompression() bool { return false }

// ConflictingFlags returns nil — no conflicting flags for redis-cli.
func (r *Redis) ConflictingFlags(_ string) []string { return nil }
