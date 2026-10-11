//! Transcoder Worker Runtime.
//!
//! This module coordinates the high-level job pipeline: polling messages from the
//! `QueueAdapter`, resolving payloads, downloading sources, calling core transcoding
//! routines, uploading deliverables, publishing success/failure metadata, and
//! handling acknowledgements and recovery actions.

use crate::error::ComputeError;
use crate::handler::handle_compute;
use crate::queue::QueueAdapter;
use crate::redis_connection;
use anyhow::Result;
use compute_types::protocol::Artifact;
use compute_types::{ComputePayload, ResultPayload, SCHEMA_VERSION};
use redis::streams::StreamId;
use std::time::Duration;

/// Worker runtime coordinating the polling, execution, and feedback loops.
pub struct Worker {
    /// The primary Redis client, kept to rebuild connections during recovery.
    redis_client: redis::Client,
    /// Adapter abstraction for Redis Stream interactions.
    queue_adapter: QueueAdapter,
    /// AWS S3 Client session.
    s3_client: aws_sdk_s3::Client,
    /// Default S3 bucket name.
    s3_bucket: String,
    /// Unique identifier for this worker node (consumer ID).
    consumer_name: String,
}

impl Worker {
    /// Constructs a new `Worker` instance.
    /// Generates a unique consumer name combining the host name, process ID, and a UUID slice.
    pub fn new(
        redis_client: redis::Client,
        queue_adapter: QueueAdapter,
        s3_client: aws_sdk_s3::Client,
        s3_bucket: String,
    ) -> Self {
        let hostname = std::env::var("HOSTNAME").unwrap_or_else(|_| "unknown".to_string());
        let pid = std::process::id();
        let rand_uuid = uuid::Uuid::new_v4().to_string();
        let consumer_name = format!("compute-{}-{}-{}", hostname, pid, &rand_uuid[0..8]);

        Self {
            redis_client,
            queue_adapter,
            s3_client,
            s3_bucket,
            consumer_name,
        }
    }

    /// Launches the main consumer group stream polling and execution loops.
    pub async fn run(&mut self) -> Result<()> {
        println!("Consumer Name: {}", self.consumer_name);

        // Ensure the consumer stream group is set up in Redis
        self.queue_adapter.setup_stream_group().await?;

        // Initialize state variables for auto-claim (XAUTOCLAIM) and reading (XREADGROUP)
        let mut claim_id = "0-0".to_string();
        let mut read_id = "0";

        loop {
            let Some(entries) = self.poll_entries(&mut claim_id, &mut read_id).await else {
                continue;
            };

            for entry in entries {
                if let Some(payload) = self.parse_entry(&entry).await {
                    self.process_job(&entry.id, payload).await;
                }
            }
        }
    }

    /// Collects the next batch of stream entries to process.
    ///
    /// First tries to claim stale entries from crashed consumers, then falls back
    /// to reading new/pending entries for this consumer. Returns `None` when
    /// there is nothing to process, after updating the read cursor and applying
    /// any needed backoff.
    async fn poll_entries(
        &mut self,
        claim_id: &mut String,
        read_id: &mut &'static str,
    ) -> Option<Vec<StreamId>> {
        // 1. Try to claim stale messages from other crashed consumers first (idle for > 60 seconds)
        match self
            .queue_adapter
            .auto_claim_jobs(60000, claim_id, &self.consumer_name)
            .await
        {
            Ok(reply) => {
                *claim_id = reply.next_stream_id;
                if !reply.claimed.is_empty() {
                    println!(
                        "XAUTOCLAIM: Claimed {} stale job(s) starting from ID {}",
                        reply.claimed.len(),
                        claim_id
                    );
                    return Some(reply.claimed);
                }
            }
            Err(e) => {
                // Log but don't abort, fall through to main read logic which handles reconnection
                eprintln!(
                    "Redis auto_claim error (will try to reconnect in main read): {}",
                    e
                );
            }
        }

        // 2. If no stale messages were claimed, poll the stream for new/pending messages
        match self
            .queue_adapter
            .read_jobs(read_id, &self.consumer_name)
            .await
        {
            Ok(reply) => {
                for stream in reply.keys {
                    if !stream.ids.is_empty() {
                        return Some(stream.ids);
                    }
                }

                // Dynamically transition consumer reading state to prevent busy loops
                if *read_id == "0" {
                    // Switch to reading new messages if no pending items exist
                    *read_id = ">";
                } else {
                    // Switch back to checking pending messages and sleep to avoid CPU spinning
                    *read_id = "0";
                    tokio::time::sleep(Duration::from_millis(1000)).await;
                }
                None
            }
            Err(e) => {
                eprintln!("Redis read error in compute worker: {}", e);
                tokio::time::sleep(Duration::from_secs(1)).await;

                // Recover connection dynamically using retry helper
                let new_conn = redis_connection::connect_with_retry(&self.redis_client).await;
                self.queue_adapter.update_connection(new_conn);
                None
            }
        }
    }

