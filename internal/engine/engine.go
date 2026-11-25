// Package engine defines the interface for database dump tools and provides
// implementations for each supported database engine.
package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
)

// Engine defines the interface that each database dump tool must implement.
type Engine interface {
	// Name returns the engine key (e.g. "pg", "mongo").
	Name() string

	// DumpCommand returns the exec.Cmd for the dump tool.
	// For stream mode, the command writes to stdout.
	// For directory mode, it writes to the provided outputDir.
	// The command is killed when ctx is done. Credentials never go in its
	// arguments; cleanup removes any option file holding them and must run
	// once the command has exited. cleanup is never nil when err is nil.
	DumpCommand(ctx context.Context, cfg *config.Config, mode string, outputDir string) (cmd *exec.Cmd, cleanup func(), err error)

	// DefaultExtension returns the file extension based on compression setting.
	DefaultExtension(compressed bool) string

	// SupportsCompression returns whether BACKUP_COMPRESS is meaningful.
	SupportsCompression() bool

	// ConflictingFlags returns flag prefixes incompatible with the given mode.
	ConflictingFlags(mode string) []string
}

// New returns the Engine implementation for the given engine key.
func New(engineKey string) (Engine, error) {
	switch strings.ToLower(engineKey) {
	case "pg":
		return &Postgres{}, nil
	case "mongo":
		return &Mongo{}, nil
	case "mysql", "mariadb":
		return &MySQL{engineKey: engineKey}, nil
	case "redis":
		return &Redis{}, nil
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engineKey)
	}
}

// dbNameFromURI extracts the database name from a URI path.
func dbNameFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// stripDBFromURI removes the database name from a URI,
// keeping the scheme, credentials, host, and query parameters intact.
func stripDBFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	u.Path = "/"
	return u.String()
}

func noCleanup() {}

// splitPassword returns uri without its password, and the password.
func splitPassword(uri string) (string, string) {
	u, err := url.Parse(uri)
	if err != nil || u.User == nil {
		return uri, ""
	}
	pw, ok := u.User.Password()
	if !ok {
		return uri, ""
	}
	u.User = url.User(u.User.Username())
	return u.String(), pw
}

// withEnv adds vars to the environment cmd inherits.
func withEnv(cmd *exec.Cmd, vars ...string) {
	if len(vars) > 0 {
		cmd.Env = append(os.Environ(), vars...)
	}
}
