package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
	"github.com/viperadnan-git/dbstash/internal/logger"
)

// DirectoryPipeline dumps to a temp directory then uploads via rclone copy.
type DirectoryPipeline struct{}

// Execute runs the directory pipeline: dump → temp dir → rclone copy.
func (p *DirectoryPipeline) Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (string, int64, error) {
	dirname := resolveDirname(cfg.BackupNameTemplate, cfg, eng)
	remotePath := strings.TrimRight(cfg.RcloneRemote, "/") + "/" + dirname + "/"

	log := logger.Log.With().Str("pipeline", "directory").Str("remote_path", remotePath).Logger()
	log.Debug().Msg("starting directory pipeline")

	// Create temp dir
	tempDir, err := os.MkdirTemp(cfg.BackupTempDir, "dbstash-dir-")
	if err != nil {
		// Try creating the parent first
		os.MkdirAll(cfg.BackupTempDir, 0o755)
		tempDir, err = os.MkdirTemp(cfg.BackupTempDir, "dbstash-dir-")
		if err != nil {
			return "", 0, fmt.Errorf("creating temp dir: %w", err)
		}
	}
	defer os.RemoveAll(tempDir)

	// Run dump to temp dir
	dumpCmd, err := eng.DumpCommand(cfg, "directory", tempDir)
	if err != nil {
		return "", 0, fmt.Errorf("building dump command: %w", err)
	}
	var dumpStderr bytes.Buffer
	dumpCmd.Stderr = &dumpStderr

	log.Debug().Str("cmd", config.MaskCmdArgs(dumpCmd.Args)).Msg("running dump")
	if err := dumpCmd.Run(); err != nil {
		return "", 0, fmt.Errorf("dump failed: %w (stderr: %s)", err, dumpStderr.String())
	}

	// Upload via rclone copy
	rcloneArgs := []string{"copy", tempDir, remotePath}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := exec.CommandContext(ctx, "rclone", rcloneArgs...)
	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	log.Debug().Str("cmd", rcloneCmd.String()).Msg("running rclone copy")
	if err := rcloneCmd.Run(); err != nil {
		return "", 0, fmt.Errorf("rclone copy failed: %w (stderr: %s)", err, rcloneStderr.String())
	}

	log.Debug().Msg("directory pipeline completed")
	return remotePath, 0, nil
}
