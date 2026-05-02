use anyhow::Result;
use redis::AsyncCommands;
use sea_orm::{ActiveModelTrait, Database, DatabaseConnection, EntityTrait, Set};

use crate::entities::job::JobStatus;

mod config;
mod entities;
mod shell;
mod strategy;

#[tokio::main]
async fn main() -> Result<()> {
    println!("Worker started.");

    // 0. Load Configuration
    let settings =
        config::Settings::load().map_err(|e| anyhow::anyhow!("Failed to load config: {}", e))?;
    println!("Configuration loaded and validated.");

    // 1. Initialize SeaORM
    let db: DatabaseConnection = Database::connect(&settings.database_url).await?;
    println!("Connected to Database.");

    // 2. Initialize Redis
    let kv_client = redis::Client::open(settings.redis_url)?;
    let mut kv_connection = kv_client.get_multiplexed_async_connection().await?;
    println!("Connected to Redis.");

    let stream_key = "transcoder:jobs:stream";
    let group_name = "transcoder:workers";
    let consumer_name = format!("worker-{}", settings.worker_id);

    // Create consumer group if not exists. MKSTREAM will create the stream if it doesn't exist.
    let _: redis::RedisResult<()> = kv_connection
        .xgroup_create_mkstream(stream_key, group_name, "$")
        .await;

    println!(
        "Waiting for jobs on stream {} (group: {})...",
        stream_key, group_name
    );

    loop {
        let opts = redis::streams::StreamReadOptions::default()
            .group(group_name, &consumer_name)
            .block(100)
            .count(1);

        let result: redis::RedisResult<redis::streams::StreamReadReply> = kv_connection
            .xread_options(&[stream_key], &[">"], &opts)
            .await;

        match result {
            Ok(reply) => {
                for stream in reply.keys {
                    for entry in stream.ids {
                        let job_id_str: String = entry.get("job_id").unwrap_or_default();
                        if job_id_str.is_empty() {
                            println!("Received empty job_id field in stream entry: {}", entry.id);
                            let _: redis::RedisResult<()> = kv_connection
                                .xack(stream_key, group_name, &[&entry.id])
                                .await;
                            continue;
                        }

                        println!("Received job ID from stream: {}", job_id_str);

                        let job_id = match uuid::Uuid::parse_str(&job_id_str) {
                            Ok(id) => id,
                            Err(e) => {
                                eprintln!("Invalid UUID {}: {}", job_id_str, e);
                                let _: redis::RedisResult<()> = kv_connection
                                    .xack(stream_key, group_name, &[&entry.id])
                                    .await;
                                continue;
                            }
                        };

                        // 1. Fetch Job from PG
                        let job = entities::job::Entity::find_by_id(job_id).one(&db).await?;

                        if let Some(job) = job {
                            println!(
                                "Processing job: {} [{} -> {}] Format: {} Params: {:?}",
                                job.id, job.input_path, job.output_path, job.target_format, job.params
                            );

                            // 2. Update status to processing
                            let mut active_job: entities::job::ActiveModel = job.into();
                            active_job.status = Set(JobStatus::Processing);
                            active_job.updated_at = Set(chrono::Utc::now().into());
                            let active_job = active_job.update(&db).await?;

                            // 3. Process (Simulation)
                            tokio::time::sleep(tokio::time::Duration::from_secs(2)).await;

                            // 4. Mark as completed
                            let mut active_job: entities::job::ActiveModel = active_job.into();
                            active_job.status = Set(JobStatus::Completed);
                            active_job.progress = Set(100);
                            active_job.updated_at = Set(chrono::Utc::now().into());
                            active_job.update(&db).await?;

                            println!("Job completed: {}", job_id_str);
                        } else {
                            eprintln!("Job not found in DB: {}", job_id_str);
                        }

                        // ACK the message
                        let _: redis::RedisResult<()> = kv_connection
                            .xack(stream_key, group_name, &[&entry.id])
                            .await;
                    }
                }
            }
            Err(e) => {
                eprintln!("Redis error: {}", e);
                tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                // 像 Go 一样简单处理重连
                if e.is_io_error() {
                    kv_connection = kv_client.get_multiplexed_async_connection().await?;
                }
            }
        }
    }
}
