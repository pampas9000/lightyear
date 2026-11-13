//! Executes one dispatch: download, process/optimize, upload.

use crate::error::ComputeError;
use crate::storage;
use compute_imageops::Settings;
use compute_types::ImageOptions;
use compute_types::protocol::{Achieved, Artifact, Dispatch};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};

pub const OP_IMAGE_CONVERT: &str = "image.convert";

pub struct Context {
    pub s3: aws_sdk_s3::Client,
    pub bucket: String,
    pub max_source_bytes: u64,
    pub settings: Settings,
}

pub struct RunOutcome {
    pub artifact: Artifact,
    pub resolved_options: Option<ImageOptions>,
    pub target_met: Option<bool>,
    pub achieved: Option<Achieved>,
    pub encode_rounds: u32,
    pub compute_ms: u64,
}

pub async fn run(
    ctx: &Context,
    dispatch: &Dispatch,
    cancelled: Arc<AtomicBool>,
    progress: Arc<AtomicU8>,
) -> Result<RunOutcome, ComputeError> {
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
    let target = dispatch.target.clone();
    let settings = ctx.settings;
    let has_target = target.is_some();
    let progress_hook = progress.clone();
    // libvips is synchronous and uses its own thread pool.
    let outcome = tokio::task::spawn_blocking(move || {
        compute_optimize::optimize(
            &source,
            &options,
            target.as_ref(),
            &settings,
            &cancelled,
            |pct| {
                progress_hook.store(pct, Ordering::Relaxed);
            },
        )
    })
    .await
    .map_err(|e| ComputeError::Internal(format!("processing task: {e}")))??;

    let artifact = Artifact {
        key: dispatch.output_key.clone(),
        size: outcome.output.bytes.len() as u64,
        mime: outcome.output.mime.to_string(),
        width: outcome.output.width,
        height: outcome.output.height,
    };
    storage::upload(
        &ctx.s3,
        &ctx.bucket,
        &artifact.key,
        outcome.output.bytes,
        outcome.output.mime,
    )
    .await?;

    Ok(RunOutcome {
        artifact,
        resolved_options: has_target.then_some(outcome.resolved_options),
        target_met: outcome.target_met,
        achieved: outcome.achieved,
        encode_rounds: outcome.encode_rounds,
        compute_ms: outcome.compute_ms,
    })
}
