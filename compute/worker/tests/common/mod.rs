#![allow(dead_code)]

use compute_imageops::Settings;
use compute_types::ImageOptions;
use compute_types::protocol::Target;
use compute_worker::process::{ComputeProcess, Request};
use std::path::Path;

pub fn fixture(path: &str) -> Vec<u8> {
    let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../../scripts/fixtures/");
    std::fs::read(format!("{root}{path}")).unwrap_or_else(|e| panic!("{path}: {e}"))
}

pub fn options(json: &str) -> ImageOptions {
    serde_json::from_str(json).unwrap()
}

pub fn spawn(source: Vec<u8>, options: ImageOptions, target: Option<Target>) -> ComputeProcess {
    let request = Request::new(options, target, &Settings::default());
    ComputeProcess::spawn(
        Path::new(env!("CARGO_BIN_EXE_compute-worker")),
        &request,
        source,
    )
    .expect("compute process starts")
}

/// An encode that takes far longer than any test waits: a 4K photo as AVIF at
/// effort 10 (aom speed 0), the case from the 2026-11 end-to-end run.
pub fn slow_encode() -> ComputeProcess {
    spawn(
        fixture("hd_benchmark/landscape_mountains_4k.jpg"),
        options(r#"{"format":"avif","effort":10}"#),
        None,
    )
}

/// Whether a process or (with a negative id) a process group still exists.
pub fn exists(id: i32) -> bool {
    unsafe { libc::kill(id, 0) == 0 }
}
