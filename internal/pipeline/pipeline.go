// Package pipeline implements the backup execution pipelines (stream,
// directory, tar) that connect database dump tools to rclone uploads.
package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
	"github.com/viperadnan-git/dbstash/internal/logger"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// Pipeline defines the interface for backup execution strategies.
type Pipeline interface {
	// Execute runs the backup pipeline and returns the remote path,
	// uploaded file size (best-effort), and any error.
	Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (remotePath string, fileSize int64, err error)
}

// CleanStaleTempDirs removes any leftover dbstash-* temp directories under
// BackupTempDir that survived a previous SIGKILL or crash.
func CleanStaleTempDirs(tempDir string) {
	entries, err := filepath.Glob(filepath.Join(tempDir, "dbstash-*"))
	if err != nil || len(entries) == 0 {
		return
	}
	for _, entry := range entries {
		if err := os.RemoveAll(entry); err != nil {
			logger.Log.Warn().Str("path", entry).Err(err).Msg("failed to remove stale temp dir")
		} else {
			logger.Log.Debug().Str("path", entry).Msg("removed stale temp dir")
		}
	}
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
	case "file":
		return &FilePipeline{}, nil
	default:
		return nil, fmt.Errorf("unsupported backup mode: %s", mode)
	}
}

// resolveFilename expands the BACKUP_NAME_TEMPLATE tokens and appends
// the appropriate file extension.
//
// Supported tokens:
//
//	{db}     — database name from DB_NAME or parsed from DB_URI (e.g. "myapp")
//	{engine} — engine key: pg, mongo, mysql, mariadb, or redis
//	{date}   — current date as YYYY-MM-DD (e.g. "2026-02-07")
//	{time}   — current time as HHmmss (e.g. "020000")
//	{timestamp} — ISO 8601 timestamp as YYYYMMDDTHHMMSSZ0700 (e.g. "20260207T020000Z", "20260207T073000+0530")
//	{ts}     — Unix timestamp in seconds (e.g. "1770508800")
//	{uuid}   — first 8 characters of a UUIDv7 (e.g. "019c38fb")
//
// The file extension is appended automatically based on the engine and
// compression setting, unless overridden by BACKUP_EXTENSION. Timestamps
// use the timezone configured via the TZ environment variable (default UTC).
func resolveFilename(template string, cfg *config.Config, eng engine.Engine, extension string) string {
	now := time.Now()
	if cfg.Timezone != "" && cfg.Timezone != "UTC" {
		if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
			now = now.In(loc)
		}
	}

	dbName := cfg.DBNameOrDefault()
	shortUUID := uuid.Must(uuid.NewV7()).String()[:8]

	name := template
	name = strings.ReplaceAll(name, "{db}", dbName)
	name = strings.ReplaceAll(name, "{engine}", eng.Name())
	name = strings.ReplaceAll(name, "{date}", now.Format("2006-01-02"))
	name = strings.ReplaceAll(name, "{time}", now.Format("150405"))
	name = strings.ReplaceAll(name, "{timestamp}", now.Format("20060102T150405Z0700"))
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
	shortUUID := uuid.Must(uuid.NewV7()).String()[:8]

	name := template
	name = strings.ReplaceAll(name, "{db}", dbName)
	name = strings.ReplaceAll(name, "{engine}", eng.Name())
	name = strings.ReplaceAll(name, "{date}", now.Format("2006-01-02"))
	name = strings.ReplaceAll(name, "{time}", now.Format("150405"))
	name = strings.ReplaceAll(name, "{timestamp}", now.Format("20060102T150405Z0700"))
	name = strings.ReplaceAll(name, "{ts}", fmt.Sprintf("%d", now.Unix()))
	name = strings.ReplaceAll(name, "{uuid}", shortUUID)

	return name
}

// runPiped runs src | dst through an OS pipe and returns each command's exit
// error, or startErr if either failed to start. With no copying goroutine in
// between, when one side exits or is killed the other sees EOF or EPIPE
// instead of Wait blocking forever.
func runPiped(src, dst *exec.Cmd) (srcErr, dstErr, startErr error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating pipe: %w", err)
	}
	src.Stdout, dst.Stdin = w, r
	if err := dst.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, nil, fmt.Errorf("starting %s: %w", dst.Args[0], err)
	}
	r.Close() // dst holds its own copy; without ours src gets EPIPE if dst dies
	if err := src.Start(); err != nil {
		w.Close()
		dst.Wait()
		return nil, nil, fmt.Errorf("starting %s: %w", src.Args[0], err)
	}
	w.Close() // src holds its own copy; dst sees EOF when src exits
	return src.Wait(), dst.Wait(), nil
}

// cleanupRemote removes a partial upload from the remote on failure, using
// rclone `deletefile` for a file or `purge` for a directory. It runs even when
// ctx is already cancelled (signal or timeout), which is usually why the upload
// failed.
func cleanupRemote(ctx context.Context, rcloneCmd, remotePath string, cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	args := []string{rcloneCmd, remotePath}
	args = append(args, rcloneConfigArgs(cfg)...)
	cmd := proc.Command(ctx, "rclone", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		logger.Log.Warn().Err(err).Str("path", remotePath).Msg("failed to clean up remote after error")
	} else {
		logger.Log.Debug().Str("path", remotePath).Msg("cleaned up remote after error")
	}
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
