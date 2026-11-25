package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/viperadnan-git/dbstash/internal/config"
	"github.com/viperadnan-git/dbstash/internal/engine"
)

func TestResolveFilename(t *testing.T) {
	mongo, _ := engine.New("mongo")
	pg, _ := engine.New("pg")
	tests := []struct {
		name     string
		template string
		cfg      config.Config
		eng      engine.Engine
		ext      string
		want     string // regexp
	}{
		{"default template", "{db}-{timestamp}", config.Config{DBName: "app", BackupCompress: true}, mongo, "",
			`^app-\d{8}T\d{6}Z\.archive\.gz$`}, // UTC by default, whatever the host zone
		{"all tokens", "{engine}_{db}_{date}_{time}_{ts}_{uuid}", config.Config{BackupAllDatabases: true}, pg, "",
			`^pg_all_\d{4}-\d{2}-\d{2}_\d{6}_\d{10}_[0-9a-f]{8}\.sql$`},
		{"extension override without dot", "{db}", config.Config{DBName: "app"}, pg, "bak", `^app\.bak$`},
		{"timezone", "{timestamp}", config.Config{DBName: "x", Timezone: "Asia/Kolkata"}, pg, ".sql",
			`^\d{8}T\d{6}\+0530\.sql$`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveFilename(tt.template, &tt.cfg, tt.eng, tt.ext)
			if !regexp.MustCompile(tt.want).MatchString(got) {
				t.Errorf("got %q, want match %s", got, tt.want)
			}
		})
	}
}

func TestResolveDirname(t *testing.T) {
	mongo, _ := engine.New("mongo")
	got := resolveDirname("{engine}-{db}", &config.Config{DBName: "app"}, mongo)
	if got != "mongo-app" {
		t.Errorf("got %q, want mongo-app", got)
	}
}

// runWithin fails the test if runPiped does not return within d.
func runWithin(t *testing.T, d time.Duration, src, dst *exec.Cmd) (srcErr, dstErr, startErr error) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		srcErr, dstErr, startErr = runPiped(src, dst)
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(d):
		src.Process.Kill()
		dst.Process.Kill()
		t.Fatalf("runPiped hung for %v", d)
		return
	}
}

func TestRunPiped(t *testing.T) {
	t.Run("data flows", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out")
		srcErr, dstErr, err := runWithin(t, 10*time.Second,
			exec.Command("printf", "hello"), exec.Command("sh", "-c", "cat > "+out))
		if err != nil || srcErr != nil || dstErr != nil {
			t.Fatalf("errors: start=%v src=%v dst=%v", err, srcErr, dstErr)
		}
		if b, _ := os.ReadFile(out); string(b) != "hello" {
			t.Fatalf("dst got %q", b)
		}
	})

	t.Run("dst exits early, src stops instead of hanging", func(t *testing.T) {
		srcErr, dstErr, err := runWithin(t, 10*time.Second,
			exec.Command("yes"), exec.Command("head", "-c", "10"))
		if err != nil || dstErr != nil {
			t.Fatalf("errors: start=%v dst=%v", err, dstErr)
		}
		if srcErr == nil {
			t.Fatal("expected src to fail with a broken pipe")
		}
	})

	t.Run("src fails, dst sees EOF", func(t *testing.T) {
		srcErr, dstErr, err := runWithin(t, 10*time.Second,
			exec.Command("sh", "-c", "exit 3"), exec.Command("cat"))
		if err != nil || dstErr != nil {
			t.Fatalf("errors: start=%v dst=%v", err, dstErr)
		}
		if ee, ok := srcErr.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
			t.Fatalf("src err = %v, want exit 3", srcErr)
		}
	})

	t.Run("missing binary is a start error", func(t *testing.T) {
		_, _, err := runPiped(exec.Command("printf", "x"), exec.Command("dbstash-no-such-binary"))
		if err == nil || !strings.Contains(err.Error(), "starting dbstash-no-such-binary") {
			t.Fatalf("err = %v", err)
		}
	})
}
