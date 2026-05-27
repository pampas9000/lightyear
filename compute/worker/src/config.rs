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
    pub database_url: String,
    pub redis_url: String,
    
    #[validate(nested)]
    pub s3: S3Config,
}

impl WorkerConfig {
    pub fn load() -> Result<Self, ConfigError> {
        // Load .env file if it exists
        let _ = dotenv();

        let mut builder = Config::builder()
            // Load from file if it exists (e.g. worker.toml/worker.json)
            .add_source(File::with_name("worker").required(false))
            // Load from environment variables
            .add_source(
                Environment::default()
                    .separator("__")
            );

        // Map standard flat environment variables if they are present
        if let Ok(db_url) = std::env::var("DATABASE_URL") {
            builder = builder.set_override("database_url", db_url)?;
        }
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
