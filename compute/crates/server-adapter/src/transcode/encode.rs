use super::args::{
    build_avifenc_file_args, build_avifenc_stdin_args, build_cjxl_args, build_cwebp_args,
    build_heif_enc_args,
};
use super::color::{OutputColor, validate_cicp};
use super::probe::ToolCapabilities;
use super::profile::{SourceProfile, write_icc_file};
use anyhow::{Result, bail};
use compute_types::{
    ChromaSubsampling, Format, LibavifParams, MatrixCoefficients, Params, YuvFormat,
};
use image::DynamicImage;
use std::ffi::OsString;
use std::io::Cursor;
use std::path::Path;

/// Encodes the decoded image into `output_path` using the requested target format.
///
/// Opaque AVIF and JXL stream raw pixels; alpha-bearing AVIF uses lossless PNG.
/// Portable fallbacks must be able to represent the resolved output color.
pub async fn encode_to_target(
    img: &DynamicImage,
    output_path: &Path,
    target_format: Format,
    params_val: &Option<Params>,
    tools: &ToolCapabilities,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    match target_format {
        Format::Jxl => encode_jxl(img, output_path, params_val, tools, source_profile, color).await,
        Format::Avif => {
            encode_avif(img, output_path, params_val, tools, source_profile, color).await
        }
        Format::Heic => {
            encode_heic(img, output_path, params_val, tools, source_profile, color).await
        }
        Format::Webp => {
            encode_webp(img, output_path, params_val, tools, source_profile, color).await
        }
        Format::Jpeg | Format::Png => {
            encode_with_portable_engine(
                img,
                output_path,
                target_format,
                params_val,
                source_profile,
                color,
            )
            .await
        }
        _ => bail!(
            "Format {:?} currently not supported via worker transcoding",
            target_format
        ),
    }
}

async fn encode_jxl(
    img: &DynamicImage,
    output_path: &Path,
    params_val: &Option<Params>,
    tools: &ToolCapabilities,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    if !tools.cjxl_available {
        // The portable pure-Rust engine has no JXL encoder, so there is nothing
        // to fall back to. Fail with an actionable message instead of surfacing
        // the engine's generic "not supported" error.
        bail!(
            "JXL encoding requires the 'cjxl' CLI, but it was not found in PATH; \
             install libjxl or enable strict mode to fail fast"
        );
    }

    let mut jxl_params = params_val
        .as_ref()
        .map(Params::parse_libjxl_params)
        .transpose()?
        .unwrap_or_default();

    if color.tone_mapped && (jxl_params.color_space.is_some() || jxl_params.icc_pathname.is_some())
    {
        bail!(
            "tone mapping cannot be combined with explicit JXL decoder color hints; use color.color_space"
        );
    }

    // PAM has no metadata. Its label must describe the processed pixels,
    // not the original HDR transfer or a stale source ICC.
    let mut icc_file = None;
    if jxl_params.color_space.is_none() && jxl_params.icc_pathname.is_none() {
        if let Some(icc) = color.icc(source_profile) {
            if let Some(space) = compute_engine::classify_icc_color_space(icc) {
                jxl_params.color_space = Some(space.cjxl_shorthand().to_string());
            } else {
                let file = write_icc_file(icc).await?;
                jxl_params.icc_pathname = Some(file.path().to_string_lossy().into_owned());
                icc_file = Some(file);
            }
        } else if let Some(hint) = color.cjxl_hint() {
            jxl_params.color_space = Some(hint.to_string());
        } else if color.tone_mapped {
            bail!("the tone-mapped color space cannot be represented by the JXL encoder");
        }
    }

    let pam_bytes = compute_engine::write_pam_rgba(img);
    // The stdin payload is always PAM, never a JPEG, so no reconstruction.
    let args = build_cjxl_args(Path::new("-"), output_path, &jxl_params, false);

    let result = run_with_stdin(
        "cjxl",
        &args,
        &pam_bytes,
        "Streaming PAM to cjxl",
        "cjxl stream",
    )
    .await;
    drop(icc_file);
    result
}

