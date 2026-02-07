# Prompt: Build dbstash

You are building **dbstash** — a Go project that backs up databases by streaming dump output directly into rclone remotes via Docker containers. Build the entire project in one pass following the reference document and instructions below.

## Reference Document

<reference_document>
{{PROJECT_SUMMARY.md}}
</reference_document>

## Scope

Implement **all milestones through v0.6** in a single pass. v0.7 features (email notifications, restore command) should be left as TODOs / placeholder interfaces only. The project must be fully buildable and functional after generation.

## Implementation Instructions

### 1. Project Setup

- Initialize a Go module: `github.com/viperadnan/dbstash`
- Use Go 1.22+ with the project structure defined in the reference document exactly
- Dependencies (use minimal, well-maintained libraries):
  - `github.com/robfig/cron/v3` — cron scheduler
  - `github.com/rs/zerolog` — structured JSON/text logging
  - `github.com/google/uuid` — short UUID generation for name templates
  - No frameworks. Standard library for HTTP (health endpoint), `os/exec` for process piping, `encoding/json` for webhook payloads.

### 2. `internal/config/config.go`

- Parse all environment variables from the reference document into a strongly-typed `Config` struct with validation
- Implement `_FILE` variant resolution: for each `_FILE` env var (`DB_URI_FILE`, `DB_PASSWORD_FILE`, `RCLONE_CONFIG_FILE`), read file contents at startup, trim whitespace, and use as the value. `_FILE` variant takes precedence over the non-file counterpart
- If `RCLONE_CONFIG` (base64) is set, decode it and write to a temp file, set `RCLONE_CONFIG_FILE` path accordingly
- Validate: either `DB_URI`/`DB_URI_FILE` OR `DB_HOST`+`DB_NAME` must be set. `RCLONE_REMOTE` is always required
- Parse `BACKUP_TIMEOUT` as a `time.Duration` (e.g. `1h`, `30m`, `0` for no limit)
- Parse `BACKUP_SCHEDULE` — accept the special value `once` (case-insensitive) or validate as a cron expression
- Default values as specified in the reference document
- Engine detection: the engine type (pg, mongo, mysql, mariadb, redis) is determined at **build time** via a build tag or a `ENGINE` env var baked into each Docker image. Do NOT auto-detect from URI — each image is purpose-built for one engine.
- Store `DUMP_EXTRA_ARGS` as raw string — conflict detection happens in `main.go` after engine initialization, not in config parsing

### 3. `internal/logger/logger.go`

- Thin wrapper around `zerolog`
- Configure from `LOG_LEVEL` and `LOG_FORMAT` env vars
- `LOG_FORMAT=json` → zerolog default JSON output to stdout
- `LOG_FORMAT=text` → zerolog `ConsoleWriter` to stdout
- Provide a package-level `Log` variable and helper to create sub-loggers with contextual fields (`engine`, `database`, `backup_id`)

### 4. `internal/engine/engine.go` + implementations

Define the `Engine` interface:

```go
type Engine interface {
    // Name returns the engine key (e.g. "pg", "mongo")
    Name() string

    // DumpCommand returns the exec.Cmd for the dump tool.
    // The command must write to stdout for stream mode.
    // For directory mode, it writes to the provided outputDir.
    DumpCommand(cfg *config.Config, mode string, outputDir string) (*exec.Cmd, error)

    // DefaultExtension returns the file extension based on compression setting.
    DefaultExtension(compressed bool) string

    // SupportsCompression returns whether BACKUP_COMPRESS is meaningful for this engine.
    SupportsCompression() bool

    // ConflictingFlags returns flag prefixes that are incompatible with the given mode.
    // Used at startup to warn about DUMP_EXTRA_ARGS conflicts.
    ConflictingFlags(mode string) []string
}
```

Implement for each engine:

> **Important — `DUMP_EXTRA_ARGS` Conflict Detection:** Each engine must define a `ConflictingFlags(mode string) []string` method that returns flag prefixes incompatible with the given mode. At startup (during config validation), if `BACKUP_MODE=stream`, scan `DUMP_EXTRA_ARGS` against the engine's conflicting flags list. If a match is found, **log a warning** with the specific flag and the expected failure behaviour, but **do not block execution**. Let the dump tool fail naturally — the error will surface through logging and notifications.
>
> Conflicting flags for stream mode:
> - PostgreSQL: `--Fd`, `--format=directory`, `-Fd`, `--file=`, `-f`
> - MongoDB: `--out=`, `-o`
> - MySQL: `--tab=`, `--tab `
> - Redis: none

**`postgres.go`**
- Stream mode: `pg_dump --format=plain` (default) or `pg_dump --Fc` (when `BACKUP_COMPRESS=true`) writing to stdout
- Directory mode: `pg_dump --Fd --file=<outputDir>`
- Connection: use `DB_URI` directly with `pg_dump <uri>`, or build from individual vars using `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` env vars (pg_dump reads these natively)
- Append `DUMP_EXTRA_ARGS` (shell-split) to the command
- Extensions: `.sql` (uncompressed), `.dump` (compressed/custom format)

