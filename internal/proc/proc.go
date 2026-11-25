// Package proc builds external commands that stop gracefully when their
// context ends.
package proc

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// WaitDelay is how long a cancelled command gets to exit after SIGTERM
// before it is killed. It fits inside Docker's default 10s stop timeout.
const WaitDelay = 5 * time.Second

// Command is exec.CommandContext, except that cancellation sends SIGTERM and
// only kills after WaitDelay. SIGKILL would stop rclone from removing its
// partial uploads. Where SIGTERM is unsupported (Windows), the kill happens
// after WaitDelay.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = WaitDelay
	return cmd
}
