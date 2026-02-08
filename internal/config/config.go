// Package config handles configuration parsing, validation, and
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
	DBURI        string
	DBHost       string
	DBPort       string
	DBName       string
	DBUser       string
	DBPassword   string
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

// Prepare validates all fields and sets derived values (ScheduleOnce,
// BackupTimeout). It returns an error if any required field is missing
// or any value is invalid. Both Load() and CLI mode call this after
// populating the Config struct.
func (c *Config) Prepare() error {
	// Engine
	c.Engine = strings.ToLower(c.Engine)
	if c.Engine == "" {
		return fmt.Errorf("ENGINE is required")
	}
	validEngines := map[string]bool{"pg": true, "mongo": true, "mysql": true, "mariadb": true, "redis": true}
	if !validEngines[c.Engine] {
		return fmt.Errorf("unsupported ENGINE: %q (valid: pg, mongo, mysql, mariadb, redis)", c.Engine)
	}

	// Connection
	if c.DBURI == "" && (c.DBHost == "" || c.DBName == "") {
		return fmt.Errorf("either DB_URI (or DB_URI_FILE) or DB_HOST + DB_NAME must be set")
	}

	// Rclone remote
	if c.RcloneRemote == "" {
		return fmt.Errorf("RCLONE_REMOTE is required")
	}

	// Schedule
	if strings.EqualFold(c.BackupSchedule, "once") {
		c.ScheduleOnce = true
		c.BackupSchedule = "once"
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(c.BackupSchedule); err != nil {
			return fmt.Errorf("invalid BACKUP_SCHEDULE %q: %w", c.BackupSchedule, err)
		}
	}

	// Backup mode
	c.BackupMode = strings.ToLower(c.BackupMode)
	validModes := map[string]bool{"stream": true, "directory": true, "tar": true}
	if !validModes[c.BackupMode] {
		return fmt.Errorf("invalid BACKUP_MODE %q (valid: stream, directory, tar)", c.BackupMode)
	}

	// Notifications
	c.NotifyOn = strings.ToLower(c.NotifyOn)
	validNotifyOn := map[string]bool{"always": true, "failure": true, "success": true}
	if !validNotifyOn[c.NotifyOn] {
		return fmt.Errorf("invalid NOTIFY_ON %q (valid: always, failure, success)", c.NotifyOn)
	}

	return nil
}

// Load reads environment variables, resolves _FILE variants, and returns
// a validated Config. It returns an error if required variables are missing
// or values are invalid.
func Load() (*Config, error) {
	cfg := &Config{}

	// Connection — resolve _FILE variants first
	cfg.Engine = envOrDefault("ENGINE", "")
	cfg.DBURI = resolveFileVar("DB_URI", "DB_URI_FILE")
	cfg.DBHost = envOrDefault("DB_HOST", "")
	cfg.DBPort = envOrDefault("DB_PORT", "")
	cfg.DBName = envOrDefault("DB_NAME", "")
	cfg.DBUser = envOrDefault("DB_USER", "")
	cfg.DBPassword = resolveFileVar("DB_PASSWORD", "DB_PASSWORD_FILE")
	cfg.DBAuthSource = envOrDefault("DB_AUTH_SOURCE", "admin")

	// Rclone
	cfg.RcloneRemote = envOrDefault("RCLONE_REMOTE", "")
	rcloneConfigFile, err := ResolveRcloneConfig(
		os.Getenv("RCLONE_CONFIG_FILE"),
		os.Getenv("RCLONE_CONFIG"),
	)
	if err != nil {
		return nil, err
	}
	cfg.RcloneConfigFile = rcloneConfigFile
	cfg.RcloneExtraArgs = envOrDefault("RCLONE_EXTRA_ARGS", "")

	// Schedule & Naming
	cfg.BackupSchedule = envOrDefault("BACKUP_SCHEDULE", "0 2 * * *")
	cfg.BackupMode = envOrDefault("BACKUP_MODE", "stream")
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
	cfg.NotifyOn = envOrDefault("NOTIFY_ON", "failure")

	// Logging
	cfg.LogLevel = envOrDefault("LOG_LEVEL", "info")
	cfg.LogFormat = envOrDefault("LOG_FORMAT", "text")

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

	// Validate
	if err := cfg.Prepare(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ResolveRcloneConfig resolves the rclone config from a file path or
// base64-encoded content. It validates that the resolved config file exists.
func ResolveRcloneConfig(configFilePath, base64Config string) (string, error) {
	// Check base64-encoded config first
	if base64Config != "" {
		decoded, err := base64.StdEncoding.DecodeString(base64Config)
		if err == nil {
			tmpFile, err := os.CreateTemp("", "rclone-*.conf")
			if err == nil {
				tmpFile.Write(decoded)
				tmpFile.Close()
				return tmpFile.Name(), nil
			}
		}
	}

	if configFilePath == "" {
		configFilePath = "/config/rclone.conf"
	}

	// Validate that the config file exists
	if _, err := os.Stat(configFilePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("rclone config file not found at %q. Please set RCLONE_CONFIG_FILE to a valid path", configFilePath)
		}
		return "", fmt.Errorf("cannot access rclone config file at %q: %w", configFilePath, err)
	}

	return configFilePath, nil
}

// ResolveFileValue reads a secret from a file path, trimming whitespace.
// Returns empty string if the file cannot be read.
func ResolveFileValue(filePath string) string {
	if filePath == "" {
		return ""
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DBNameOrDefault returns the database name. If DB_NAME is not set,
// it attempts to extract the database name from DB_URI.
func (c *Config) DBNameOrDefault() string {
	if c.DBName != "" {
		return c.DBName
	}
	if c.DBURI != "" {
		return dbNameFromURI(c.DBURI)
	}
	return "unknown"
}

// dbNameFromURI extracts the database name from a connection URI path.
func dbNameFromURI(uri string) string {
	// Handle schemes like mongodb+srv:// , postgresql://, etc.
	idx := strings.Index(uri, "://")
	if idx < 0 {
		return "unknown"
	}
	rest := uri[idx+3:]
	// Skip user:pass@host portion
	if at := strings.Index(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	// Find path after host(:port)
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "unknown"
	}
	dbName := rest[slash+1:]
	// Strip query parameters
	if q := strings.Index(dbName, "?"); q >= 0 {
		dbName = dbName[:q]
	}
	if dbName == "" {
		return "unknown"
	}
	return dbName
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
