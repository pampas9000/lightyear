# Server

Go service for the `lightyear` monorepo. It owns the HTTP API, task persistence and orchestration, and system integrations. It does **not** perform media transcoding — that work belongs to `compute`.

Feature contracts live in the docs site: [HTTP API](../docs/contracts/http-api.md), [Queue](../docs/contracts/queue.md), [Reliability](../docs/architecture/reliability.md).

## Responsibilities

- accept API requests from `web` and future native clients
- validate and persist transcoding tasks/jobs (GORM + PostgreSQL)
- schedule work through a transactional Outbox and Redis Streams; coordinate installed executors
- aggregate job results into task state; run leases, retries and stale recovery
- manage uploads, download URLs, drafts, workflows and sessions

## Processes

| Binary | Command | Role |
| :--- | :--- | :--- |
| API | `go run ./cmd/api` | Fiber v3 HTTP API, session middleware, background storage cleanup |
| Orchestrator | `go run ./cmd/worker` | Consumes `transcoder:jobs:stream` / `transcoder:results:stream`, claims jobs, dispatches installed executor payloads, recovers stale leases |
| Migration | `go run ./cmd/migrate` | Runs the repeatable PostgreSQL schema migration when `AUTO_MIGRATE=false` |
| Codegen | `go run ./cmd/gen` | Regenerates `internal/query` with gorm/gen |

Suggested local run (from repo root):

```bash
task api
task orchestrator
```

## Directory layout

```text
server/
├── cmd/
│   ├── api/               # API bootstrap and dependency wiring
│   ├── migrate/           # explicit PostgreSQL migration command
│   ├── worker/            # orchestrator daemon
│   └── gen/               # gorm/gen code generator
├── internal/
│   ├── api/               # routes, handlers, middleware, response envelope
│   ├── config/            # koanf configuration loading
│   ├── models/            # GORM entities (UUID v7, no physical FKs)
│   ├── query/             # generated type-safe queries
│   ├── services/          # auth / job / task / workflow / storage
│   ├── task/              # Redis Streams queue contract and payloads
│   ├── transcode/         # transcode params model + validation
│   └── pkg/s3/            # AWS SDK v2 client factory
└── pkg/util/              # UUID v7 helpers
```

## Development

```bash
task bootstrap:server
task test:server          # or: go test ./server/...
task fmt:server           # or: gofmt -w server
```

Module path `transcoder/server`, workspace root `../go.work`.

## Configuration

Loaded with koanf from defaults, `server/.env` (falls back to `.env`) and environment variables.

| Variable | Default | Notes |
| :--- | :--- | :--- |
| `APP_ENV` | `development` | `production` enables Secure cookies |
| `SERVER_ADDR` | `[::]:8080` | Wins over `PORT` |
| `PORT` | — | Normalized to `:PORT` |
| `DATABASE_URL` | local postgres `transcoder` | PostgreSQL |
| `REDIS_URL` | `redis://localhost:6379/0` | Sessions, streams, cancellation flags |
| `AUTO_MIGRATE` | `true` | GORM AutoMigrate on API boot |
| `FRONTEND_URL` | `http://localhost:3000` | OAuth success redirect |
| `S3_ENDPOINT` / `S3_ACCESS_ID` / `S3_ACCESS_KEY` / `S3_BUCKET` | — | S3/R2 storage |
| `GOOGLE_CLIENT_ID` / `_SECRET` / `_CALLBACK_URL` | — | Google OIDC (enabled when set) |
| `GITHUB_CLIENT_ID` / `_SECRET` / `_CALLBACK_URL` | — | GitHub OAuth (enabled when set) |
| `JWT_SECRET` | dev value | Reserved; JWT middleware exists but is unused (session auth is active) |

## Current status

- [x] Fiber v3 API with unified response envelope and stable response codes
- [x] Session auth (Redis store) + GitHub/Google OAuth, bearer-token or cookie
- [x] GORM/PostgreSQL persistence with UUID v7, no physical foreign keys
- [x] Task create/list/detail/retry/cancel and stats
- [x] Workflows, multipart uploads with SHA-256 fast path, presigned downloads, drafts
- [x] Redis Streams orchestration with transactional Outbox, atomic attempt claim, attempt-scoped outputs/cancellation, heartbeats, stale recovery, and artifact validation
- [x] Generic operation model for transcode, image generation, upscaling, and editing; AI executors are gated until installed
- [x] Durable external-provider invocation ledger for idempotent submission and reconciliation of unknown outcomes
- [x] S3/R2 storage integration and hourly cleanup of expired uploads/drafts

### Known gaps

- `POST /api/jobs` is removed from routing (handler remains); jobs are created through tasks
- HTTP routes are unversioned (`/api`), and a few historical endpoints use `camelCase` fields (see HTTP contract)
- `JWT_SECRET` and the JWT middleware are dead code
- No concrete AI provider adapter or AI worker is deployed yet; AI requests are rejected with `422` until an executor is registered
- Redis Outbox delivery is at-least-once; external providers still require provider-side idempotency and reconciliation

## Migrations

For production deployments with `AUTO_MIGRATE=false`, run:

```bash
go run ./server/cmd/migrate
```

The migration is repeatable and backfills legacy job input/output relations.
