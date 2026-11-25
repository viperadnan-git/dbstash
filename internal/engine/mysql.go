package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/logger"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// MySQL implements the Engine interface for MySQL and MariaDB using mysqldump.
type MySQL struct {
	engineKey string
}

// Name returns the engine key ("mysql" or "mariadb").
func (m *MySQL) Name() string { return m.engineKey }

// DumpCommand builds the mysqldump command for the given mode.
func (m *MySQL) DumpCommand(ctx context.Context, cfg *config.Config, mode string, outputDir string) (*exec.Cmd, func(), error) {
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

	switch mode {
	case "stream", "file":
		// writes to stdout; file pipeline redirects stdout to temp file
	case "directory":
		if cfg.BackupAllDatabases {
			return nil, nil, fmt.Errorf("mysqldump --all-databases is incompatible with directory mode (--tab)")
		}
		args = append(args, fmt.Sprintf("--tab=%s", outputDir))
	default:
		return nil, nil, fmt.Errorf("unsupported mode for mysql: %s", mode)
	}

	if cfg.BackupCompress {
		logger.Log.Warn().Str("engine", m.engineKey).Msg("mysqldump has no native compression; BACKUP_COMPRESS=true is a no-op")
	}

	// Extra args
	if cfg.DumpExtraArgs != "" {
		args = append(args, config.SplitArgs(cfg.DumpExtraArgs)...)
	}

	// Database selection
	if cfg.BackupAllDatabases {
		args = append(args, "--all-databases")
	} else if dbName != "" {
		args = append(args, dbName)
	} else {
		return nil, nil, fmt.Errorf("no database name found; set DB_NAME, add a database to the URI, or use --all-databases")
	}

	// mariadb-dump: MariaDB 11+ clients may not ship the mysqldump alias.
	bin := "mysqldump"
	if m.engineKey == "mariadb" {
		bin = "mariadb-dump"
	}
	cleanup := noCleanup
	if password != "" {
		path, rm, err := config.WriteTempFile("dbstash-*.cnf", []byte("[client]\npassword="+optionValue(password)+"\n"))
		if err != nil {
			return nil, nil, err
		}
		args = append([]string{"--defaults-extra-file=" + path}, args...) // must come first
		cleanup = rm
	}
	cmd := proc.Command(ctx, bin, args...)
	cmd.Stderr = os.Stderr
	return cmd, cleanup, nil
}

// optionValue quotes s for a MySQL option file.
func optionValue(s string) string {
	s = strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
	q := `"`
	if strings.Contains(s, q) {
		q = "'"
	}
	return q + s + q
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
	if mode == "stream" || mode == "file" {
		return []string{"--tab=", "--tab "}
	}
	return nil
}
