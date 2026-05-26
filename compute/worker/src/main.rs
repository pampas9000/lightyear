use anyhow::Result;
use redis::AsyncCommands;
use sea_orm::{ActiveModelTrait, Database, DatabaseConnection, EntityTrait, Set};
use std::path::Path;
use compute_types::{Format, Params};

use crate::entities::job::JobStatus;

mod config;
mod entities;
mod shell;
mod storage;
mod strategy;

async fn handle_job_processing(
    db: &DatabaseConnection,
    s3_client: &aws_sdk_s3::Client,
    default_bucket: &str,
    job_id_str: &str,
) -> Result<bool, anyhow::Error> {
    let job_id = uuid::Uuid::parse_str(job_id_str)
        .map_err(|e| anyhow::anyhow!("Invalid UUID format: {}", e))?;

    // 1. Fetch Job from PG
    let job = entities::job::Entity::find_by_id(job_id)
        .one(db)
        .await
        .map_err(|e| anyhow::anyhow!("Failed to fetch job from DB: {}", e))?;

    let Some(job) = job else {
        return Ok(false); // Job not found in DB
    };

    println!(
        "Processing job: {} [{} -> {}] Format: {} Params: {:?}",
        job.id, job.input_path, job.output_path, job.target_format, job.params
    );

    // 2. Update status to processing
    let mut active_job: entities::job::ActiveModel = job.clone().into();
    active_job.status = Set(JobStatus::Processing);
    active_job.updated_at = Set(chrono::Utc::now().into());
    let active_job = active_job
        .update(db)
        .await
        .map_err(|e| anyhow::anyhow!("Failed to update job status to Processing: {}", e))?;

    // 3. Process the S3 download, transcode, and upload sandbox flow
    let process_result: Result<(), anyhow::Error> = async {
        // Parse target format case-insensitively
        let format_json_val = serde_json::Value::String(job.target_format.trim().to_lowercase());
        let target_format: Format = serde_json::from_value(format_json_val)
            .map_err(|_| anyhow::anyhow!("Unsupported or invalid target format: '{}'", job.target_format))?;

        // Parse outer Params
        let mut parsed_params = None;
        if let Some(ref val) = job.params {
            let params: Params = serde_json::from_value(val.clone())
                .map_err(|e| anyhow::anyhow!("Failed to parse transcode Params: {}", e))?;
            params.validate_for_format(target_format.clone())
                .map_err(|e| anyhow::anyhow!("Transcode parameter validation failed: {}", e))?;
            parsed_params = Some(params);
        }

        // Create a local temp sandboxed directory
        let temp_dir = tempfile::tempdir()
            .map_err(|e| anyhow::anyhow!("Failed to create temporary directory: {}", e))?;

        let (input_bucket, input_key) = storage::parse_s3_path(&job.input_path, default_bucket);
        let (output_bucket, output_key) = storage::parse_s3_path(&job.output_path, default_bucket);

        // Keep extension for input to assist format detection
        let ext = Path::new(&input_key)
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("tmp");
        let local_input_path = temp_dir.path().join(format!("input.{}", ext));

        // Keep extension for output based on target format name (e.g. "avif", "jxl")
        let out_ext = job.target_format.trim().to_lowercase();
        let local_output_path = temp_dir.path().join(format!("output.{}", out_ext));

        // A. Download from S3
        storage::download_from_s3(s3_client, &input_bucket, &input_key, &local_input_path)
            .await
            .map_err(|e| anyhow::anyhow!("download input from S3 failed: {}", e))?;

        // B. Perform Local Transcoding
        shell::run_transcode(&local_input_path, &local_output_path, target_format, &parsed_params)
            .await
            .map_err(|e| anyhow::anyhow!("local transcode execution failed: {}", e))?;

        // C. Upload output to S3
        storage::upload_to_s3(s3_client, &output_bucket, &output_key, &local_output_path)
            .await
            .map_err(|e| anyhow::anyhow!("upload output to S3 failed: {}", e))?;

        Ok(())
    }.await;

    // 4. Update status based on the result
    let mut active_job: entities::job::ActiveModel = active_job.into();
    match process_result {
        Ok(()) => {
            println!("Transcoding succeeded for job: {}", job_id_str);
            active_job.status = Set(JobStatus::Completed);
            active_job.progress = Set(100);
            active_job.error_message = Set(None);
        }
        Err(err) => {
            let error_text = err.to_string();
            eprintln!("Transcoding failed for job {}: {}", job_id_str, error_text);
            active_job.status = Set(JobStatus::Failed);
            active_job.progress = Set(0);
            active_job.error_message = Set(Some(error_text));
        }
    }
    active_job.updated_at = Set(chrono::Utc::now().into());
    active_job
        .update(db)
        .await
        .map_err(|e| anyhow::anyhow!("Failed to save final job status (Completed/Failed) to DB: {}", e))?;

    Ok(true)
}

