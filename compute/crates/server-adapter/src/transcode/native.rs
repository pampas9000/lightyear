//! Zero-conversion hand-off between a planar decoder and a planar encoder.
//!
//! `avifdec`/`heif-dec` hand back a file's own YUV planes as Y4M, and `avifenc`
//! accepts Y4M on stdin. When both sides agree on the layout, the samples never
//! have to be reconstructed into RGB and projected back — which is the largest
//! avoidable loss in a YUV→YUV transcode, and the difference between one matrix
//! round trip and none.

use super::args::build_avifenc_stdin_args;
use super::color::{resolve_native_cicp, validate_cicp};
use super::detect::InputProbe;
use super::encode::run_with_stdin;
use super::probe::ToolCapabilities;
use super::process::run_checked;
use super::profile::{SourceProfile, write_icc_file};
use anyhow::Result;
use compute_engine::Nclx;
use compute_types::{
    ChromaSubsampling, ColorRange, Format, ImageEditOp, MatrixCoefficients, Params,
    ToneMappingOperator, YuvFormat,
};
use std::path::Path;

/// A decoded Y4M stream together with the layout it carries.
pub struct NativeTranscode {
    pub y4m: Vec<u8>,
    pub nclx: Option<Nclx>,
    pub representation: YuvLayout,
}

/// Only identical planar layouts are supported by the native handoff.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct YuvLayout {
    pub chroma: ChromaSubsampling,
    pub matrix: MatrixCoefficients,
    pub range: ColorRange,
    pub depth: u8,
}

/// Decides whether the input can be forwarded to the target without touching a
/// single sample, and if so decodes it into raw planes.
///
/// Returns `None` whenever the premise does not hold, in which case the caller
/// falls back to the pixel pipeline.
///
/// NOTE: The native zero-conversion YUV path is marked **EXPERIMENTAL**. It is
/// strictly restricted to a whitelist of pairs (AVIF -> AVIF, HEIC -> AVIF) with
/// matching layout, no editing, no tone-mapping, and no alpha channels.
pub async fn try_native_path(
    input_path: &Path,
    probe: InputProbe,
    target_format: Format,
    params: &Option<Params>,
    ops: &[ImageEditOp],
    tools: &ToolCapabilities,
) -> Option<NativeTranscode> {
    // Any edit rewrites pixels, which invalidates the whole premise.
    if !ops.is_empty() {
        return None;
    }
    if target_format != Format::Avif || !tools.avifenc_available {
        return None;
    }
    // Tone mapping rewrites the transfer function — a pixel operation.
    if params
        .as_ref()
        .and_then(|p| p.color.as_ref())
        .and_then(|c| c.tone_mapping)
        .is_some_and(|tmo| tmo != ToneMappingOperator::None)
    {
        return None;
    }

    let tool = if probe.is_avif() && tools.avifdec_available {
        "avifdec"
    } else if probe.is_heic() && tools.heif_dec_available {
        "heif-dec"
    } else {
        return None;
    };

    let source_bytes = tokio::fs::read(input_path).await.ok()?;
    // Y4M has no alpha channel, so an alpha plane would be dropped silently.
    if compute_engine::probe_alpha_channel(&source_bytes) {
        return None;
    }

    let y4m = decode_y4m(tool, input_path).await.ok()?;
    let layout = compute_engine::probe_y4m(&y4m).ok()?;
    let nclx = compute_engine::probe_nclx(&source_bytes);
    let matrix = nclx
        .and_then(|n| n.matrix_coefficients())
        .unwrap_or(MatrixCoefficients::Bt709);

    let source = YuvLayout {
        chroma: layout.chroma,
        matrix,
        range: layout.range,
        depth: layout.depth,
    };
    let target = target_representation(target_format, params, &source)?;

    if source == target {
        Some(NativeTranscode {
            y4m,
            nclx,
            representation: source,
        })
    } else {
        None
    }
}

/// The layout the target encoder will be asked to produce.
fn target_representation(
    target_format: Format,
    params: &Option<Params>,
    source: &YuvLayout,
) -> Option<YuvLayout> {
    // AVIF is the only YUV-accepting target on this pipeline; JXL takes RGB.
    if target_format != Format::Avif {
        return None;
    }
    let color = params.as_ref().and_then(|p| p.color.as_ref());
    let avif = params
        .as_ref()
        .map(Params::parse_libavif_params)
        .transpose()
        .ok()?
        .unwrap_or_default();
    Some(YuvLayout {
        chroma: color
            .map(|c| c.chroma)
            .filter(|c| *c != ChromaSubsampling::Auto)
            .or(match avif.yuv {
                Some(YuvFormat::Yuv420) => Some(ChromaSubsampling::Yuv420),
                Some(YuvFormat::Yuv422) => Some(ChromaSubsampling::Yuv422),
                Some(YuvFormat::Yuv444) => Some(ChromaSubsampling::Yuv444),
                _ => None,
            })
            .unwrap_or(source.chroma),
        matrix: color.map(|c| c.matrix).unwrap_or(source.matrix),
        // The pipeline always writes full-range planes.
        range: ColorRange::Full,
        depth: color
            .map(|c| c.nominal_bit_depth)
            .or_else(|| avif.depth.and_then(|d| u8::try_from(d).ok()))
            .unwrap_or(source.depth),
    })
}

/// Streams the decoded planes straight into the target encoder.
pub async fn finish(
    native: NativeTranscode,
    output_path: &Path,
    params: &Option<Params>,
    source_profile: &SourceProfile,
) -> Result<()> {
    let mut avif_params = params
        .as_ref()
        .map(Params::parse_libavif_params)
        .transpose()?
        .unwrap_or_default();

    let cicp = resolve_native_cicp(native.nclx, params, source_profile.icc.is_some());
    validate_cicp(avif_params.cicp, cicp)?;
    avif_params.cicp = Some(cicp);
    let icc_file = if avif_params.icc_path.is_none()
        && let Some(icc) = source_profile.icc.as_deref()
    {
        let file = write_icc_file(icc).await?;
        avif_params.icc_path = Some(file.path().to_string_lossy().into_owned());
        Some(file)
    } else {
        None
    };

    // The Y4M header is authoritative for subsampling and depth here; passing a
    // mismatching `--yuv`/`--depth` would either be ignored or rejected.
    avif_params.yuv = None;
    avif_params.depth = None;

    let forwarded = native.representation;
    println!(
        "[Transcode] [EXPERIMENTAL] Forwarding native {:?} {}-bit {:?} planes to avifenc without colour conversion",
        forwarded.chroma, forwarded.depth, forwarded.range
    );

    let args = build_avifenc_stdin_args(output_path, &avif_params);
    let result = run_with_stdin(
        "avifenc",
        &args,
        &native.y4m,
        "Streaming native Y4M planes to avifenc (zero colour conversion)",
        "avifenc native y4m stream",
    )
    .await;
    drop(icc_file);
    result
}

/// Runs `tool <input> <temp>.y4m` and returns the raw Y4M bytes.
async fn decode_y4m(tool: &str, input_path: &Path) -> Result<Vec<u8>> {
    let temp_dir = tempfile::tempdir()?;
    let temp_y4m = temp_dir.path().join("native.y4m");

    let mut cmd = tokio::process::Command::new(tool);
    cmd.arg(input_path).arg(&temp_y4m);
    run_checked(&mut cmd, tool).await?;

    Ok(tokio::fs::read(&temp_y4m).await?)
}