**`mongo.go`**
- Stream mode: `mongodump --archive --uri=<uri>` to stdout. Add `--gzip` when `BACKUP_COMPRESS=true`
- Directory mode: `mongodump --out=<outputDir>` (native multi-file). Add `--gzip` when compressed
- Connection: use `DB_URI` with `--uri=`, or build `--host`, `--port`, `--db`, `--username`, `--password`, `--authenticationDatabase` flags
- Extensions: `.archive` (uncompressed), `.archive.gz` (compressed), directory mode has no single extension

**`mysql.go`** (covers both MySQL and MariaDB — same binary interface)
- Stream mode: `mysqldump --host=<host> --port=<port> --user=<user> -p<password> <dbname>` to stdout
- Directory mode: `mysqldump --tab=<outputDir>` (per-table `.sql` + `.txt` files)
- Connection: use individual vars (mysqldump does not accept a URI). If `DB_URI` is provided, parse it into components
- `BACKUP_COMPRESS=true` → log a warning that mysqldump has no native compression, no-op
- Extensions: `.sql`

**`redis.go`**
- Stream mode only: `redis-cli -h <host> -p <port> -a <password> --rdb -` to stdout
- Directory/tar modes: return error — not supported for Redis
- Connection: parse `DB_URI` (redis://...) into host/port/password, or use individual vars
- Extensions: `.rdb`

### 5. `internal/pipeline/pipeline.go` + implementations

Define the `Pipeline` interface:

```go
type Pipeline interface {
    Execute(ctx context.Context, engine engine.Engine, cfg *config.Config) (remotePath string, fileSize int64, err error)
}
```

**`stream.go`** — Stream pipeline:
- Build dump command via `engine.DumpCommand(cfg, "stream", "")`
- Build rclone command: `rclone rcat <RCLONE_REMOTE>/<resolved_filename> --config <rclone_config_path>` + `RCLONE_EXTRA_ARGS`
- Pipe: `dumpCmd.Stdout` → `rcloneCmd.Stdin` using `io.Pipe()` or direct `os.Pipe()`
- Start both commands, wait for both, propagate errors from either side
- Return the remote path and attempt to get file size via `rclone size` after upload

**`directory.go`** — Directory pipeline:
- Create temp dir under `BACKUP_TEMP_DIR`
- Run dump command with `engine.DumpCommand(cfg, "directory", tempDir)`
- Run `rclone copy <tempDir> <RCLONE_REMOTE>/<resolved_dirname>/ --config ...` + `RCLONE_EXTRA_ARGS`
- Cleanup temp dir (deferred)
- Return remote directory path

**`tar.go`** — Tar pipeline:
- Create temp dir, run dump to it
- Pipe: `tar cf - -C <tempDir> .` stdout → `rclone rcat <RCLONE_REMOTE>/<resolved_filename>.tar`
- Cleanup temp dir
- Return remote path

All pipelines must:
- Resolve the filename/dirname from `BACKUP_NAME_TEMPLATE` using the token system from the reference doc
- Respect `ctx` for timeout cancellation
- Log progress at debug level

### 6. `internal/retention/retention.go`

- After each successful backup, run retention cleanup
- Use `rclone lsjson <RCLONE_REMOTE> --config ...` to list existing backups
- For `RETENTION_MAX_FILES`: sort by modification time, delete oldest entries beyond the limit using `rclone deletefile` (stream/tar) or `rclone purge` (directory)
- For `RETENTION_MAX_DAYS`: delete entries older than N days
- Both constraints can be active simultaneously — apply both, union of deletions
- Skip retention entirely if both values are `0`
- Log each deletion at info level

### 7. `internal/hooks/hooks.go`

- `RunPreBackup(ctx context.Context, command string) error` — execute via `sh -c <command>`, inherit container env. Return error if exit code != 0
- `RunPostBackup(ctx context.Context, command string, status string, remotePath string) error` — same, but inject `DBSTASH_STATUS` and `DBSTASH_FILE` into the command's environment
- If hook command is empty string, no-op (return nil)
- Hooks must respect context timeout

### 8. `internal/notify/notify.go`

- Detect platform from URL: contains `discord.com/api/webhooks` → Discord, contains `hooks.slack.com` → Slack
- Build platform-appropriate JSON payload:
  - **Slack**: `{"text": "...", "attachments": [{"color": "#...", "fields": [...]}]}`
  - **Discord**: `{"embeds": [{"title": "...", "color": ..., "fields": [...]}]}`
- Payload fields: status (✅/❌), engine, database, remote path, file size (human-readable), duration, error message (on failure), timestamp
- Send via HTTP POST with `Content-Type: application/json`
- Respect `NOTIFY_ON` setting: `always` → send on every backup, `failure` → only on failure, `success` → only on success
- Log notification result, but never fail the backup due to notification failure

### 9. `internal/scheduler/scheduler.go`

- Use `robfig/cron` to schedule the backup job
- If `BACKUP_SCHEDULE=once`, the scheduler is not used — `main.go` calls the backup job directly and exits. The scheduler package should expose the single-run backup orchestration as a standalone function (e.g. `RunOnce(ctx, ...)`) reusable by both cron and once mode.
- Implement lock guard: use a `sync.Mutex` (or `atomic` flag). If the previous run is still in progress when cron fires, skip and log a warning. Controlled by `BACKUP_LOCK` env var (default `true`)
- The backup job orchestration (single run):
  1. Generate a unique `backup_id` (short UUID)
  2. Create context with timeout from `BACKUP_TIMEOUT` (if set)
  3. Run `HOOK_PRE_BACKUP` → if fails, skip to notification
  4. Select pipeline based on `BACKUP_MODE`
  5. Execute pipeline
  6. Run retention cleanup
  7. Run `HOOK_POST_BACKUP` (with status and remote path)
  8. Send notification (based on `NOTIFY_ON` and result)
  9. Log summary: backup_id, status, duration, file size, remote path

### 10. `internal/health/health.go`

- HTTP server on `:8080`
- `GET /healthz` → 200 OK with JSON: `{"status": "healthy", "engine": "pg", "last_backup": "...", "last_status": "success|failure|pending"}`
- Track last backup time and status via a thread-safe struct updated by the scheduler

### 11. `cmd/dbstash/main.go`

- Load config
- Initialize logger
- Initialize the appropriate engine based on build tag / `ENGINE` env
- Run `DUMP_EXTRA_ARGS` conflict detection against the engine's `ConflictingFlags()` — log warnings, don't exit
- If `DRY_RUN=true`: log resolved config (masking passwords), resolved dump command, rclone target path, then exit 0
- If `BACKUP_SCHEDULE=once`: run a single backup (full orchestration: hooks → pipeline → retention → notification), then exit with code `0` on success or `1` on failure. No cron scheduler, no health endpoint.
- If `BACKUP_ON_START=true`: trigger one immediate backup run before starting the scheduler
- Start health server in a goroutine
- Start cron scheduler
- Block on `SIGINT`/`SIGTERM` for graceful shutdown: stop cron, wait for in-progress backup to finish (up to 30s), then exit

### 12. Dockerfiles (`docker/`)

Each Dockerfile follows the same multi-stage pattern:

```dockerfile
# Stage 1: Build Go binary
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dbstash ./cmd/dbstash

# Stage 2: Runtime image with engine tools + rclone
FROM <engine-specific-base>
RUN <install rclone + any missing deps>
COPY --from=builder /dbstash /usr/local/bin/dbstash
ENV ENGINE=<engine_key>
ENTRYPOINT ["dbstash"]
```

Create these Dockerfiles:

- **`Dockerfile.pg`**: Base `postgres:<version>-alpine` (provides `pg_dump`). Install `rclone` via apk or curl.
- **`Dockerfile.mongo`**: Base `alpine`. Install `mongodump` from `mongodb-database-tools` and `rclone`.
- **`Dockerfile.mysql`**: Base `mysql:<version>` or `alpine` + `mysql-client`. Install `rclone`.
- **`Dockerfile.redis`**: Base `redis:<version>-alpine` (provides `redis-cli`). Install `rclone`.

Use build args for version parameterization:
```dockerfile
ARG DB_VERSION=16
FROM postgres:${DB_VERSION}-alpine
```

### 13. GitHub Actions (`.github/workflows/build.yml`)

- Trigger on push to `main` and tags `v*`
- Matrix strategy as shown in the reference document
- Steps: checkout, setup Go, build binary, login to GHCR, build + push Docker image with tags `ghcr.io/viperadnan/dbstash:<engine>-<version>`
- On tag push: also tag as `<engine>-latest`

### 14. `docker-compose.example.yml`

Copy the Docker Compose example from the reference document exactly.

### 15. `README.md`

Generate a clean README covering:
- Project description (one paragraph)
- Quick start (minimal docker run example)
- Docker Compose example
- Full environment variables reference (link to or embed the tables)
- Backup modes explanation
- Encryption at rest guide
- Docker secrets guide
- Available images table
- License (MIT)

## Code Quality Requirements

- All exported types and functions must have godoc comments
- Use `context.Context` consistently for cancellation and timeouts
- Error wrapping with `fmt.Errorf("...: %w", err)` throughout
- No `panic` outside of truly unrecoverable situations
- Use `zerolog` for all logging — never `fmt.Println` or `log.*`
- Test files: at minimum, unit tests for `config` (parsing, validation, _FILE resolution), `retention` (sorting/filtering logic), and `notify` (payload generation). Use table-driven tests.
- `go vet` and `golangci-lint` clean

## What NOT to Implement

- Email notifications (v0.7 — leave a `TODO` comment and an `EmailNotifier` interface stub)
- Restore command (v0.7 — leave a `TODO` comment in main.go)
- SQLite engine (future — don't create the file)
- Kubernetes/Helm manifests (out of scope)
