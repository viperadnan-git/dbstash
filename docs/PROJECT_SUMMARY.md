# dbstash — Dockerized Database Backup via Rclone

## Overview

**dbstash** is a suite of lightweight Docker images that back up databases by streaming dump output directly into an rclone-mounted remote — no intermediate files, no local disk pressure. Each image targets a specific database engine and major version, configured entirely through environment variables.

---

## Language Choice: Go

| Consideration | Go | Shell | Python |
|---|---|---|---|
| Process/pipe management | Excellent (`os/exec`, `io.Pipe`) | Native but fragile | Subprocess works but heavier |
| Error handling | Strong typed errors | Brittle (`set -e` traps) | Good but adds runtime |
| Image size | ~10–15 MB static binary + db tools | Smallest (just scripts + tools) | ~80 MB+ (interpreter + deps) |
| Concurrency (scheduling) | Goroutines, built-in cron libs | Needs external cron | Needs asyncio or APScheduler |
| Testability | First-class unit/integration tests | Painful | Good |
| Docker ecosystem alignment | Lingua franca (Docker, K8s, rclone itself) | — | — |

**Verdict:** Go gives us a single static binary with robust pipe streaming, excellent error handling, built-in scheduling, and trivial multi-stage Docker builds. It's the natural fit for infrastructure tooling.

---

## Image Naming & Tag Schema

**Registry path:** `ghcr.io/viperadnan-git/dbstash` (or Docker Hub: `viperadnan/dbstash`)

### Tag Format

```
<engine>-<major_version>
```

### Engine Keys (fixed, short, unambiguous)

| Database | Engine Key | Example Tags |
|---|---|---|
| PostgreSQL | `pg` | `pg-15`, `pg-16`, `pg-17` |
| MongoDB | `mongo` | `mongo-6`, `mongo-7`, `mongo-8` |
| MySQL | `mysql` | `mysql-8`, `mysql-9` |
| MariaDB | `mariadb` | `mariadb-10`, `mariadb-11` |
| Redis | `redis` | `redis-7`, `redis-8` |
| SQLite | `sqlite` | `sqlite-3` |

### Special Tags

| Tag | Meaning |
|---|---|
| `pg-latest` | Latest supported PostgreSQL version |
| `mongo-latest` | Latest supported MongoDB version |
| `latest` | **Not used** — always requires explicit engine tag |

### Future-proofing

The schema extends naturally: adding ClickHouse → `clickhouse-24`, CockroachDB → `crdb-24`, etc. The convention is always `<engine_short_name>-<major_version>`. No `latest` root tag prevents accidental cross-engine upgrades.

---

## Architecture

