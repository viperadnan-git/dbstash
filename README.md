# dbstash

[![Build Status](https://img.shields.io/github/actions/workflow/status/viperadnan-git/dbstash/build.yml?branch=main&logo=github)](https://github.com/viperadnan-git/dbstash/actions)
[![Docker Pulls](https://img.shields.io/docker/pulls/viperadnan/dbstash?logo=docker)](https://hub.docker.com/r/viperadnan/dbstash)
[![Docker Image Size](https://img.shields.io/docker/image-size/viperadnan/dbstash/pg?logo=docker&label=image%20size)](https://hub.docker.com/r/viperadnan/dbstash)
[![GitHub Stars](https://img.shields.io/github/stars/viperadnan-git/dbstash?logo=github)](https://github.com/viperadnan-git/dbstash/stargazers)
[![License](https://img.shields.io/github/license/viperadnan-git/dbstash)](https://github.com/viperadnan-git/dbstash/blob/main/LICENSE)
[![GitHub Release](https://img.shields.io/github/v/release/viperadnan-git/dbstash)](https://github.com/viperadnan-git/dbstash/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/viperadnan-git/dbstash?logo=go)](https://github.com/viperadnan-git/dbstash)

Database backup via rclone. Stream database dumps directly to any cloud storage without intermediate files or local disk pressure. Run as a standalone CLI or as Docker containers.

---

## Features

- **Zero Disk Usage** — Stream dumps directly to cloud storage (stream mode)
- **CLI and Docker** — Run as a standalone binary with flags or as purpose-built Docker images
- **Any Cloud Storage** — Works with S3, GCS, Azure, Dropbox, 40+ backends via rclone
- **Multiplatform** — Built for `linux/amd64` and `linux/arm64`
- **Flexible Scheduling** — Cron expressions or one-time backups
- **Secrets Support** — Docker secrets via `_FILE` environment variables
- **Compression** — Native dump tool compression support
- **Retention Policies** — Automatic cleanup by age or file count
- **Notifications** — Slack/Discord webhooks on success or failure
- **Hooks** — Pre/post-backup shell command execution
- **Encryption at Rest** — Transparent encryption via rclone's crypt remote

## Quick Start

dbstash requires only three things to run:

1. **Database connection** — `DB_URI` or `DB_HOST` + `DB_NAME`
2. **Rclone remote** — `RCLONE_REMOTE` (where to store backups)
3. **Rclone config** — A valid rclone config file

```bash
# One-time PostgreSQL backup to S3
docker run --rm \
  -e DB_URI="postgresql://user:pass@host:5432/mydb" \
  -e RCLONE_REMOTE="s3:my-bucket/backups" \
  -e BACKUP_SCHEDULE=once \
  -v /path/to/rclone.conf:/config/rclone.conf:ro \
  ghcr.io/viperadnan-git/dbstash:pg-17

# Dump all MongoDB databases
docker run --rm \
  -e DB_URI="mongodb+srv://user:pass@cluster.mongodb.net" \
  -e BACKUP_ALL_DATABASES=true \
  -e RCLONE_REMOTE="s3:my-bucket/backups" \
  -e BACKUP_SCHEDULE=once \
  -v /path/to/rclone.conf:/config/rclone.conf:ro \
  ghcr.io/viperadnan-git/dbstash:mongo-8
```

## CLI Usage

dbstash is also available as a standalone binary. Pre-built binaries for Linux, macOS, and Windows can be downloaded from [Releases](https://github.com/viperadnan-git/dbstash/releases). Requires `rclone` and the engine's dump tool (`pg_dump`, `mongodump`, `mysqldump`, `redis-cli`) to be installed.

```bash
dbstash <engine> [flags]
```

All flags map 1:1 to environment variables (`DB_HOST` -> `--db-host`). Run `dbstash <engine> --help` for details.


Flags and environment variables can be mixed. **Precedence:** CLI flag > environment variable > default value.

## Available Docker Images

| Database   | Engine Key | Latest Alias Tags | Version-Specific Tags | Latest Version |
|------------|------------|-------------------|------------------------|----------------|
| PostgreSQL | `pg`       | `:pg`, `:pg-latest` | `:pg-15`, `:pg-16`, `:pg-17` | 17 |
| MongoDB    | `mongo`    | `:mongo`, `:mongo-latest` | `:mongo-7`, `:mongo-8` | 8 |
| MySQL      | `mysql`    | `:mysql`, `:mysql-latest` | `:mysql-8`, `:mysql-9` | 9 |
| MariaDB    | `mariadb`  | `:mariadb`, `:mariadb-latest` | `:mariadb-10`, `:mariadb-11` | 11 |
| Redis      | `redis`    | `:redis`, `:redis-latest` | `:redis-7`, `:redis-8` | 8 |

**Tag Strategy:**
- **`:engine-version`** (e.g. `:pg-17`) — Pinned to specific database version
- **`:engine`** and **`:engine-latest`** (e.g. `:pg`, `:pg-latest`) — Both point to the latest version
- **`:engine-version-dbstashversion`** (e.g. `:pg-17-0.7.0`) — Created on git tag releases

All images: `ghcr.io/viperadnan-git/dbstash:<tag>`

## How It Works

dbstash streams database dump output directly to rclone, avoiding local disk usage:

```
dump stdout --> rclone rcat remote:path/filename
```

No intermediate files are created on disk (in stream mode). The Go binary handles process piping, scheduling, retention cleanup, and notifications.

## Docker Compose Example

```yaml
services:
  backup-pg:
    image: ghcr.io/viperadnan-git/dbstash:pg-16
    environment:
      DB_URI_FILE: /run/secrets/pg_uri
      RCLONE_REMOTE: "s3:my-bucket/backups/pg"
      RCLONE_CONFIG_FILE: /run/secrets/rclone_conf
      BACKUP_SCHEDULE: "0 */6 * * *"
      BACKUP_NAME_TEMPLATE: "{db}-{date}-{time}"
      BACKUP_TIMEOUT: "1h"
      RETENTION_MAX_FILES: 20
      RETENTION_MAX_DAYS: 30
      NOTIFY_WEBHOOK_URL: ${DISCORD_WEBHOOK_URL}
      NOTIFY_ON: failure
      LOG_LEVEL: info
      TZ: Asia/Kolkata
    secrets:
      - pg_uri
      - rclone_conf
    restart: unless-stopped

secrets:
  pg_uri:
    file: ./secrets/pg_uri.txt
  rclone_conf:
    file: ./secrets/rclone.conf
```

## Configuration

All options can be set via environment variables or CLI flags. In Docker mode, use environment variables. In CLI mode, use flags (which fall back to environment variables automatically).

### Connection

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `DB_URI` | `--db-uri` | No* | — | Full connection URI (e.g. `postgresql://user:pass@host:5432/mydb`) |
| `DB_URI_FILE` | `--db-uri-file` | No* | — | Path to a file containing the connection URI (Docker secrets) |
| `DB_HOST` | `--db-host` | No* | — | Database host |
| `DB_PORT` | `--db-port` | No | Engine default | Database port |
| `DB_NAME` | `--db-name` | No* | — | Database name |
| `DB_USER` | `--db-user` | No | — | Database user |
| `DB_PASSWORD` | `--db-password` | No | — | Database password |
| `DB_PASSWORD_FILE` | `--db-password-file` | No | — | Path to file containing the password (Docker secrets) |
| `DB_AUTH_SOURCE` | `--db-auth-source` | No | `admin` | MongoDB auth database |

*Either `DB_URI`/`DB_URI_FILE` **or** `DB_HOST` + `DB_NAME` must be provided. When `BACKUP_ALL_DATABASES=true`, `DB_NAME` is not required. `DB_NAME` and `BACKUP_ALL_DATABASES` are mutually exclusive.

### Rclone

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `RCLONE_REMOTE` | `--rclone-remote` | Yes | — | Rclone remote path (e.g. `s3:my-bucket/backups`) |
| `RCLONE_CONFIG` | `--rclone-config` | No | — | Base64-encoded rclone.conf content |
| `RCLONE_CONFIG_FILE` | `--rclone-config-file` | No | `~/.config/rclone/rclone.conf` | Path to rclone config file |
| `RCLONE_EXTRA_ARGS` | `--rclone-extra-args` | No | — | Additional rclone flags |

### Schedule & Naming

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `BACKUP_SCHEDULE` | `--backup-schedule` | No | `0 2 * * *` | Cron expression or `once` for a single backup |
| `BACKUP_MODE` | `--backup-mode` | No | `stream` | `stream`, `directory`, or `tar` |
| `BACKUP_NAME_TEMPLATE` | `--backup-name-template` | No | `{db}-{date}-{time}` | Filename template |
| `BACKUP_COMPRESS` | `--backup-compress` | No | `false` | Enable native compression via dump tool |
| `BACKUP_EXTENSION` | `--backup-extension` | No | auto | Override file extension |
| `BACKUP_ALL_DATABASES` | `--backup-all-databases` | No | `false` | Dump all databases (pg, mysql/mariadb, mongo). Alias: `BACKUP_ALL_DBS` / `--backup-all-dbs` |
| `BACKUP_ON_START` | `--backup-on-start` | No | `false` | Run backup immediately on start |
| `BACKUP_TIMEOUT` | `--backup-timeout` | No | `0` | Max duration for a backup (e.g. `1h`, `30m`) |
| `BACKUP_LOCK` | `--backup-lock` | No | `true` | Prevent overlapping backup runs |
| `BACKUP_TEMP_DIR` | `--backup-temp-dir` | No | `/tmp/dbstash-work` | Temp directory for directory/tar modes |
| `DUMP_EXTRA_ARGS` | `--dump-extra-args` | No | — | Additional flags for the dump tool |
| `DRY_RUN` | `--dry-run` | No | `false` | Log config without executing |
| `TZ` | `--tz` | No | `UTC` | Timezone for schedule and filenames |

#### Name Template Tokens

The `BACKUP_NAME_TEMPLATE` value is expanded at backup time by replacing tokens with runtime values. The file extension is appended automatically based on the engine and compression setting (override with `BACKUP_EXTENSION`). All timestamps respect the `TZ` environment variable (default `UTC`).

| Token | Expands To | Example |
|---|---|---|
| `{db}` | Database name from `DB_NAME` or parsed from `DB_URI` (`all` when `BACKUP_ALL_DATABASES=true`) | `myapp` |
| `{engine}` | Engine key | `pg` |
| `{date}` | Current date as `YYYY-MM-DD` | `2026-02-07` |
| `{time}` | Current time as `HHmmss` | `020000` |
| `{ts}` | Unix timestamp in seconds | `1770508800` |
| `{uuid}` | First 8 characters of a UUIDv7 (time-ordered) | `019c38fb` |

**Default template:** `{db}-{date}-{time}` produces filenames like `myapp-2026-02-07-020000.sql`.

### Retention

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `RETENTION_MAX_FILES` | `--retention-max-files` | No | `0` (unlimited) | Keep at most N backup files |
| `RETENTION_MAX_DAYS` | `--retention-max-days` | No | `0` (unlimited) | Delete backups older than N days |

### Notifications

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `NOTIFY_WEBHOOK_URL` | `--notify-webhook-url` | No | — | Slack or Discord webhook URL |
| `NOTIFY_ON` | `--notify-on` | No | `failure` | When to notify: `always`, `failure`, `success` |

### Hooks

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `HOOK_PRE_BACKUP` | `--hook-pre-backup` | No | — | Shell command to run before backup |
| `HOOK_POST_BACKUP` | `--hook-post-backup` | No | — | Shell command to run after backup |

Post-backup hooks receive `DBSTASH_STATUS` (`success`/`failure`) and `DBSTASH_FILE` (remote path) as environment variables.

### Logging

| Variable | Flag | Required | Default | Description |
|---|---|---|---|---|
| `LOG_LEVEL` | `--log-level` | No | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `--log-format` | No | `text` | `json` or `text` |

## Backup Modes

| Mode | `BACKUP_MODE` | How It Works | Disk Usage |
|---|---|---|---|
| **Stream** (default) | `stream` | Pipes dump stdout directly to `rclone rcat` | Zero |
| **Directory** | `directory` | Dumps to temp dir, uploads via `rclone copy` | Requires temp space |
| **Tar** | `tar` | Dumps to temp dir, tar streams to `rclone rcat` | Requires temp space |

### Compression

| Engine | `BACKUP_COMPRESS=true` | Manual via `DUMP_EXTRA_ARGS` |
|---|---|---|
| PostgreSQL | `--Fc` (custom format) | `--compress=zstd:9`, etc. |
| MongoDB | `--gzip` | `--gzip` |
| MySQL/MariaDB | No-op (warning logged) | — |
| Redis | No change (RDB already compact) | — |

### All Databases

Set `BACKUP_ALL_DATABASES=true` to dump every database on the server instead of a single one.

| Engine | Tool Used | Limitations |
|---|---|---|
| PostgreSQL | `pg_dumpall` | Stream mode only (plain SQL, no native compression) |
| MongoDB | `mongodump` (no `--db`) | None |
| MySQL/MariaDB | `mysqldump --all-databases` | Stream mode only (`--tab` incompatible) |
| Redis | No change | Always dumps the full RDB snapshot |

`DB_NAME` and `BACKUP_ALL_DATABASES` are mutually exclusive. When using a URI, the database name is automatically stripped for engines that would otherwise scope the dump to a single database.

## Encryption at Rest

Use rclone's native `crypt` remote:

```ini
[s3-backup]
type = s3
provider = AWS
access_key_id = ...
secret_access_key = ...

[s3-backup-encrypted]
type = crypt
remote = s3-backup:my-bucket/backups
password = ... (obscured)
```

Set `RCLONE_REMOTE=s3-backup-encrypted:` and all backups are transparently encrypted.

## Docker Secrets

Any sensitive env var supports a `_FILE` suffix. dbstash reads the secret from the file at startup:

```yaml
services:
  backup:
    image: ghcr.io/viperadnan-git/dbstash:pg-16
    environment:
      DB_URI_FILE: /run/secrets/db_uri
      RCLONE_CONFIG_FILE: /run/secrets/rclone_conf
    secrets:
      - db_uri
      - rclone_conf

secrets:
  db_uri:
    file: ./secrets/db_uri.txt
  rclone_conf:
    file: ./secrets/rclone.conf
```

Supported `_FILE` variants: `DB_URI_FILE`, `DB_PASSWORD_FILE`, `RCLONE_CONFIG_FILE`.

## Health Check

When running with a cron schedule, dbstash serves a health endpoint:

```
GET :8080/healthz
```

Returns:
```json
{"status": "healthy", "engine": "pg", "last_backup": "2026-02-07T02:00:05Z", "last_status": "success"}
```

## License

[MIT](./LICENSE)