    /// Extracts and deserializes the payload of a stream entry.
    ///
    /// Empty payloads and unparseable JSON are acknowledged and dropped so they
    /// do not clog the pending entries list.
    async fn parse_entry(&mut self, entry: &StreamId) -> Option<ComputePayload> {
        let payload_str: String = entry.get("payload").unwrap_or_default();

        // Check for blank entries and ACK them immediately to clear queue clutter
        if payload_str.is_empty() {
            println!("Received empty payload field in stream entry: {}", entry.id);
            let _ = self.queue_adapter.ack_job(&entry.id).await;
            return None;
        }

        // Deserialize DTO payload parameters
        match serde_json::from_str::<ComputePayload>(&payload_str) {
            Ok(payload)
                if payload.schema_version == SCHEMA_VERSION
                    && uuid::Uuid::parse_str(&payload.job_id).is_ok()
                    && uuid::Uuid::parse_str(&payload.attempt_id).is_ok() =>
            {
                Some(payload)
            }
            Ok(_) => {
                eprintln!("Unsupported schema or invalid execution identifiers");
                let _ = self.queue_adapter.ack_job(&entry.id).await;
                None
            }
            Err(e) => {
                eprintln!("Failed to parse JSON payload {}: {}", payload_str, e);
                let _ = self.queue_adapter.ack_job(&entry.id).await;
                None
            }
        }
    }

    /// Runs one compute job end to end while keeping its lease renewed.
    async fn process_job(&mut self, entry_id: &str, payload: ComputePayload) {
        println!(
            "Processing compute request: job_id={}, attempt_id={}",
            payload.job_id, payload.attempt_id
        );

        // Check if job was cancelled before beginning work
        match self.queue_adapter.is_job_cancelled(&payload).await {
            Err(err) => {
                eprintln!("Failed to check cancellation: {err}");
                return;
            }
            Ok(true) => {
                println!(
                    "Job {} was cancelled before execution, skipping",
                    payload.job_id
                );
                let _ = self.queue_adapter.ack_job(entry_id).await;
                return;
            }
            Ok(false) => {}
        }
        match self
            .queue_adapter
            .claim_attempt(entry_id, &payload.attempt_id, &self.consumer_name)
            .await
        {
            Ok(1) => {}
            Ok(-1) => return,
            Ok(_) => {
                tokio::time::sleep(Duration::from_secs(1)).await;
                return;
            }
            Err(err) => {
                eprintln!("Failed to claim attempt: {err}");
                return;
            }
        }

        // Spawn a background task for periodic heartbeats (PROCESSING status updates)
        let (heartbeat_tx, heartbeat_handle, mut lease_lost) =
            self.spawn_heartbeat(entry_id, &payload);

        // Execute local transcode request in sandboxed directory. The wait is
        // raced against a cancellation poll so an abandoned job stops burning
        // resources promptly: dropping the compute future tears down the
        // sandbox, and every external CLI is spawned with kill_on_drop, so a
        // running avifenc/cjxl dies with it instead of running to completion.
        let s3_client = self.s3_client.clone();
        let s3_bucket = self.s3_bucket.clone();
        let mut cancel_adapter = self.queue_adapter.clone();
        let cancel_payload = payload.clone();
        let cancel_poller = async move {
            loop {
                tokio::time::sleep(Duration::from_secs(3)).await;
                if cancel_adapter
                    .is_job_cancelled(&cancel_payload)
                    .await
                    .unwrap_or(true)
                {
                    break;
                }
            }
        };

        let process_res = tokio::select! {
            biased;
            res = handle_compute(&s3_client, &s3_bucket, &payload) => res,
            () = cancel_poller => {
                println!(
                    "Job {} was cancelled during execution; aborting in-flight work",
                    payload.job_id
                );
                let _ = heartbeat_tx.send(());
                let _ = heartbeat_handle.await;
                let _ = self.queue_adapter.release_attempt(entry_id, &payload.attempt_id, &self.consumer_name).await;
                let _ = self.queue_adapter.ack_job(entry_id).await;
                return;
            }
            _ = &mut lease_lost => {
                eprintln!("Lost execution ownership for attempt {}", payload.attempt_id);
                let _ = heartbeat_tx.send(());
                let _ = heartbeat_handle.await;
                return;
            }
        };

        // Check again if job was cancelled while transcoding
        if self
            .queue_adapter
            .is_job_cancelled(&payload)
            .await
            .unwrap_or(true)
        {
            println!(
                "Job {} was cancelled after compute finished, skipping result publish",
                payload.job_id
            );
            let _ = heartbeat_tx.send(());
            let _ = heartbeat_handle.await;
            let _ = self
                .queue_adapter
                .release_attempt(entry_id, &payload.attempt_id, &self.consumer_name)
                .await;
            let _ = self.queue_adapter.ack_job(entry_id).await;
            return;
        }

        // Package outcome payload DTO details
        let result_payload = build_result_payload(&payload, process_res);

        // Keep the computed outcome and its lease while publication is retried.
        // Cache, result publication and XACK are one Redis operation.
        loop {
            match self
                .queue_adapter
                .finish_attempt(entry_id, &self.consumer_name, &result_payload)
                .await
            {
                Ok(_) => break,
                Err(err) => {
                    eprintln!("Failed to publish result: {err}");
                    tokio::select! {
                        _ = tokio::time::sleep(Duration::from_secs(1)) => {}
                        _ = &mut lease_lost => break,
                    }
                }
            }
        }
        let _ = heartbeat_tx.send(());
        let _ = heartbeat_handle.await;
    }

