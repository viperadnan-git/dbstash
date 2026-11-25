package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
	"github.com/viperadnan-git/dbstash/internal/logger"
	"github.com/viperadnan-git/dbstash/internal/proc"
)

// StreamPipeline pipes dump stdout directly into rclone rcat.
type StreamPipeline struct{}

// Execute runs the streaming pipeline: dump stdout → rclone rcat.
func (p *StreamPipeline) Execute(ctx context.Context, eng engine.Engine, cfg *config.Config) (string, int64, error) {
	filename := resolveFilename(cfg.BackupNameTemplate, cfg, eng, cfg.BackupExtension)
	remotePath := strings.TrimRight(cfg.RcloneRemote, "/") + "/" + filename

	log := logger.Log.With().Str("pipeline", "stream").Str("remote_path", remotePath).Logger()
	log.Debug().Msg("starting stream pipeline")

	// Build dump command
	dumpCmd, err := eng.DumpCommand(ctx, cfg, "stream", "")
	if err != nil {
		return "", 0, fmt.Errorf("building dump command: %w", err)
	}

	// Build rclone command
	rcloneArgs := []string{"rcat", remotePath}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := proc.Command(ctx, "rclone", rcloneArgs...)

	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	var dumpStderr bytes.Buffer
	dumpCmd.Stderr = &dumpStderr

	log.Debug().Str("dump_cmd", config.MaskCmdArgs(dumpCmd.Args)).Msg("executing dump command")
	log.Debug().Strs("rclone_args", rcloneArgs).Msg("executing rclone command")

	// Pipe dump stdout → rclone stdin
	dumpErr, rcloneErr, err := runPiped(dumpCmd, rcloneCmd)
	if err != nil {
		return "", 0, err
	}
	log.Debug().AnErr("dump_err", dumpErr).AnErr("rclone_err", rcloneErr).Msg("dump and rclone finished")

	// rclone rcat keeps whatever it received, so drop it whichever side failed.
	if dumpErr != nil || rcloneErr != nil {
		log.Debug().Str("remote_path", remotePath).Msg("backup failed, cleaning up remote file")
		cleanupRemote(ctx, "deletefile", remotePath, cfg)
	}
	if dumpErr != nil {
		return "", 0, fmt.Errorf("dump failed: %w (stderr: %s)", dumpErr, dumpStderr.String())
	}
	if rcloneErr != nil {
		return "", 0, fmt.Errorf("rclone rcat failed: %w (stderr: %s)", rcloneErr, rcloneStderr.String())
	}

	// Best-effort file size retrieval
	fileSize := getRemoteFileSize(ctx, remotePath, cfg)

	log.Debug().Int64("file_size", fileSize).Msg("stream pipeline completed")
	return remotePath, fileSize, nil
}

// getRemoteFileSize attempts to get the size of the uploaded file via rclone size.
func getRemoteFileSize(ctx context.Context, remotePath string, cfg *config.Config) int64 {
	args := []string{"size", remotePath, "--json"}
	args = append(args, rcloneConfigArgs(cfg)...)
	cmd := proc.Command(ctx, "rclone", args...)

	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	// Parse simple JSON: {"count":1,"bytes":12345}
	s := string(output)
	idx := strings.Index(s, `"bytes":`)
	if idx < 0 {
		return 0
	}
	s = s[idx+8:]
	end := strings.IndexAny(s, ",}")
	if end < 0 {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(s[:end]), 10, 64)
	return n
}