```
┌──────────────────────────────────────────────────┐
│                  dbstash container                │
│                                                   │
│  ┌───────────────────┐  stdout  ┌──────────────────┐ │
│  │ pg_dump           │─────────▶│  rclone rcat     │ │
│  │ mongodump         │ (stream) │  (writes to      │ │
│  │ mysqldump         │          │   remote path)   │ │
│  └───────────────────┘          └──────────────────┘ │
│                                                   │
│  ┌──────────────────────────────────────────────┐ │
│  │              Go binary (dbstash)             │ │
│  │  • Cron scheduler (with flock guard)         │ │
│  │  • Env config parser + _FILE secrets loader  │ │
│  │  • Pre/post backup hooks                     │ │
│  │  • Pipe: dump stdout → rclone rcat stdin     │ │
│  │  • Retention manager (max files / max days)  │ │
│  │  • Webhook notifier (Slack / Discord)        │ │
│  │  • Structured JSON logger                    │ │
│  │  • Health check endpoint (:8080/healthz)     │ │
│  └──────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

### Streaming Pipeline

The core innovation is **zero-disk-usage backups**. Instead of:

```
dump → local file → rclone copy → delete local file
```

We do:

```
dump stdout → rclone rcat remote:path/filename
```

Compression and archiving are **opt-in** — by default, dump tools output uncompressed streams. Users can enable native compression via the `BACKUP_COMPRESS` env var or by passing tool-specific flags through `DUMP_EXTRA_ARGS`.

| Engine | Tool | Default Output | `BACKUP_COMPRESS=true` Behaviour | Manual Alternative (`DUMP_EXTRA_ARGS`) |
|---|---|---|---|---|
| PostgreSQL | `pg_dump` | Plain SQL (`--format=plain`) | Switches to custom format (`--Fc`) | `--compress=zstd:9`, `--Fc`, etc. |
| MongoDB | `mongodump` | `--archive` (uncompressed) | Adds `--gzip` | `--gzip` |
| MySQL | `mysqldump` | Plain SQL | No native compression available* | — |
| MariaDB | `mariadb-dump` | Plain SQL | No native compression available* | — |
| Redis | `redis-cli` | Binary RDB (`--rdb -`) | Already compact, no change | — |

\* For engines without native compression, `BACKUP_COMPRESS=true` is a no-op and a warning is logged.

#### `DUMP_EXTRA_ARGS` Conflict Detection

When `BACKUP_MODE=stream`, dbstash validates `DUMP_EXTRA_ARGS` at startup against a per-engine blocklist of flags that would cause the dump tool to write to disk instead of stdout. If a conflicting flag is detected, dbstash logs a **warning** but does **not** block execution — the dump tool's own exit code and error output will surface the real failure through logging and notifications.

| Engine | Conflicting Flags (stream mode) |
|---|---|
| PostgreSQL | `--Fd`, `--format=directory`, `-Fd`, `--file=`, `-f` |
| MongoDB | `--out=`, `-o`, `--out ` (without `--archive`) |
| MySQL | `--tab=`, `--tab ` |
| Redis | — (no conflicting flags) |

### Multi-File Backup Handling

Some dump tools can produce multiple files instead of a single stream (e.g., `mongodump` without `--archive` outputs a directory tree of BSON files per collection, `mysqldump --tab` outputs separate `.sql` and `.txt` per table, or backing up all databases individually). The `BACKUP_MODE` env var controls how this is handled.

#### Modes

| Mode | `BACKUP_MODE` | How It Works | Rclone Method | Pros | Cons |
|---|---|---|---|---|---|
| **Stream** (default) | `stream` | Enforces flags that produce a single stdout stream (`--archive` for mongo, `--Fc` or plain SQL for pg). One stream → one file on remote. | `rclone rcat` | Zero disk usage, simplest, fastest | Not all dump options are compatible |
| **Directory** | `directory` | Dumps to a temp directory, then uploads the entire directory tree preserving structure. | `rclone copy` | Preserves per-collection/per-table files, easy selective restore | Requires local temp disk space |
| **Tar** | `tar` | Dumps to a temp directory, then tars (optionally compressed) and streams the tar to remote as a single file. | `rclone rcat` | Single file on remote, no streaming limitation, works with any dump flags | Requires local temp disk space during tar creation |

#### Decision Flow

```
BACKUP_MODE=stream (default)
  └─▶ dump --stdout-flags | rclone rcat remote:path/backup.ext

BACKUP_MODE=directory
  └─▶ dump → /tmp/dbstash-work/
  └─▶ rclone copy /tmp/dbstash-work/ remote:path/backup-{template}/
  └─▶ cleanup temp dir

BACKUP_MODE=tar
  └─▶ dump → /tmp/dbstash-work/
  └─▶ tar cf - /tmp/dbstash-work/ | rclone rcat remote:path/backup.tar
  └─▶ cleanup temp dir
```

#### Mode Compatibility Matrix

| Engine | `stream` | `directory` | `tar` |
|---|---|---|---|
| PostgreSQL | ✅ Default (`--Fp` or `--Fc` to stdout) | ✅ `--Fd` (directory format) | ✅ |
| MongoDB | ✅ `--archive` to stdout | ✅ Native (default `mongodump` behaviour) | ✅ |
| MySQL | ✅ Default (`mysqldump` to stdout) | ✅ `--tab` (per-table files) | ✅ |
| MariaDB | ✅ Default to stdout | ✅ `--tab` | ✅ |
| Redis | ✅ `--rdb -` to stdout | ❌ Single file only | ❌ |

#### Retention Behaviour by Mode

- **`stream` / `tar`:** Each backup is a single file. `RETENTION_MAX_FILES` counts files, `RETENTION_MAX_DAYS` checks file timestamps. Straightforward.
- **`directory`:** Each backup is a folder on the remote. `RETENTION_MAX_FILES` counts top-level backup folders, `RETENTION_MAX_DAYS` checks folder creation timestamps. Cleanup uses `rclone purge` on expired folders.

#### Additional Env Vars

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKUP_MODE` | No | `stream` | `stream`, `directory`, or `tar` |
| `BACKUP_TEMP_DIR` | No | `/tmp/dbstash-work` | Temp directory for `directory` and `tar` modes (ensure sufficient disk/volume) |

