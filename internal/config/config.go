// Package config handles configuration parsing, validation, and
// Docker secrets (_FILE variant) resolution for dbstash.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
	BackupAllDatabases bool
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

	// cleanup removes the temp rclone config written from RCLONE_CONFIG.
	cleanup func()
}

// Close removes the temp rclone config file written from RCLONE_CONFIG, if
// any. It is safe to call more than once and on a nil Config.
func (c *Config) Close() {
	if c != nil && c.cleanup != nil {
		c.cleanup()
		c.cleanup = nil
	}
}

// Location is the time zone for schedules and filenames; UTC when TZ is unset.
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC // Prepare rejects unknown zones
	}
	return loc
}

// SetRcloneConfig records the resolved rclone config path and the cleanup
// that Close will run.
func (c *Config) SetRcloneConfig(path string, cleanup func()) {
	c.RcloneConfigFile = path
	c.cleanup = cleanup
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

	// All-databases vs DB_NAME mutual exclusion
	if c.BackupAllDatabases && c.DBName != "" {
		return fmt.Errorf("DB_NAME and BACKUP_ALL_DATABASES are mutually exclusive")
	}

	// Connection
	if c.DBURI == "" && (c.DBHost == "" || (c.DBName == "" && !c.BackupAllDatabases)) {
		return fmt.Errorf("either DB_URI (or DB_URI_FILE) or DB_HOST + DB_NAME (or BACKUP_ALL_DATABASES=true) must be set")
	}

	// Timezone
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("invalid TZ %q: %w", c.Timezone, err)
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

	// Backup mode: stream, except redis, whose redis-cli can only drop the
	// diskless-sync EOF marker from a real file.
	c.BackupMode = strings.ToLower(c.BackupMode)
	if c.BackupMode == "" {
		c.BackupMode = "stream"
		if c.Engine == "redis" {
			c.BackupMode = "file"
		}
	}
	validModes := map[string]bool{"stream": true, "directory": true, "tar": true, "file": true}
	if !validModes[c.BackupMode] {
		return fmt.Errorf("invalid BACKUP_MODE %q (valid: stream, directory, tar, file)", c.BackupMode)
	}
	if c.Engine == "redis" && c.BackupMode != "file" {
		return fmt.Errorf("redis only supports BACKUP_MODE=file (got %q)", c.BackupMode)
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
// or values are invalid. The caller must Close the returned Config.
func Load() (_ *Config, err error) {
	cfg := &Config{}
	defer func() {
		if err != nil {
			cfg.Close()
		}
	}()

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
	rcloneConfigFile, cleanup, err := ResolveRcloneConfig(
		os.Getenv("RCLONE_CONFIG_FILE"),
		os.Getenv("RCLONE_CONFIG"),
	)
	if err != nil {
		return nil, err
	}
	cfg.SetRcloneConfig(rcloneConfigFile, cleanup)
	cfg.RcloneExtraArgs = envOrDefault("RCLONE_EXTRA_ARGS", "")

	// Schedule & Naming
	cfg.BackupSchedule = envOrDefault("BACKUP_SCHEDULE", "0 2 * * *")
	cfg.BackupMode = os.Getenv("BACKUP_MODE")
	cfg.BackupNameTemplate = envOrDefault("BACKUP_NAME_TEMPLATE", "{db}-{timestamp}")
	cfg.BackupCompress = strings.EqualFold(envOrDefault("BACKUP_COMPRESS", "false"), "true")
	cfg.BackupExtension = envOrDefault("BACKUP_EXTENSION", "")
	cfg.BackupOnStart = strings.EqualFold(envOrDefault("BACKUP_ON_START", "false"), "true")
	cfg.BackupAllDatabases = strings.EqualFold(envOrDefault("BACKUP_ALL_DATABASES", "false"), "true")
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
// The returned cleanup removes the temp file written for base64 content and
// is a no-op for a caller-supplied path; it is never nil when err is nil.
func ResolveRcloneConfig(configFilePath, base64Config string) (string, func(), error) {
	// Check base64-encoded config first
	if base64Config != "" {
		decoded, err := base64.StdEncoding.DecodeString(base64Config)
		if err != nil {
			return "", nil, fmt.Errorf("RCLONE_CONFIG is not valid base64: %w", err)
		}
		return WriteTempFile("rclone-*.conf", decoded)
	}

	if configFilePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/"
		}
		configFilePath = filepath.Join(home, ".config", "rclone", "rclone.conf")
	}

	// Validate that the config file exists
	if _, err := os.Stat(configFilePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("rclone config file not found at %q. Please set RCLONE_CONFIG_FILE to a valid path", configFilePath)
		}
		return "", nil, fmt.Errorf("cannot access rclone config file at %q: %w", configFilePath, err)
	}

	return configFilePath, func() {}, nil
}

// WriteTempFile writes data to a new owner-only (0600) temp file and returns
// its path and a cleanup that removes it; a partial file is removed on failure.
func WriteTempFile(pattern string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, fmt.Errorf("cannot create temp file: %w", err)
	}
	cleanup := func() { os.Remove(f.Name()) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("cannot write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("cannot write temp file: %w", err)
	}
	return f.Name(), cleanup, nil
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
	if c.BackupAllDatabases {
		return "all"
	}
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

// MaskURI masks the password in a connection URI for safe logging.
// e.g. postgresql://user:secret@host:5432/db -> postgresql://user:****@host:5432/db
func MaskURI(uri string) string {
	parts := strings.SplitN(uri, "@", 2)
	if len(parts) != 2 {
		return uri
	}
	prefix := parts[0]
	idx := strings.LastIndex(prefix, ":")
	if idx < 0 {
		return uri
	}
	schemeEnd := strings.Index(prefix, "://")
	if schemeEnd >= 0 && idx <= schemeEnd+2 {
		return uri
	}
	return prefix[:idx+1] + "****@" + parts[1]
}

// MaskCmdArgs masks URIs with credentials in command arguments for safe logging.
func MaskCmdArgs(args []string) string {
	masked := make([]string, len(args))
	for i, arg := range args {
		if strings.Contains(arg, "://") && strings.Contains(arg, "@") {
			masked[i] = MaskURI(arg)
		} else {
			masked[i] = arg
		}
	}
	return strings.Join(masked, " ")
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

// SplitArgs splits DUMP_EXTRA_ARGS/RCLONE_EXTRA_ARGS into arguments, honouring
// single and double quotes.
func SplitArgs(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
