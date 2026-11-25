package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testRcloneConf = "[s3]\ntype = s3\n"

var configEnvKeys = []string{
	"ENGINE", "DB_URI", "DB_URI_FILE", "DB_HOST", "DB_PORT", "DB_NAME",
	"DB_USER", "DB_PASSWORD", "DB_PASSWORD_FILE", "DB_AUTH_SOURCE",
	"RCLONE_REMOTE", "RCLONE_CONFIG", "RCLONE_CONFIG_FILE", "RCLONE_EXTRA_ARGS",
	"BACKUP_SCHEDULE", "BACKUP_MODE", "BACKUP_NAME_TEMPLATE", "BACKUP_COMPRESS",
	"BACKUP_EXTENSION", "BACKUP_ON_START", "BACKUP_ALL_DATABASES", "DUMP_EXTRA_ARGS", "TZ",
	"BACKUP_TEMP_DIR", "RETENTION_MAX_FILES", "RETENTION_MAX_DAYS",
	"NOTIFY_WEBHOOK_URL", "NOTIFY_ON", "LOG_LEVEL", "LOG_FORMAT",
	"HOOK_PRE_BACKUP", "HOOK_POST_BACKUP", "BACKUP_TIMEOUT", "BACKUP_LOCK", "DRY_RUN",
}

// setEnv clears every config variable, applies a valid baseline (pg over
// host+name, an rclone config file), then the overrides. A "" override unsets.
func setEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "")
	}
	rcloneFile := filepath.Join(t.TempDir(), "rclone.conf")
	os.WriteFile(rcloneFile, []byte(testRcloneConf), 0o600)
	base := map[string]string{
		"ENGINE": "pg", "DB_HOST": "localhost", "DB_NAME": "testdb",
		"RCLONE_REMOTE": "s3:bucket/backups", "RCLONE_CONFIG_FILE": rcloneFile,
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte(content), 0o600)
	return p
}

func TestLoad_Defaults(t *testing.T) {
	setEnv(t, nil)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cfg.Close()
	for name, got := range map[string][2]string{
		"Engine":             {cfg.Engine, "pg"},
		"BackupSchedule":     {cfg.BackupSchedule, "0 2 * * *"},
		"BackupMode":         {cfg.BackupMode, "stream"},
		"BackupNameTemplate": {cfg.BackupNameTemplate, "{db}-{timestamp}"},
		"NotifyOn":           {cfg.NotifyOn, "failure"},
		"LogLevel":           {cfg.LogLevel, "info"},
		"LogFormat":          {cfg.LogFormat, "text"},
		"Timezone":           {cfg.Timezone, "UTC"},
		"BackupTempDir":      {cfg.BackupTempDir, "/tmp/dbstash-work"},
		"DBAuthSource":       {cfg.DBAuthSource, "admin"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %q, want %q", name, got[0], got[1])
		}
	}
	if !cfg.BackupLock || cfg.ScheduleOnce || cfg.BackupTimeout != 0 {
		t.Errorf("BackupLock=%v ScheduleOnce=%v BackupTimeout=%v", cfg.BackupLock, cfg.ScheduleOnce, cfg.BackupTimeout)
	}
}

