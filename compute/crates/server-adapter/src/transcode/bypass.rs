use super::args::build_cjxl_args;
use super::probe::ToolCapabilities;
use super::process::run_checked;
use anyhow::Result;
use compute_engine::ImageEditOp;
use compute_types::{Format, LibjxlParams, Params, ProfilePolicy};
use std::path::Path;

/// Strict JPEG -> JXL direct bypass: when a lossless JPEG reconstruction is
/// requested and there are no edits, cjxl can wrap the JPEG directly instead of
/// going through decode/re-encode.
pub fn can_use_jpeg_to_jxl_direct_bypass(
    is_jpeg: bool,
    target_format: Format,
    ops: &[ImageEditOp],
    params: &Option<Params>,
    tools: &ToolCapabilities,
) -> bool {
    if !is_jpeg || target_format != Format::Jxl {
        return false;
    }
    // Any pixel edit invalidates the direct lossless bitstream reconstruction
    if !ops.is_empty() {
        return false;
    }
    if !tools.cjxl_available {
        return false;
    }
    if params
        .as_ref()
        .and_then(|p| p.profile_policy)
        .is_some_and(|policy| policy == ProfilePolicy::Strip)
    {
        return false;
    }
    // Any color transformation (Tone Mapping / HDR->SDR / Bit depth downscaling)
    // requires pixel-level computation, which invalidates direct bitstream reconstruction.
    if let Some(color) = params.as_ref().and_then(|p| p.color.as_ref()) {
        if color
            .tone_mapping
            .is_some_and(|t| t != compute_types::ToneMappingOperator::None)
        {
            return false;
        }
        if color.nominal_bit_depth == 8 {
            // Note: JPEG is natively 8-bit, but if user explicitly asks for color adjustments,
            // don't bypass.
        }
    }
    if let Some(p) = params {
        if let Ok(jxl_opts) = p.parse_libjxl_params() {
            if jxl_opts.lossless.unwrap_or(false) || jxl_opts.jpeg_reconstruction.unwrap_or(false) {
                return true;
            }
        }
    }
    false
}

/// Runs the direct cjxl JPEG -> JXL bypass.
pub async fn run_jpeg_to_jxl_direct_bypass(
    input_path: &Path,
    output_path: &Path,
    params_val: &Option<Params>,
) -> Result<()> {
    let jxl_params = if let Some(params) = params_val {
        params.parse_libjxl_params()?
    } else {
        LibjxlParams {
            lossless: Some(true),
            ..Default::default()
        }
    };
    let args = build_cjxl_args(input_path, output_path, &jxl_params, true);
    let mut cmd = tokio::process::Command::new("cjxl");
    cmd.args(&args);
    println!("[Transcode] Running direct cjxl bypass: {:?}", args);
    run_checked(&mut cmd, "Direct cjxl bypass").await
}
