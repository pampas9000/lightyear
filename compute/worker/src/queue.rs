//! Redis Queue Adapter for the compute worker.
//!
//! This module encapsulates direct Redis Stream commands, keeping the rest of the
//! codebase decoupled from Redis-specific stream data structures and command invocations.

use anyhow::Result;
use compute_types::{
    COMPUTE_GROUP_NAME, COMPUTE_STREAM_KEY, ComputePayload, RESULTS_STREAM_KEY, ResultPayload,
};
use redis::AsyncCommands;

/// Adapter wrapping a Redis multiplexed asynchronous connection to perform
/// stream-specific queue operations.
#[derive(Clone)]
pub struct QueueAdapter {
    /// Active multiplexed connection to Redis.
    conn: redis::aio::MultiplexedConnection,
    compute_stream: String,
    compute_group: String,
    results_stream: String,
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
            .arg(&self.compute_stream)
            .arg(&self.compute_group)
            .arg(consumer_name)
            .arg(min_idle_time_ms)
            .arg(start_id)
            // The worker executes one job at a time and only renews that job.
            .arg("COUNT")
            .arg(1)
            .query_async(&mut self.conn)
            .await
    }

    /// Constructs a new `QueueAdapter` with an active connection.
    pub fn new(conn: redis::aio::MultiplexedConnection) -> Self {
        Self::with_streams(
            conn,
            COMPUTE_STREAM_KEY.to_string(),
            COMPUTE_GROUP_NAME.to_string(),
            RESULTS_STREAM_KEY.to_string(),
        )
    }

    pub fn with_streams(
        conn: redis::aio::MultiplexedConnection,
        compute_stream: String,
        compute_group: String,
        results_stream: String,
    ) -> Self {
        Self {
            conn,
            compute_stream,
            compute_group,
            results_stream,
        }
    }

    /// Replaces the active connection handle. Used during runtime connection recovery.
    pub fn update_connection(&mut self, conn: redis::aio::MultiplexedConnection) {
        self.conn = conn;
    }

    /// Creates the consumer group on the compute stream.
    /// Uses `MKSTREAM` to automatically create the stream if it does not already exist.
    pub async fn setup_stream_group(&mut self) -> Result<()> {
        let result: redis::RedisResult<()> = self
            .conn
            .xgroup_create_mkstream(&self.compute_stream, &self.compute_group, "0")
            .await;
        if let Err(err) = result {
            if !err.to_string().starts_with("BUSYGROUP") {
                return Err(err.into());
            }
        }
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
            .group(&self.compute_group, consumer_name)
            .count(1);

        self.conn
            .xread_options(&[&self.compute_stream], &[read_id], &opts)
            .await
    }

    /// Acknowledges (XACK) a processed stream entry so it is removed from the PEL.
    pub async fn ack_job(&mut self, entry_id: &str) -> redis::RedisResult<()> {
        self.conn
            .xack(&self.compute_stream, &self.compute_group, &[entry_id])
            .await
    }

    /// Claims one attempt across duplicate stream entries.
    pub async fn claim_attempt(
        &mut self,
        entry_id: &str,
        attempt_id: &str,
        consumer_name: &str,
    ) -> redis::RedisResult<i32> {
        // Cached outcomes are replayed without recomputing. An attempt lock also
        // covers duplicate stream entries produced by an outbox commit crash.
        redis::cmd("EVAL")
            .arg(
                r#"
                local cached = redis.call('GET', KEYS[2])
                if cached then
                    redis.call('XADD', KEYS[4], 'MAXLEN', '~', 100000, '*', 'payload', cached)
                    redis.call('XACK', KEYS[3], ARGV[2], ARGV[3])
                    return -1
                end
                if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', 90000) then return 1 end
                return 0
            "#,
            )
            .arg(4)
            .arg(format!("transcoder:attempt:lock:{attempt_id}"))
            .arg(format!("transcoder:attempt:result:{attempt_id}"))
            .arg(&self.compute_stream)
            .arg(&self.results_stream)
            .arg(format!("{consumer_name}:{entry_id}"))
            .arg(&self.compute_group)
            .arg(entry_id)
            .query_async(&mut self.conn)
            .await
    }

    pub async fn renew_job_lease(
        &mut self,
        entry_id: &str,
        attempt_id: &str,
        consumer_name: &str,
    ) -> redis::RedisResult<bool> {
        let renewed: i32 = redis::cmd("EVAL")
            .arg(
                r#"
                if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
                local pending = redis.call('XPENDING', KEYS[2], ARGV[2], ARGV[3], ARGV[3], 1)
                if #pending == 0 or pending[1][2] ~= ARGV[4] then return 0 end
                redis.call('XCLAIM', KEYS[2], ARGV[2], ARGV[4], 0, ARGV[3], 'JUSTID')
                redis.call('PEXPIRE', KEYS[1], 90000)
                return 1
            "#,
            )
            .arg(2)
            .arg(format!("transcoder:attempt:lock:{attempt_id}"))
            .arg(&self.compute_stream)
            .arg(format!("{consumer_name}:{entry_id}"))
            .arg(&self.compute_group)
            .arg(entry_id)
            .arg(consumer_name)
            .query_async(&mut self.conn)
            .await?;
        Ok(renewed == 1)
    }

    pub async fn finish_attempt(
        &mut self,
        entry_id: &str,
        consumer_name: &str,
        result: &ResultPayload,
    ) -> Result<bool> {
        let raw = serde_json::to_string(result)?;
        let finished: i32 = redis::cmd("EVAL")
            .arg(
                r#"
                if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
                local pending = redis.call('XPENDING', KEYS[3], ARGV[2], ARGV[3], ARGV[3], 1)
                if #pending == 0 or pending[1][2] ~= ARGV[4] then return 0 end
                redis.call('SET', KEYS[2], ARGV[5], 'EX', 604800)
                redis.call('XADD', KEYS[4], 'MAXLEN', '~', 100000, '*', 'payload', ARGV[5])
                redis.call('XACK', KEYS[3], ARGV[2], ARGV[3])
                redis.call('DEL', KEYS[1])
                return 1
            "#,
            )
            .arg(4)
            .arg(format!("transcoder:attempt:lock:{}", result.attempt_id))
            .arg(format!("transcoder:attempt:result:{}", result.attempt_id))
            .arg(&self.compute_stream)
            .arg(&self.results_stream)
            .arg(format!("{consumer_name}:{entry_id}"))
            .arg(&self.compute_group)
            .arg(entry_id)
            .arg(consumer_name)
            .arg(raw)
            .query_async(&mut self.conn)
            .await?;
        Ok(finished == 1)
    }

    pub async fn release_attempt(
        &mut self,
        entry_id: &str,
        attempt_id: &str,
        consumer_name: &str,
    ) -> redis::RedisResult<()> {
        let _: i32 = redis::cmd("EVAL")
            .arg("if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0")
            .arg(1)
            .arg(format!("transcoder:attempt:lock:{attempt_id}"))
            .arg(format!("{consumer_name}:{entry_id}"))
            .query_async(&mut self.conn)
            .await?;
        Ok(())
    }

    pub async fn is_job_cancelled(&mut self, payload: &ComputePayload) -> redis::RedisResult<bool> {
        let mut keys = vec![format!("transcoder:cancel:attempt:{}", payload.attempt_id)];
        if !payload.attempt_scoped_cancel {
            keys.push(format!("transcoder:cancel:job:{}", payload.job_id));
        }
        let count: usize = self.conn.exists(keys).await?;
        Ok(count > 0)
    }

    /// Publishes (XADD) a transcode outcome back to the results stream with MAXLEN retention.
    pub async fn publish_result(&mut self, result_payload: &ResultPayload) -> Result<()> {
        let result_str = serde_json::to_string(result_payload)?;
        let _: String = redis::cmd("XADD")
            .arg(&self.results_stream)
            .arg("MAXLEN")
            .arg("~")
            .arg(100000)
            .arg("*")
            .arg("payload")
            .arg(&result_str)
            .query_async(&mut self.conn)
            .await?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::SCHEMA_VERSION;
    use redis::AsyncCommands;
    use serde_json::json;
    use std::env;
    use uuid::Uuid;

    struct TestQueue {
        adapter: QueueAdapter,
        compute_stream: String,
        compute_group: String,
        results_stream: String,
    }

    async fn test_queue() -> Option<TestQueue> {
        let url = match env::var("TEST_REDIS_URL") {
            Ok(url) => url,
            Err(_) => {
                eprintln!("set TEST_REDIS_URL to run Redis integration tests");
                return None;
            }
        };
        let client = redis::Client::open(url).expect("open TEST_REDIS_URL");
        let conn = client
            .get_multiplexed_async_connection()
            .await
            .expect("connect to TEST_REDIS_URL");
        let namespace = format!("test:compute-worker:{}", Uuid::new_v4());
        let compute_stream = format!("{namespace}:compute");
        let compute_group = format!("{namespace}:group");
        let results_stream = format!("{namespace}:results");
        let adapter = QueueAdapter::with_streams(
            conn,
            compute_stream.clone(),
            compute_group.clone(),
            results_stream.clone(),
        );
        Some(TestQueue {
            adapter,
            compute_stream,
            compute_group,
            results_stream,
        })
    }

    async fn cleanup(queue: &mut TestQueue, attempt_ids: &[&str]) {
        let mut keys = vec![queue.compute_stream.clone(), queue.results_stream.clone()];
        for attempt_id in attempt_ids {
            keys.push(format!("transcoder:attempt:lock:{attempt_id}"));
            keys.push(format!("transcoder:attempt:result:{attempt_id}"));
        }
        let _: i32 = redis::cmd("DEL")
            .arg(keys)
            .query_async(&mut queue.adapter.conn)
            .await
            .expect("delete test streams");
    }

    fn compute_payload(job_id: &str, attempt_id: &str, scoped_cancel: bool) -> String {
        serde_json::to_string(&json!({
            "schema_version": SCHEMA_VERSION,
            "job_id": job_id,
            "attempt_id": attempt_id,
            "input_path": "uploads/source.png",
            "output_path": "attempts/output.png",
            "target_format": "png",
            "attempt_scoped_cancel": scoped_cancel
        }))
        .expect("serialize compute payload")
    }

    fn result_payload(job_id: &str, attempt_id: &str) -> ResultPayload {
        ResultPayload {
            schema_version: SCHEMA_VERSION.to_string(),
            job_id: job_id.to_string(),
            attempt_id: attempt_id.to_string(),
            status: "COMPLETED".to_string(),
            error_message: None,
            error_code: None,
            retryable: None,
            progress: Some(100),
            metadata: json!({"output_size_bytes": 1}),
            artifacts: Vec::new(),
        }
    }

    async fn add_entry(queue: &mut TestQueue, payload: &str) -> String {
        redis::cmd("XADD")
            .arg(&queue.compute_stream)
            .arg("*")
            .arg("payload")
            .arg(payload)
            .query_async(&mut queue.adapter.conn)
            .await
            .expect("add compute entry")
    }

    async fn read_one(
        queue: &mut TestQueue,
        read_id: &str,
        consumer: &str,
    ) -> redis::streams::StreamId {
        let reply = queue
            .adapter
            .read_jobs(read_id, consumer)
            .await
            .expect("read compute entry");
        reply
            .keys
            .into_iter()
            .flat_map(|stream| stream.ids)
            .next()
            .expect("expected one compute entry")
    }

    #[tokio::test]
    async fn reads_entries_added_before_group_creation() {
        let Some(mut queue) = test_queue().await else {
            return;
        };
        let job_id = Uuid::new_v4().to_string();
        let attempt_id = Uuid::new_v4().to_string();
        add_entry(&mut queue, &compute_payload(&job_id, &attempt_id, true)).await;
        queue
            .adapter
            .setup_stream_group()
            .await
            .expect("create compute group");

        let pending = queue.adapter.read_jobs("0", "consumer-a").await.unwrap();
        assert!(pending.keys.iter().all(|stream| stream.ids.is_empty()));
        let entry = read_one(&mut queue, ">", "consumer-a").await;
        queue.adapter.ack_job(&entry.id).await.unwrap();
        cleanup(&mut queue, &[&attempt_id]).await;
    }

    #[tokio::test]
    async fn duplicate_attempt_replays_cached_result_and_acks_duplicate() {
        let Some(mut queue) = test_queue().await else {
            return;
        };
        queue.adapter.setup_stream_group().await.unwrap();
        let job_id = Uuid::new_v4().to_string();
        let attempt_id = Uuid::new_v4().to_string();
        let payload = compute_payload(&job_id, &attempt_id, true);
        add_entry(&mut queue, &payload).await;
        add_entry(&mut queue, &payload).await;

        let first = read_one(&mut queue, ">", "consumer-a").await;
        let second = read_one(&mut queue, ">", "consumer-a").await;
        assert_eq!(
            queue
                .adapter
                .claim_attempt(&first.id, &attempt_id, "consumer-a")
                .await
                .unwrap(),
            1
        );
        let mut duplicate = queue.adapter.clone();
        assert_eq!(
            duplicate
                .claim_attempt(&second.id, &attempt_id, "consumer-a")
                .await
                .unwrap(),
            0
        );
        assert!(
            queue
                .adapter
                .renew_job_lease(&first.id, &attempt_id, "consumer-a")
                .await
                .unwrap()
        );
        assert!(
            queue
                .adapter
                .finish_attempt(
                    &first.id,
                    "consumer-a",
                    &result_payload(&job_id, &attempt_id),
                )
                .await
                .unwrap()
        );
        assert_eq!(
            queue
                .adapter
                .claim_attempt(&second.id, &attempt_id, "consumer-a")
                .await
                .unwrap(),
            -1
        );

        let range: redis::streams::StreamRangeReply = redis::cmd("XRANGE")
            .arg(&queue.results_stream)
            .arg("-")
            .arg("+")
            .query_async(&mut queue.adapter.conn)
            .await
            .unwrap();
        assert_eq!(range.ids.len(), 2);
        assert_eq!(
            range.ids[0].get::<String>("payload").unwrap(),
            range.ids[1].get::<String>("payload").unwrap()
        );
        cleanup(&mut queue, &[&attempt_id]).await;
    }

    #[tokio::test]
    async fn old_consumer_cannot_renew_or_finish_after_pel_transfer() {
        let Some(mut queue) = test_queue().await else {
            return;
        };
        queue.adapter.setup_stream_group().await.unwrap();
        let job_id = Uuid::new_v4().to_string();
        let attempt_id = Uuid::new_v4().to_string();
        let entry_id = add_entry(&mut queue, &compute_payload(&job_id, &attempt_id, true)).await;
        let entry = read_one(&mut queue, ">", "consumer-a").await;
        assert_eq!(entry.id, entry_id);
        assert_eq!(
            queue
                .adapter
                .claim_attempt(&entry.id, &attempt_id, "consumer-a")
                .await
                .unwrap(),
            1
        );
        let _: Vec<String> = redis::cmd("XCLAIM")
            .arg(&queue.compute_stream)
            .arg(&queue.compute_group)
            .arg("consumer-b")
            .arg(0)
            .arg(&entry.id)
            .arg("JUSTID")
            .query_async(&mut queue.adapter.conn)
            .await
            .unwrap();

        assert!(
            !queue
                .adapter
                .renew_job_lease(&entry.id, &attempt_id, "consumer-a")
                .await
                .unwrap()
        );
        assert!(
            !queue
                .adapter
                .finish_attempt(
                    &entry.id,
                    "consumer-a",
                    &result_payload(&job_id, &attempt_id),
                )
                .await
                .unwrap()
        );
        queue.adapter.ack_job(&entry.id).await.unwrap();
        cleanup(&mut queue, &[&attempt_id]).await;
    }

    #[tokio::test]
    async fn failed_result_publication_keeps_pending_entry_for_recovery() {
        let Some(mut queue) = test_queue().await else {
            return;
        };
        queue.adapter.setup_stream_group().await.unwrap();
        let job_id = Uuid::new_v4().to_string();
        let attempt_id = Uuid::new_v4().to_string();
        add_entry(&mut queue, &compute_payload(&job_id, &attempt_id, true)).await;
        let entry = read_one(&mut queue, ">", "consumer-a").await;
        assert_eq!(
            queue
                .adapter
                .claim_attempt(&entry.id, &attempt_id, "consumer-a")
                .await
                .unwrap(),
            1
        );

        let _: () = queue
            .adapter
            .conn
            .set(&queue.results_stream, "wrong-type")
            .await
            .unwrap();
        assert!(
            queue
                .adapter
                .finish_attempt(
                    &entry.id,
                    "consumer-a",
                    &result_payload(&job_id, &attempt_id),
                )
                .await
                .is_err()
        );
        let cached: Option<String> = queue
            .adapter
            .conn
            .get(format!("transcoder:attempt:result:{attempt_id}"))
            .await
            .unwrap();
        assert!(cached.is_some());
        let pending = queue.adapter.read_jobs("0", "consumer-a").await.unwrap();
        assert_eq!(
            pending
                .keys
                .iter()
                .map(|stream| stream.ids.len())
                .sum::<usize>(),
            1
        );

        let _: i32 = redis::cmd("DEL")
            .arg(&queue.results_stream)
            .query_async(&mut queue.adapter.conn)
            .await
            .unwrap();
        assert!(
            queue
                .adapter
                .finish_attempt(
                    &entry.id,
                    "consumer-a",
                    &result_payload(&job_id, &attempt_id),
                )
                .await
                .unwrap()
        );
        let pending = queue.adapter.read_jobs("0", "consumer-a").await.unwrap();
        assert!(pending.keys.iter().all(|stream| stream.ids.is_empty()));
        cleanup(&mut queue, &[&attempt_id]).await;
    }

    #[tokio::test]
    async fn scoped_cancellation_ignores_legacy_job_flag() {
        let Some(mut queue) = test_queue().await else {
            return;
        };
        let job_id = Uuid::new_v4().to_string();
        let attempt_id = Uuid::new_v4().to_string();
        let job_key = format!("transcoder:cancel:job:{job_id}");
        let attempt_key = format!("transcoder:cancel:attempt:{attempt_id}");
        let _: () = queue.adapter.conn.set(&job_key, "1").await.unwrap();
        let scoped: ComputePayload =
            serde_json::from_str(&compute_payload(&job_id, &attempt_id, true)).unwrap();
        assert!(!queue.adapter.is_job_cancelled(&scoped).await.unwrap());
        let legacy: ComputePayload =
            serde_json::from_str(&compute_payload(&job_id, &attempt_id, false)).unwrap();
        assert!(queue.adapter.is_job_cancelled(&legacy).await.unwrap());
        let _: i32 = redis::cmd("DEL")
            .arg(&[job_key.as_str(), attempt_key.as_str()])
            .query_async(&mut queue.adapter.conn)
            .await
            .unwrap();
        cleanup(&mut queue, &[&attempt_id]).await;
    }
}
