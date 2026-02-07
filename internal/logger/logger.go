// Package logger provides structured logging for dbstash using zerolog.
package logger

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Log is the package-level logger instance used throughout the application.
var Log zerolog.Logger

// Init configures the global logger based on the provided level and format.
// Supported levels: debug, info, warn, error.
// Supported formats: json (default), text.
func Init(level, format string) {
	var l zerolog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = zerolog.DebugLevel
	case "warn":
		l = zerolog.WarnLevel
	case "error":
		l = zerolog.ErrorLevel
	default:
		l = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(l)

	if strings.ToLower(format) == "text" {
		Log = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}).With().Timestamp().Logger()
	} else {
		Log = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}
}

// With returns a sub-logger with the given contextual fields.
func With(engine, database, backupID string) zerolog.Logger {
	ctx := Log.With()
	if engine != "" {
		ctx = ctx.Str("engine", engine)
	}
	if database != "" {
		ctx = ctx.Str("database", database)
	}
	if backupID != "" {
		ctx = ctx.Str("backup_id", backupID)
	}
	return ctx.Logger()
}