    /// Spawns a background task that renews the job lease every 30 seconds by
    /// publishing PROCESSING updates. The returned sender stops the task.
    fn spawn_heartbeat(
        &self,
        entry_id: &str,
        payload: &ComputePayload,
    ) -> (
        tokio::sync::oneshot::Sender<()>,
        tokio::task::JoinHandle<()>,
        tokio::sync::oneshot::Receiver<()>,
    ) {
        let heartbeat_job_id = payload.job_id.clone();
        let heartbeat_attempt_id = payload.attempt_id.clone();
        let heartbeat_entry_id = entry_id.to_string();
        let heartbeat_consumer_name = self.consumer_name.clone();
        let heartbeat_payload = payload.clone();
        let mut heartbeat_adapter = self.queue_adapter.clone();

        let (heartbeat_tx, mut heartbeat_rx) = tokio::sync::oneshot::channel::<()>();
        let (lease_lost_tx, lease_lost_rx) = tokio::sync::oneshot::channel::<()>();

        let heartbeat_handle = tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(30));
            loop {
                tokio::select! {
                    _ = interval.tick() => {
                        match heartbeat_adapter
                            .renew_job_lease(&heartbeat_entry_id, &heartbeat_attempt_id, &heartbeat_consumer_name)
                            .await
                        {
                            Ok(true) => {}
                            other => {
                                eprintln!("Execution lease lost for job {heartbeat_job_id}: {other:?}");
                                let _ = lease_lost_tx.send(());
                                return;
                            }
                        }

                        // Check cancellation during long-running heartbeats
                        if heartbeat_adapter.is_job_cancelled(&heartbeat_payload).await.unwrap_or(true) {
                            println!("Job {} was cancelled during execution", heartbeat_job_id);
                            let _ = lease_lost_tx.send(());
                            return;
                        }

                        let hb_payload = ResultPayload {
                            schema_version: SCHEMA_VERSION.to_string(),
                            job_id: heartbeat_job_id.clone(),
                            attempt_id: heartbeat_attempt_id.clone(),
                            status: "PROCESSING".to_string(),
                            error_message: None,
                            error_code: None,
                            retryable: None,
                            progress: Some(0), // Keep progress 0 or intermediate value
                            metadata: serde_json::json!({}),
                            artifacts: Vec::new(),
                        };
                        if let Err(e) = heartbeat_adapter.publish_result(&hb_payload).await {
                            eprintln!("Failed to publish heartbeat for job {}: {}", heartbeat_job_id, e);
                        } else {
                            println!("Sent heartbeat (lease renewal) for job {}", heartbeat_job_id);
                        }
                    }
                    _ = &mut heartbeat_rx => {
                        break;
                    }
                }
            }
        });

        (heartbeat_tx, heartbeat_handle, lease_lost_rx)
    }
}

/// Packages a compute outcome into the result payload published to the results stream.
fn build_result_payload(
    payload: &ComputePayload,
    process_res: Result<serde_json::Value, ComputeError>,
) -> ResultPayload {
    match process_res {
        Ok(metadata) => {
            let artifacts = payload
                .output_file_id
                .as_ref()
                .map(|file_id| Artifact {
                    file_id: file_id.clone(),
                    path: payload.output_path.clone(),
                    size: metadata["output_size_bytes"].as_u64().unwrap_or(0),
                    mime_type: metadata["mime_type"].as_str().unwrap_or("").to_string(),
                    width: metadata["width"].as_u64().unwrap_or(0) as u32,
                    height: metadata["height"].as_u64().unwrap_or(0) as u32,
                })
                .into_iter()
                .collect();
            ResultPayload {
                schema_version: SCHEMA_VERSION.to_string(),
                job_id: payload.job_id.clone(),
                attempt_id: payload.attempt_id.clone(),
                status: "COMPLETED".to_string(),
                error_message: None,
                error_code: None,
                retryable: None,
                progress: Some(100),
                metadata,
                artifacts,
            }
        }
        Err(err) => {
            eprintln!(
                "Transcoding failed for job {}: {} [{}]",
                payload.job_id,
                err,
                err.error_code()
            );
            ResultPayload {
                schema_version: SCHEMA_VERSION.to_string(),
                job_id: payload.job_id.clone(),
                attempt_id: payload.attempt_id.clone(),
                status: "FAILED".to_string(),
                error_message: Some(err.to_string()),
                error_code: Some(err.error_code().to_string()),
                retryable: Some(err.is_retryable()),
                progress: Some(0),
                metadata: serde_json::Value::Null,
                artifacts: Vec::new(),
            }
        }
    }
}
