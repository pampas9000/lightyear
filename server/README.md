# Server

Go service for the `transcoder` monorepo.

This service is responsible for:

- exposing the HTTP API used by `web`
- validating and creating transcoding jobs
- coordinating task handoff to the Rust compute layer
- owning server-side orchestration concerns such as queueing, auth, storage integration, and job lifecycle management

The server does **not** perform heavy media transcoding itself. That work belongs to `compute`.

## Responsibilities

- accept API requests from clients
- create and persist transcoding jobs
- enqueue work for `compute/worker`
- return job status, metadata, and results
- host server-only integrations that should not live in the browser or in portable Rust bindings

## Directory Layout

```text
server/
├── cmd/
│   └── api/               # service entrypoint and bootstrap wiring
├── ent/                   # generated ORM code and schema definitions
├── internal/
│   ├── config/            # runtime configuration loading
│   ├── http/              # HTTP handlers, DTOs, route registration
│   └── tasks/             # application logic and queue orchestration
└── go.mod
```

## Development

From the monorepo root, prefer the root `justfile` for day-to-day server work:

```bash
just bootstrap-server
just dev-server
```

You can still invoke the underlying commands directly when needed:

```bash
go run ./server/cmd/api
npm run dev:server
```

Run tests:

```bash
just test-server
```

Or directly:

```bash
go test ./server/...
```

Format Go code:

```bash
just fmt-server
```

Or directly:

```bash
gofmt -w server
```

## Module and Workspace

This service is a normal Go module:

- module path: `transcoder/server`
- workspace root: `../go.work`

That means you can work on it either from the repo root or from inside `server/`.

## Current Layering

The current server code is split into a reasonable set of responsibilities:

- `cmd/api`
  - process bootstrap only
  - loads config
  - opens PostgreSQL and Redis
  - builds the Fiber app and starts listening
- `internal/config`
  - owns runtime configuration defaults and environment parsing
- `internal/http`
  - owns route registration, request binding, and HTTP error mapping
  - should stay transport-focused
- `internal/tasks`
  - owns job validation, persistence orchestration, and queue handoff
- `ent`
  - owns generated ORM code and schema definitions
  - should remain generated rather than wrapped prematurely

This is a solid direction for a Go service: `cmd` stays thin, `internal/http` handles transport, and application logic is pushed inward.

## Extension Guidance

If you continue evolving this service, these are the most natural next additions:

- `internal/queue`
  - extract Redis-specific broker behavior here if queue logic grows beyond a single enqueue call
- `internal/storage`
  - object storage and artifact persistence
- `internal/auth`
  - authentication and authorization concerns
- `internal/http/middleware`
  - structured logging, request IDs, auth middleware, rate limiting

A few conventions are worth preserving:

- keep `cmd/api` limited to wiring and startup
- keep `internal/http` free of direct business orchestration where possible
- keep Ent as the main persistence layer instead of adding a generic repository abstraction too early
- add new packages only when they represent a clear boundary, not just to make the tree look larger

## Service Boundaries

### Input

The server receives requests from:

- `web` for user-facing job creation and status queries
- future native clients that may use the same API surface

### Output

The server hands work off to:

- `compute/worker` for task execution
- `compute/bindings/ffi` or `compute/bindings/wasm` indirectly when those artifacts are used by other applications

## Planned Internal Conventions

As the service grows, keep the structure organized by capability:

- `internal/http` for request/response concerns
- `internal/tasks` for job orchestration
- `internal/queue` for broker integration
- `internal/storage` for object/file persistence
- `internal/config` for configuration loading
- `internal/auth` for authentication and authorization

Use `cmd/api` only for process bootstrap and wiring. Keep business logic in `internal/...`.

## Configuration

Current runtime configuration is loaded from environment variables with defaults:

- `APP_ENV`
- `SERVER_ADDR`
- `PORT`
- `DATABASE_URL`
- `REDIS_URL`
- `AUTO_MIGRATE`

Behavior notes:

- `SERVER_ADDR` wins over `PORT`
- `PORT` is normalized to `:PORT`
- `AUTO_MIGRATE` defaults to enabled

## Technologies

- **Language**: Go 1.26+
- **API Framework**: [Fiber v3](https://gofiber.io/)
- **ORM**: [Ent](https://entgo.io/)
- **Database**: PostgreSQL
- **Queue**: Redis

## Current Status

The service is operational with a cleaner boundary between bootstrap, transport, and orchestration:

- [x] Fiber v3 server bootstrap in `cmd/api`
- [x] Environment-driven runtime config in `internal/config`
- [x] Ent ORM with PostgreSQL persistence
- [x] Job creation and status endpoints
- [x] Task service orchestration in `internal/tasks`
- [x] Redis LPUSH task enqueueing