use anyhow::Result;
use compute_worker::{
    config, fetch, job, process, queue, redis_connection, shutdown, storage, worker,
};

fn main() -> Result<()> {
    // A compute child (see `process.rs`) needs neither the runtime nor the
    // worker configuration.
    if std::env::args_os()
        .nth(1)
        .is_some_and(|a| a == process::ARG)
    {
        std::process::exit(process::serve());
    }
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?
        .block_on(run())
}

async fn run() -> Result<()> {
    // Install the rustls crypto provider
    let _ = rustls::crypto::ring::default_provider().install_default();

    println!("Rust Compute Worker started.");

    // Load configurations
    let config = config::WorkerConfig::load()
        .map_err(|e| anyhow::anyhow!("Failed to load configuration: {}", e))?;
    println!("Configuration loaded and validated.");

    // JPEG → JXL lossless recompression needs cjxl on every worker.
    compute_imageops::check_cjxl().map_err(|e| anyhow::anyhow!("cjxl is required: {}", e))?;

    let avif_encoder = config
        .avif_encoder
        .parse()
        .map_err(|e: String| anyhow::anyhow!("WORKER_AVIF_ENCODER: {e}"))?;
    println!("AVIF encoder: {}", config.avif_encoder);

    // Mask Redis URL password for safe logging
    let masked_redis_url = mask_url_credentials(&config.redis_url);
    println!("Redis URL: {}", masked_redis_url);
    if let Some(ref ep) = config.s3.endpoint {
        println!("S3 Endpoint: {}", ep);
    }
    println!("S3 Bucket: {}", config.s3.bucket);

    let policy = fetch::FetchPolicy::parse(&config.fetch_allow_cidrs)
        .map_err(|e| anyhow::anyhow!("WORKER_FETCH_ALLOW_CIDRS: {e}"))?;
    if !policy.allow.is_empty() {
        let nets: Vec<String> = policy.allow.iter().map(ToString::to_string).collect();
        eprintln!(
            "WARNING: source URLs may reach {} besides the public internet (WORKER_FETCH_ALLOW_CIDRS); \
             leave it empty in production",
            nets.join(", ")
        );
    }
    let fetcher = fetch::Fetcher::new(
        policy,
        &fetch::FetchSettings {
            connect_timeout: std::time::Duration::from_secs(config.fetch_connect_timeout_secs),
            read_timeout: std::time::Duration::from_secs(config.fetch_read_timeout_secs),
        },
    )
    .map_err(|e| anyhow::anyhow!("{e}"))?;

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
        fetcher,
        compute_program: std::env::current_exe()
            .map_err(|e| anyhow::anyhow!("Cannot locate the worker executable: {e}"))?,
        settings: compute_imageops::Settings {
            max_pixels: config.max_pixels,
            avif_encoder,
        },
    };
    let shutdown = shutdown::listen()
        .map_err(|e| anyhow::anyhow!("Cannot listen for shutdown signals: {e}"))?;
    let grace = std::time::Duration::from_secs(config.shutdown_grace_secs);
    println!("Shutdown grace period: {grace:?}");
    worker::Worker::new(redis_client, queue::Queue::new(conn), ctx, shutdown, grace)
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
