package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
	"github.com/viperadnan-git/dbstash/internal/logger"
)

// FilePipeline dumps to a temp file then uploads via rclone copy.
// Unlike stream mode, the dump fully completes before the upload starts.
type FilePipeline struct{}

// Execute runs the file pipeline: dump → temp file → rclone copy.
func (p *FilePipeline) Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (string, int64, error) {
	filename := resolveFilename(cfg.BackupNameTemplate, cfg, eng, cfg.BackupExtension)
	remoteDir := strings.TrimRight(cfg.RcloneRemote, "/") + "/"
	remotePath := remoteDir + filename

	log := logger.Log.With().Str("pipeline", "file").Str("remote_path", remotePath).Logger()
	log.Debug().Msg("starting file pipeline")

	// Create temp dir
	tempDir, err := os.MkdirTemp(cfg.BackupTempDir, "dbstash-file-")
	if err != nil {
		os.MkdirAll(cfg.BackupTempDir, 0o755)
		tempDir, err = os.MkdirTemp(cfg.BackupTempDir, "dbstash-file-")
		if err != nil {
			return "", 0, fmt.Errorf("creating temp dir: %w", err)
		}
	}
	defer os.RemoveAll(tempDir)

	tempFilePath := filepath.Join(tempDir, filename)

	// Build dump command — engines that support direct file output (mongo, pg)
	// write to tempFilePath natively; others (mysql, redis) write to stdout
	// which is redirected to tempFilePath via cmd.Stdout below.
	dumpCmd, err := eng.DumpCommand(cfg, "file", tempFilePath)
	if err != nil {
		return "", 0, fmt.Errorf("building dump command: %w", err)
	}

	// Open temp file; used as stdout fallback for engines that write to stdout
	f, err := os.Create(tempFilePath)
	if err != nil {
		return "", 0, fmt.Errorf("creating temp file: %w", err)
	}
	dumpCmd.Stdout = f

	var dumpStderr bytes.Buffer
	dumpCmd.Stderr = &dumpStderr

	log.Debug().Str("dump_cmd", config.MaskCmdArgs(dumpCmd.Args)).Msg("running dump")
	dumpErr := dumpCmd.Run()
	f.Close()

	if dumpErr != nil {
		return "", 0, fmt.Errorf("dump failed: %w (stderr: %s)", dumpErr, dumpStderr.String())
	}

	// Upload via rclone copy — runs after dump is fully complete
	rcloneArgs := []string{"copy", tempDir, remoteDir}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := exec.CommandContext(ctx, "rclone", rcloneArgs...)
	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	log.Debug().Strs("rclone_args", rcloneArgs).Msg("running rclone copy")
	if err := rcloneCmd.Run(); err != nil {
		return "", 0, fmt.Errorf("rclone copy failed: %w (stderr: %s)", err, rcloneStderr.String())
	}

	fileSize := getRemoteFileSize(ctx, remotePath, cfg)
	log.Debug().Int64("file_size", fileSize).Msg("file pipeline completed")
	return remotePath, fileSize, nil
}
