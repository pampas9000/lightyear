//! Transcoding pipeline facade.
//!
//! `run_transcode` orchestrates the stages, each backed by a focused submodule:
//! tool probing (`probe`), magic-byte input detection (`detect`), decoding (`decode`),
//! image edits and color transforms (`pipeline`), source profile extraction
//! (`profile`), CLI argument construction (`args`), direct bypasses (`bypass`)
//! and target encoding (`encode`).

mod args;
mod bypass;
mod color;
mod decode;
mod detect;
mod encode;
mod native;
mod pipeline;
mod probe;
mod process;
mod profile;

#[cfg(test)]
mod tests;

use anyhow::Result;
use bypass::can_use_jpeg_to_jxl_direct_bypass;
use compute_engine::ImageEditOp;
use compute_types::{Format, Params};
use probe::ToolCapabilities;
use std::path::Path;

/// Executes transcoding pipeline via external tools or pure Rust memory fallback.
pub async fn run_transcode(
    input_path: &Path,
    output_path: &Path,
    target_format: Format,
    params_val: &Option<Params>,
    input_format: Option<Format>,
    ops: &[ImageEditOp],
) -> Result<()> {
    if let Some(params) = params_val {
        params.validate_for_format(target_format)?;
    }
    let tools = ToolCapabilities::cached().await;

    // Magic bytes are authoritative; the payload's declared format and the
    // path extension are only fallbacks (see `detect`).
    let probe = detect::InputProbe::from_file(input_path, input_format).await;

    // 1. Strict JPEG -> JXL direct bypass
    if can_use_jpeg_to_jxl_direct_bypass(probe.is_jpeg(), target_format, ops, params_val, &tools) {
        return bypass::run_jpeg_to_jxl_direct_bypass(input_path, output_path, params_val).await;
    }

    // Check strict requirement if external tools are unavailable.
    // HEIC target requires heif-enc, even in non-strict mode because there is no portable fallback.
    let is_strict = params_val.as_ref().and_then(|p| p.strict).unwrap_or(false);
    if is_strict || target_format == Format::Heic {
        tools.require(target_format, probe.is_heic(), params_val)?;
    }

    // 2. Zero-conversion native path: forward the decoder's YUV planes straight
    //    to the encoder when both sides already agree on the layout. This skips
    //    the YUV→RGB→YUV round trip entirely, which is the largest avoidable
    //    loss in a YUV→YUV transcode such as HEIC → AVIF.
    if let Some(native) =
        native::try_native_path(input_path, probe, target_format, params_val, ops, &tools).await
    {
        let source_profile = profile::load_source_profile(input_path, params_val).await?;
        return native::finish(native, output_path, params_val, &source_profile).await;
    }

    // 3. Decode into memory
    let mut img = decode::decode_to_image(input_path, probe, params_val, &tools).await?;

    // 4. Extract source colour metadata (ICC + nclx). Done before edits so the
    //    edit pipeline and the output labelling can both inherit the source's
    //    own description when the caller specifies none.
    let source_profile = profile::load_source_profile(input_path, params_val).await?;

    // 5. Apply edit pipeline if requested (aware of transfer characteristics)
    let output_color =
        color::OutputColor::resolve(&img, target_format, params_val, &source_profile)?;
    pipeline::apply_edits(&mut img, ops, &output_color)?;

    // 6. Color transformations: Tone Mapping (transfer conversion) and Dithering
    pipeline::apply_color_transforms(&mut img, params_val, &output_color);

    // 7. Stream or encode to target format
    encode::encode_to_target(
        &img,
        output_path,
        target_format,
        params_val,
        &tools,
        &source_profile,
        &output_color,
    )
    .await
}

fn truncate_str(s: &str, max_len: usize) -> &str {
    if s.len() <= max_len {
        s
    } else {
        let mut index = max_len;
        while !s.is_char_boundary(index) {
            index -= 1;
        }
        &s[..index]
    }
}
