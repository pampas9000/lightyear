# Server

Go service for the `lightyear` monorepo: HTTP API, task persistence, orchestration and storage integration. It never processes images; that is `compute`.

Contracts: [HTTP API](../docs/contracts/http-api.md), [ImageOptions](../docs/contracts/image-options.md), [Queue](../docs/contracts/queue.md). Design: [Reliability](../docs/architecture/reliability.md).

## Processes

| Binary | Command | Role |
| :--- | :--- | :--- |
| API | `go run ./cmd/api` | Fiber v3 HTTP API, sessions, uploads, hourly storage cleanup |
| Orchestrator | `go run ./cmd/worker` | Claims due jobs from PostgreSQL and dispatches attempts to `q:image`, applies results from `q:results`, recovers unsent and expired attempts |
| Migration | `go run ./cmd/migrate [-reset]` | Creates the schema; `-reset` drops every application table first (development only) |

## Directory layout

```text
server/
├── cmd/{api,worker,migrate}/
├── internal/
│   ├── api/               # routes, handlers, middleware, response envelope
│   ├── config/            # koanf configuration
│   ├── imageoptions/      # Go mirror of ImageOptions: strict parsing, canonicalization, artifact key
│   ├── models/            # GORM entities (UUID v7, no physical foreign keys)
│   ├── schema/            # migration and reset
│   ├── services/          # auth, task, job, workflow, orchestrator, jobstate, storage
│   ├── task/              # queue contract v2 and the Redis implementation
│   ├── testutil/          # isolated PostgreSQL databases for tests
│   └── pkg/s3/            # AWS SDK v2 client factory
└── pkg/util/              # UUID v7 helpers
```

## Development

```bash
task test:server          # go test ./server/...
task fmt:server
```

Tests that need PostgreSQL create and drop their own schema in the database at `TEST_DATABASE_URL` and are skipped when it is unset. Module path `transcoder/server`, workspace root `../go.work`.

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
| `S3_REGION` | `auto` | |
| `GOOGLE_CLIENT_ID` / `_SECRET` / `_CALLBACK_URL` | — | Google OIDC (enabled when set) |
| `GITHUB_CLIENT_ID` / `_SECRET` / `_CALLBACK_URL` | — | GitHub OAuth (enabled when set) |
| `JWT_SECRET` | dev value | Unused: the JWT middleware is dead code (session auth is active) |

## Known gaps

- HTTP routes are unversioned (`/api`); upload endpoints return camelCase objects outside the response envelope. See the HTTP contract.
- `JWT_SECRET` and the JWT middleware are dead code.
- The public `/v1` API, API keys, webhooks and MCP are planned in the [roadmap](../docs/roadmap.md).
