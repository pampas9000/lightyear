//! Transcoder Worker Runtime.
//! 
//! This module coordinates the high-level job pipeline: polling messages from the
//! `QueueAdapter`, resolving payloads, downloading sources, calling core transcoding
//! routines, uploading deliverables, publishing success/failure metadata, and
//! handling acknowledgements and recovery actions.

use anyhow::Result;
use std::time::Duration;
use crate::queue::QueueAdapter;
use crate::protocol::{ComputePayload, ResultPayload, SCHEMA_VERSION};
use crate::handler::handle_compute;
use crate::redis_connection;

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
            let mut entries = Vec::new();

            // 1. Try to claim stale messages from other crashed consumers first (idle for > 60 seconds)
            match self.queue_adapter.auto_claim_jobs(60000, &claim_id, &self.consumer_name).await {
                Ok(reply) => {
                    claim_id = reply.next_stream_id;
                    if !reply.claimed.is_empty() {
                        println!("XAUTOCLAIM: Claimed {} stale job(s) starting from ID {}", reply.claimed.len(), claim_id);
                        entries = reply.claimed;
                    }
                }
                Err(e) => {
                    // Log but don't abort, fall through to main read logic which handles reconnection
                    eprintln!("Redis auto_claim error (will try to reconnect in main read): {}", e);
                }
            }

            // 2. If no stale messages were claimed, poll the stream for new/pending messages
            if entries.is_empty() {
                let result = self.queue_adapter.read_jobs(read_id, &self.consumer_name).await;

                match result {
                    Ok(reply) => {
                        let mut processed_any = false;
                        for stream in reply.keys {
                            if !stream.ids.is_empty() {
                                entries = stream.ids;
                                processed_any = true;
                                break;
                            }
                        }

                        // Dynamically transition consumer reading state to prevent busy loops
                        if !processed_any {
                            if read_id == "0" {
                                // Switch to reading new messages if no pending items exist
                                read_id = ">";
                            } else {
                                // Switch back to checking pending messages and sleep to avoid CPU spinning
                                read_id = "0";
                                tokio::time::sleep(Duration::from_millis(1000)).await;
                            }
                            continue; // Skip processing and loop again
                        }
                    }
                    Err(e) => {
                        eprintln!("Redis read error in compute worker: {}", e);
                        tokio::time::sleep(Duration::from_secs(1)).await;
                        
                        // Recover connection dynamically using retry helper
                        let new_conn = redis_connection::connect_with_retry(&self.redis_client).await;
                        self.queue_adapter.update_connection(new_conn);
                        continue; // Retry the loop iteration with the new connection
                    }
                }
            }

            // 3. Process the collected messages
            for entry in entries {
                let payload_str: String = entry.get("payload").unwrap_or_default();
                
                // Check for blank entries and ACK them immediately to clear queue clutter
                if payload_str.is_empty() {
                    println!("Received empty payload field in stream entry: {}", entry.id);
                    let _ = self.queue_adapter.ack_job(&entry.id).await;
                    continue;
                }

                // Deserialize DTO payload parameters
                let payload: ComputePayload = match serde_json::from_str(&payload_str) {
                    Ok(p) => p,
                    Err(e) => {
                        eprintln!("Failed to parse JSON payload {}: {}", payload_str, e);
                        let _ = self.queue_adapter.ack_job(&entry.id).await;
                        continue;
                    }
                };

                println!(
                    "Processing compute request: job_id={}, attempt_id={}",
                    payload.job_id, payload.attempt_id
                );

                // Spawn a background task for periodic heartbeats (PROCESSING status updates)
                let heartbeat_job_id = payload.job_id.clone();
                let heartbeat_attempt_id = payload.attempt_id.clone();
                let mut heartbeat_adapter = self.queue_adapter.clone();
                
                let (heartbeat_tx, mut heartbeat_rx) = tokio::sync::oneshot::channel::<()>();
                
                let heartbeat_handle = tokio::spawn(async move {
                    let mut interval = tokio::time::interval(Duration::from_secs(30));
                    // Skip the first immediate tick
                    interval.tick().await;
                    
                    loop {
                        tokio::select! {
                            _ = interval.tick() => {
                                let hb_payload = ResultPayload {
                                    schema_version: SCHEMA_VERSION.to_string(),
                                    job_id: heartbeat_job_id.clone(),
                                    attempt_id: heartbeat_attempt_id.clone(),
                                    status: "PROCESSING".to_string(),
                                    error_message: None,
                                    progress: Some(0), // Keep progress 0 or intermediate value
                                    metadata: serde_json::json!({}),
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

                // Execute local transcode request in sandboxed directory
                let process_res = handle_compute(
                    &self.s3_client,
                    &self.s3_bucket,
                    &payload
                ).await;

                // Stop the heartbeat background task
                let _ = heartbeat_tx.send(());
                let _ = heartbeat_handle.await;

                // Package outcome payload DTO details
                let result_payload = match process_res {
                    Ok(metadata) => ResultPayload {
                        schema_version: SCHEMA_VERSION.to_string(),
                        job_id: payload.job_id.clone(),
                        attempt_id: payload.attempt_id.clone(),
                        status: "COMPLETED".to_string(),
                        error_message: None,
                        progress: Some(100),
                        metadata,
                    },
                    Err(err) => {
                        eprintln!("Transcoding failed for job {}: {}", payload.job_id, err);
                        ResultPayload {
                            schema_version: SCHEMA_VERSION.to_string(),
                            job_id: payload.job_id.clone(),
                            attempt_id: payload.attempt_id.clone(),
                            status: "FAILED".to_string(),
                            error_message: Some(err.to_string()),
                            progress: Some(0),
                            metadata: serde_json::Value::Null,
                        }
                    }
                };

                // Deliver transcode outcome feedback to results queue
                match self.queue_adapter.publish_result(&result_payload).await {
                    Ok(_) => {
                        // ACK job stream message upon confirmation
                        let _ = self.queue_adapter.ack_job(&entry.id).await;
                    }
                    Err(e) => {
                        eprintln!("Failed to publish transcode result to results stream: {}", e);
                    }
                }
            }
        }
    }
}
