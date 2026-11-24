use config::{Config, ConfigError, Environment, File};
use dotenvy::dotenv;
use serde::Deserialize;
use validator::Validate;

#[derive(Debug, Deserialize, Validate, Clone)]
pub struct S3Config {
    pub endpoint: Option<String>,
    pub access_id: String,
    pub access_key: String,
    pub region: Option<String>,
    pub bucket: String,
}

#[derive(Debug, Deserialize, Validate, Clone)]
pub struct WorkerConfig {
    pub redis_url: String,

    #[validate(nested)]
    pub s3: S3Config,

    /// Largest source object accepted, in bytes.
    #[serde(default = "default_max_source_bytes")]
    pub max_source_bytes: u64,
    /// Largest source image accepted, in pixels.
    #[serde(default = "default_max_pixels")]
    pub max_pixels: u64,
    /// `libavif` (default) or `libvips`, the fallback.
    #[serde(default = "default_avif_encoder")]
    pub avif_encoder: String,
    /// Comma-separated networks that source URLs may reach besides the
    /// public internet, e.g. `127.0.0.0/8` for local development. Empty by
    /// default; does not concern the worker's own `S3_ENDPOINT`.
    #[serde(default)]
    pub fetch_allow_cidrs: String,
    /// Seconds to wait for a source URL's connection.
    #[serde(default = "default_fetch_connect_timeout_secs")]
    pub fetch_connect_timeout_secs: u64,
    /// Seconds to wait for any read of a source URL (head or next chunk).
    #[serde(default = "default_fetch_read_timeout_secs")]
    pub fetch_read_timeout_secs: u64,
    /// Seconds a running job may still take after SIGTERM/SIGINT before it
    /// is stopped and reported as `worker_shutdown` (retryable).
    #[serde(default = "default_shutdown_grace_secs")]
    pub shutdown_grace_secs: u64,
}

fn default_shutdown_grace_secs() -> u64 {
    30
}

fn default_fetch_connect_timeout_secs() -> u64 {
    10
}

fn default_fetch_read_timeout_secs() -> u64 {
    30
}

fn default_max_source_bytes() -> u64 {
    200 * 1024 * 1024
}

fn default_avif_encoder() -> String {
    "libavif".into()
}

fn default_max_pixels() -> u64 {
    compute_imageops::Settings::default().max_pixels
}

impl WorkerConfig {
    pub fn load() -> Result<Self, ConfigError> {
        // Load .env file if it exists
        let _ = dotenv();

        let mut builder = Config::builder()
            // Load from file if it exists (e.g. worker.toml/worker.json)
            .add_source(File::with_name("worker").required(false))
            // Load from environment variables
            .add_source(Environment::default().separator("__"));

        // Map standard flat environment variables if they are present
        if let Ok(redis_url) = std::env::var("REDIS_URL") {
            builder = builder.set_override("redis_url", redis_url)?;
        }
        if let Ok(ep) = std::env::var("S3_ENDPOINT") {
            builder = builder.set_override("s3.endpoint", ep)?;
        }
        if let Ok(id) = std::env::var("S3_ACCESS_ID") {
            builder = builder.set_override("s3.access_id", id)?;
        }
        if let Ok(key) = std::env::var("S3_ACCESS_KEY") {
            builder = builder.set_override("s3.access_key", key)?;
        }
        if let Ok(reg) = std::env::var("S3_REGION") {
            builder = builder.set_override("s3.region", reg)?;
        }
        if let Ok(bucket) = std::env::var("S3_BUCKET") {
            builder = builder.set_override("s3.bucket", bucket)?;
        }
        if let Ok(bytes) = std::env::var("WORKER_MAX_SOURCE_BYTES") {
            builder = builder.set_override("max_source_bytes", bytes)?;
        }
        if let Ok(pixels) = std::env::var("WORKER_MAX_PIXELS") {
            builder = builder.set_override("max_pixels", pixels)?;
        }
        if let Ok(encoder) = std::env::var("WORKER_AVIF_ENCODER") {
            builder = builder.set_override("avif_encoder", encoder)?;
        }
        if let Ok(cidrs) = std::env::var("WORKER_FETCH_ALLOW_CIDRS") {
            builder = builder.set_override("fetch_allow_cidrs", cidrs)?;
        }
        if let Ok(secs) = std::env::var("WORKER_FETCH_CONNECT_TIMEOUT_SECS") {
            builder = builder.set_override("fetch_connect_timeout_secs", secs)?;
        }
        if let Ok(secs) = std::env::var("WORKER_FETCH_READ_TIMEOUT_SECS") {
            builder = builder.set_override("fetch_read_timeout_secs", secs)?;
        }
        if let Ok(secs) = std::env::var("WORKER_SHUTDOWN_GRACE_SECS") {
            builder = builder.set_override("shutdown_grace_secs", secs)?;
        }

        let settings: WorkerConfig = builder.build()?.try_deserialize()?;

        if let Err(e) = settings.validate() {
            return Err(ConfigError::Message(format!(
                "Configuration validation failed: {}",
                e
            )));
        }

        Ok(settings)
    }
}