async fn encode_avif(
    img: &DynamicImage,
    output_path: &Path,
    params_val: &Option<Params>,
    tools: &ToolCapabilities,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    let parsed_avif_params = params_val
        .as_ref()
        .map(Params::parse_libavif_params)
        .transpose()?
        .unwrap_or_default();

    validate_cicp(parsed_avif_params.cicp, color.cicp)?;
    if color.tone_mapped && parsed_avif_params.icc_path.is_some() {
        bail!("tone mapping cannot be combined with engine_params.icc_path");
    }
    if !tools.avifenc_available {
        if color.spec.bit_depth > 8
            || color.icc(source_profile).is_some()
            || parsed_avif_params.icc_path.is_some()
            || color.cicp != compute_types::Cicp::new(1, 13, 1)
        {
            bail!(
                "avifenc is required for 10-bit, ICC or non-default color encoding; portable fallback cannot preserve them"
            );
        }
        println!("[Transcode] avifenc unavailable; using portable 8-bit sRGB AVIF encoding");
        let quality = parsed_avif_params.quality.unwrap_or(80) as u8;
        let output_bytes = compute_engine::encode(img, Format::Avif, quality)?;
        tokio::fs::write(output_path, output_bytes).await?;
        return Ok(());
    }

    let mut avif_params = parsed_avif_params;

    avif_params.cicp = Some(color.cicp);

    let icc_file = if avif_params.icc_path.is_none()
        && let Some(icc) = color.icc(source_profile)
    {
        let file = write_icc_file(icc).await?;
        avif_params.icc_path = Some(file.path().to_string_lossy().into_owned());
        Some(file)
    } else {
        None
    };

    let effective_yuv = resolve_effective_yuv(img, params_val, &avif_params)?;
    avif_params.yuv = Some(effective_yuv);
    let wants_10bit = color.spec.bit_depth > 8;
    avif_params.depth = Some(if wants_10bit { 10 } else { 8 });

    let has_alpha = has_transparency(img);
    // If image has alpha, standard Y4M cannot carry it safely (only 8-bit C444alpha exists
    // and is not universally supported; libheif drops it completely).
    // avifenc also ignores `--sharpyuv` for Y4M input, so a sharp 4:2:0 request
    // must hand it RGB. Both cases route through a lossless intermediate PNG.
    // Sharp YUV is dropped rather than failing the job when this avifenc build
    // lacks it; 16-bit RGB is also excluded, since builds without libyuv reject it.
    if !tools.avifenc_sharpyuv || compute_engine::is_16bit_image(img) {
        avif_params.sharp_yuv = None;
    }
    let wants_sharp_yuv =
        avif_params.sharp_yuv == Some(true) && effective_yuv == YuvFormat::Yuv420;
    if has_alpha || wants_sharp_yuv {
        let temp_dir = tempfile::tempdir()?;
        let temp_png = temp_dir.path().join("avifenc_input.png");
        let png_bytes = compute_engine::encode(img, Format::Png, 100)?;
        tokio::fs::write(&temp_png, png_bytes).await?;

        let args = build_avifenc_file_args(&temp_png, output_path, &avif_params);
        let result = run_command(
            "avifenc",
            &args,
            "Encoding AVIF from intermediate PNG (alpha or sharp YUV)",
            "avifenc",
        )
        .await;
        drop(icc_file);
        return result;
    }

    let matrix = match color.cicp.matrix {
        6 => MatrixCoefficients::Bt601,
        9 => MatrixCoefficients::Bt2020Ncl,
        _ => MatrixCoefficients::Bt709,
    };
    let y4m_bytes = build_y4m(img, effective_yuv, wants_10bit, matrix);

    let args = build_avifenc_stdin_args(output_path, &avif_params);
    let result = run_with_stdin(
        "avifenc",
        &args,
        &y4m_bytes,
        "Streaming uncompressed Y4M to avifenc",
        "avifenc y4m stream",
    )
    .await;
    drop(icc_file);
    result
}

