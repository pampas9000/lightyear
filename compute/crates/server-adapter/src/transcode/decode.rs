use super::detect::InputProbe;
use super::probe::ToolCapabilities;
use super::process::run_checked;
use anyhow::{Result, bail};
use compute_types::{MatrixCoefficients, Params};
use image::DynamicImage;
use std::path::Path;

/// Decodes the input file into memory.
///
/// AVIF and HEIC inputs prefer the tool-based decode path into uncompressed Y4M
/// so high bit-depth and chroma survive; everything else goes through the
/// portable pure-Rust decoder. The `probe` comes from magic bytes, so a
/// mislabelled or extension-less file still takes the right path.
///
/// Y4M carries colour planes only. An input that declares an alpha plane is
/// therefore routed through 16-bit PNG, because the planar path would drop
/// transparency silently.
pub async fn decode_to_image(
    input_path: &Path,
    probe: InputProbe,
    params: &Option<Params>,
    tools: &ToolCapabilities,
) -> Result<DynamicImage> {
    let is_avif_input = probe.is_avif();
    let is_heic_input = probe.is_heic();

    let planar_tool = if is_avif_input && tools.avifdec_available {
        Some("avifdec")
    } else if is_heic_input {
        if !tools.heif_dec_available {
            bail!(
                "HEIC decoding requires 'heif-dec' CLI installed on worker, but it was not found in PATH"
            );
        }
        Some("heif-dec")
    } else {
        None
    };

    let Some(tool) = planar_tool else {
        let input_bytes = tokio::fs::read(input_path).await?;
        return compute_engine::load_from_memory(&input_bytes);
    };

    let source_bytes = tokio::fs::read(input_path).await.unwrap_or_default();

    if compute_engine::probe_alpha_channel(&source_bytes) {
        println!(
            "[Transcode] {} input declares an alpha plane; decoding via 16-bit PNG since Y4M cannot carry alpha",
            tool
        );
        return decode_alpha_with_tool(tool, input_path, &source_bytes).await;
    }

    decode_planar_with_tool(tool, input_path, &source_bytes, params).await
}

/// Decodes through an external CLI into Y4M planes and reconstructs RGB.
///
/// The CLI hands back the file's own YUV planes untouched, so the RGB
/// reconstruction matrix must come from the container's `colr`/`nclx` box —
/// using a caller-supplied default would mis-decode e.g. BT.601 or BT.2020-NCL
/// content. The source bytes are also used to bake in the EXIF orientation,
/// which the Y4M round trip would otherwise discard.
async fn decode_planar_with_tool(
    tool: &str,
    input_path: &Path,
    source_bytes: &[u8],
    params: &Option<Params>,
) -> Result<DynamicImage> {
    let temp_dir = tempfile::tempdir()?;
    let temp_y4m = temp_dir.path().join("decoded.y4m");

    let mut cmd = tokio::process::Command::new(tool);
    cmd.arg(input_path);
    cmd.arg(&temp_y4m);
    run_checked(&mut cmd, tool).await?;
    let y4m_bytes = tokio::fs::read(&temp_y4m).await?;

    let matrix = compute_engine::probe_nclx(source_bytes)
        .and_then(|nclx| nclx.matrix_coefficients())
        .unwrap_or_else(|| requested_matrix(params));

    let mut img = compute_engine::read_y4m_to_image(&y4m_bytes, matrix)?;
    compute_engine::apply_exif_orientation(&mut img, source_bytes);
    Ok(img)
}

/// Decodes through an external CLI into a 16-bit PNG.
///
/// This is the fallback for alpha-bearing sources: PNG is the only output these
/// tools offer that can carry both an alpha plane and more than 8 bits per
/// channel.
async fn decode_alpha_with_tool(
    tool: &str,
    input_path: &Path,
    source_bytes: &[u8],
) -> Result<DynamicImage> {
    let temp_dir = tempfile::tempdir()?;
    let temp_png = temp_dir.path().join("decoded.png");

    let mut cmd = tokio::process::Command::new(tool);
    cmd.arg(input_path);
    if tool == "avifdec" {
        // `-d 16` is PNG-only and keeps 10/12-bit sources from being truncated.
        cmd.arg("-d").arg("16");
    }
    cmd.arg(&temp_png);
    run_checked(&mut cmd, tool).await?;

    let png_bytes = tokio::fs::read(&temp_png).await?;
    let mut img = compute_engine::load_from_memory(&png_bytes)?;
    compute_engine::apply_exif_orientation(&mut img, source_bytes);
    Ok(img)
}

/// The matrix coefficients the engine used to interpret the decoded Y4M planes.
fn requested_matrix(params: &Option<Params>) -> MatrixCoefficients {
    params
        .as_ref()
        .and_then(|p| p.color.as_ref())
        .map(|c| c.matrix)
        .unwrap_or(MatrixCoefficients::Bt709)
}
