package proc

import (
	"context"
	"testing"
	"time"
)

func runCancelled(t *testing.T, script string) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, "sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the trap install
	start := time.Now()
	cancel()
	err := cmd.Wait()
	return time.Since(start), err
}

func TestCommand_CancelSendsSIGTERM(t *testing.T) {
	took, err := runCancelled(t, `trap 'exit 7' TERM; sleep 30 & wait`)
	if took >= WaitDelay {
		t.Fatalf("took %v: SIGTERM not delivered", took)
	}
	if err == nil {
		t.Fatal("expected an error from a cancelled command")
	}
}

func TestCommand_KilledAfterWaitDelay(t *testing.T) {
	took, err := runCancelled(t, `trap '' TERM; sleep 30 & wait`)
	if took < WaitDelay || took > WaitDelay+3*time.Second {
		t.Fatalf("took %v, want about %v", took, WaitDelay)
	}
	if err == nil {
		t.Fatal("expected an error from a killed command")
	}
}
