use config::{Config, ConfigError, Environment, File};
use dotenvy::dotenv;
use serde::Deserialize;
use validator::Validate;

#[derive(Debug, Deserialize, Validate, Clone)]
pub struct Settings {
    pub database_url: String,
    pub redis_url: String,
    pub worker_id: i32,
    pub s3_endpoint: Option<String>,
    pub s3_access_id: String,
    pub s3_access_key: String,
    pub s3_region: Option<String>,
    pub s3_bucket: String,
}

impl Settings {
    pub fn load() -> Result<Self, ConfigError> {
        // Load .env file if it exists
        dotenv().ok();

        let s = Config::builder()
            // Set defaults
            .set_default(
                "database_url",
                "postgres://postgres:postgres@localhost:5432/transcoder",
            )?
            .set_default("redis_url", "redis://localhost:6379/0")?
            .set_default("worker_id", 1)?
            // Load from file if exists
            .add_source(File::with_name("worker").required(false))
            // Load from environment variables (e.g. DATABASE_URL, REDIS_URL, WORKER_ID)
            .add_source(Environment::default())
            .build()?;

        let settings: Settings = s.try_deserialize()?;

        if let Err(e) = settings.validate() {
            return Err(ConfigError::Message(format!(
                "Configuration validation failed: {}",
                e
            )));
        }

        Ok(settings)
    }
}
