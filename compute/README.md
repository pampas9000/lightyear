# Compute

Rust workspace for `lightyear`. It owns all media processing: portable codecs, color pipeline, host adapters, browser/native bindings, and the async worker.

Current pipeline behavior is documented in the docs site: [Color pipeline](../docs/compute/color-pipeline.md), [Metadata & alpha](../docs/compute/metadata.md), [Limitations](../docs/compute/limitations.md). Queue payloads are mirrored in [Queue](../docs/contracts/queue.md).

Note: the Cargo workspace root lives at `../Cargo.toml`. `compute/bindings/ffi` is a standalone crate and is deliberately **excluded** from workspace builds until a native consumer exists.

## Directory layout

```text
compute/
├── crates/
│   ├── types/            # compute-types: shared domain types + Redis Streams protocol
│   ├── engine/           # compute-engine: portable codec/color/edit core (WASM-safe)
│   └── server-adapter/   # compute-server-adapter: run_transcode host pipeline
├── bindings/
│   ├── wasm/             # compute-wasm: browser exports (wasm-bindgen)
│   └── ffi/              # compute-ffi: C ABI exports (experimental, standalone)
└── worker/               # compute-worker: stateless Redis/S3 consumer
```

## Crate responsibilities

### `compute-types`

Shared schemas and transport-safe models: `Format`, `Params` / `LibavifParams` / `LibjxlParams`, the color model (`ColorEncoding`, `ColorSpace`, `ProfilePolicy`, `ToneMappingOperator`, `ChromaSubsampling`), `ImageEditOp`, and `protocol.rs` (stream keys + `ComputePayload` / `ResultPayload`). Native YUV layout matching is private to the server adapter.

### `compute-engine`

Portable codec and transformation logic. Stays usable from WASM and native targets; no server-only dependencies and no C linkage.

- `color/`: transfer functions (sRGB/BT.709/HLG/PQ + HLG OOTF), YUV planes + Y4M/PAM serialization, chroma detection, ICC containers, EXIF orientation, output pipeline
- `edit.rs`: transfer-aware linear-light edit pipeline with `fast_image_resize`
- `dither.rs` / `tonemap.rs`: Blue Noise dithering and ACES Filmic tone mapping
- `lib.rs`: `load_from_memory` / `encode`; JXL and HEIC encoding are intentionally unavailable here

### `compute-server-adapter`

Host-only pipeline. `run_transcode` caches tool availability for the process lifetime, detects input format by magic bytes, then chooses JPEG→JXL bypass, native YUV passthrough, or the pixel path (decode → resolve output color → edits → tone map/dither → encode). Restart the worker after changing installed tools.

- Pixel transforms, AVIF CICP and JXL hints share one output-color decision; conflicting explicit CICP is rejected.
- Source ICC stays in memory and is written to a temporary file only for CLIs that need a pathname. Tone-mapped pixels never reuse the source ICC.
- Tone-mapped wide-gamut output currently requires AVIF or JXL. Other targets fail instead of emitting an incorrectly tagged image.

- Host CLIs: `cjxl`, `avifenc`, `avifdec`, `heif-dec`, `cwebp` (`djxl` is probed/tests only)
- Falls back to the portable engine when CLIs are absent (`strict=true` fails instead)
- Not WASM-safe and not used by the browser bindings

### `compute-wasm`

Thin browser binding layer. Exposes six functions (`browser_convert`, `browser_convert_advanced`, `browser_edit_image`, `browser_edit_to_raw_rgba`, `browser_decode_raw_pixels`, `browser_get_metadata`); JXL encoding is delegated to `@jsquash/jxl` in the web worker. See the [WASM contract](../docs/contracts/wasm.md).

### `compute-ffi`

Thin C ABI layer, currently experimental: `transcoder_version`, `transcoder_process` (decode + re-encode, quality fixed at 80) and `transcoder_free_buffer`. See the [FFI contract](../docs/contracts/ffi.md).

### `compute-worker`

Runtime boundary for asynchronous jobs. It is stateless with respect to the database.

- [x] Consumes `transcoder:compute:stream` (`transcoder:compute:group`) with XAUTOCLAIM first, claiming one job per execution slot
- [x] Downloads input from S3/R2, uploads output, writes `transcoder:results:stream`
- [x] Validates payloads, resolves target format and color params, calls `run_transcode`
- [x] Sends `PROCESSING` heartbeats every 30s and honors cancellation keys
- [x] Reports output size, dimensions, mime type in result metadata

## Dependency direction

`compute-types` ← `compute-engine` ← `compute-wasm` / `compute-ffi` / `compute-server-adapter` / `compute-worker`.

## Commands

Run from the repository root:

```bash
task test:compute          # or: cargo test --workspace
task build:compute         # or: cargo build --workspace --release
task build:compute:wasm    # wasm-pack -> web/public/wasm
task build:compute:ffi     # cargo build -p compute-ffi --release
task worker                # cargo run -p compute-worker
```

External CLI tools unlock the full-fidelity paths (10-bit, ICC, native passthrough, Brunsli bypass). Without them the adapter degrades gracefully, and CLI-gated e2e tests print a skip.