---

## Environment Variables

### Connection

| Variable | Required | Default | Description |
|---|---|---|---|
| `DB_URI` | No* | — | Full connection URI (e.g. `postgresql://user:pass@host:5432/mydb`, `mongodb://user:pass@host:27017/mydb?authSource=admin`). When set, takes precedence over individual `DB_*` vars below. |
| `DB_URI_FILE` | No* | — | Path to a file containing the connection URI (for Docker secrets) |
| `DB_HOST` | No* | — | Database host |
| `DB_PORT` | No | Engine default | Database port |
| `DB_NAME` | No* | — | Database name (or `--all-databases` equivalent) |
| `DB_USER` | No | — | Database user |
| `DB_PASSWORD` | No | — | Database password |
| `DB_PASSWORD_FILE` | No | — | Path to a file containing the password (for Docker secrets) |
| `DB_AUTH_SOURCE` | No | `admin` | MongoDB auth database |

\* Either `DB_URI` (or `DB_URI_FILE`) or `DB_HOST` + `DB_NAME` must be provided. If `DB_URI` is set, individual fields are ignored. `_FILE` variants are read at startup and take precedence over their non-file counterparts.

#### Docker Secrets Support

Any sensitive env var supports a `_FILE` suffix variant. When the `_FILE` variant is set, dbstash reads the secret from the file path at startup. This avoids exposing credentials in env vars or `docker inspect` output.

```yaml
services:
  backup-pg:
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

#### URI Format per Engine

| Engine | Example URI |
|---|---|
| PostgreSQL | `postgresql://user:pass@host:5432/dbname?sslmode=require` |
| MongoDB | `mongodb://user:pass@host:27017/dbname?authSource=admin` |
| MySQL / MariaDB | `mysql://user:pass@host:3306/dbname` |
| Redis | `redis://:pass@host:6379/0` |

### Rclone

| Variable | Required | Default | Description |
|---|---|---|---|
| `RCLONE_REMOTE` | Yes | — | Rclone remote path (e.g., `s3:my-bucket/backups`) |
| `RCLONE_CONFIG` | No | — | Base64-encoded rclone.conf content |
| `RCLONE_CONFIG_FILE` | No | `/config/rclone.conf` | Path to mounted rclone config file (Docker secrets compatible) |
| `RCLONE_EXTRA_ARGS` | No | — | Additional rclone flags |

#### Encryption at Rest

dbstash does not implement its own encryption — instead, it leverages rclone's native [`crypt`](https://rclone.org/crypt/) remote. To encrypt backups, configure a `crypt` remote in your `rclone.conf` that wraps the real storage backend:

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

Then point `RCLONE_REMOTE=s3-backup-encrypted:` — all backups are transparently encrypted before upload and decrypted on download. No changes needed in dbstash configuration.

