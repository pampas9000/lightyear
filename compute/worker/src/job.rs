//! Executes one dispatch: download, process, upload.

use crate::error::ComputeError;
use crate::storage;
use compute_imageops::Settings;
use compute_types::protocol::{Artifact, Dispatch};

pub const OP_IMAGE_CONVERT: &str = "image.convert";

pub struct Context {
    pub s3: aws_sdk_s3::Client,
    pub bucket: String,
    pub max_source_bytes: u64,
    pub settings: Settings,
}

pub async fn run(ctx: &Context, dispatch: &Dispatch) -> Result<Artifact, ComputeError> {
    if dispatch.op != OP_IMAGE_CONVERT {
        return Err(ComputeError::InvalidRequest(format!(
            "unknown op {:?}",
            dispatch.op
        )));
    }
    let source = storage::download(
        &ctx.s3,
        &ctx.bucket,
        &dispatch.source_key,
        ctx.max_source_bytes,
    )
    .await?;
    let options = dispatch.options.clone();
    let settings = ctx.settings;
    // libvips is synchronous and uses its own thread pool.
    let output = tokio::task::spawn_blocking(move || {
        compute_imageops::process(&source, &options, &settings)
    })
    .await
    .map_err(|e| ComputeError::Internal(format!("processing task: {e}")))??;
    let artifact = Artifact {
        key: dispatch.output_key.clone(),
        size: output.bytes.len() as u64,
        mime: output.mime.to_string(),
        width: output.width,
        height: output.height,
    };
    storage::upload(
        &ctx.s3,
        &ctx.bucket,
        &artifact.key,
        output.bytes,
        output.mime,
    )
    .await?;
    Ok(artifact)
}
