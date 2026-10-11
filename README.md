# lightyear

lightyear is a multi-language monorepo for a media transcoding platform: a Go orchestration server, a portable Rust compute core, and a Nuxt web console with an in-browser WebAssembly sandbox.

Architecture, contracts and engineering notes live in the [documentation site](docs/index.md).

## Architecture at a glance

```text
web ──HTTP──▶ server ──Redis Streams──▶ compute-worker ──▶ compute-server-adapter
                │                                              │
                ├── PostgreSQL (GORM)                           ├── compute-engine (portable Rust core)
                └── S3 / R2 (uploads, artifacts)                └── host CLIs (cjxl/avifenc/avifdec/heif-dec/cwebp)

browser ──Web Worker──▶ compute-wasm ──▶ compute-engine
native  ──C ABI──────▶ compute-ffi  ──▶ compute-engine
```

- `server`: Go 1.26 + Fiber v3 HTTP API, GORM/PostgreSQL persistence, Redis Streams orchestration (`cmd/api` + `cmd/worker` orchestrator), S3/R2 storage integration. It never implements codecs.
- `compute`: Rust workspace (edition 2024). `compute-engine` owns the portable pipeline, `compute-server-adapter` owns `run_transcode` (host CLI composition, magic-byte detection, native YUV passthrough), `compute-worker` is a stateless Redis/S3 consumer, `compute-wasm` / `compute-ffi` are thin host bindings.
- `web`: Nuxt 4 thin client. All business requests are proxied to the Go API; the WASM sandbox runs locally in a Web Worker.

## WASM local transcoding sandbox

The web console ships a fully client-side sandbox: drag-and-drop queue, per-file settings, draggable quality comparison, batch download — no server work, no uploads.

![WASM local transcoding sandbox](docs/public/assets/local-wasm-transcoding.avif)

> ⚠️ **Known limitation — wide gamut**: browser-side encoders (`image`/`ravif`, `@jsquash/jxl`) cannot write ICC/CICP metadata, and JPEG XL stores its color encoding in the codestream header, which cannot be patched afterwards. The sandbox does not convert gamut: wide-gamut sources are emitted as untagged pixels and may look oversaturated. Use the server pipeline to preserve wide-gamut profiles. See [Known limitations](docs/compute/limitations.md).

## Repository layout

```text
server/
  cmd/api/         HTTP API bootstrap
  cmd/worker/      orchestrator daemon (dispatch, result processing, stale recovery)
  cmd/gen/         gorm/gen code generation
  internal/api/    routes, handlers, middleware, response envelope
  internal/services/  auth, job, task, workflow, storage
  internal/task/   Redis Streams queue contract
  internal/transcode/  transcode params + validation
  internal/models/ GORM entities
  internal/config/ koanf configuration
compute/
  crates/types/           shared domain types + wire protocol
  crates/engine/          portable codec/color/edit core
  crates/server-adapter/  run_transcode host pipeline
  bindings/wasm/          browser bindings
  bindings/ffi/           C ABI bindings (standalone crate, excluded from the workspace)
  worker/                 Redis/S3 consumer
web/                Nuxt 4 app (app/ directory)
docs/               documentation site (VitePress)
scripts/            benchmark + quality fixtures
```

## Development

Prerequisites: Bun 1.0+, Go 1.26+, Rust stable, `task`, `wasm-pack`. Optional server-side tools: `cjxl`, `avifenc`, `avifdec`, `heif-dec`, `cwebp`.

`Taskfile.yml` is the primary entrypoint:

```bash
task --list
task bootstrap        # bun install + go work sync + cargo fetch
task dev              # api + orchestrator + worker + web
task dev:backend      # api + orchestrator + worker
task api              # Go HTTP API (default :8080)
task orchestrator     # Go scheduler/recovery daemon
task worker           # Rust compute-worker
task web              # Nuxt dev server (:3000)
task check            # go build / cargo check / vue-tsc
task test             # go test + cargo test + vue-tsc
task build:all        # server binaries + cargo release + wasm + ffi + web
```

Direct fallbacks:

```bash
go run ./server/cmd/api
go run ./server/cmd/worker
cargo run -p compute-worker
cargo test --workspace        # excludes compute-ffi (standalone crate)
go test ./server/...
cd web && bun run dev
bun run build:compute:wasm
bun run benchmark             # fixtures + container-header assertions + cargo test
bun run docs:dev              # docs site
```

## Conventions

- lowercase kebab-case directories; singular names for runtime boundaries (`server`, `web`, `worker`, `api`, `wasm`, `ffi`), plural for collections (`crates`, `bindings`, `components`, `pages`)
- Rust crates in `compute` are prefixed with `compute-`
- no physical foreign keys; UUID v7 primary keys; `snake_case` DB fields
- update the nearest doc when a boundary or subsystem changes

## Documentation map

- [Getting started](docs/getting-started.md) — layout, commands, ports, configuration
- [Architecture overview](docs/architecture/overview.md) — boundaries, components, data model
- [Request flows](docs/architecture/request-flows.md) · [Reliability](docs/architecture/reliability.md)
- [Contracts](docs/contracts/index.md) — HTTP API, Redis Streams queue, WASM, FFI
- [Color pipeline](docs/compute/color-pipeline.md) · [Metadata & alpha](docs/compute/metadata.md) · [Limitations](docs/compute/limitations.md)
- [Fidelity benchmark](docs/quality/fidelity-benchmark.md)
- [Engineering notes](docs/engineering-notes/index.md)

## License

MIT — see [LICENSE](LICENSE).
