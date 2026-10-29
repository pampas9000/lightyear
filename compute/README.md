# Compute

Rust workspace for image processing. The Cargo workspace root is `../Cargo.toml`; the toolchain is nightly (`../rust-toolchain.toml`).

```text
compute/
├── crates/
│   ├── types/      # compute-types: ImageOptions (single source of truth) + queue protocol v2
│   └── imageops/   # compute-imageops: ImageOptions → libvips, input guards, cjxl bypass, libavif
└── worker/         # compute-worker: stateless Redis/S3 consumer
```

Dependency direction: `compute-types` ← `compute-imageops` ← `compute-worker`.

## `compute-types`

- `image_options.rs`: `ImageOptions` with validation and canonicalization. The JSON Schema in `docs/contracts/schemas/` is generated from it (`UPDATE_SCHEMAS=1 cargo test -p compute-types`, or `task gen:types` which also regenerates the TS types); `docs/contracts/fixtures/image-options.json` is shared with the Go mirror.
- `protocol.rs`: `Dispatch` / `JobResult`, stream and group names, cancel key prefix. Mirrored by `server/internal/task/queue.go`; both round-trip `docs/contracts/fixtures/{dispatch,results}-v2.json`.

## `compute-imageops`

`process(input, &options, &settings) -> Output` runs one conversion with libvips (via `rs-vips`, linked through pkg-config, libvips ≥ 8.17). It rejects oversized inputs before decoding, blocks libvips' untrusted loaders (re-allowing JPEG XL), refuses PQ/HLG sources that libvips would mislabel as sRGB, keeps UltraHDR gain maps for JPEG output, repacks JPEG → lossless JXL with `cjxl` so `djxl` restores the original bytes, and encodes AVIF with libavif (aom) through a small C shim, since libvips' `heifsave` disables aom's intra block copy; `WORKER_AVIF_ENCODER=libvips` switches back to `heifsave`. Design and libvips behaviors: [docs/compute/imageops.md](../docs/compute/imageops.md).

`examples/process.rs` runs one conversion from the command line; `scripts/consistency.ts` uses it to compare against the browser implementation.

## `compute-worker`

- Reads `q:image` (group `workers`) with `XREADGROUP … NOACK`: delivery is the acknowledgement; database leases in the orchestrator handle lost work.
- Downloads the source (limit `WORKER_MAX_SOURCE_BYTES`, default 200 MB), runs `compute-imageops` on a blocking thread (pixel limit `WORKER_MAX_PIXELS`), uploads to the dispatch's `output_key`, and reports to `q:results`.
- Sends `RUNNING` at start and every 20 s, checks `cancel:attempt:<id>` every 3 s, retries publishing a result for up to 90 s.
- Never connects to the database. Configuration: `REDIS_URL`, `S3_ENDPOINT`, `S3_ACCESS_ID`, `S3_ACCESS_KEY`, `S3_REGION`, `S3_BUCKET`.

Queue contract: [docs/contracts/queue.md](../docs/contracts/queue.md).

## Commands

From the repository root:

```bash
task test:compute       # cargo test --workspace (needs cjxl and djxl)
task test:fixtures      # every fixture × every format, release build
task test:consistency   # compare with the browser implementation (Node ≥ 23.6)
task worker             # cargo run -p compute-worker
task build:worker       # release binary
```
