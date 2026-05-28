# Contracts

This document defines the canonical cross-component contracts for the `transcoder` monorepo.

It covers four boundaries:

- `web` -> `server`
- `server` -> `compute/worker`
- external apps -> `compute/bindings/wasm`
- external apps -> `compute/bindings/ffi`

It also identifies which contracts are already implemented in code and which are the repo-wide conventions to implement next.

## Status

Current implementation status in the repository:

- Implemented: core Rust domain types in `compute/crates/types`
- Implemented: WebAssembly entrypoint `browser_convert` in `compute/bindings/wasm/src/lib.rs`
- Implemented: FFI entrypoint `compute_version` in `compute/bindings/ffi/src/lib.rs`
- Implemented: HTTP API surface for Users, Jobs, Tasks, Workflows, and Files in `server`
- Implemented: OAuth integration (GitHub, Google) and Session-based authentication
- Planned: complete queue and storage transport implementation
- Planned: richer FFI surface for Flutter, SwiftUI, and other native clients

## Contract Ownership

Canonical ownership is split as follows:

| Area | Owner | Notes |
| --- | --- | --- |
| HTTP request and response shapes | `server` | `server` owns public API DTOs and versioning |
| Queue payloads and task lifecycle records | `server` | `server` owns transport-level contracts and persistence format |
| Media domain types | `compute/crates/types` | Shared Rust source of truth for format, options, and in-memory jobs |
| Browser-facing binding surface | `compute/bindings/wasm` | Exposes JS/WASM-safe functions and adapts to core types |
| Native app binding surface | `compute/bindings/ffi` | Exposes stable C ABI-compatible functions |
| UI-only state | `web` | Not part of the canonical backend contract |

## Naming and Serialization Rules

These rules apply to every cross-process or cross-language contract.

- API versioning uses path-based versions such as `/api/v1/...`
- JSON field names use `snake_case`
- Primary Keys use UUID v7 (represented as string/UUID in JSON)
- Timestamps use RFC 3339 in UTC
- Enum values are lowercase strings at HTTP and queue boundaries
- Binary payloads should not be embedded directly in JSON for large files; pass object references or upload handles instead
- Additive optional fields are allowed in `v1`; breaking changes require a new version

## Canonical Domain Types

The canonical in-process Rust domain types live in `compute/crates/types/src/lib.rs`.

### `Format`

Supported format variants:

- `JXL`
- `AVIF`
- `WEBP`
- `JPEG`
- `HEIC`
- `HEIF`
- `PNG`
- `MP4`
- `MOV`
- `MKV`
- `FLV`

External string representation should be:

| Rust | External |
| --- | --- |
| `JXL` | `jxl` |
| `AVIF` | `avif` |
| `WEBP` | `webp` |
| `JPEG` | `jpeg` |
| `HEIC` | `heic` |
| `HEIF` | `heif` |
| `PNG` | `png` |
| `MP4` | `mp4` |
| `MOV` | `mov` |
| `MKV` | `mkv` |
| `FLV` | `flv` |

### `EncodeOptions`

Current shape:

- `quality: u8`

Contract rules:

- recommended range is `0..=100`
- semantics must stay stable within `v1`
- future fields must be optional to remain backward compatible

### `Job`

Current in-memory Rust shape:

- `id: String`
- `data: Vec<u8>`
- `target_format: Format`
- `options: Option<EncodeOptions>`

Important note:

- `Job` is the in-process compute contract
- `Job` is not the recommended HTTP or queue wire format because `data: Vec<u8>` does not scale as a transport contract

## Transport Contract v1

The transport contract is the normalized shape that `server` should use over HTTP, queue, and storage boundaries.


Recommended response body:

- `task_id`: string
- `status`: `queued` or `running`
- `contract_version`: `v1`
- `accepted_at`: RFC 3339 timestamp
- `poll_url`: string
- `result_url`: optional string

### Task Status

Current status values in `server/internal/models`:

- `PENDING`
- `PROCESSING`
- `COMPLETED`
- `FAILED`
- `CANCELLED`

Recommended task record fields (mapping to `models.Task` and `models.Job`):

- `id`: UUID v7
- `status`: string
- `owner_id`: UUID
- `target_format`: string
- `params`: JSON string/object
- `progress`: integer (0-100)
- `error_message`: optional string
- `created_at`: timestamp
- `updated_at`: timestamp

### Error Contract

Recommended error shape across API and worker boundaries:

- `code`: stable machine-readable string
- `message`: human-readable message
- `retryable`: boolean
- `details`: optional object

Recommended error code families:

- `invalid_request`
- `unsupported_format`
- `decode_failed`
- `encode_failed`
- `source_unavailable`
- `storage_failed`
- `internal_error`

## Queue Contract v1

`server` should publish a transport envelope that `compute/worker` can resolve into an in-memory `Job`.

Recommended queue payload fields:

- `contract_version`: `v1`
- `task_id`: string
- `trace_id`: optional string
- `input_ref`
  - `kind`: `object`, `upload`, or `url`
  - `value`: source pointer
  - `mime_type`: optional string
  - `checksum_sha256`: optional string
- `target_format`: external format string
- `options`
  - `quality`: optional integer
- `submitted_at`: timestamp
- `requested_by`: optional string or subject identifier

Normalization rule inside the worker:

- resolve `input_ref` to bytes
- map `target_format` into `compute_types::Format`
- build `compute_types::Job`
- execute routing logic in `compute/worker`

