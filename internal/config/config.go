// Package config handles environment variable parsing, validation, and
// Docker secrets (_FILE variant) resolution for dbstash.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Config holds all parsed and validated configuration for a dbstash run.
type Config struct {
	// Engine is the database engine key (pg, mongo, mysql, mariadb, redis).
	Engine string

	// Connection
	DBURI      string
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBAuthSource string

	// Rclone
	RcloneRemote     string
	RcloneConfigFile string
	RcloneExtraArgs  string

	// Schedule & Naming
	BackupSchedule     string
	BackupMode         string
	BackupNameTemplate string
	BackupCompress     bool
	BackupExtension    string
	BackupOnStart      bool
	DumpExtraArgs      string
	Timezone           string

	// Retention
	RetentionMaxFiles int
	RetentionMaxDays  int

	// Notifications
	NotifyWebhookURL string
	NotifyOn         string

	// Logging
	LogLevel  string
	LogFormat string

	// Hooks
	HookPreBackup  string
	HookPostBackup string

	// Safety
	BackupTimeout time.Duration
	BackupLock    bool
	DryRun        bool

	// Backup temp directory for directory/tar modes
	BackupTempDir string

	// ScheduleOnce indicates BACKUP_SCHEDULE=once
	ScheduleOnce bool
}

// Load reads environment variables, resolves _FILE variants, and returns
// a validated Config. It returns an error if required variables are missing
// or values are invalid.
func Load() (*Config, error) {
	cfg := &Config{}

	// Engine detection
	cfg.Engine = strings.ToLower(envOrDefault("ENGINE", ""))
	if cfg.Engine == "" {
		return nil, fmt.Errorf("ENGINE env var is required (set by Docker image)")
	}
	validEngines := map[string]bool{"pg": true, "mongo": true, "mysql": true, "mariadb": true, "redis": true}
	if !validEngines[cfg.Engine] {
		return nil, fmt.Errorf("unsupported ENGINE: %q (valid: pg, mongo, mysql, mariadb, redis)", cfg.Engine)
	}

	// Connection — resolve _FILE variants first
	cfg.DBURI = resolveFileVar("DB_URI", "DB_URI_FILE")
	cfg.DBHost = envOrDefault("DB_HOST", "")
	cfg.DBPort = envOrDefault("DB_PORT", "")
	cfg.DBName = envOrDefault("DB_NAME", "")
	cfg.DBUser = envOrDefault("DB_USER", "")
	cfg.DBPassword = resolveFileVar("DB_PASSWORD", "DB_PASSWORD_FILE")
	cfg.DBAuthSource = envOrDefault("DB_AUTH_SOURCE", "admin")

	// Validate connection: either DB_URI or DB_HOST+DB_NAME
	if cfg.DBURI == "" && (cfg.DBHost == "" || cfg.DBName == "") {
		return nil, fmt.Errorf("either DB_URI (or DB_URI_FILE) or DB_HOST + DB_NAME must be set")
	}

	// Rclone
	cfg.RcloneRemote = envOrDefault("RCLONE_REMOTE", "")
	if cfg.RcloneRemote == "" {
		return nil, fmt.Errorf("RCLONE_REMOTE is required")
	}

	cfg.RcloneConfigFile = resolveRcloneConfig()
	cfg.RcloneExtraArgs = envOrDefault("RCLONE_EXTRA_ARGS", "")

	// Schedule
	cfg.BackupSchedule = envOrDefault("BACKUP_SCHEDULE", "0 2 * * *")
	if strings.EqualFold(cfg.BackupSchedule, "once") {
		cfg.ScheduleOnce = true
		cfg.BackupSchedule = "once"
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(cfg.BackupSchedule); err != nil {
			return nil, fmt.Errorf("invalid BACKUP_SCHEDULE %q: %w", cfg.BackupSchedule, err)
		}
	}

	// Backup mode
	cfg.BackupMode = strings.ToLower(envOrDefault("BACKUP_MODE", "stream"))
	validModes := map[string]bool{"stream": true, "directory": true, "tar": true}
	if !validModes[cfg.BackupMode] {
		return nil, fmt.Errorf("invalid BACKUP_MODE %q (valid: stream, directory, tar)", cfg.BackupMode)
	}

	cfg.BackupNameTemplate = envOrDefault("BACKUP_NAME_TEMPLATE", "{db}-{date}-{time}")
	cfg.BackupCompress = strings.EqualFold(envOrDefault("BACKUP_COMPRESS", "false"), "true")
	cfg.BackupExtension = envOrDefault("BACKUP_EXTENSION", "")
	cfg.BackupOnStart = strings.EqualFold(envOrDefault("BACKUP_ON_START", "false"), "true")
	cfg.DumpExtraArgs = envOrDefault("DUMP_EXTRA_ARGS", "")
	cfg.Timezone = envOrDefault("TZ", "UTC")
	cfg.BackupTempDir = envOrDefault("BACKUP_TEMP_DIR", "/tmp/dbstash-work")

	// Retention
	cfg.RetentionMaxFiles = envOrDefaultInt("RETENTION_MAX_FILES", 0)
	cfg.RetentionMaxDays = envOrDefaultInt("RETENTION_MAX_DAYS", 0)

	// Notifications
	cfg.NotifyWebhookURL = envOrDefault("NOTIFY_WEBHOOK_URL", "")
	cfg.NotifyOn = strings.ToLower(envOrDefault("NOTIFY_ON", "failure"))
	validNotifyOn := map[string]bool{"always": true, "failure": true, "success": true}
	if !validNotifyOn[cfg.NotifyOn] {
		return nil, fmt.Errorf("invalid NOTIFY_ON %q (valid: always, failure, success)", cfg.NotifyOn)
	}

	// Logging
	cfg.LogLevel = envOrDefault("LOG_LEVEL", "info")
	cfg.LogFormat = envOrDefault("LOG_FORMAT", "json")

	// Hooks
	cfg.HookPreBackup = envOrDefault("HOOK_PRE_BACKUP", "")
	cfg.HookPostBackup = envOrDefault("HOOK_POST_BACKUP", "")

	// Safety
	timeoutStr := envOrDefault("BACKUP_TIMEOUT", "0")
	if timeoutStr != "0" && timeoutStr != "" {
		d, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid BACKUP_TIMEOUT %q: %w", timeoutStr, err)
		}
		cfg.BackupTimeout = d
	}

	cfg.BackupLock = !strings.EqualFold(envOrDefault("BACKUP_LOCK", "true"), "false")
	cfg.DryRun = strings.EqualFold(envOrDefault("DRY_RUN", "false"), "true")

	return cfg, nil
}

