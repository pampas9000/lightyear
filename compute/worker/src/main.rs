use anyhow::Result;

mod config;
mod handler;
mod protocol;
mod queue;
mod redis_connection;
mod shell;
mod storage;
mod worker;

#[tokio::main]
async fn main() -> Result<()> {
    // Install the rustls crypto provider
    let _ = rustls::crypto::ring::default_provider().install_default();

    println!("Rust Compute Worker started.");

    // Load configurations
    let config = config::WorkerConfig::load()
        .map_err(|e| anyhow::anyhow!("Failed to load configuration: {}", e))?;
    println!("Configuration loaded and validated.");

    println!("Redis URL: {}", config.redis_url);
    if let Some(ref ep) = config.s3.endpoint {
        println!("S3 Endpoint: {}", ep);
    }
    println!("S3 Bucket: {}", config.s3.bucket);

    // Initialize S3 client
    let s3_client = storage::build_s3_client(
        &config.s3.endpoint,
        &config.s3.access_id,
        &config.s3.access_key,
        &config.s3.region,
    )
    .await;
    println!("S3 client initialized.");

    // Initialize Redis client
    let redis_client = redis::Client::open(config.redis_url.as_str())?;
    println!("Redis client initialized.");

    // Connect to Redis once (Fail fast during startup if Redis is unreachable)
    let conn = redis_connection::connect_once(&redis_client)
        .await
        .map_err(|e| anyhow::anyhow!("Failed to connect to Redis on startup: {}", e))?;
    println!("Connected to Redis successfully.");

    // Initialize Queue adapter
    let queue_adapter = queue::QueueAdapter::new(conn);

    // Initialize and run the Worker consumer runtime
    let mut worker_runtime = worker::Worker::new(
        redis_client,
        queue_adapter,
        s3_client,
        config.s3.bucket.clone(),
    );
    worker_runtime.run().await?;

    Ok(())
}