## Authentication Contract

The system uses Session-based authentication and OAuth 2.0.

### OAuth Flow

- **Providers**: `github`, `google`
- **Callback URL**: `/api/v1/auth/callback/:provider`
- **Success Redirect**: Configured via `FRONTEND_URL` environment variable

### Session Management

- **Storage**: Redis
- **Cookie Name**: `session_id` (or similar, managed by Fiber session middleware)
- **Transport**: HttpOnly, Secure cookies

## Artifact Reference Contract

Whenever `server` or `worker` returns a durable output, use an artifact reference rather than embedding bytes in API responses.

Recommended fields:

- `provider`: `local`, `s3`, `r2`, `gcs`, or another backend identifier
- `bucket`: optional string
- `key`: string
- `mime_type`: string
- `size_bytes`: optional integer
- `checksum_sha256`: optional string
- `public_url`: optional string
- `expires_at`: optional timestamp

## WASM Boundary Contract

The browser-facing contract is defined in `compute/bindings/wasm/src/lib.rs`.

Current exported functions:

1. `browser_convert(data, format, quality) -> Result<Vec<u8>, JsValue>`
   - Converts raw input bytes (`data`) into a specific target `format` with compression `quality` (range `1..=100`).
2. `browser_get_metadata(data) -> Result<String, JsValue>`
   - Inspects the image header of raw bytes (`data`) and returns a JSON string metadata schema:
     ```json
     {
       "width": number,
       "height": number,
       "format": string,
       "size": number
     }
     ```
3. `browser_edit_image(data, ops_json, format, quality) -> Result<Vec<u8>, JsValue>`
   - Applies a list of transformations defined by the `ops_json` array payload (e.g. resizes, rotations, flips, grayscale, contrast/brightness adjustments, blur) and encodes the output.

Current accepted format strings at the WASM boundary:
- `jxl` (JPEG XL)
- `avif`
- `webp`
- `heic`
- `jfif` / `jpg` / `jpeg` (normalize to `JPEG`)
- `png`

Supported operations in `browser_edit_image` JSON array (`ops_json`):
- **Resize**: `{"type": "resize", "width": number, "height": number, "filter": "nearest" | "triangle" | "catmull-rom" | "gaussian" | "lanczos3"}`
- **Rotate**: `{"type": "rotate", "degree": 90 | 180 | 270}`
- **Flip**: `{"type": "flip", "direction": "h" | "v"}`
- **Grayscale**: `{"type": "grayscale"}`
- **Blur**: `{"type": "blur", "sigma": number}`
- **Adjust**: `{"type": "adjust", "brightness": number, "contrast": number}`

Current implementation status:
- `compute-engine` fully succeeds for `webp`, `jpeg`, `png`, `avif`, and `jxl` in pure Rust.
- `heic` is accepted by the WASM adapter but currently returns an unsupported-format error if not compiled with specific feature flags.

WASM compatibility rules:
- Keep `browser_convert`, `browser_get_metadata`, and `browser_edit_image` signatures stable for `v1`.
- Add new exported functions instead of changing existing argument order or result semantics.
- Changing string format values or editing operation JSON schemas is considered a breaking change.
- Changing return ownership or binary encoding is breaking.

## FFI Boundary Contract

The native app-facing ABI lives in `compute/bindings/ffi`.

Current exported symbol:

- `compute_version() -> *const c_char`

Current contract rules:

- exported symbols must remain C ABI-compatible
- ownership rules must be explicit for any allocated memory returned across the boundary
- new symbols are additive
- changing symbol names, signatures, or memory ownership rules is breaking

Planned FFI expansion should follow this pattern:

- explicit create and destroy pairs for opaque handles
- pointer plus length pairs for byte buffers
- separate error retrieval or explicit result structs
- no Rust-specific types in the public ABI

## Execution Routing Contract

The worker currently routes work as follows:

- image-like targets such as `JXL`, `AVIF`, `WEBP`, `JPEG`, `PNG`, and `HEIC` go through `compute-engine`
- `MP4` is reserved for `compute-server-adapter`
- other formats fall back to shell execution

Important status note:

- `compute-server-adapter` is currently a placeholder
- shell fallback is currently a placeholder
- only the `compute-engine` path is partially implemented today

This means the routing contract exists, but the execution support is not yet complete for all formats.

## Compatibility Rules

These rules apply to all contract surfaces.

### Backward-compatible changes

Allowed within `v1`:

- adding optional fields
- adding new response metadata fields
- expanding error `details`
- adding new non-defaulted internal implementation paths

### Breaking changes

Require a new version:

- renaming or removing fields
- making an optional field required
- changing field meaning
- changing enum string values
- changing binary ownership or layout in FFI
- changing function signatures in WASM or FFI

### Enum evolution

`Format` deserves special handling:

- adding a new `Format` variant is safe inside the repo when all consumers are updated together
- adding a new public format string at HTTP, queue, WASM, or FFI boundaries is a breaking contract change for clients that match exhaustively
- treat public `Format` expansion as versioned contract work

## Recommended Next Steps

To keep contracts enforceable, the repository should next adopt:

- explicit API DTOs in `server`
- generated or shared schema tests for `web` and `server`
- queue envelope structs that map cleanly to `compute_types`
- a versioned artifact reference model
- conformance tests for `compute-wasm` and future `compute-ffi` exports

Until those are implemented, this document is the canonical contract specification for `v1`.