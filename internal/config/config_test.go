package config

import (
	"os"
	"path/filepath"
	"testing"
)

func clearEnv() {
	for _, key := range []string{
		"ENGINE", "DB_URI", "DB_URI_FILE", "DB_HOST", "DB_PORT", "DB_NAME",
		"DB_USER", "DB_PASSWORD", "DB_PASSWORD_FILE", "DB_AUTH_SOURCE",
		"RCLONE_REMOTE", "RCLONE_CONFIG", "RCLONE_CONFIG_FILE", "RCLONE_EXTRA_ARGS",
		"BACKUP_SCHEDULE", "BACKUP_MODE", "BACKUP_NAME_TEMPLATE", "BACKUP_COMPRESS",
		"BACKUP_EXTENSION", "BACKUP_ON_START", "BACKUP_ALL_DATABASES", "DUMP_EXTRA_ARGS", "TZ",
		"BACKUP_TEMP_DIR", "RETENTION_MAX_FILES", "RETENTION_MAX_DAYS",
		"NOTIFY_WEBHOOK_URL", "NOTIFY_ON", "LOG_LEVEL", "LOG_FORMAT",
		"HOOK_PRE_BACKUP", "HOOK_POST_BACKUP", "BACKUP_TIMEOUT", "BACKUP_LOCK",
		"DRY_RUN",
	} {
		os.Unsetenv(key)
	}
}

func setMinimalEnv(t *testing.T) {
	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("RCLONE_REMOTE", "s3:my-bucket/backups")

	// Create temporary rclone config file
	dir := t.TempDir()
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)
}

func TestLoad_MinimalConfig(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Engine != "pg" {
		t.Errorf("expected engine 'pg', got %q", cfg.Engine)
	}
	if cfg.DBHost != "localhost" {
		t.Errorf("expected host 'localhost', got %q", cfg.DBHost)
	}
	if cfg.DBName != "testdb" {
		t.Errorf("expected name 'testdb', got %q", cfg.DBName)
	}
	if cfg.BackupSchedule != "0 2 * * *" {
		t.Errorf("expected default schedule, got %q", cfg.BackupSchedule)
	}
	if cfg.BackupMode != "stream" {
		t.Errorf("expected default mode 'stream', got %q", cfg.BackupMode)
	}
	if cfg.BackupLock != true {
		t.Error("expected BackupLock to be true by default")
	}
	if cfg.NotifyOn != "failure" {
		t.Errorf("expected NotifyOn 'failure', got %q", cfg.NotifyOn)
	}
}

func TestLoad_MissingEngine(t *testing.T) {
	clearEnv()
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing ENGINE")
	}
}

func TestLoad_InvalidEngine(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "oracle")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid ENGINE")
	}
}

func TestLoad_MissingConnection(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "pg")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when neither DB_URI nor DB_HOST+DB_NAME is set")
	}
}

func TestLoad_MissingRcloneRemote(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing RCLONE_REMOTE")
	}
}

func TestLoad_DBURI(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "mongo")
	os.Setenv("DB_URI", "mongodb://user:pass@host:27017/mydb")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	// Create temporary rclone config file
	dir := t.TempDir()
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBURI != "mongodb://user:pass@host:27017/mydb" {
		t.Errorf("unexpected DB_URI: %q", cfg.DBURI)
	}
}

func TestLoad_FileVariant(t *testing.T) {
	clearEnv()

	// Create temp file with URI
	dir := t.TempDir()
	uriFile := filepath.Join(dir, "db_uri.txt")
	os.WriteFile(uriFile, []byte("postgresql://user:secret@dbhost:5432/prod\n"), 0o644)

	// Create temporary rclone config file
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)

	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_URI_FILE", uriFile)
	os.Setenv("RCLONE_REMOTE", "s3:bucket")
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBURI != "postgresql://user:secret@dbhost:5432/prod" {
		t.Errorf("expected URI from file, got %q", cfg.DBURI)
	}
}

func TestLoad_FileVariantPrecedence(t *testing.T) {
	clearEnv()

	dir := t.TempDir()
	uriFile := filepath.Join(dir, "db_uri.txt")
	os.WriteFile(uriFile, []byte("from-file"), 0o644)

	// Create temporary rclone config file
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)

	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_URI", "from-env")
	os.Setenv("DB_URI_FILE", uriFile)
	os.Setenv("RCLONE_REMOTE", "s3:bucket")
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// _FILE variant takes precedence
	if cfg.DBURI != "from-file" {
		t.Errorf("expected _FILE to take precedence, got %q", cfg.DBURI)
	}
}

