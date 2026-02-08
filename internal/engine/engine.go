// Package engine defines the interface for database dump tools and provides
// implementations for each supported database engine.
package engine

import (
	"fmt"
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
	DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error)

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

// shellSplit performs basic splitting of a string into arguments, respecting
// single and double quotes. This is used to split DUMP_EXTRA_ARGS.
func shellSplit(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
