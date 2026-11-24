//! Worker loop: receive a dispatch, report RUNNING, keep heart-beating while
//! the job runs, publish the outcome.
//!
//! Ownership lives in the orchestrator's database lease. The worker only has
//! to keep beating while it works and to stop when its attempt is cancelled.
//!
//! On shutdown (see `shutdown.rs`) it reads no further dispatches; a running
//! job gets the grace period to finish and report, and is otherwise stopped
//! like a cancelled one and reported as `worker_shutdown` (retryable).

use crate::error::ComputeError;
use crate::job::{self, Context};
use crate::queue::Queue;
use crate::redis_connection;
use crate::shutdown::Shutdown;
use compute_types::protocol::{Dispatch, JobResult, ResultError, Status, VERSION};
use std::sync::Arc;
use std::sync::atomic::{AtomicU8, Ordering};
use std::time::Duration;

/// Must stay well under the orchestrator's lease (two minutes).
const HEARTBEAT: Duration = Duration::from_secs(20);
const CANCEL_POLL: Duration = Duration::from_secs(3);
/// How long to keep retrying a result publication before giving up; the lease
/// then expires and the orchestrator schedules another attempt.
const PUBLISH_DEADLINE: Duration = Duration::from_secs(90);
/// How long one read waits for a dispatch. Short, because the shutdown flag
/// is checked between reads: a pending read is never abandoned (see
/// `shutdown.rs`), so this bounds how long an idle worker takes to stop.
const READ_BLOCK_MS: usize = 1500;

pub struct Worker {
    redis: redis::Client,
    queue: Queue,
    ctx: Context,
    consumer: String,
    shutdown: Shutdown,
    grace: Duration,
}

/// Why the watcher stopped a running job.
enum Stop {
    /// The attempt was cancelled: stop without reporting.
    Cancelled,
    /// The shutdown grace period ran out: stop and report `worker_shutdown`.
    Shutdown,
}

impl Worker {
    pub fn new(
        redis: redis::Client,
        queue: Queue,
        ctx: Context,
        shutdown: Shutdown,
        grace: Duration,
    ) -> Self {
        let host = std::env::var("HOSTNAME").unwrap_or_else(|_| "unknown".into());
        let consumer = format!(
            "worker-{host}-{}-{}",
            std::process::id(),
            &uuid::Uuid::new_v4().to_string()[..8]
        );
        Self {
            redis,
            queue,
            ctx,
            consumer,
            shutdown,
            grace,
        }
    }

    /// Reads and runs dispatches until shutdown is requested; returns once
    /// the job in hand (if any) has finished or been stopped and reported.
    pub async fn run(&mut self) -> anyhow::Result<()> {
        println!("Consumer: {}", self.consumer);
        self.queue.ensure_group().await?;
        while !self.shutdown.requested() {
            match self.queue.next(&self.consumer, READ_BLOCK_MS).await {
                Ok(Some(raw)) => {
                    if let Some(dispatch) = parse(&raw) {
                        self.handle(dispatch).await;
                    }
                }
                Ok(None) => {}
                // The stream or group vanished (Redis restarted without
                // persistence): recreate it; lost dispatches are re-sent by
                // the orchestrator.
                Err(e) if e.code() == Some("NOGROUP") => {
                    eprintln!("Consumer group missing; recreating");
                    if let Err(e) = self.queue.ensure_group().await {
                        eprintln!("Recreating consumer group failed: {e}");
                        tokio::time::sleep(Duration::from_secs(1)).await;
                    }
                }
                Err(e) => {
                    eprintln!("Redis read failed: {e}; reconnecting");
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    // Unlike a read, connecting may be abandoned.
                    let mut shutdown = self.shutdown.clone();
                    tokio::select! {
                        conn = redis_connection::connect_with_retry(&self.redis) => {
                            self.queue.set_connection(conn);
                        }
                        () = shutdown.wait() => {}
                    }
                }
            }
        }
        println!("Worker stopped");
        Ok(())
    }

