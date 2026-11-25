package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
)

// blockingPipeline runs until its context is cancelled.
type blockingPipeline struct {
	started   chan struct{}
	runs      atomic.Int32
	cancelled atomic.Bool
}

func (p *blockingPipeline) Execute(ctx context.Context, _ engine.Engine, _ *config.Config) (string, int64, error) {
	p.runs.Add(1)
	p.started <- struct{}{}
	<-ctx.Done()
	p.cancelled.Store(true)
	return "", 0, ctx.Err()
}

func newTestScheduler(t *testing.T, lock bool) (*Scheduler, *blockingPipeline) {
	t.Helper()
	eng, _ := engine.New("mongo")
	pipe := &blockingPipeline{started: make(chan struct{}, 2)}
	cfg := &config.Config{BackupSchedule: "0 2 * * *", BackupLock: lock, NotifyOn: "failure"}
	s := New(cfg, eng, pipe, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	return s, pipe
}

func TestStop_CancelsInProgressBackup(t *testing.T) {
	s, pipe := newTestScheduler(t, true)
	s.RunNow()
	<-pipe.started

	start := time.Now()
	s.Stop(10 * time.Second)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("Stop took %v; it should cancel the backup, not wait it out", took)
	}
	if !pipe.cancelled.Load() {
		t.Fatal("in-progress backup was not cancelled")
	}
}

func TestRunNow_RespectsLock(t *testing.T) {
	s, pipe := newTestScheduler(t, true)
	s.RunNow()
	<-pipe.started
	s.RunNow() // skipped: previous run still in progress
	time.Sleep(200 * time.Millisecond)
	s.Stop(10 * time.Second)
	if n := pipe.runs.Load(); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}