func TestLoad_Valid(t *testing.T) {
	tests := []struct {
		name  string
		env   func(t *testing.T) map[string]string
		check func(t *testing.T, c *Config)
	}{
		{"DB_URI", func(*testing.T) map[string]string {
			return map[string]string{"ENGINE": "mongo", "DB_HOST": "", "DB_NAME": "", "DB_URI": "mongodb://u:p@h:27017/app"}
		}, func(t *testing.T, c *Config) {
			if c.DBURI != "mongodb://u:p@h:27017/app" {
				t.Errorf("DBURI = %q", c.DBURI)
			}
		}},
		{"DB_URI_FILE is read and trimmed", func(t *testing.T) map[string]string {
			return map[string]string{"DB_HOST": "", "DB_NAME": "", "DB_URI_FILE": writeFile(t, "postgresql://h/prod\n")}
		}, func(t *testing.T, c *Config) {
			if c.DBURI != "postgresql://h/prod" {
				t.Errorf("DBURI = %q", c.DBURI)
			}
		}},
		{"DB_URI_FILE wins over DB_URI", func(t *testing.T) map[string]string {
			return map[string]string{"DB_URI": "from-env", "DB_URI_FILE": writeFile(t, "from-file")}
		}, func(t *testing.T, c *Config) {
			if c.DBURI != "from-file" {
				t.Errorf("DBURI = %q", c.DBURI)
			}
		}},
		{"DB_PASSWORD_FILE is trimmed", func(t *testing.T) map[string]string {
			return map[string]string{"DB_PASSWORD_FILE": writeFile(t, "  s3cret  \n")}
		}, func(t *testing.T, c *Config) {
			if c.DBPassword != "s3cret" {
				t.Errorf("DBPassword = %q", c.DBPassword)
			}
		}},
		{"schedule once is case-insensitive", func(*testing.T) map[string]string {
			return map[string]string{"BACKUP_SCHEDULE": "Once"}
		}, func(t *testing.T, c *Config) {
			if !c.ScheduleOnce || c.BackupSchedule != "once" {
				t.Errorf("ScheduleOnce=%v BackupSchedule=%q", c.ScheduleOnce, c.BackupSchedule)
			}
		}},
		{"timeout", func(*testing.T) map[string]string {
			return map[string]string{"BACKUP_TIMEOUT": "30m"}
		}, func(t *testing.T, c *Config) {
			if c.BackupTimeout != 30*time.Minute {
				t.Errorf("BackupTimeout = %v", c.BackupTimeout)
			}
		}},
		{"all databases over host", func(*testing.T) map[string]string {
			return map[string]string{"DB_NAME": "", "BACKUP_ALL_DATABASES": "true"}
		}, func(t *testing.T, c *Config) {
			if !c.BackupAllDatabases || c.DBNameOrDefault() != "all" {
				t.Errorf("BackupAllDatabases=%v DBNameOrDefault=%q", c.BackupAllDatabases, c.DBNameOrDefault())
			}
		}},
		{"engine and mode are lower-cased", func(*testing.T) map[string]string {
			return map[string]string{"ENGINE": "MariaDB", "BACKUP_MODE": "TAR"}
		}, func(t *testing.T, c *Config) {
			if c.Engine != "mariadb" || c.BackupMode != "tar" {
				t.Errorf("Engine=%q BackupMode=%q", c.Engine, c.BackupMode)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env(t))
			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer cfg.Close()
			tt.check(t, cfg)
		})
	}
}

// Each case must fail for its own reason, not an unrelated one.
func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing engine", map[string]string{"ENGINE": ""}, "ENGINE is required"},
		{"unsupported engine", map[string]string{"ENGINE": "oracle"}, "unsupported ENGINE"},
		{"missing connection", map[string]string{"DB_HOST": "", "DB_NAME": ""}, "either DB_URI"},
		{"missing remote", map[string]string{"RCLONE_REMOTE": ""}, "RCLONE_REMOTE is required"},
		{"invalid schedule", map[string]string{"BACKUP_SCHEDULE": "invalid-cron"}, "invalid BACKUP_SCHEDULE"},
		{"invalid mode", map[string]string{"BACKUP_MODE": "invalid"}, "invalid BACKUP_MODE"},
		{"invalid timeout", map[string]string{"BACKUP_TIMEOUT": "notaduration"}, "invalid BACKUP_TIMEOUT"},
		{"invalid notify_on", map[string]string{"NOTIFY_ON": "never"}, "invalid NOTIFY_ON"},
		{"db name with all databases", map[string]string{"BACKUP_ALL_DATABASES": "true"}, "mutually exclusive"},
		{"missing rclone config file", map[string]string{"RCLONE_CONFIG_FILE": "/nonexistent/rclone.conf"}, "rclone config file not found"},
		{"invalid rclone base64", map[string]string{"RCLONE_CONFIG": "not base64!"}, "RCLONE_CONFIG is not valid base64"},
		{"unknown timezone", map[string]string{"TZ": "Mars/Base"}, "invalid TZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDBNameOrDefault(t *testing.T) {
	tests := []struct {
		cfg  Config
		want string
	}{
		{Config{BackupAllDatabases: true}, "all"},
		{Config{DBName: "mydb"}, "mydb"},
		{Config{DBURI: "postgres://host/db"}, "db"},
		{Config{}, "unknown"},
	}
	for _, tt := range tests {
		if got := tt.cfg.DBNameOrDefault(); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.cfg, got, tt.want)
		}
	}
}