// DBNameOrDefault returns the database name, or a fallback for display purposes.
func (c *Config) DBNameOrDefault() string {
	if c.DBName != "" {
		return c.DBName
	}
	if c.DBURI != "" {
		return "from-uri"
	}
	return "unknown"
}

// resolveFileVar checks for a _FILE variant first. If the file exists, its
// contents (trimmed) are returned. Otherwise falls back to the base env var.
func resolveFileVar(baseVar, fileVar string) string {
	filePath := os.Getenv(fileVar)
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return os.Getenv(baseVar)
}

// resolveRcloneConfig handles RCLONE_CONFIG (base64), RCLONE_CONFIG_FILE,
// and the default path.
func resolveRcloneConfig() string {
	// Check _FILE variant first
	filePath := resolveFileVar("RCLONE_CONFIG_FILE", "")
	if filePath == "" {
		filePath = os.Getenv("RCLONE_CONFIG_FILE")
	}

	// Check base64-encoded config
	b64 := os.Getenv("RCLONE_CONFIG")
	if b64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err == nil {
			tmpFile, err := os.CreateTemp("", "rclone-*.conf")
			if err == nil {
				tmpFile.Write(decoded)
				tmpFile.Close()
				return tmpFile.Name()
			}
		}
	}

	if filePath != "" {
		return filePath
	}
	return "/config/rclone.conf"
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	fmt.Sscanf(v, "%d", &n)
	return n
}
