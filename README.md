# Transcoder

Transcoder is a multi-language monorepo for a media transcoding platform featuring a highly reliable Go orchestration server, high-performance pure Rust compute core, and an interactive browser-side WebAssembly playground.

### WebAssembly (WASM) Local Transcoding Sandbox

The platform features a state-of-the-art **WASM Local Transcoding Sandbox** allowing you to process, edit, and optimize images directly in your browser with **zero server costs** and **100% data privacy**:

![WASM Local Transcoding Sandbox](/Users/kazuha/dev/lightyear/assets/local-wasm-transcoding.avif)

**Key Capabilities:**
- ⚡ **Non-Blocking Processing**: Heavy media operations are offloaded to dedicated background **Web Workers**, ensuring the UI main thread remains completely responsive and lag-free.
- 📦 **Batch Queue Mode**: Drag-and-drop multiple files to manage a processing queue, selectively preview optimizations, and download all compressed files in bulk.
- 🎛️ **Draggable Quality Comparison**: Inspect and compare your changes side-by-side or using an interactive dragging slider with smooth pointer-tracking.
- 🎨 **Advanced Transform Pipeline**: Pure Rust codecs (`WebP`, `JPEG`, `PNG`, `AVIF`, and `JXL`) combined with high-fidelity rescaling (*Lanczos3*, *Catmull-Rom*), rotation, flipping, brightness/contrast adjustments, blur, and grayscale filters.

---

The repository is organized around three product boundaries:

- `server`: Go API service that accepts requests, creates jobs, and coordinates execution
- `compute`: Rust compute workspace that owns codecs, bindings, and worker execution
- `web`: Nuxt frontend for submitting and tracking transcoding jobs


## Repository Layout

    .
    |- server/
    |  |- cmd/
    |  |  `- api/
    |  `- internal/
    |     |- http/
    |     `- tasks/
    |- compute/
    |  |- crates/
    |  |  |- types/
    |  |  |- engine/
    |  |  `- server-adapter/
    |  |- bindings/
    |  |  |- wasm/
    |  |  `- ffi/
    |  `- worker/
    |- web/
    |  |- app/
    |  |  |- assets/
    |  |  |  `- styles/
    |  |  |- components/
    |  |  |  |- tasks/
    |  |  |  `- ui/
    |  |  |- composables/
    |  |  |- layouts/
    |  |  |- lib/
    |  |  |  `- api/
    |  |  |- pages/
    |  |  `- app.vue
    |  `- public/
    |- docs/
    |- Cargo.toml
    |- go.work
    `- package.json

## Root Control Files

The repo is controlled from the root with one manifest per ecosystem, and `justfile` is the primary developer entrypoint:

- `justfile`: primary monorepo task runner for bootstrap, dev, build, check, test, and clean flows
- `Cargo.toml`: Rust workspace root for all `compute` crates
- `go.work`: Go workspace root that includes `./server`
- `package.json`: bun workspace root and JavaScript workspace fallback for direct script execution

## Final Naming Conventions

These conventions apply across the repo:

- Use lowercase kebab-case for directory names
- Use singular names for deployable or runtime boundaries: `server`, `web`, `worker`, `api`, `wasm`, `ffi`
- Use plural names only for collections: `crates`, `bindings`, `components`, `pages`
- Use explicit, role-based names instead of vague names like `libs`, `common`, `misc`, or `ext`
- Prefix Rust crate package names in `compute` with `compute-` for a consistent public surface

Examples from this repo:

- `compute/crates/types`
- `compute/crates/engine`
- `compute/crates/server-adapter`
- `compute/bindings/wasm`
- `compute/bindings/ffi`
- `server/cmd/api`
- `server/internal/http`

## Responsibility Boundaries

Each top-level directory owns a clear slice of the system:

### `server`

The Go service (Fiber v3 + Ent) owns:

- HTTP APIs
- request validation
- task creation and job persistence via Ent ORM
- orchestration and scheduling
- queue (Redis) and storage integration
- coordination with worker execution

`server` should not implement codec logic directly. It should delegate transcoding work to `compute` and `compute/worker`.

### `compute`

The Rust workspace (SeaORM) owns:

- portable media encode/decode logic
- browser-safe WebAssembly exports
- native FFI exports for host apps
- background worker execution via Redis & SeaORM
- shared domain contracts for jobs and formats

The internal `compute` split is:

- `compute/crates/types`: shared media and job contracts
- `compute/crates/engine`: portable Rust codec and transform logic
- `compute/crates/server-adapter`: server-only native integrations such as FFmpeg bindings
- `compute/bindings/wasm`: browser-facing WebAssembly APIs
- `compute/bindings/ffi`: native bindings for Flutter, SwiftUI, or other host apps
- `compute/worker`: queued job execution

### `web`

The Nuxt app (Thin Client + Proxy) owns:

- user-facing pages
- task submission flows
- job status views
- API client integration (proxied via Nitro to Go)
- presentation logic and UI state

`web` talks to `server` via a transparent proxy. It does not possess direct database or Redis connections; all backend orchestration is delegated to the Fiber-based Go API.

It also hosts the interactive **WASM Local Transcoding Sandbox** featured at the top of this README, which enables zero-server-overhead, browser-side image processing and transcoding offloaded to background Web Workers.

## Rust Package Map

The current Rust package naming scheme is:

- `compute-types`: shared media and job contracts
- `compute-engine`: portable encoding and decoding engine
- `compute-server-adapter`: server-only native adapters
- `compute-wasm`: WebAssembly binding crate
- `compute-ffi`: native FFI binding crate
- `compute-worker`: background worker binary

When a media or job type must exist in multiple languages, treat `compute-types` as the source of truth for the media domain and mirror it carefully in Go and TypeScript.

## Web Conventions

The frontend follows Nuxt 4's `app/` directory structure:

- `app/app.vue`: root app shell
- `app/components/ui`: design-system or generated UI primitives
- `app/components/tasks`: task-related product components
- `app/composables`: reusable frontend state and behavior
- `app/lib/api`: API clients and transport helpers
- `app/layouts`: Nuxt layout boundaries
- `app/pages`: route-level views
- `app/assets/styles`: shared styling assets and Tailwind theme tokens
- `public`: static assets served as-is

Keep feature code separate from generated UI primitives. Do not place app-specific logic under `app/components/ui`.

## Server Conventions

The Go service follows these rules:

- `cmd/api`: binary entrypoint
- `internal/http`: HTTP transport layer and route wiring
- `internal/tasks`: task creation and orchestration logic

Prefer capability-oriented package names such as `http`, `tasks`, `queue`, `storage`, and `config` over generic layer names.

## Development Workflow

Prerequisites:

- Bun 1.0+
- Go 1.26+
- Rust stable
- `wasm-pack` for WebAssembly builds

Primary monorepo entrypoint:

    just help
    just bootstrap
    just dev-web
    just dev-server
    just dev-worker
    just check
    just test
    just build-all

Direct toolchain fallbacks:

    bun install
    bun run dev:web
    bun run dev:server
    cargo run -p compute-worker
    cargo test --workspace
    go test ./server/...
    cd web && bunx nuxt typecheck
    bun run build:compute:wasm

`package.json` still provides shared bun scripts such as `bun run dev:web`, `bun run dev:server`, and `bun run build:compute:wasm`, but `justfile` is the primary root orchestration layer for monorepo tasks.
Use `justfile` for normal day-to-day monorepo work. Reach for the raw bun, Go, and Cargo commands when you need a narrower ecosystem-specific command or debugging flow.

## README and Docs Standards

Each major boundary should document the same topics in the same order:

1. Purpose
2. Directory layout
3. Run and build commands
4. Configuration and environment variables
5. Interfaces it owns or consumes

Documentation locations are reserved as follows:

- `README.md`: repo overview and conventions
- `server/README.md`: API service workflow and boundary ownership
- `compute/README.md`: compute workspace map and artifact outputs
- `web/README.md`: frontend workflow and integration surface
- `docs/architecture.md`: end-to-end system flow
- `docs/contracts.md`: API payloads, job schemas, and compatibility rules

## High-Level System Flow

    web -> server -> compute-worker -> compute-engine
                       |               |
                       |               |- compute-server-adapter
                       |               |- compute-wasm
                       |               `- compute-ffi
                       `-> task creation and orchestration

## Contribution Rules

- Add a new top-level directory only when it represents a distinct product or deployable boundary
- Put reusable Rust code under `compute/crates`
- Put exported host-specific surfaces under `compute/bindings`
- Keep generated artifacts and build outputs out of version control
- Update the nearest README or `docs/` entry whenever you introduce a new subsystem or change a boundary
- This README defines the final monorepo shape. New code should follow these boundaries and naming rules unless there is a strong reason to revise the architecture.

## License

This project is licensed under the MIT License - see the [LICENSE](file:///Users/kazuha/dev/lightyear/LICENSE) file for details.