/// Encodes HEIC through the `heif-enc` CLI using PNG as the intermediate vehicle.
///
/// heif-enc takes PNG directly and natively handles 8/16-bit, alpha channels and ICC profiles.
async fn encode_heic(
    img: &DynamicImage,
    output_path: &Path,
    params_val: &Option<Params>,
    tools: &ToolCapabilities,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    if !tools.heif_enc_available {
        bail!(
            "HEIC encoding requires the 'heif-enc' CLI, but it was not found in PATH; \
             install libheif or enable strict mode to fail fast"
        );
    }

    let quality = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("quality"))
        .and_then(|q| q.as_u64())
        .map(|q| q.clamp(1, 100) as u8)
        .unwrap_or(80);

    let lossless = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("lossless"))
        .and_then(|v| v.as_bool())
        .unwrap_or(false);

    let has_alpha = has_transparency(img);
    let bit_depth = Some(color.spec.bit_depth);

    let temp_dir = tempfile::tempdir()?;
    let temp_png = temp_dir.path().join("input.png");

    let mut png_bytes = compute_engine::encode(img, Format::Png, 100)?;
    if let Some(icc) = color.icc(source_profile) {
        png_bytes = compute_engine::inject_icc_into_png(&png_bytes, icc);
    }
    tokio::fs::write(&temp_png, png_bytes).await?;

    let args = build_heif_enc_args(
        &temp_png,
        output_path,
        quality,
        lossless,
        has_alpha,
        bit_depth,
    );
    run_command("heif-enc", &args, "Encoding HEIC via heif-enc", "heif-enc").await
}

/// Encodes WebP through the `cwebp` CLI when available so that lossy quality
/// and method settings are honored; the portable pure-Rust encoder can only
/// emit lossless WebP and is kept as the fallback.
async fn encode_webp(
    img: &DynamicImage,
    output_path: &Path,
    params_val: &Option<Params>,
    tools: &ToolCapabilities,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    let quality = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("quality"))
        .and_then(|q| q.as_u64())
        .map(|q| q.clamp(1, 100) as u8)
        .unwrap_or(85);
    let lossless = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("lossless"))
        .and_then(|v| v.as_bool())
        .unwrap_or(false);
    let method = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("method"))
        .and_then(|v| v.as_u64())
        .map(|m| m.min(6) as u8);

    if !tools.cwebp_available {
        if color.icc(source_profile).is_some() {
            bail!("cwebp is required to preserve the source ICC profile in WebP output");
        }
        if !lossless {
            println!(
                "[Transcode] cwebp CLI unavailable; falling back to lossless WebP via portable engine"
            );
        }
        let output_bytes = compute_engine::encode(img, Format::Webp, quality)?;
        tokio::fs::write(output_path, output_bytes).await?;
        return Ok(());
    }

    // cwebp has no stdin input mode, so materialize a lossless PNG in a
    // scratch directory that is removed even when encoding fails.
    let scratch_dir = tempfile::tempdir()?;
    let tmp_input = scratch_dir.path().join("cwebp-src.png");
    let mut png_buffer = Cursor::new(Vec::new());
    img.write_to(&mut png_buffer, image::ImageFormat::Png)?;

    // `cwebp -metadata icc` copies the profile from the input file, so the
    // intermediate PNG has to carry it. Writing it here is what keeps a
    // wide-gamut source identifiable without converting its pixels.
    let mut png_bytes = png_buffer.into_inner();
    let icc = color.icc(source_profile);
    if let Some(icc) = icc {
        png_bytes = compute_engine::inject_icc_into_png(&png_bytes, icc);
    }
    tokio::fs::write(&tmp_input, png_bytes).await?;

    let args = build_cwebp_args(
        &tmp_input,
        output_path,
        quality,
        lossless,
        method,
        icc.is_some(),
    );
    let result = run_command("cwebp", &args, "cwebp CLI", "cwebp").await;
    drop(scratch_dir);
    result
}

