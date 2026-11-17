//! Cancelling stops the computation itself, not just the wait for it. Kept in
//! its own test binary so that no other test's child processes are counted.

mod common;

use common::{exists, slow_encode};
use compute_worker::error::ComputeError;
use std::sync::atomic::AtomicU8;
use std::time::{Duration, Instant};

fn children_of_this_process() -> Vec<String> {
    let out = std::process::Command::new("pgrep")
        .arg("-P")
        .arg(std::process::id().to_string())
        .output()
        .expect("pgrep runs");
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .map(str::to_owned)
        .collect()
}

#[tokio::test]
async fn cancel_kills_the_encode_and_returns_only_once_it_is_gone() {
    let (cancel, mut cancelled) = tokio::sync::watch::channel(false);
    let progress = AtomicU8::new(0);
    let child = slow_encode();
    let pid = child.pid() as i32;

    // Let the encode get going: one compute process and nothing else.
    tokio::time::sleep(Duration::from_secs(2)).await;
    assert!(exists(pid), "the encode should still be running");
    assert_eq!(children_of_this_process(), vec![pid.to_string()]);

    let finished = tokio::spawn(async move { child.finish(&mut cancelled, &progress).await });
    let started = Instant::now();
    cancel.send(true).unwrap();
    let outcome = finished.await.unwrap();
    let took = started.elapsed();

    assert!(
        matches!(outcome, Err(ComputeError::Cancelled)),
        "{outcome:?}"
    );
    assert!(took < Duration::from_secs(2), "cancel took {took:?}");
    // Returned only after the child and its whole process group were gone, so
    // the worker cannot start a second encode next to this one.
    assert!(!exists(pid), "compute process {pid} survived the cancel");
    assert!(!exists(-pid), "process group {pid} survived the cancel");
    assert!(children_of_this_process().is_empty());
}
