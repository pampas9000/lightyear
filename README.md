# lightyear

lightyear is an image processing platform: upload images, describe the output you want once (`ImageOptions`: format, quality, resize, color, metadata), and get the results asynchronously. A Go service orchestrates, a Rust worker processes with libvips, and the Nuxt console has an in-browser sandbox running the same libvips through wasm-vips.

Architecture, contracts and design notes live in the [documentation site](docs/index.md) (Chinese).

## Architecture at a glance

```text
web ──HTTP──▶ API (Go) ──▶ PostgreSQL (source of truth) ◀── orchestrator (Go)
                │                                            │ XADD q:image   ▲ q:results
                └── S3 / R2 (uploads, outputs)               ▼                │
                                                 image worker (Rust + libvips, no DB access)

browser ──Web Worker──▶ wasm-vips (same libvips; AVIF/HEIF and JXL modules loaded on demand)
```

- `server`: Fiber v3 HTTP API and the orchestrator. PostgreSQL owns all state; the orchestrator claims due jobs from the database, writes an attempt (which doubles as the outbox), and publishes it to Redis. Leases on attempts recover lost messages and dead workers.
- `compute`: `compute-types` defines `ImageOptions` and the queue protocol; `compute-imageops` maps options onto libvips and guards inputs (pixel limits, untrusted loaders, PQ/HLG rejection); `compute-worker` is a stateless Redis/S3 consumer.
- `web`: Nuxt 4 console. Business requests are proxied to the API; the sandbox runs `app/lib/vips-ops.ts`, a line-by-line twin of `compute-imageops`, in a Web Worker.

## Browser sandbox

Drag-and-drop queue, per-file options, side-by-side comparison and batch download, all local.

![Browser sandbox](docs/public/assets/local-wasm-transcoding.avif)

It needs cross-origin isolation (`SharedArrayBuffer`), which Safari cannot provide with COEP `credentialless`, and wasm-vips has no HEVC, so HEIC is server-only. See [limitations](docs/compute/limitations.md).

## Development

Prerequisites: Bun, Node ≥ 23.6, Go 1.26+, Rust nightly (pinned by `rust-toolchain.toml`), `task`, libvips ≥ 8.17 with libheif/libjxl/libultrahdr, libavif ≥ 1.1 with aom, and the libjxl tools (`cjxl`, `djxl`). On macOS: `brew install vips libavif jpeg-xl pkgconf go-task`. `task doctor` checks them.

```bash
task bootstrap         # bun install + go work sync + cargo fetch
cp .env.example .env   # DATABASE_URL, REDIS_URL, S3_*
task dev               # API + orchestrator + worker + web (:3000)
task check             # Go build, cargo check, vue-tsc, contracts up to date
task test              # go test + cargo test + vue-tsc
task test:fixtures     # every fixture × every output format
task test:consistency  # server vs browser artifacts
```

After an incompatible schema change, rebuild a development database with `go run ./server/cmd/migrate -reset` (drops all application tables).

## Repository layout

```text
server/    Go: cmd/{api,worker,migrate}, internal/{api,imageoptions,models,schema,services,task}
compute/   Rust: crates/types, crates/imageops, worker
web/       Nuxt 4 app (app/ directory); sandbox on wasm-vips
docs/      VitePress site; contracts/ holds the JSON Schema and shared fixtures
scripts/   consistency test and image fixtures
```

## Conventions

- lowercase kebab-case directories; Rust crates in `compute` are prefixed with `compute-`
- UUID v7 primary keys, no physical foreign keys, `snake_case` columns and JSON fields
- `ImageOptions` is defined once in Rust; the JSON Schema and TS types are generated (`task gen:types`), Go runs the shared fixtures
- update the nearest doc when a boundary changes; plans and known issues go to [the roadmap](docs/roadmap.md)

## License

MIT — see [LICENSE](LICENSE).
