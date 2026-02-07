// Package pipeline implements the backup execution pipelines (stream,
// directory, tar) that connect database dump tools to rclone uploads.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/engine"
)

// Pipeline defines the interface for backup execution strategies.
type Pipeline interface {
	// Execute runs the backup pipeline and returns the remote path,
	// uploaded file size (best-effort), and any error.
	Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (remotePath string, fileSize int64, err error)
}

// New returns the appropriate Pipeline for the configured backup mode.
func New(mode string) (Pipeline, error) {
	switch strings.ToLower(mode) {
	case "stream":
		return &StreamPipeline{}, nil
	case "directory":
		return &DirectoryPipeline{}, nil
	case "tar":
		return &TarPipeline{}, nil
	default:
		return nil, fmt.Errorf("unsupported backup mode: %s", mode)
	}
}

// resolveFilename expands the BACKUP_NAME_TEMPLATE tokens and appends
// the appropriate file extension.
func resolveFilename(template string, cfg *config.Config, eng engine.Engine, extension string) string {
	now := time.Now()
	if cfg.Timezone != "" && cfg.Timezone != "UTC" {
		if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
			now = now.In(loc)
		}
	}

	dbName := cfg.DBNameOrDefault()
	shortUUID := uuid.New().String()[:8]

	name := template
	name = strings.ReplaceAll(name, "{db}", dbName)
	name = strings.ReplaceAll(name, "{engine}", eng.Name())
	name = strings.ReplaceAll(name, "{date}", now.Format("2006-01-02"))
	name = strings.ReplaceAll(name, "{time}", now.Format("150405"))
	name = strings.ReplaceAll(name, "{ts}", fmt.Sprintf("%d", now.Unix()))
	name = strings.ReplaceAll(name, "{uuid}", shortUUID)

	// Determine extension
	if extension == "" {
		extension = eng.DefaultExtension(cfg.BackupCompress)
	}
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}

	return name + extension
}

// resolveDirname expands the template for directory mode (no extension).
func resolveDirname(template string, cfg *config.Config, eng engine.Engine) string {
	now := time.Now()
	if cfg.Timezone != "" && cfg.Timezone != "UTC" {
		if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
			now = now.In(loc)
		}
	}

	dbName := cfg.DBNameOrDefault()
	shortUUID := uuid.New().String()[:8]

	name := template
	name = strings.ReplaceAll(name, "{db}", dbName)
	name = strings.ReplaceAll(name, "{engine}", eng.Name())
	name = strings.ReplaceAll(name, "{date}", now.Format("2006-01-02"))
	name = strings.ReplaceAll(name, "{time}", now.Format("150405"))
	name = strings.ReplaceAll(name, "{ts}", fmt.Sprintf("%d", now.Unix()))
	name = strings.ReplaceAll(name, "{uuid}", shortUUID)

	return name
}

// rcloneConfigArgs returns the config flag for rclone if a config file is set.
func rcloneConfigArgs(cfg *config.Config) []string {
	var args []string
	if cfg.RcloneConfigFile != "" {
		args = append(args, "--config", cfg.RcloneConfigFile)
	}
	if cfg.RcloneExtraArgs != "" {
		args = append(args, shellSplit(cfg.RcloneExtraArgs)...)
	}
	return args
}

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
