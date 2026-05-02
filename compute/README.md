# Compute

The `compute` directory contains the Rust-based media compute layer for the monorepo.

It is responsible for:

- portable transcoding and codec logic
- browser-facing WebAssembly bindings
- native FFI bindings for mobile and desktop clients
- worker-side job execution

Note: the Cargo workspace root lives at `../Cargo.toml`. The code in this directory is one logical subsystem inside the root Rust workspace.

## Directory Layout

- `bindings/wasm`  
  Browser-facing WebAssembly package built with `wasm-bindgen` and `wasm-pack`.

- `bindings/ffi`  
  Native FFI package that exposes a C-compatible ABI for embedding in Flutter, SwiftUI, or other native clients.

- `crates/types`  
  Shared domain types such as formats, jobs, and encode options. This is the contract crate used across the compute layer.

- `crates/engine`  
  Portable Rust compute core. This crate should stay reusable across WASM, FFI, and worker contexts.

- `crates/server-adapter`  
  Server-only or native integration layer for capabilities that are not suitable for WASM, such as FFmpeg bindings or other host-side adapters.

- `worker`  
  Job execution runtime. It receives jobs, selects the appropriate execution strategy, and invokes the portable engine or server adapter.

## Crate Responsibilities

### `compute-types`

Owns shared schemas and transport-safe models.

Use this crate for:

- job payload definitions
- encode and transcode options
- format enums
- types shared between bindings, worker, and adapters

### `compute-engine`

Owns portable codec and transformation logic.

Rules:

- keep it usable from WASM and native targets
- avoid server-only dependencies here
- prefer pure Rust implementations when possible

### `compute-server-adapter`

Owns host-only extensions.

Use this crate for:

- FFmpeg bindings
- native media tool adapters
- server-side acceleration paths
- functionality that cannot run in the browser

### `compute-wasm`

Thin binding layer for browser consumers.

Use this crate to:

- expose stable browser-facing APIs
- convert JavaScript inputs into `compute-types`
- delegate actual processing to `compute-engine`

### `compute-ffi`

Thin binding layer for native clients.

Use this crate to:

- expose a C ABI
- provide FFI-safe function boundaries
- wrap `compute-engine` for mobile and desktop consumers

### `compute-worker`

Runtime execution boundary for asynchronous jobs.

Use this crate to:

- [x] Accept queued jobs via Redis `BRPOP`
- [x] Fetch and update job status via **SeaORM**
- [ ] Choose the processing strategy
- [ ] Call `compute-engine`, `compute-server-adapter`, or shell-based fallbacks

## Naming Convention

All Rust crates in this subsystem use the `compute-*` prefix:

- `compute-types`
- `compute-engine`
- `compute-server-adapter`
- `compute-wasm`
- `compute-ffi`
- `compute-worker`

This keeps ownership and dependency direction obvious across the monorepo.

## Dependency Direction

Keep dependencies flowing inward:

- bindings and worker depend on core crates
- `compute-engine` depends on `compute-types`
- `compute-server-adapter` depends on `compute-types`
- `compute-types` should stay at the bottom and avoid depending on other compute crates

In practice:

- `compute-types` <- `compute-engine`
- `compute-types` <- `compute-server-adapter`
- `compute-engine` <- `compute-wasm`
- `compute-engine` <- `compute-ffi`
- `compute-engine` and `compute-server-adapter` <- `compute-worker`

## Working With This Layer

Run commands from the repository root.

If you are using the monorepo task runner, prefer these `just` recipes:

### Common `just` commands

    just test-compute
    just build-compute
    just dev-worker
    just build-compute-ffi
    just build-compute-wasm

You can also invoke the underlying Rust and WASM commands directly:

### Test everything

    cargo test --workspace

### Build all Rust crates

    cargo build --workspace --release

### Run the worker

    cargo run -p compute-worker

### Build the FFI library

    cargo build -p compute-ffi --release

### Build the WASM package

    wasm-pack build compute/bindings/wasm --target web

## Output Artifacts

- Rust build outputs go to the root `target/` directory
- WASM package output is generated under `compute/bindings/wasm/pkg`

## Design Rules

- Put all shared payloads and transport contracts in `compute-types`
- Keep `compute-engine` portable and reusable
- Keep `compute-server-adapter` host-specific
- Keep `compute-wasm` and `compute-ffi` thin
- Keep orchestration logic in `compute-worker`, not in the bindings
- Add new crates only when they define a clear boundary

## Future Growth

This layout is designed to support:

- more codecs in the portable engine
- stronger native server integrations
- stable mobile bindings
- reusable contracts across backend, worker, and client runtimes

When adding new functionality, prefer extending the existing boundaries before introducing a new crate.