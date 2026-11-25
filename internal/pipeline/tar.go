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
	"github.com/viperadnan-git/dbstash/internal/proc"
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
	dumpCmd, err := eng.DumpCommand(ctx, cfg, "directory", tempDir)
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
		tarCmd = proc.Command(ctx, "tar", "czf", "-", "-C", tempDir, ".")
	} else {
		tarCmd = proc.Command(ctx, "tar", "cf", "-", "-C", tempDir, ".")
	}
	rcloneArgs := []string{"rcat", remotePath}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := proc.Command(ctx, "rclone", rcloneArgs...)

	var tarStderr bytes.Buffer
	tarCmd.Stderr = &tarStderr
	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	tarErr, rcloneErr, err := runPiped(tarCmd, rcloneCmd)
	if err != nil {
		return "", 0, err
	}

	// rclone rcat keeps whatever it received, so drop it whichever side failed.
	if tarErr != nil || rcloneErr != nil {
		log.Debug().Str("remote_path", remotePath).Msg("backup failed, cleaning up remote file")
		cleanupRemote(ctx, "deletefile", remotePath, cfg)
	}
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
