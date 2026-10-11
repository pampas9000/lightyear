//! Worker loop: receive a dispatch, report RUNNING, keep heart-beating while
//! the job runs, publish the outcome.
//!
//! Ownership lives in the orchestrator's database lease. The worker only has
//! to keep beating while it works and to stop when its attempt is cancelled.

use crate::job::{self, Context};
use crate::queue::Queue;
use crate::redis_connection;
use compute_types::protocol::{Dispatch, JobResult, ResultError, Status, VERSION};
use std::time::Duration;

/// Must stay well under the orchestrator's lease (two minutes).
const HEARTBEAT: Duration = Duration::from_secs(20);
const CANCEL_POLL: Duration = Duration::from_secs(3);
/// How long to keep retrying a result publication before giving up; the lease
/// then expires and the orchestrator schedules another attempt.
const PUBLISH_DEADLINE: Duration = Duration::from_secs(90);

pub struct Worker {
    redis: redis::Client,
    queue: Queue,
    ctx: Context,
    consumer: String,
}

impl Worker {
    pub fn new(redis: redis::Client, queue: Queue, ctx: Context) -> Self {
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
        }
    }

    pub async fn run(&mut self) -> anyhow::Result<()> {
        println!("Consumer: {}", self.consumer);
        self.queue.ensure_group().await?;
        loop {
            match self.queue.next(&self.consumer, 5000).await {
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
                    let conn = redis_connection::connect_with_retry(&self.redis).await;
                    self.queue.set_connection(conn);
                }
            }
        }
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

        let mut beats = self.queue.clone();
        let watcher = async {
            let mut heartbeat = tokio::time::interval(HEARTBEAT);
            heartbeat.tick().await;
            let mut poll = tokio::time::interval(CANCEL_POLL);
            loop {
                tokio::select! {
                    _ = heartbeat.tick() => {
                        if let Err(e) = beats.publish(&running).await {
                            eprintln!("Heartbeat for {attempt} failed: {e}");
                        }
                    }
                    _ = poll.tick() => {
                        if beats.is_cancelled(&attempt).await.unwrap_or(false) {
                            return;
                        }
                    }
                }
            }
        };
        let outcome = tokio::select! {
            outcome = job::run(&self.ctx, &dispatch) => outcome,
            () = watcher => {
                // Dropping the job future stops downloads and uploads; a libvips
                // call already running finishes in the background, unpublished.
                println!("Attempt {attempt} was cancelled while running");
                return;
            }
        };

        let mut result = JobResult::for_dispatch(&dispatch, Status::Completed);
        match outcome {
            Ok(artifact) => {
                result.progress = Some(100);
                result.artifact = Some(artifact);
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
        assert!(parse(&fixture.replace("\"v\": 2", "\"v\": 1")).is_none());
        assert!(parse(&fixture.replace("0198a000-0000-7000-8000-000000000002", "nope")).is_none());
        assert!(parse("{}").is_none());
        // Options that fail to decode reject the whole dispatch.
        assert!(parse(&fixture.replace("\"format\": \"avif\"", "\"format\": \"gif\"")).is_none());
    }
}