// isolateTemp points os.CreateTemp at a fresh dir and returns a counter of
// rclone temp configs left in it.
func isolateTemp(t *testing.T) func() int {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return func() int {
		m, _ := filepath.Glob(filepath.Join(dir, "rclone-*.conf"))
		return len(m)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestResolveRcloneConfig(t *testing.T) {
	t.Run("base64 is written privately and cleaned up", func(t *testing.T) {
		leftover := isolateTemp(t)
		path, cleanup, err := ResolveRcloneConfig("", b64(testRcloneConf))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != testRcloneConf {
			t.Fatalf("content = %q", got)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
		cleanup()
		cleanup()
		if n := leftover(); n != 0 {
			t.Fatalf("%d temp config(s) left", n)
		}
	})

	t.Run("line-wrapped base64 is accepted", func(t *testing.T) {
		isolateTemp(t)
		enc := b64(testRcloneConf)
		path, cleanup, err := ResolveRcloneConfig("", enc[:8]+"\n"+enc[8:]+"\n")
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if got, _ := os.ReadFile(path); string(got) != testRcloneConf {
			t.Fatalf("content = %q", got)
		}
	})

	t.Run("caller's file is never removed", func(t *testing.T) {
		path := writeFile(t, testRcloneConf)
		got, cleanup, err := ResolveRcloneConfig(path, "")
		if err != nil || got != path {
			t.Fatalf("got %q, %v", got, err)
		}
		cleanup()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("caller's config removed: %v", err)
		}
	})

	t.Run("invalid base64 does not fall back to the default config", func(t *testing.T) {
		leftover := isolateTemp(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		os.MkdirAll(filepath.Join(home, ".config", "rclone"), 0o700)
		os.WriteFile(filepath.Join(home, ".config", "rclone", "rclone.conf"), []byte(testRcloneConf), 0o600)
		if _, _, err := ResolveRcloneConfig("", "not base64!"); err == nil {
			t.Fatal("expected an error")
		}
		if n := leftover(); n != 0 {
			t.Fatalf("%d temp config(s) left", n)
		}
	})
}

func TestLoad_TempRcloneConfigLifetime(t *testing.T) {
	t.Run("removed on Close", func(t *testing.T) {
		setEnv(t, map[string]string{"RCLONE_CONFIG": b64(testRcloneConf)})
		leftover := isolateTemp(t)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if leftover() != 1 {
			t.Fatal("temp config should exist while the Config is open")
		}
		cfg.Close()
		cfg.Close()
		if n := leftover(); n != 0 {
			t.Fatalf("%d temp config(s) left after Close", n)
		}
	})

	for name, env := range map[string][2]string{
		"removed when validation fails": {"DB_HOST", ""},
		"removed when parsing fails":    {"BACKUP_TIMEOUT", "not-a-duration"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, map[string]string{"RCLONE_CONFIG": b64(testRcloneConf), env[0]: env[1]})
			leftover := isolateTemp(t)
			if _, err := Load(); err == nil {
				t.Fatal("expected error")
			}
			if n := leftover(); n != 0 {
				t.Fatalf("%d temp config(s) left after a failed Load", n)
			}
		})
	}

	t.Run("Close is safe on nil and empty configs", func(t *testing.T) {
		var nilCfg *Config
		nilCfg.Close()
		(&Config{}).Close()
	})
}

func TestSplitArgs(t *testing.T) {
	tests := map[string][]string{
		"":                           nil,
		"--a --b=1":                  {"--a", "--b=1"},
		`--x "a b" 'c d'`:            {"--x", "a b", "c d"},
		`--q="it's"  --r='say "hi"'`: {"--q=it's", `--r=say "hi"`},
	}
	for in, want := range tests {
		if got := SplitArgs(in); !slices.Equal(got, want) {
			t.Errorf("SplitArgs(%q) = %q, want %q", in, got, want)
		}
	}
}
