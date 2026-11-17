//! The compute child gives the same results and errors as an in-process call.

mod common;

use common::{exists, fixture, options, slow_encode, spawn};
use compute_imageops::Settings;
use compute_types::protocol::Target;
use compute_worker::error::ComputeError;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};

fn in_process(
    source: &[u8],
    json: &str,
    target: Option<&Target>,
) -> Result<compute_optimize::Outcome, compute_optimize::Error> {
    compute_optimize::optimize(
        source,
        &options(json),
        target,
        &Settings::default(),
        &AtomicBool::new(false),
        |_| {},
    )
}

async fn in_child(
    source: Vec<u8>,
    json: &str,
    target: Option<Target>,
    progress: &AtomicU8,
) -> Result<compute_worker::process::Computed, ComputeError> {
    let (_cancel, mut cancelled) = tokio::sync::watch::channel(false);
    spawn(source, options(json), target)
        .finish(&mut cancelled, progress)
        .await
}

#[tokio::test]
async fn single_encode_matches_in_process() {
    let source = fixture("quality/checker_rb_444.png");
    let json = r#"{"format":"webp","resize":{"width":32}}"#;
    let expected = in_process(&source, json, None).unwrap();
    let got = in_child(source, json, None, &AtomicU8::new(0))
        .await
        .unwrap();
    assert_eq!(got.bytes, expected.output.bytes);
    assert_eq!(got.mime, expected.output.mime);
    assert_eq!(
        (got.width, got.height),
        (expected.output.width, expected.output.height)
    );
    assert_eq!(got.resolved_options, expected.resolved_options);
    assert_eq!(got.encode_rounds, 1);
}

#[tokio::test]
async fn target_search_matches_in_process_and_reports_progress() {
    let source = fixture("hd_benchmark/kodim23_eagle_feathers_2k.png");
    let json = r#"{"format":"webp"}"#;
    let target = Target {
        max_bytes: Some(60_000),
    };
    let expected = in_process(&source, json, Some(&target)).unwrap();
    let progress = AtomicU8::new(0);
    let got = in_child(source, json, Some(target), &progress)
        .await
        .unwrap();
    assert_eq!(got.bytes, expected.output.bytes);
    assert_eq!(got.resolved_options, expected.resolved_options);
    assert_eq!(got.target_met, expected.target_met);
    assert_eq!(got.achieved, expected.achieved);
    assert_eq!(got.encode_rounds, expected.encode_rounds);
    assert!(got.encode_rounds > 1);
    assert!(progress.load(Ordering::Relaxed) > 0);
}

#[tokio::test]
async fn errors_keep_their_codes_and_messages() {
    let garbage = b"not an image".to_vec();
    let expected =
        ComputeError::from(in_process(&garbage, r#"{"format":"webp"}"#, None).unwrap_err());
    let got = in_child(garbage, r#"{"format":"webp"}"#, None, &AtomicU8::new(0))
        .await
        .unwrap_err();
    assert_eq!(
        (got.code(), got.to_string()),
        (expected.code(), expected.to_string())
    );

    let png = fixture("quality/checker_rb_444.png");
    let target = Target {
        max_bytes: Some(10_000),
    };
    let got = in_child(png, r#"{"format":"png"}"#, Some(target), &AtomicU8::new(0))
        .await
        .unwrap_err();
    assert_eq!(got.code(), "invalid_request");
}

#[tokio::test]
async fn a_child_that_dies_is_a_processing_failure() {
    let child = slow_encode();
    let pid = child.pid() as i32;
    unsafe { libc::kill(pid, libc::SIGKILL) };
    let (_cancel, mut cancelled) = tokio::sync::watch::channel(false);
    let err = child
        .finish(&mut cancelled, &AtomicU8::new(0))
        .await
        .unwrap_err();
    assert_eq!(err.code(), "processing_failed", "{err}");
    assert!(!exists(pid));
}
