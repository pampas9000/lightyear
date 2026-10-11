//! Redis Streams access for the worker.

use compute_types::protocol::{
    CANCEL_KEY_PREFIX, JobResult, QUEUE_IMAGE, QUEUE_RESULTS, RESULTS_MAX_LEN, WORKER_GROUP,
};
use redis::AsyncCommands;
use redis::streams::{StreamReadOptions, StreamReadReply};

#[derive(Clone)]
pub struct Queue {
    conn: redis::aio::MultiplexedConnection,
    jobs: String,
    results: String,
}

impl Queue {
    pub fn new(conn: redis::aio::MultiplexedConnection) -> Self {
        Self::with_streams(conn, QUEUE_IMAGE, QUEUE_RESULTS)
    }

    pub fn with_streams(
        conn: redis::aio::MultiplexedConnection,
        jobs: &str,
        results: &str,
    ) -> Self {
        Self {
            conn,
            jobs: jobs.into(),
            results: results.into(),
        }
    }

    pub fn set_connection(&mut self, conn: redis::aio::MultiplexedConnection) {
        self.conn = conn;
    }

    /// Creates the consumer group, starting from the beginning of the stream so
    /// dispatches sent before any worker existed are still delivered.
    pub async fn ensure_group(&mut self) -> redis::RedisResult<()> {
        match self
            .conn
            .xgroup_create_mkstream::<_, _, _, ()>(&self.jobs, WORKER_GROUP, "0")
            .await
        {
            Err(e) if e.code() == Some("BUSYGROUP") => Ok(()),
            other => other,
        }
    }

    /// Waits up to `block_ms` for the next dispatch. `NOACK`: delivery is the
    /// acknowledgement, so a crashed worker leaves nothing pending in Redis;
    /// the orchestrator re-sends attempts that never start.
    pub async fn next(
        &mut self,
        consumer: &str,
        block_ms: usize,
    ) -> redis::RedisResult<Option<String>> {
        let opts = StreamReadOptions::default()
            .group(WORKER_GROUP, consumer)
            .noack()
            .count(1)
            .block(block_ms);
        let reply: StreamReadReply = self
            .conn
            .xread_options(&[&self.jobs], &[">"], &opts)
            .await?;
        Ok(reply
            .keys
            .into_iter()
            .flat_map(|stream| stream.ids)
            .next()
            .map(|entry| entry.get::<String>("payload").unwrap_or_default()))
    }

    pub async fn publish(&mut self, result: &JobResult) -> anyhow::Result<()> {
        let payload = serde_json::to_string(result)?;
        let _: String = self
            .conn
            .xadd_maxlen(
                &self.results,
                redis::streams::StreamMaxlen::Approx(RESULTS_MAX_LEN),
                "*",
                &[("payload", payload)],
            )
            .await?;
        Ok(())
    }

    pub async fn is_cancelled(&mut self, attempt_id: &str) -> redis::RedisResult<bool> {
        self.conn
            .exists(format!("{CANCEL_KEY_PREFIX}{attempt_id}"))
            .await
    }
}