### Schedule & Naming

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKUP_SCHEDULE` | No | `0 2 * * *` | Cron expression (default: daily 2 AM), or the special value `once` to run a single backup and exit |
| `BACKUP_MODE` | No | `stream` | `stream`, `directory`, or `tar` (see Multi-File Backup Handling) |
| `BACKUP_NAME_TEMPLATE` | No | `{db}-{date}-{time}` | Filename template (see below) |
| `BACKUP_COMPRESS` | No | `false` | Enable native compression via the dump tool (see compression table above) |
| `BACKUP_EXTENSION` | No | auto | Override file extension (auto picks based on engine and compression, e.g. `.sql`, `.dump`, `.archive`, `.archive.gz`) |
| `BACKUP_ON_START` | No | `false` | Run a backup immediately on container start |
| `DUMP_EXTRA_ARGS` | No | — | Additional flags passed to the dump tool (e.g. `--compress=zstd:9` for pg_dump). Overrides `BACKUP_COMPRESS` behaviour |
| `TZ` | No | `UTC` | Timezone for schedule and filenames |

#### One-Time Backup (`BACKUP_SCHEDULE=once`)

When `BACKUP_SCHEDULE=once`, dbstash runs a single backup, executes retention cleanup, sends notifications, and exits immediately with code `0` (success) or `1` (failure). No cron scheduler is started, no health endpoint is served. This is useful for Kubernetes Jobs, CI pipelines, `docker run --rm` one-offs, or pre-deployment backup scripts.

Note: `BACKUP_ON_START=true` is a **different feature** — it runs an immediate backup on container start *and then* starts the cron scheduler for recurring backups. `BACKUP_SCHEDULE=once` runs one backup and exits.

### Retention

| Variable | Required | Default | Description |
|---|---|---|---|
| `RETENTION_MAX_FILES` | No | `0` (unlimited) | Keep at most N backup files |
| `RETENTION_MAX_DAYS` | No | `0` (unlimited) | Delete backups older than N days |

### Notifications

| Variable | Required | Default | Description |
|---|---|---|---|
| `NOTIFY_WEBHOOK_URL` | No | — | Slack or Discord webhook URL for backup notifications |
| `NOTIFY_ON` | No | `failure` | When to send notifications: `always`, `failure`, or `success` |

Webhook payloads are automatically formatted for the detected platform (Slack or Discord) based on the URL pattern. Notifications include: backup status, database name, file size, duration, and error details on failure.

### Logging

| Variable | Required | Default | Description |
|---|---|---|---|
| `LOG_LEVEL` | No | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | No | `json` | Log format: `json` (structured) or `text` (human-readable) |

All log output is structured JSON by default for easy ingestion into log aggregators (Loki, CloudWatch, Datadog, etc.). Each log entry includes: timestamp, level, message, and contextual fields (engine, database, backup_id, duration, etc.).

### Hooks

| Variable | Required | Default | Description |
|---|---|---|---|
| `HOOK_PRE_BACKUP` | No | — | Shell command to run before each backup (e.g. `CHECKPOINT` for Postgres, `BGSAVE` wait for Redis) |
| `HOOK_POST_BACKUP` | No | — | Shell command to run after each backup (runs on both success and failure) |

Hooks run inside the container with the same environment. If `HOOK_PRE_BACKUP` exits non-zero, the backup is aborted and a failure notification is sent. `HOOK_POST_BACKUP` receives `DBSTASH_STATUS` (`success` or `failure`) and `DBSTASH_FILE` (remote path) as environment variables.

### Safety & Reliability

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKUP_TIMEOUT` | No | `0` (no limit) | Maximum duration for a single backup run (e.g. `1h`, `30m`). Kills the dump process if exceeded. |
| `BACKUP_LOCK` | No | `true` | Prevent overlapping backup runs. If a backup is still running when the next cron tick fires, the new run is skipped and a warning is logged. |
| `DRY_RUN` | No | `false` | Log what would happen (resolved config, dump command, rclone target) without executing anything. Useful for validating configuration. |

### Name Template Tokens

| Token | Expands To | Example |
|---|---|---|
| `{db}` | Database name | `myapp` |
| `{engine}` | Engine key | `pg` |
| `{date}` | `YYYY-MM-DD` | `2026-02-07` |
| `{time}` | `HHmmss` | `020000` |
| `{ts}` | Unix timestamp | `1770508800` |
| `{uuid}` | Short UUID (8 chars) | `a1b2c3d4` |

Default produces: `myapp-2026-02-07-020000.sql` (PostgreSQL) or `analytics-2026-02-07-020000.archive` (MongoDB). With `BACKUP_COMPRESS=true`: `myapp-2026-02-07-020000.dump` / `analytics-2026-02-07-020000.archive.gz`.

---

## Docker Compose Example