/// Encodes through the portable pure-Rust engine and, for containers that the
/// engine cannot tag itself, injects the source ICC profile afterwards.
async fn encode_with_portable_engine(
    img: &DynamicImage,
    output_path: &Path,
    target_format: Format,
    params_val: &Option<Params>,
    source_profile: &SourceProfile,
    color: &OutputColor,
) -> Result<()> {
    let quality = params_val
        .as_ref()
        .and_then(|p| p.engine_params.get("quality"))
        .and_then(|q| q.as_u64())
        .map(|q| q.clamp(1, 100) as u8)
        .unwrap_or(85);
    let mut output_bytes = compute_engine::encode(img, target_format, quality)?;

    if let Some(icc) = color.icc(source_profile) {
        output_bytes = match target_format {
            Format::Jpeg => compute_engine::inject_icc_into_jpeg(&output_bytes, icc),
            Format::Png => compute_engine::inject_icc_into_png(&output_bytes, icc),
            _ => output_bytes,
        };
    }

    tokio::fs::write(output_path, output_bytes).await?;
    Ok(())
}

/// Resolves the effective YUV chroma subsampling. An explicit `color.chroma`
/// from the color pipeline wins, then the libavif `yuv` parameter, and both
/// `auto` and an absent value fall back to the engine's content detection.
fn resolve_effective_yuv(
    img: &DynamicImage,
    params_val: &Option<Params>,
    avif_params: &LibavifParams,
) -> Result<YuvFormat> {
    if let Some(chroma) = params_val
        .as_ref()
        .and_then(|p| p.color.as_ref())
        .map(|c| c.chroma)
    {
        return match chroma {
            ChromaSubsampling::Auto => Ok(detect_yuv(img)),
            ChromaSubsampling::Yuv444 => Ok(YuvFormat::Yuv444),
            ChromaSubsampling::Yuv422 => Ok(YuvFormat::Yuv422),
            ChromaSubsampling::Yuv420 => Ok(YuvFormat::Yuv420),
            ChromaSubsampling::Yuv400 => bail!(
                "monochrome (4:0:0) output is not supported by the AVIF encoder; choose 4:4:4, 4:2:2, 4:2:0 or auto"
            ),
        };
    }

    match avif_params.yuv {
        Some(YuvFormat::Auto) | None => Ok(detect_yuv(img)),
        Some(explicit) => Ok(explicit),
    }
}

/// Maps the engine's content-adaptive chroma decision onto an encoder format.
fn detect_yuv(img: &DynamicImage) -> YuvFormat {
    match compute_engine::detect_optimal_chroma_subsampling(img) {
        ChromaSubsampling::Yuv444 => YuvFormat::Yuv444,
        ChromaSubsampling::Yuv422 => YuvFormat::Yuv422,
        _ => YuvFormat::Yuv420,
    }
}

fn has_transparency(img: &DynamicImage) -> bool {
    match img {
        DynamicImage::ImageRgba8(pixels) => pixels.pixels().any(|p| p[3] < 255),
        DynamicImage::ImageRgba16(pixels) => pixels.pixels().any(|p| p[3] < 65535),
        DynamicImage::ImageLumaA8(pixels) => pixels.pixels().any(|p| p[1] < 255),
        DynamicImage::ImageLumaA16(pixels) => pixels.pixels().any(|p| p[1] < 65535),
        _ => false,
    }
}

