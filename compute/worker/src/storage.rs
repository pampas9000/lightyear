use anyhow::{anyhow, Result};
use aws_sdk_s3::config::{Credentials, Region};
use aws_sdk_s3::Client;
use std::path::Path;

/// Builds a Cloudflare R2 compatible AWS S3 client using static credentials.
pub async fn build_s3_client(
    endpoint: &Option<String>,
    access_key_id: &str,
    secret_access_key: &str,
    region_opt: &Option<String>,
) -> Client {
    let credentials = Credentials::new(
        access_key_id,
        secret_access_key,
        None,
        None,
        "Static",
    );

    let region = Region::new(region_opt.clone().unwrap_or_else(|| "auto".to_string()));

    let mut config_builder = aws_sdk_s3::config::Builder::new()
        .behavior_version(aws_sdk_s3::config::BehaviorVersion::latest())
        .credentials_provider(credentials)
        .region(region)
        .force_path_style(true);

    if let Some(ep) = endpoint {
        config_builder = config_builder.endpoint_url(ep);
    }

    Client::from_conf(config_builder.build())
}

/// Parses an S3 URI string into a (bucket, key) tuple.
/// If it does not start with `s3://`, treats the path as a key under the default bucket.
pub fn parse_s3_path(path: &str, default_bucket: &str) -> (String, String) {
    if let Some(stripped) = path.strip_prefix("s3://") {
        if let Some((bucket, key)) = stripped.split_once('/') {
            (bucket.to_string(), key.to_string())
        } else {
            (stripped.to_string(), String::new())
        }
    } else {
        (default_bucket.to_string(), path.to_string())
    }
}

/// Downloads a file from S3 and writes it to a local file path.
pub async fn download_from_s3(
    client: &Client,
    bucket: &str,
    key: &str,
    local_path: &Path,
) -> Result<()> {
    let result = client
        .get_object()
        .bucket(bucket)
        .key(key)
        .send()
        .await
        .map_err(|e| anyhow!("Failed to download from S3 (bucket: {}, key: {}): {}", bucket, key, e))?;

    let data = result.body.collect().await
        .map_err(|e| anyhow!("Failed to read S3 body: {}", e))?;
    
    tokio::fs::write(local_path, data.into_bytes()).await
        .map_err(|e| anyhow!("Failed to write downloaded file to local path: {}", e))?;
    
    Ok(())
}

/// Uploads a local file back to S3.
pub async fn upload_to_s3(
    client: &Client,
    bucket: &str,
    key: &str,
    local_path: &Path,
) -> Result<()> {
    let body = aws_sdk_s3::primitives::ByteStream::from_path(local_path).await
        .map_err(|e| anyhow!("Failed to read local file for S3 upload: {}", e))?;

    client
        .put_object()
        .bucket(bucket)
        .key(key)
        .body(body)
        .send()
        .await
        .map_err(|e| anyhow!("Failed to upload to S3 (bucket: {}, key: {}): {}", bucket, key, e))?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_s3_path() {
        // Full URI
        let (bucket, key) = parse_s3_path("s3://my-bucket/uploads/file.png", "default-bucket");
        assert_eq!(bucket, "my-bucket");
        assert_eq!(key, "uploads/file.png");

        // Raw key fallback
        let (bucket, key) = parse_s3_path("uploads/file.png", "default-bucket");
        assert_eq!(bucket, "default-bucket");
        assert_eq!(key, "uploads/file.png");

        // URI without slash
        let (bucket, key) = parse_s3_path("s3://isolated-bucket", "default-bucket");
        assert_eq!(bucket, "isolated-bucket");
        assert_eq!(key, "");
    }
}
