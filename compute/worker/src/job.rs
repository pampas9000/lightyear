//! Executes one dispatch: download, process/optimize in a child process, upload.

use crate::error::ComputeError;
use crate::process::{self, ComputeProcess, Request};
use crate::storage;
use compute_imageops::Settings;
use compute_types::ImageOptions;
use compute_types::protocol::{Achieved, Artifact, Dispatch};
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::AtomicU8;
use tokio::sync::watch;

pub const OP_IMAGE_CONVERT: &str = "image.convert";

pub struct Context {
    pub s3: aws_sdk_s3::Client,
    pub bucket: String,
    pub max_source_bytes: u64,
    /// This executable; started with `process::ARG` for each computation.
    pub compute_program: PathBuf,
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

/// Runs one dispatch. Once `cancel` turns true, returns `Cancelled` promptly;
/// the computation is killed and reaped before that, so nothing of this job
/// keeps running afterwards.
pub async fn run(
    ctx: &Context,
    dispatch: &Dispatch,
    mut cancel: watch::Receiver<bool>,
    progress: Arc<AtomicU8>,
) -> Result<RunOutcome, ComputeError> {
    if dispatch.op != OP_IMAGE_CONVERT {
        return Err(ComputeError::InvalidRequest(format!(
            "unknown op {:?}",
            dispatch.op
        )));
    }
    let source = unless_cancelled(
        &mut cancel,
        storage::download(
            &ctx.s3,
            &ctx.bucket,
            &dispatch.source_key,
            ctx.max_source_bytes,
        ),
    )
    .await?;
    let has_target = dispatch.target.is_some();
    let request = Request::new(
        dispatch.options.clone(),
        dispatch.target.clone(),
        &ctx.settings,
    );
    let computed = ComputeProcess::spawn(&ctx.compute_program, &request, source)?
        .finish(&mut cancel, &progress)
        .await?;

    let artifact = Artifact {
        key: dispatch.output_key.clone(),
        size: computed.bytes.len() as u64,
        mime: computed.mime.clone(),
        width: computed.width,
        height: computed.height,
    };
    unless_cancelled(
        &mut cancel,
        storage::upload(
            &ctx.s3,
            &ctx.bucket,
            &artifact.key,
            computed.bytes,
            &computed.mime,
        ),
    )
    .await?;

    Ok(RunOutcome {
        artifact,
        resolved_options: has_target.then_some(computed.resolved_options),
        target_met: computed.target_met,
        achieved: computed.achieved,
        encode_rounds: computed.encode_rounds,
        compute_ms: computed.compute_ms,
    })
}

/// Network steps hold no CPU worth reclaiming; they are simply abandoned.
async fn unless_cancelled<T>(
    cancel: &mut watch::Receiver<bool>,
    step: impl Future<Output = Result<T, ComputeError>>,
) -> Result<T, ComputeError> {
    tokio::select! {
        result = step => result,
        () = process::cancelled(cancel) => Err(ComputeError::Cancelled),
    }
}