```yaml
services:
  backup-pg:
    image: ghcr.io/viperadnan-git/dbstash:pg-16
    environment:
      DB_URI_FILE: /run/secrets/pg_uri
      RCLONE_REMOTE: "s3:my-bucket/backups/pg"
      RCLONE_CONFIG_FILE: /run/secrets/rclone_conf
      BACKUP_SCHEDULE: "0 */6 * * *"        # every 6 hours
      BACKUP_NAME_TEMPLATE: "{db}-{date}-{time}"
      BACKUP_TIMEOUT: "1h"
      RETENTION_MAX_FILES: 20
      RETENTION_MAX_DAYS: 30
      HOOK_PRE_BACKUP: "echo 'Starting PG backup'"
      NOTIFY_WEBHOOK_URL: ${DISCORD_WEBHOOK_URL}
      NOTIFY_ON: failure
      LOG_LEVEL: info
      TZ: Asia/Kolkata
    secrets:
      - pg_uri
      - rclone_conf
    restart: unless-stopped

  backup-mongo:
    image: ghcr.io/viperadnan-git/dbstash:mongo-7
    environment:
      # Individual vars also supported
      DB_HOST: mongodb
      DB_PORT: 27017
      DB_NAME: analytics
      DB_USER: root
      DB_PASSWORD_FILE: /run/secrets/mongo_password
      DB_AUTH_SOURCE: admin
      RCLONE_REMOTE: "s3:my-bucket/backups/mongo"
      RCLONE_CONFIG_FILE: /run/secrets/rclone_conf
      BACKUP_SCHEDULE: "0 3 * * *"
      RETENTION_MAX_DAYS: 14
      NOTIFY_WEBHOOK_URL: ${SLACK_WEBHOOK_URL}
      NOTIFY_ON: always
      TZ: Asia/Kolkata
    secrets:
      - mongo_password
      - rclone_conf
    restart: unless-stopped

secrets:
  pg_uri:
    file: ./secrets/pg_uri.txt
  mongo_password:
    file: ./secrets/mongo_password.txt
  rclone_conf:
    file: ./secrets/rclone.conf
```

---

## Project Structure

```
dbstash/
├── cmd/
│   └── dbstash/
│       └── main.go              # Entrypoint
├── internal/
│   ├── config/
│   │   └── config.go            # Env parsing, validation & _FILE secrets loader
│   ├── engine/
│   │   ├── engine.go            # Engine interface
│   │   ├── postgres.go          # pg_dump streaming
│   │   ├── mongo.go             # mongodump streaming
│   │   ├── mysql.go             # mysqldump streaming
│   │   └── redis.go             # redis-cli --rdb streaming
│   ├── pipeline/
│   │   ├── pipeline.go          # Base pipeline interface
│   │   ├── stream.go            # dump stdout → rclone rcat
│   │   ├── directory.go         # dump → temp dir → rclone copy
│   │   └── tar.go               # dump → temp dir → tar → rclone rcat
│   ├── retention/
│   │   └── retention.go         # Cleanup by max files / max days
│   ├── hooks/
│   │   └── hooks.go             # Pre/post backup command runner
│   ├── notify/
│   │   └── notify.go            # Slack & Discord webhook sender
│   ├── logger/
│   │   └── logger.go            # Structured JSON / text logger
│   ├── scheduler/
│   │   └── scheduler.go         # Cron wrapper with lock guard
│   └── health/
│       └── health.go            # HTTP health check
├── docker/
│   ├── Dockerfile.pg
│   ├── Dockerfile.mongo
│   ├── Dockerfile.mysql
│   └── Dockerfile.redis
├── .github/
│   └── workflows/
│       └── build.yml            # Matrix build for all engine×version combos
├── docker-compose.example.yml
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

---

## Build Strategy

A single GitHub Actions workflow uses a matrix to build all combinations:

```yaml
strategy:
  matrix:
    include:
      - engine: pg
        version: 15
        base: postgres:15-alpine
      - engine: pg
        version: 16
        base: postgres:16-alpine
      - engine: pg
        version: 17
        base: postgres:17-alpine
      - engine: mongo
        version: 6
        tools_version: "100.9"
      - engine: mongo
        version: 7
        tools_version: "100.10"
      # ...
```

Each Dockerfile is a multi-stage build: compile Go binary in `golang:alpine`, then copy into a minimal image with only the required database client tools and rclone.

---

## Milestones

| Phase | Scope |
|---|---|
| **v0.1** | PostgreSQL streaming backup + rclone rcat + env config + basic retention + structured JSON logging + one-time backup (`BACKUP_SCHEDULE=once`) |
| **v0.2** | MongoDB support, opt-in compression (`BACKUP_COMPRESS`), name templates, `DB_URI` support |
| **v0.3** | `directory` and `tar` backup modes for multi-file dumps |
| **v0.4** | Docker secrets (`_FILE` vars), backup locking, timeout (`BACKUP_TIMEOUT`), dry-run mode |
| **v0.5** | Slack & Discord webhook notifications, pre/post backup hooks |
| **v0.6** | MySQL/MariaDB support, health endpoint, Docker Compose examples, encryption-at-rest docs |
| **v0.7** | Email notifications, restore support (`dbstash restore` command) |
| **v1.0** | Stable release, full docs, CI matrix builds for all engines |
