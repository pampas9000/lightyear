use anyhow::Result;

mod config;
mod error;
mod job;
mod queue;
mod redis_connection;
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

    // Mask Redis URL password for safe logging
    let masked_redis_url = mask_url_credentials(&config.redis_url);
    println!("Redis URL: {}", masked_redis_url);
    if let Some(ref ep) = config.s3.endpoint {
        println!("S3 Endpoint: {}", ep);
    }
    println!("S3 Bucket: {}", config.s3.bucket);

    let s3 = storage::build_client(
        &config.s3.endpoint,
        &config.s3.access_id,
        &config.s3.access_key,
        &config.s3.region,
    );
    let redis_client = redis::Client::open(config.redis_url.as_str())?;
    // Fail fast at startup if Redis is unreachable.
    let conn = redis_connection::connect_once(&redis_client)
        .await
        .map_err(|e| anyhow::anyhow!("Failed to connect to Redis on startup: {}", e))?;
    println!("Connected to Redis.");

    let ctx = job::Context {
        s3,
        bucket: config.s3.bucket.clone(),
        max_source_bytes: config.max_source_bytes,
        limits: compute_imageops::Limits {
            max_pixels: config.max_pixels,
        },
    };
    worker::Worker::new(redis_client, queue::Queue::new(conn), ctx)
        .run()
        .await?;

    Ok(())
}

/// Masks user credentials (password) inside a URL string for safe logging.
/// Example: `rediss://default:secret@host:6379` -> `rediss://default:******@host:6379`
fn mask_url_credentials(raw: &str) -> String {
    let Some(scheme_end) = raw.find("://") else {
        return raw.to_string();
    };
    let authority_start = scheme_end + 3;
    let authority = &raw[authority_start..];
    let authority_end = authority
        .find(['/', '?', '#'])
        .map(|i| authority_start + i)
        .unwrap_or(raw.len());

    let userinfo = &raw[authority_start..authority_end];
    let Some(at_pos) = userinfo.rfind('@') else {
        return raw.to_string();
    };

    let credentials = &userinfo[..at_pos];
    let masked = match credentials.split_once(':') {
        Some((user, _pass)) => format!("{}:******", user),
        None => credentials.to_string(),
    };

    format!(
        "{}{}{}",
        &raw[..authority_start],
        masked,
        &raw[authority_start + at_pos..]
    )
}
