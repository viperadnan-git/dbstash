package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/viperadnan/dbstash/internal/config"
	"github.com/viperadnan/dbstash/internal/engine"
	"github.com/viperadnan/dbstash/internal/logger"
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
	dumpCmd, err := eng.DumpCommand(cfg, "stream", "")
	if err != nil {
		return "", 0, fmt.Errorf("building dump command: %w", err)
	}

	// Build rclone command
	rcloneArgs := []string{"rcat", remotePath}
	rcloneArgs = append(rcloneArgs, rcloneConfigArgs(cfg)...)
	rcloneCmd := exec.CommandContext(ctx, "rclone", rcloneArgs...)

	// Pipe dump stdout → rclone stdin
	pr, pw := io.Pipe()
	dumpCmd.Stdout = pw

	rcloneCmd.Stdin = pr
	var rcloneStderr bytes.Buffer
	rcloneCmd.Stderr = &rcloneStderr

	var dumpStderr bytes.Buffer
	dumpCmd.Stderr = &dumpStderr

	log.Debug().Str("dump_cmd", dumpCmd.String()).Msg("executing dump command")
	log.Debug().Strs("rclone_args", rcloneArgs).Msg("executing rclone command")

	// Start rclone first, then dump
	log.Debug().Msg("starting rclone process")
	if err := rcloneCmd.Start(); err != nil {
		return "", 0, fmt.Errorf("starting rclone: %w", err)
	}

	log.Debug().Msg("starting dump process")
	if err := dumpCmd.Start(); err != nil {
		pw.Close()
		rcloneCmd.Wait()
		return "", 0, fmt.Errorf("starting dump: %w", err)
	}

	log.Debug().Msg("waiting for dump to complete")

	// Wait for dump to finish, then close the pipe
	dumpErr := dumpCmd.Wait()
	log.Debug().Err(dumpErr).Msg("dump process finished")
	pw.Close()

	// Wait for rclone to finish
	log.Debug().Msg("waiting for rclone to complete")
	rcloneErr := rcloneCmd.Wait()
	log.Debug().Err(rcloneErr).Msg("rclone process finished")
	pr.Close()

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
	cmd := exec.CommandContext(ctx, "rclone", args...)

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
