use anyhow::Result;
use std::path::Path;
use compute_types::{Format, Params};
use crate::protocol::ComputePayload;
use crate::storage;
use crate::shell;

pub async fn handle_compute(
    s3_client: &aws_sdk_s3::Client,
    default_bucket: &str,
    payload: &ComputePayload,
) -> Result<serde_json::Value, anyhow::Error> {
    // Parse target format case-insensitively
    let format_json_val = serde_json::Value::String(payload.target_format.trim().to_lowercase());
    let target_format: Format = serde_json::from_value(format_json_val)
        .map_err(|_| anyhow::anyhow!("Unsupported or invalid target format: '{}'", payload.target_format))?;

    // Parse outer Params
    let mut parsed_params = None;
    if let Some(ref val) = payload.params {
        let params: Params = serde_json::from_value(val.clone())
            .map_err(|e| anyhow::anyhow!("Failed to parse transcode Params: {}", e))?;
        params.validate_for_format(target_format.clone())
            .map_err(|e| anyhow::anyhow!("Transcode parameter validation failed: {}", e))?;
        parsed_params = Some(params);
    }

    // Create a local temp sandboxed directory
    let temp_dir = tempfile::tempdir()
        .map_err(|e| anyhow::anyhow!("Failed to create temporary directory: {}", e))?;

    let (input_bucket, input_key) = storage::parse_s3_path(&payload.input_path, default_bucket);
    let (output_bucket, output_key) = storage::parse_s3_path(&payload.output_path, default_bucket);

    // Keep extension for input to assist format detection
    let ext = Path::new(&input_key)
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("tmp");
    let local_input_path = temp_dir.path().join(format!("input.{}", ext));

    // Keep extension for output based on target format name (e.g. "avif", "jxl")
    let out_ext = payload.target_format.trim().to_lowercase();
    let local_output_path = temp_dir.path().join(format!("output.{}", out_ext));

    // A. Download from S3
    storage::download_from_s3(s3_client, &input_bucket, &input_key, &local_input_path)
        .await
        .map_err(|e| anyhow::anyhow!("download input from S3 failed: {}", e))?;

    // B. Perform Local Transcoding
    shell::run_transcode(
        &local_input_path,
        &local_output_path,
        target_format,
        &parsed_params,
        payload.input_format,
    )
    .await
    .map_err(|e| anyhow::anyhow!("local transcode execution failed: {}", e))?;

    // Get output metadata details
    let metadata_size = tokio::fs::metadata(&local_output_path).await?.len();
    let mut width = 0;
    let mut height = 0;
    if let Ok(img) = image::image_dimensions(&local_output_path) {
        width = img.0;
        height = img.1;
    }

    // C. Upload output to S3
    storage::upload_to_s3(s3_client, &output_bucket, &output_key, &local_output_path)
        .await
        .map_err(|e| anyhow::anyhow!("upload output to S3 failed: {}", e))?;

    // Determine output mime type
    let mime_type = match out_ext.as_str() {
        "avif" => "image/avif",
        "jxl" => "image/jxl",
        "webp" => "image/webp",
        "heic" => "image/heic",
        "jpg" | "jpeg" => "image/jpeg",
        "png" => "image/png",
        _ => "application/octet-stream",
    };

    Ok(serde_json::json!({
        "output_size_bytes": metadata_size,
        "width": width,
        "height": height,
        "mime_type": mime_type,
    }))
}
