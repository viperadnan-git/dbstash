package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
	"github.com/viperadnan-git/dbstash/internal/logger"
)

// TarPipeline dumps to a temp directory, then streams tar output to rclone rcat.
type TarPipeline struct{}

// Execute runs the tar pipeline: dump → temp dir → tar → rclone rcat.
func (p *TarPipeline) Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (string, int64, error) {
	ext := ".tar"
	if cfg.BackupCompress {
		ext = ".tar.gz"
	}
	filename := resolveDirname(cfg.BackupNameTemplate, cfg, eng) + ext
	remotePath := strings.TrimRight(cfg.RcloneRemote, "/") + "/" + filename

	log := logger.Log.With().Str("pipeline", "tar").Str("remote_path", remotePath).Logger()
	log.Debug().Msg("starting tar pipeline")

	// Create temp dir
	tempDir, err := os.MkdirTemp(cfg.BackupTempDir, "dbstash-tar-")
	if err != nil {
		os.MkdirAll(cfg.BackupTempDir, 0o755)
		tempDir, err = os.MkdirTemp(cfg.BackupTempDir, "dbstash-tar-")
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

	// Pipe: tar → rclone rcat (with gzip if compression is enabled)
	var tarCmd *exec.Cmd
	if cfg.BackupCompress {
		tarCmd = exec.CommandContext(ctx, "tar", "czf", "-", "-C", tempDir, ".")
	} else {
		tarCmd = exec.CommandContext(ctx, "tar", "cf", "-", "-C", tempDir, ".")
	}
	rcloneArgs := []string{"rcat", remotePath}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := exec.CommandContext(ctx, "rclone", rcloneArgs...)

	pr, pw := io.Pipe()
	tarCmd.Stdout = pw
	var tarStderr bytes.Buffer
	tarCmd.Stderr = &tarStderr

	rcloneCmd.Stdin = pr
	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	if err := rcloneCmd.Start(); err != nil {
		return "", 0, fmt.Errorf("starting rclone: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		pw.Close()
		rcloneCmd.Wait()
		return "", 0, fmt.Errorf("starting tar: %w", err)
	}

	tarErr := tarCmd.Wait()
	pw.Close()

	rcloneErr := rcloneCmd.Wait()
	pr.Close()

	if tarErr != nil {
		return "", 0, fmt.Errorf("tar failed: %w (stderr: %s)", tarErr, tarStderr.String())
	}
	if rcloneErr != nil {
		return "", 0, fmt.Errorf("rclone rcat failed: %w (stderr: %s)", rcloneErr, rcloneStderr.String())
	}

	// Best-effort file size
	fileSize := getRemoteFileSize(ctx, remotePath, cfg)

	log.Debug().Int64("file_size", fileSize).Msg("tar pipeline completed")
	return remotePath, fileSize, nil
}
