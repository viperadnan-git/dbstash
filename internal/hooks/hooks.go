// Package hooks provides pre- and post-backup shell command execution.
package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/viperadnan-git/dbstash/internal/logger"
)

// RunPreBackup executes the pre-backup hook command via sh -c.
// Returns nil if the command is empty. Returns an error if the command
// exits with a non-zero code.
func RunPreBackup(ctx context.Context, command string) error {
	if command == "" {
		return nil
	}
	logger.Log.Info().Str("hook", "pre_backup").Str("command", command).Msg("running pre-backup hook")
	return runHook(ctx, command, nil)
}

// RunPostBackup executes the post-backup hook command via sh -c.
// It injects DBSTASH_STATUS and DBSTASH_FILE into the command's environment.
// Returns nil if the command is empty.
func RunPostBackup(ctx context.Context, command string, status string, remotePath string) error {
	if command == "" {
		return nil
	}
	logger.Log.Info().
		Str("hook", "post_backup").
		Str("command", command).
		Str("status", status).
		Str("remote_path", remotePath).
		Msg("running post-backup hook")

	env := []string{
		fmt.Sprintf("DBSTASH_STATUS=%s", status),
		fmt.Sprintf("DBSTASH_FILE=%s", remotePath),
	}
	return runHook(ctx, command, env)
}

func runHook(ctx context.Context, command string, extraEnv []string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), extraEnv...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook command %q failed: %w", command, err)
	}
	return nil
}