/// Builds the uncompressed Y4M stream handed to avifenc.
fn build_y4m(
    img: &DynamicImage,
    yuv: YuvFormat,
    wants_10bit: bool,
    matrix: MatrixCoefficients,
) -> Vec<u8> {
    match yuv {
        YuvFormat::Yuv444 => {
            if wants_10bit {
                let (y, u, v) = compute_engine::rgb_to_yuv444_planar_10bit(img, matrix);
                compute_engine::write_y4m_c444_p10(img.width(), img.height(), &y, &u, &v)
            } else {
                let (y, u, v) = compute_engine::rgb_to_yuv444_planar(img, matrix);
                compute_engine::write_y4m_c444(img.width(), img.height(), &y, &u, &v)
            }
        }
        YuvFormat::Yuv422 => {
            if wants_10bit {
                let (y, u, v) = compute_engine::rgb_to_yuv422_planar_10bit(img, matrix);
                compute_engine::write_y4m_c422_p10(img.width(), img.height(), &y, &u, &v)
            } else {
                let (y, u, v) = compute_engine::rgb_to_yuv422_planar(img, matrix);
                compute_engine::write_y4m_c422(img.width(), img.height(), &y, &u, &v)
            }
        }
        // Direct uncompressed YUV 4:2:0 stream (zero intermediate compressed container)
        _ => {
            if wants_10bit {
                let (y, u, v) = compute_engine::rgb_to_yuv420_planar_10bit(img, matrix);
                compute_engine::write_y4m_c420_p10(img.width(), img.height(), &y, &u, &v)
            } else {
                let (y, u, v) = compute_engine::rgb_to_yuv420_planar(img, matrix);
                compute_engine::write_y4m_c420(img.width(), img.height(), &y, &u, &v)
            }
        }
    }
}

/// Spawns an encoder CLI and surfaces its stderr when it exits non-zero.
async fn run_command(
    program: &str,
    args: &[OsString],
    log_prefix: &str,
    error_label: &str,
) -> Result<()> {
    let mut cmd = tokio::process::Command::new(program);
    cmd.args(args);
    cmd.stdin(std::process::Stdio::null());
    println!("[Transcode] {}: {:?}", log_prefix, args);
    super::process::run_checked(&mut cmd, error_label).await
}

