//! Host-side adapters for the compute layer.
//!
//! Owns capabilities that cannot run in WASM and are specific to a host
//! environment: probing and driving external encoder/decoder CLIs (`cjxl`,
//! `avifenc`, `avifdec`, `heif-dec`) and composing them with the portable
//! `compute-engine` into a full transcode pipeline.

mod transcode;

pub use transcode::run_transcode;
