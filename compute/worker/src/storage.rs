use crate::error::ComputeError;
use aws_sdk_s3::Client;
use aws_sdk_s3::config::{Credentials, Region};
use aws_sdk_s3::primitives::ByteStream;

/// Builds an S3 (R2-compatible) client with static credentials.
pub fn build_client(
    endpoint: &Option<String>,
    access_key_id: &str,
    secret_access_key: &str,
    region: &Option<String>,
) -> Client {
    let credentials = Credentials::new(access_key_id, secret_access_key, None, None, "Static");
    let mut builder = aws_sdk_s3::config::Builder::new()
        .behavior_version(aws_sdk_s3::config::BehaviorVersion::latest())
        .credentials_provider(credentials)
        .region(Region::new(region.clone().unwrap_or_else(|| "auto".into())))
        .force_path_style(true);
    if let Some(ep) = endpoint {
        builder = builder.endpoint_url(ep);
    }
    Client::from_conf(builder.build())
}

/// Downloads an object, refusing anything larger than `max_bytes`.
pub async fn download(
    client: &Client,
    bucket: &str,
    key: &str,
    max_bytes: u64,
) -> Result<Vec<u8>, ComputeError> {
    let object = client
        .get_object()
        .bucket(bucket)
        .key(key)
        .send()
        .await
        .map_err(|e| match e.as_service_error() {
            Some(service) if service.is_no_such_key() => {
                ComputeError::SourceUnavailable(format!("source object {key} does not exist"))
            }
            _ => ComputeError::StorageFailed(format!("download {key}: {e}")),
        })?;
    let size = object.content_length().unwrap_or(0).max(0) as u64;
    if size > max_bytes {
        return Err(ComputeError::SourceUnavailable(format!(
            "source is {size} bytes, above the {max_bytes}-byte limit"
        )));
    }
    let bytes = object
        .body
        .collect()
        .await
        .map_err(|e| ComputeError::StorageFailed(format!("read {key}: {e}")))?
        .into_bytes();
    if bytes.len() as u64 > max_bytes {
        return Err(ComputeError::SourceUnavailable(format!(
            "source exceeds the {max_bytes}-byte limit"
        )));
    }
    Ok(bytes.to_vec())
}

pub async fn upload(
    client: &Client,
    bucket: &str,
    key: &str,
    bytes: Vec<u8>,
    content_type: &str,
) -> Result<(), ComputeError> {
    client
        .put_object()
        .bucket(bucket)
        .key(key)
        .content_type(content_type)
        .body(ByteStream::from(bytes))
        .send()
        .await
        .map_err(|e| ComputeError::StorageFailed(format!("upload {key}: {e}")))?;
    Ok(())
}
