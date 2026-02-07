package engine

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/logger"
)

// MySQL implements the Engine interface for MySQL and MariaDB using mysqldump.
type MySQL struct {
	engineKey string
}

// Name returns the engine key ("mysql" or "mariadb").
func (m *MySQL) Name() string { return m.engineKey }

// DumpCommand builds the mysqldump command for the given mode.
func (m *MySQL) DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error) {
	var args []string

	// Resolve connection params — mysqldump doesn't accept URIs directly
	host, port, user, password, dbName := m.resolveConnection(cfg)

	if host != "" {
		args = append(args, fmt.Sprintf("--host=%s", host))
	}
	if port != "" {
		args = append(args, fmt.Sprintf("--port=%s", port))
	}
	if user != "" {
		args = append(args, fmt.Sprintf("--user=%s", user))
	}
	if password != "" {
		args = append(args, fmt.Sprintf("-p%s", password))
	}

	switch mode {
	case "stream":
		// Default stdout output
	case "directory":
		args = append(args, fmt.Sprintf("--tab=%s", outputDir))
	default:
		return nil, fmt.Errorf("unsupported mode for mysql: %s", mode)
	}

	if cfg.BackupCompress {
		logger.Log.Warn().Str("engine", m.engineKey).Msg("mysqldump has no native compression; BACKUP_COMPRESS=true is a no-op")
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, shellSplit(cfg.DumpExtraArgs)...)
	}

	// Database name
	if dbName != "" {
		args = append(args, dbName)
	}

	cmd := exec.Command("mysqldump", args...)
	cmd.Stderr = os.Stderr
	return cmd, nil
}

// resolveConnection parses DB_URI into components or uses individual vars.
func (m *MySQL) resolveConnection(cfg *config.Config) (host, port, user, password, dbName string) {
	if cfg.DBURI != "" {
		// Parse mysql://user:pass@host:port/dbname
		u, err := url.Parse(cfg.DBURI)
		if err == nil {
			host = u.Hostname()
			port = u.Port()
			user = u.User.Username()
			password, _ = u.User.Password()
			dbName = strings.TrimPrefix(u.Path, "/")
			return
		}
	}
	return cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName
}

// DefaultExtension returns ".sql" — mysqldump always outputs SQL.
func (m *MySQL) DefaultExtension(_ bool) string { return ".sql" }

// SupportsCompression returns false — mysqldump has no native compression.
func (m *MySQL) SupportsCompression() bool { return false }

// ConflictingFlags returns flags incompatible with stream mode.
func (m *MySQL) ConflictingFlags(mode string) []string {
	if mode == "stream" {
		return []string{"--tab=", "--tab "}
	}
	return nil
}