#[tokio::main]
async fn main() -> Result<()> {
    // Install the rustls crypto provider before any TLS connection is established
    let _ = rustls::crypto::ring::default_provider().install_default();

    println!("Worker started.");


    // 0. Load Configuration
    let settings =
        config::Settings::load().map_err(|e| anyhow::anyhow!("Failed to load config: {}", e))?;
    println!("Configuration loaded and validated.");

    // 1. Initialize SeaORM
    let db: DatabaseConnection = Database::connect(&settings.database_url).await?;
    println!("Connected to Database.");

    // 2. Initialize S3 client
    let s3_client = storage::build_s3_client(
        &settings.s3_endpoint,
        &settings.s3_access_id,
        &settings.s3_access_key,
        &settings.s3_region,
    ).await;
    println!("S3 client initialized.");

    // 3. Initialize Redis
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

    let mut read_id = "0";
    let mut failed_attempts = std::collections::HashMap::new();

    loop {
        let opts = redis::streams::StreamReadOptions::default()
            .group(group_name, &consumer_name)
            .count(1);

        let result: redis::RedisResult<redis::streams::StreamReadReply> = kv_connection
            .xread_options(&[stream_key], &[read_id], &opts)
            .await;

        match result {
            Ok(reply) => {
                let mut processed_any = false;
                for stream in reply.keys {
                    for entry in stream.ids {
                        processed_any = true;
                        let job_id_str: String = entry.get("job_id").unwrap_or_default();
                        if job_id_str.is_empty() {
                            println!("Received empty job_id field in stream entry: {}", entry.id);
                            let _: redis::RedisResult<()> = kv_connection
                                .xack(stream_key, group_name, &[&entry.id])
                                .await;
                            continue;
                        }

                        println!("Received job ID from stream (read_id: {}): {}", read_id, job_id_str);

                        match handle_job_processing(&db, &s3_client, &settings.s3_bucket, &job_id_str).await {
                            Ok(_) => {
                                let _: redis::RedisResult<()> = kv_connection
                                    .xack(stream_key, group_name, &[&entry.id])
                                    .await;
                                failed_attempts.remove(&entry.id);
                            }
                            Err(e) => {
                                eprintln!("Error processing job {} (retaining in stream): {}", job_id_str, e);
                                let attempts = failed_attempts.entry(entry.id.clone()).or_insert(0);
                                *attempts += 1;
                                if *attempts >= 3 {
                                    eprintln!("Job {} / Entry {} failed {} times. Acknowledging to avoid infinite loop.", job_id_str, entry.id, attempts);
                                    
                                    // Try to mark the job as FAILED in the database if possible
                                    if let Ok(job_id) = uuid::Uuid::parse_str(&job_id_str) {
                                        let active_job = entities::job::ActiveModel {
                                            id: Set(job_id),
                                            status: Set(JobStatus::Failed),
                                            error_message: Set(Some(format!("Worker internal error: exceeded max retry attempts ({}). Original error: {}", attempts, e))),
                                            updated_at: Set(chrono::Utc::now().into()),
                                            ..Default::default()
                                        };
                                        if let Err(update_err) = active_job.update(&db).await {
                                            eprintln!("Failed to update job status to Failed in DB: {}", update_err);
                                        } else {
                                            println!("Successfully marked job {} as Failed in DB.", job_id_str);
                                        }
                                    }

                                    let _: redis::RedisResult<()> = kv_connection
                                        .xack(stream_key, group_name, &[&entry.id])
                                        .await;
                                    failed_attempts.remove(&entry.id);
                                }
                            }
                        }
                    }
                }

                if !processed_any {
                    if read_id == "0" {
                        // Switch to reading new messages if no pending messages are found
                        read_id = ">";
                    } else {
                        // Switch back to checking pending messages and sleep for a bit to avoid busy looping
                        read_id = "0";
                        tokio::time::sleep(tokio::time::Duration::from_millis(1000)).await;
                    }
                }
            }
            Err(e) => {
                eprintln!("Redis error: {}", e);
                tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                // Simple reconnection logic
                if e.is_io_error() {
                    kv_connection = kv_client.get_multiplexed_async_connection().await?;
                }
            }
        }
    }
}
