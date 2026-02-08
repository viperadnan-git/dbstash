// Package retention manages cleanup of old backup files on the remote
// based on max file count and max age constraints.
package retention

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/logger"
)

// RemoteEntry represents a single item returned by rclone lsjson.
type RemoteEntry struct {
	Path    string    `json:"Path"`
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

// Run performs retention cleanup on the remote based on the configured
// RETENTION_MAX_FILES and RETENTION_MAX_DAYS. It returns the number
// of entries deleted.
func Run(ctx context.Context, cfg *config.Config) (int, error) {
	if cfg.RetentionMaxFiles <= 0 && cfg.RetentionMaxDays <= 0 {
		logger.Log.Debug().Msg("retention: no constraints configured, skipping")
		return 0, nil
	}

	entries, err := listRemote(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("listing remote for retention: %w", err)
	}

	if len(entries) == 0 {
		logger.Log.Debug().Msg("retention: no entries found on remote")
		return 0, nil
	}

	toDelete := selectDeletions(entries, cfg.RetentionMaxFiles, cfg.RetentionMaxDays)
	if len(toDelete) == 0 {
		logger.Log.Debug().Int("total_entries", len(entries)).Msg("retention: nothing to delete")
		return 0, nil
	}

	deleted := 0
	for _, entry := range toDelete {
		if err := deleteEntry(ctx, cfg, entry); err != nil {
			logger.Log.Warn().Err(err).Str("path", entry.Path).Msg("retention: failed to delete entry")
			continue
		}
		logger.Log.Info().Str("path", entry.Path).Time("mod_time", entry.ModTime).Msg("retention: deleted old backup")
		deleted++
	}

	return deleted, nil
}

// SelectDeletions determines which entries to delete based on max files and max days.
// Exported for testing.
func SelectDeletions(entries []RemoteEntry, maxFiles, maxDays int) []RemoteEntry {
	return selectDeletions(entries, maxFiles, maxDays)
}

func selectDeletions(entries []RemoteEntry, maxFiles, maxDays int) []RemoteEntry {
	// Sort by modification time, newest first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})

	deleteSet := make(map[string]bool)

	// Max files: keep only the newest N
	if maxFiles > 0 && len(entries) > maxFiles {
		for _, entry := range entries[maxFiles:] {
			deleteSet[entry.Path] = true
		}
	}

	// Max days: delete entries older than N days
	if maxDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -maxDays)
		for _, entry := range entries {
			if entry.ModTime.Before(cutoff) {
				deleteSet[entry.Path] = true
			}
		}
	}

	var result []RemoteEntry
	for _, entry := range entries {
		if deleteSet[entry.Path] {
			result = append(result, entry)
		}
	}
	return result
}

func listRemote(ctx context.Context, cfg *config.Config) ([]RemoteEntry, error) {
	args := []string{"lsjson", cfg.RcloneRemote}
	if cfg.RcloneConfigFile != "" {
		args = append(args, "--config", cfg.RcloneConfigFile)
	}

	cmd := exec.CommandContext(ctx, "rclone", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rclone lsjson: %w (stderr: %s)", err, stderr.String())
	}

	var entries []RemoteEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		return nil, fmt.Errorf("parsing rclone lsjson output: %w", err)
	}

	return entries, nil
}

func deleteEntry(ctx context.Context, cfg *config.Config, entry RemoteEntry) error {
	remotePath := strings.TrimRight(cfg.RcloneRemote, "/") + "/" + entry.Path
	var args []string

	if entry.IsDir {
		args = []string{"purge", remotePath}
	} else {
		args = []string{"deletefile", remotePath}
	}

	if cfg.RcloneConfigFile != "" {
		args = append(args, "--config", cfg.RcloneConfigFile)
	}

	cmd := exec.CommandContext(ctx, "rclone", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rclone delete %s: %w (stderr: %s)", remotePath, err, stderr.String())
	}
	return nil
}