/// Spawns an encoder CLI, streams the uncompressed payload into its stdin and
/// surfaces its stderr when it exits non-zero.
pub(super) async fn run_with_stdin(
    program: &str,
    args: &[OsString],
    stdin_data: &[u8],
    log_prefix: &str,
    error_label: &str,
) -> Result<()> {
    let mut cmd = tokio::process::Command::new(program);
    cmd.args(args);
    cmd.stdin(std::process::Stdio::piped());
    println!("[Transcode] {}: {:?}", log_prefix, args);
    super::process::run_with_stdin(&mut cmd, stdin_data, error_label).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::ColorEncoding;
    use image::{ImageBuffer, LumaA, Rgba};

    fn rgba8() -> DynamicImage {
        DynamicImage::ImageRgba8(ImageBuffer::from_pixel(8, 8, Rgba([10, 20, 30, 255])))
    }

    fn rgba16() -> DynamicImage {
        DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
            8,
            8,
            Rgba([1000u16, 2000, 3000, 65535]),
        ))
    }

    fn color_params(chroma: ChromaSubsampling, depth: u8) -> Option<Params> {
        Some(Params {
            color: Some(ColorEncoding {
                chroma,
                nominal_bit_depth: depth,
                ..Default::default()
            }),
            ..Default::default()
        })
    }

    fn engine_yuv_params(yuv: &str) -> Option<Params> {
        Some(Params {
            engine_params: serde_json::json!({ "yuv": yuv }),
            ..Default::default()
        })
    }

    #[test]
    fn color_pipeline_chroma_wins_over_encoder_yuv() {
        let img = rgba8();
        let params = color_params(ChromaSubsampling::Yuv422, 8);
        let avif = params.as_ref().unwrap().parse_libavif_params().unwrap();
        assert_eq!(
            resolve_effective_yuv(&img, &params, &avif).unwrap(),
            YuvFormat::Yuv422
        );

        let legacy = engine_yuv_params("444");
        let avif = legacy.as_ref().unwrap().parse_libavif_params().unwrap();
        assert_eq!(
            resolve_effective_yuv(&img, &legacy, &avif).unwrap(),
            YuvFormat::Yuv444
        );
    }

    #[test]
    fn monochrome_chroma_is_rejected() {
        let img = rgba8();
        let params = color_params(ChromaSubsampling::Yuv400, 8);
        let avif = params.as_ref().unwrap().parse_libavif_params().unwrap();
        assert!(resolve_effective_yuv(&img, &params, &avif).is_err());
    }

    #[test]
    fn color_pipeline_depth_wins_over_encoder_depth() {
        let depth = |img: &DynamicImage, params: &Option<Params>| {
            OutputColor::resolve(img, Format::Avif, params, &SourceProfile::default())
                .unwrap()
                .spec
                .bit_depth
        };
        let mut shallow = color_params(ChromaSubsampling::Auto, 8);
        shallow.as_mut().unwrap().engine_params = serde_json::json!({"depth": 10});
        assert_eq!(depth(&rgba16(), &shallow), 8);

        let mut deep = color_params(ChromaSubsampling::Auto, 10);
        deep.as_mut().unwrap().engine_params = serde_json::json!({"depth": 8});
        assert_eq!(depth(&rgba16(), &deep), 10);
        assert_eq!(depth(&rgba16(), &None), 10);
        assert_eq!(depth(&rgba8(), &None), 8);
        assert_eq!(
            depth(
                &rgba16(),
                &Some(Params {
                    engine_params: serde_json::json!({"depth": 8}),
                    ..Default::default()
                })
            ),
            8
        );
    }

    #[test]
    fn gray_alpha16_is_transparent_and_keeps_requested_avif_layout() {
        let img =
            DynamicImage::ImageLumaA16(ImageBuffer::from_pixel(2, 2, LumaA([20_001u16, 30_000])));
        assert!(has_transparency(&img));
        assert_eq!(
            OutputColor::resolve(&img, Format::Avif, &None, &SourceProfile::default())
                .unwrap()
                .spec
                .bit_depth,
            10
        );
        let params = color_params(ChromaSubsampling::Yuv420, 10);
        let avif = params.as_ref().unwrap().parse_libavif_params().unwrap();
        assert_eq!(
            resolve_effective_yuv(&img, &params, &avif).unwrap(),
            YuvFormat::Yuv420
        );
    }

    #[test]
    fn source_nclx_maps_to_supported_jxl_color_hint() {
        let hint = |primaries| {
            OutputColor::resolve(
                &rgba16(),
                Format::Jxl,
                &None,
                &SourceProfile {
                    icc: None,
                    nclx: Some(compute_engine::Nclx {
                        primaries,
                        transfer: 16,
                        matrix: 9,
                        full_range: true,
                    }),
                },
            )
            .unwrap()
            .cjxl_hint()
        };
        assert_eq!(hint(9), Some("Rec2100PQ"));
        assert_eq!(hint(7), None);
    }

    #[tokio::test]
    async fn portable_avif_cannot_ignore_legacy_10bit_request() {
        let tools = ToolCapabilities {
            cjxl_available: false,
            djxl_available: false,
            avifenc_available: false,
            avifenc_sharpyuv: false,
            avifdec_available: false,
            heif_enc_available: false,
            heif_dec_available: false,
            cwebp_available: false,
        };
        let params = Some(Params {
            engine_params: serde_json::json!({"depth": 10}),
            ..Default::default()
        });
        let profile = SourceProfile::default();
        let img = rgba8();
        let color = OutputColor::resolve(&img, Format::Avif, &params, &profile).unwrap();
        let dir = tempfile::tempdir().unwrap();
        let error = encode_to_target(
            &img,
            &dir.path().join("out.avif"),
            Format::Avif,
            &params,
            &tools,
            &profile,
            &color,
        )
        .await
        .unwrap_err();
        assert!(error.to_string().contains("avifenc is required"));
    }
}