func TestLoad_PasswordFile(t *testing.T) {
	clearEnv()

	dir := t.TempDir()
	pwFile := filepath.Join(dir, "password.txt")
	os.WriteFile(pwFile, []byte("  s3cret  \n"), 0o644)

	// Create temporary rclone config file
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)

	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("DB_PASSWORD_FILE", pwFile)
	os.Setenv("RCLONE_REMOTE", "s3:bucket")
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBPassword != "s3cret" {
		t.Errorf("expected trimmed password 's3cret', got %q", cfg.DBPassword)
	}
}

func TestLoad_ScheduleOnce(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_SCHEDULE", "once")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ScheduleOnce {
		t.Error("expected ScheduleOnce to be true")
	}
}

func TestLoad_ScheduleOnceCaseInsensitive(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_SCHEDULE", "Once")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ScheduleOnce {
		t.Error("expected ScheduleOnce to be true for 'Once'")
	}
}

func TestLoad_InvalidSchedule(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_SCHEDULE", "invalid-cron")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

func TestLoad_InvalidMode(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_MODE", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid BACKUP_MODE")
	}
}

func TestLoad_Timeout(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_TIMEOUT", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BackupTimeout.Minutes() != 30 {
		t.Errorf("expected 30m timeout, got %v", cfg.BackupTimeout)
	}
}

func TestLoad_InvalidTimeout(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("BACKUP_TIMEOUT", "notaduration")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid BACKUP_TIMEOUT")
	}
}

func TestLoad_InvalidNotifyOn(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)
	os.Setenv("NOTIFY_ON", "never")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid NOTIFY_ON")
	}
}

func TestLoad_AllEngines(t *testing.T) {
	engines := []string{"pg", "mongo", "mysql", "mariadb", "redis"}
	for _, eng := range engines {
		t.Run(eng, func(t *testing.T) {
			clearEnv()
			os.Setenv("ENGINE", eng)
			os.Setenv("DB_HOST", "localhost")
			os.Setenv("DB_NAME", "testdb")
			os.Setenv("RCLONE_REMOTE", "s3:bucket")

			// Create temporary rclone config file
			dir := t.TempDir()
			rcloneConfigFile := filepath.Join(dir, "rclone.conf")
			os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)
			os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("unexpected error for engine %s: %v", eng, err)
			}
			if cfg.Engine != eng {
				t.Errorf("expected engine %q, got %q", eng, cfg.Engine)
			}
		})
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv()
	setMinimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"BackupMode", cfg.BackupMode, "stream"},
		{"BackupNameTemplate", cfg.BackupNameTemplate, "{db}-{date}-{time}"},
		{"NotifyOn", cfg.NotifyOn, "failure"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "text"},
		{"Timezone", cfg.Timezone, "UTC"},
		{"BackupTempDir", cfg.BackupTempDir, "/tmp/dbstash-work"},
		{"DBAuthSource", cfg.DBAuthSource, "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.got)
			}
		})
	}
}

func TestDBNameOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected string
	}{
		{"all databases", Config{BackupAllDatabases: true}, "all"},
		{"with name", Config{DBName: "mydb"}, "mydb"},
		{"with uri", Config{DBURI: "postgres://host/db"}, "db"},
		{"neither", Config{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DBNameOrDefault(); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestLoad_BackupAllDatabases(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("BACKUP_ALL_DATABASES", "true")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	dir := t.TempDir()
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.BackupAllDatabases {
		t.Error("expected BackupAllDatabases to be true")
	}
}

func TestLoad_BackupAllDatabases_MutualExclusion(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "pg")
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("BACKUP_ALL_DATABASES", "true")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both DB_NAME and BACKUP_ALL_DATABASES are set")
	}
}

func TestLoad_BackupAllDatabases_WithURI(t *testing.T) {
	clearEnv()
	os.Setenv("ENGINE", "mongo")
	os.Setenv("DB_URI", "mongodb://host:27017")
	os.Setenv("BACKUP_ALL_DATABASES", "true")
	os.Setenv("RCLONE_REMOTE", "s3:bucket")

	dir := t.TempDir()
	rcloneConfigFile := filepath.Join(dir, "rclone.conf")
	os.WriteFile(rcloneConfigFile, []byte("[s3]\ntype = s3\n"), 0o644)
	os.Setenv("RCLONE_CONFIG_FILE", rcloneConfigFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.BackupAllDatabases {
		t.Error("expected BackupAllDatabases to be true")
	}
	if cfg.DBNameOrDefault() != "all" {
		t.Errorf("expected DBNameOrDefault 'all', got %q", cfg.DBNameOrDefault())
	}
}