    async fn handle(&mut self, dispatch: Dispatch) {
        let attempt = dispatch.attempt_id.clone();
        if self.queue.is_cancelled(&attempt).await.unwrap_or(false) {
            println!("Attempt {attempt} was cancelled before it started");
            return;
        }
        println!("Running job {} attempt {attempt}", dispatch.job_id);
        let mut running = JobResult::for_dispatch(&dispatch, Status::Running);
        running.progress = Some(0);
        if let Err(e) = self.queue.publish(&running).await {
            eprintln!("Failed to report start of {attempt}: {e}");
        }

        let (cancel, cancelled) = tokio::sync::watch::channel(false);
        let progress = Arc::new(AtomicU8::new(0));
        // Set once a source URL has been fetched and stored. Reported at once
        // and on every later message, so that a retry does not fetch again.
        let (fetched, mut fetch_reports) = tokio::sync::watch::channel(None);
        let fetch_outcome = fetch_reports.clone();
        let mut beats = self.queue.clone();
        let watcher_progress = progress.clone();
        let grace = self.shutdown.clone().grace_expired(self.grace);
        let watcher = async {
            tokio::pin!(grace);
            let mut heartbeat = tokio::time::interval(HEARTBEAT);
            heartbeat.tick().await;
            let mut poll = tokio::time::interval(CANCEL_POLL);
            let mut fetching = true;
            loop {
                tokio::select! {
                    _ = heartbeat.tick() => {
                        running.progress = Some(watcher_progress.load(Ordering::Relaxed));
                        if let Err(e) = beats.publish(&running).await {
                            eprintln!("Heartbeat for {attempt} failed: {e}");
                        }
                    }
                    _ = poll.tick() => {
                        if beats.is_cancelled(&attempt).await.unwrap_or(false) {
                            let _ = cancel.send(true);
                            return Stop::Cancelled;
                        }
                    }
                    () = &mut grace => {
                        eprintln!("Attempt {attempt} outlived the shutdown grace period; stopping it");
                        let _ = cancel.send(true);
                        return Stop::Shutdown;
                    }
                    changed = fetch_reports.changed(), if fetching => {
                        if changed.is_err() {
                            fetching = false;
                            continue;
                        }
                        running.source = fetch_reports.borrow_and_update().clone();
                        running.progress = Some(watcher_progress.load(Ordering::Relaxed));
                        if let Err(e) = beats.publish(&running).await {
                            eprintln!("Reporting the fetched source of {attempt} failed: {e}");
                        }
                    }
                }
            }
        };
        let job = job::run(&self.ctx, &dispatch, cancelled, progress, fetched);
        tokio::pin!(job);
        let outcome = tokio::select! {
            outcome = &mut job => outcome,
            stop = watcher => {
                // The job kills its compute process and returns once it has
                // been reaped, so the next message is read (or the worker
                // exits) only after this attempt has stopped using CPU.
                let outcome = job.await;
                match stop {
                    // Its outcome is not published.
                    Stop::Cancelled => {
                        println!("Attempt {attempt} was cancelled while running");
                        return;
                    }
                    Stop::Shutdown => after_shutdown(outcome),
                }
            }
        };

        let mut result = JobResult::for_dispatch(&dispatch, Status::Completed);
        result.source = fetch_outcome.borrow().clone();
        match outcome {
            Ok(done) => {
                result.progress = Some(100);
                result.artifact = Some(done.artifact);
                result.resolved_options = done.resolved_options;
                result.target_met = done.target_met;
                result.achieved = done.achieved;
                result.encode_rounds = Some(done.encode_rounds);
                result.compute_ms = Some(done.compute_ms);
            }
            Err(ComputeError::Cancelled) => {
                println!("Attempt {attempt} cancelled during processing");
                return;
            }
            Err(err) => {
                eprintln!("Attempt {attempt} failed: {err} [{}]", err.code());
                result.status = Status::Failed;
                result.error = Some(ResultError {
                    code: err.code().into(),
                    message: err.to_string(),
                    retryable: err.retryable(),
                });
            }
        }
        let deadline = tokio::time::Instant::now() + PUBLISH_DEADLINE;
        while let Err(e) = self.queue.publish(&result).await {
            if tokio::time::Instant::now() >= deadline {
                eprintln!("Giving up publishing the result of {attempt}: {e}");
                return;
            }
            eprintln!("Publishing the result of {attempt} failed: {e}; retrying");
            tokio::time::sleep(Duration::from_secs(1)).await;
            let conn = redis_connection::connect_with_retry(&self.redis).await;
            self.queue.set_connection(conn);
        }
        println!("Attempt {attempt}: {:?}", result.status);
    }
}

/// The outcome of a job stopped at the end of the shutdown grace period. One
/// that finished meanwhile (uploaded included) or failed on its own is
/// reported as such; one that was stopped becomes `worker_shutdown`, which
/// the orchestrator retries like any retryable failure.
fn after_shutdown(
    outcome: Result<job::RunOutcome, ComputeError>,
) -> Result<job::RunOutcome, ComputeError> {
    match outcome {
        Err(ComputeError::Cancelled) => Err(ComputeError::WorkerShutdown),
        other => other,
    }
}

/// Malformed dispatches are logged and dropped; the orchestrator times them out.
fn parse(raw: &str) -> Option<Dispatch> {
    match serde_json::from_str::<Dispatch>(raw) {
        Ok(d)
            if d.v == VERSION
                && uuid::Uuid::parse_str(&d.job_id).is_ok()
                && uuid::Uuid::parse_str(&d.attempt_id).is_ok() =>
        {
            Some(d)
        }
        Ok(d) => {
            eprintln!(
                "Dropping dispatch with unsupported version or ids: v={} job={}",
                d.v, d.job_id
            );
            None
        }
        Err(e) => {
            eprintln!("Dropping unparseable dispatch: {e}");
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_only_current_well_formed_dispatches() {
        let fixture = include_str!("../../../docs/contracts/fixtures/dispatch-v2.json");
        assert!(parse(fixture).is_some());
        let target_fixture =
            include_str!("../../../docs/contracts/fixtures/dispatch-target-v2.json");
        assert!(parse(target_fixture).is_some());
        assert!(parse(&fixture.replace("\"v\": 2", "\"v\": 1")).is_none());
        assert!(parse(&fixture.replace("0198a000-0000-7000-8000-000000000002", "nope")).is_none());
        assert!(parse("{}").is_none());
        // Options that fail to decode reject the whole dispatch.
        assert!(parse(&fixture.replace("\"format\": \"avif\"", "\"format\": \"gif\"")).is_none());
    }

    #[test]
    fn a_job_stopped_at_shutdown_is_a_retryable_worker_shutdown() {
        let err = after_shutdown(Err(ComputeError::Cancelled)).err().unwrap();
        assert_eq!(err.code(), "worker_shutdown");
        assert!(err.retryable());
        // A job that failed on its own keeps its own error.
        let err = after_shutdown(Err(ComputeError::ProcessingFailed("x".into())))
            .err()
            .unwrap();
        assert_eq!(err.code(), "processing_failed");
    }
}
