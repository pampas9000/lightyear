//! Redis Queue Adapter for the compute worker.
//! 
//! This module encapsulates direct Redis Stream commands, keeping the rest of the
//! codebase decoupled from Redis-specific stream data structures and command invocations.

use anyhow::Result;
use redis::AsyncCommands;
use crate::protocol::{
    ResultPayload, COMPUTE_GROUP_NAME, COMPUTE_STREAM_KEY,
    RESULTS_STREAM_KEY,
};

/// Adapter wrapping a Redis multiplexed asynchronous connection to perform
/// stream-specific queue operations.
#[derive(Clone)]
pub struct QueueAdapter {
    /// Active multiplexed connection to Redis.
    conn: redis::aio::MultiplexedConnection,
}

impl QueueAdapter {
    /// Claims stale pending entries from the compute stream.
    ///
    /// # Parameters
    /// - `min_idle_time_ms`: Minimum duration a message must be idle to be claimed.
    /// - `start_id`: The ID to start scanning from (e.g. `"0-0"`).
    /// - `consumer_name`: The name of this consumer.
    pub async fn auto_claim_jobs(
        &mut self,
        min_idle_time_ms: usize,
        start_id: &str,
        consumer_name: &str,
    ) -> redis::RedisResult<redis::streams::StreamAutoClaimReply> {
        redis::cmd("XAUTOCLAIM")
            .arg(COMPUTE_STREAM_KEY)
            .arg(COMPUTE_GROUP_NAME)
            .arg(consumer_name)
            .arg(min_idle_time_ms)
            .arg(start_id)
            .query_async(&mut self.conn)
            .await
    }

    /// Constructs a new `QueueAdapter` with an active connection.
    pub fn new(conn: redis::aio::MultiplexedConnection) -> Self {
        Self { conn }
    }

    /// Replaces the active connection handle. Used during runtime connection recovery.
    pub fn update_connection(&mut self, conn: redis::aio::MultiplexedConnection) {
        self.conn = conn;
    }

    /// Creates the consumer group on the compute stream.
    /// Uses `MKSTREAM` to automatically create the stream if it does not already exist.
    pub async fn setup_stream_group(&mut self) -> Result<()> {
        let _: redis::RedisResult<()> = self.conn
            .xgroup_create_mkstream(COMPUTE_STREAM_KEY, COMPUTE_GROUP_NAME, "$")
            .await;
        Ok(())
    }

    /// Reads jobs from the compute stream for the specified consumer.
    ///
    /// # Parameters
    /// - `read_id`: The message ID to start reading from (`0` for pending, `>` for new).
    /// - `consumer_name`: The unique consumer node name.
    pub async fn read_jobs(
        &mut self,
        read_id: &str,
        consumer_name: &str,
    ) -> redis::RedisResult<redis::streams::StreamReadReply> {
        let opts = redis::streams::StreamReadOptions::default()
            .group(COMPUTE_GROUP_NAME, consumer_name)
            .count(1);

        self.conn
            .xread_options(&[COMPUTE_STREAM_KEY], &[read_id], &opts)
            .await
    }

    /// Acknowledges (XACK) a processed stream entry so it is removed from the PEL.
    pub async fn ack_job(&mut self, entry_id: &str) -> redis::RedisResult<()> {
        self.conn
            .xack(COMPUTE_STREAM_KEY, COMPUTE_GROUP_NAME, &[entry_id])
            .await
    }

    /// Publishes (XADD) a transcode outcome back to the results stream.
    pub async fn publish_result(&mut self, result_payload: &ResultPayload) -> Result<()> {
        let result_str = serde_json::to_string(result_payload)?;
        let _: redis::RedisResult<()> = self.conn
            .xadd(RESULTS_STREAM_KEY, "*", &[("payload", &result_str)])
            .await?;
        Ok(())
    }
}
