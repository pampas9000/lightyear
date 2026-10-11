use super::args::{build_avifenc_stdin_args, build_cjxl_args, build_cwebp_args};
use super::bypass::can_use_jpeg_to_jxl_direct_bypass;
use super::color::resolve_output_cicp;
use super::probe::ToolCapabilities;
use super::run_transcode;
use compute_engine::ImageEditOp;
use compute_types::{
    Cicp, ColorEncoding, ColorPrimaries, ColorRange, Format, LibavifAdvancedParams, LibavifParams,
    LibjxlParams, MatrixCoefficients, Params, ProfilePolicy, ToneMappingOperator,
    TransferCharacteristics, YuvFormat,
};
use std::ffi::OsString;
use std::path::Path;

#[test]
fn test_build_avifenc_stdin_args_cicp_and_range() {
    let output = Path::new("output.avif");
    let params = LibavifParams {
        quality: Some(85),
        alpha_quality: Some(90),
        speed: Some(6),
        jobs: Some(4),
        sharp_yuv: Some(true),
        yuv: Some(YuvFormat::Yuv420),
        depth: Some(10),
        color_range: Some(ColorRange::Full),
        cicp: Some(Cicp::new(12, 13, 6)),
        icc_path: Some("/tmp/test.icc".to_string()),
        target_size: None,
        advanced: Some(LibavifAdvancedParams {
            sharpness: Some(3),
            color_sharpness: Some(2),
            alpha_sharpness: Some(0),
        }),
    };

    let args = build_avifenc_stdin_args(output, &params);

    assert!(args.contains(&OsString::from("--stdin")));
    assert!(args.contains(&OsString::from("--qcolor")));
    assert!(args.contains(&OsString::from("85")));
    assert!(args.contains(&OsString::from("--qalpha")));
    assert!(args.contains(&OsString::from("90")));
    assert!(args.contains(&OsString::from("--speed")));
    assert!(args.contains(&OsString::from("6")));
    assert!(args.contains(&OsString::from("--jobs")));
    assert!(args.contains(&OsString::from("4")));
    assert!(args.contains(&OsString::from("--sharpyuv")));
    assert!(args.contains(&OsString::from("--depth")));
    assert!(args.contains(&OsString::from("10")));
    assert!(args.contains(&OsString::from("--yuv")));
    assert!(args.contains(&OsString::from("420")));
    assert!(args.contains(&OsString::from("-r")));
    assert!(args.contains(&OsString::from("full")));
    assert!(args.contains(&OsString::from("--cicp")));
    assert!(args.contains(&OsString::from("12/13/6")));
    assert!(args.contains(&OsString::from("--icc")));
    assert!(args.contains(&OsString::from("/tmp/test.icc")));

    assert_eq!(args.first().unwrap(), "--stdin");
    assert_eq!(args.last().unwrap(), "output.avif");
}

#[test]
fn test_avifenc_emits_single_color_sharpness() {
    // `sharpness` and `color_sharpness` are the same aom knob; emitting both
    // would silently let the last one win.
    let params = LibavifParams {
        advanced: Some(LibavifAdvancedParams {
            sharpness: Some(3),
            color_sharpness: Some(5),
            alpha_sharpness: Some(1),
        }),
        ..Default::default()
    };

    let args = build_avifenc_stdin_args(Path::new("output.avif"), &params);
    let color_sharpness: Vec<_> = args
        .iter()
        .filter(|arg| arg.to_string_lossy() == "c:sharpness=5")
        .collect();
    assert_eq!(
        color_sharpness.len(),
        1,
        "expected one c:sharpness, got {args:?}"
    );
    assert!(
        !args
            .iter()
            .any(|arg| arg.to_string_lossy() == "c:sharpness=3"),
        "the aliased `sharpness` value must not also be emitted: {args:?}"
    );

    // Only the legacy alias set: it must still be honored.
    let alias_only = LibavifParams {
        advanced: Some(LibavifAdvancedParams {
            sharpness: Some(4),
            ..Default::default()
        }),
        ..Default::default()
    };
    let args = build_avifenc_stdin_args(Path::new("output.avif"), &alias_only);
    assert!(
        args.iter()
            .any(|arg| arg.to_string_lossy() == "c:sharpness=4")
    );
}

#[test]
fn test_build_cjxl_args_with_dec_hints() {
    let input = Path::new("-");
    let output = Path::new("output.jxl");
    let params = LibjxlParams {
        distance: Some(1.0),
        quality: None,
        effort: Some(7),
        progressive: Some(true),
        color_space: Some("DisplayP3".to_string()),
        icc_pathname: Some("/tmp/p3.icc".to_string()),
        ..Default::default()
    };

    let args = build_cjxl_args(input, output, &params, false);

    assert!(args.contains(&OsString::from("--distance")));
    assert!(args.contains(&OsString::from("1")));
    assert!(args.contains(&OsString::from("--effort")));
    assert!(args.contains(&OsString::from("7")));
    assert!(args.contains(&OsString::from("-x")));
    assert!(args.contains(&OsString::from("color_space=DisplayP3")));
    assert!(args.contains(&OsString::from("icc_pathname=/tmp/p3.icc")));

    assert_eq!(args.last().unwrap(), "output.jxl");
    assert_eq!(&args[args.len() - 2], "-");
}

#[test]
fn test_build_cwebp_args_lossy_and_lossless() {
    let input = Path::new("input.png");
    let output = Path::new("output.webp");

    let lossy = build_cwebp_args(input, output, 80, false, Some(4), false);
    assert!(lossy.contains(&OsString::from("-q")));
    assert!(lossy.contains(&OsString::from("80")));
    assert!(lossy.contains(&OsString::from("-m")));
    assert!(lossy.contains(&OsString::from("4")));
    assert!(!lossy.contains(&OsString::from("-lossless")));
    assert_eq!(lossy[lossy.len() - 3], OsString::from("input.png"));
    assert_eq!(lossy[lossy.len() - 2], OsString::from("-o"));
    assert_eq!(lossy.last().unwrap(), "output.webp");

    let lossless = build_cwebp_args(input, output, 80, true, None, false);
    assert!(lossless.contains(&OsString::from("-lossless")));
    assert!(!lossless.contains(&OsString::from("-q")));
    assert!(!lossless.contains(&OsString::from("-m")));
}

#[test]
fn test_can_use_jpeg_to_jxl_direct_bypass() {
    let tools = ToolCapabilities {
        cjxl_available: true,
        djxl_available: true,
        avifenc_available: true,
        avifenc_sharpyuv: true,
        avifdec_available: true,
        heif_enc_available: true,
        heif_dec_available: true,
        cwebp_available: true,
    };
    let params = Some(Params {
        engine_params: serde_json::json!({
            "lossless": true
        }),
        ..Default::default()
    });

    assert!(can_use_jpeg_to_jxl_direct_bypass(
        true,
        Format::Jxl,
        &[],
        &params,
        &tools
    ));

    // False if edit ops exist
    let ops = vec![ImageEditOp::Grayscale];
    assert!(!can_use_jpeg_to_jxl_direct_bypass(
        true,
        Format::Jxl,
        &ops,
        &params,
        &tools
    ));

    // False if tone mapping is requested
    let tone_map_params = Some(Params {
        engine_params: serde_json::json!({ "lossless": true }),
        color: Some(compute_types::ColorEncoding {
            tone_mapping: Some(compute_types::ToneMappingOperator::AcesFilmic),
            ..Default::default()
        }),
        ..Default::default()
    });
    assert!(!can_use_jpeg_to_jxl_direct_bypass(
        true,
        Format::Jxl,
        &[],
        &tone_map_params,
        &tools
    ));

    // False if not JPEG
    assert!(!can_use_jpeg_to_jxl_direct_bypass(
        false,
        Format::Jxl,
        &[],
        &params,
        &tools
    ));

    let strip_params = Some(Params {
        engine_params: serde_json::json!({ "lossless": true }),
        profile_policy: Some(ProfilePolicy::Strip),
        ..Default::default()
    });
    assert!(!can_use_jpeg_to_jxl_direct_bypass(
        true,
        Format::Jxl,
        &[],
        &strip_params,
        &tools
    ));
}

#[test]
fn test_hdr_source_wins_over_partial_color_controls() {
    use compute_engine::Nclx;

    let params = Some(Params {
        color: Some(ColorEncoding {
            nominal_bit_depth: 10,
            chroma: compute_types::ChromaSubsampling::Yuv420,
            ..Default::default()
        }),
        ..Default::default()
    });
    let hdr = Some(Nclx {
        primaries: 9,
        transfer: 16,
        matrix: 9,
        full_range: true,
    });

    assert_eq!(
        super::color::source_transfer(&params, hdr),
        TransferCharacteristics::Pq
    );
    assert_eq!(
        resolve_output_cicp(&params, false, hdr),
        Cicp::new(9, 16, 1)
    );
}

#[test]
fn test_tone_mapping_changes_transfer_without_claiming_gamut_conversion() {
    use compute_engine::Nclx;

    let params = Some(Params {
        color: Some(ColorEncoding {
            tone_mapping: Some(ToneMappingOperator::AcesFilmic),
            ..Default::default()
        }),
        ..Default::default()
    });
    let hdr = Some(Nclx {
        primaries: 9,
        transfer: 16,
        matrix: 9,
        full_range: true,
    });

    assert_eq!(
        resolve_output_cicp(&params, false, hdr),
        Cicp::new(9, 13, 1)
    );
}

#[tokio::test]
async fn test_e2e_jxl_pam_stream_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.cjxl_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input.png");
    let output_path = temp_dir.path().join("output.jxl");

    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([255, 0, 0, 255]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::DisplayP3,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: compute_types::ChromaSubsampling::Yuv444,
            ..Default::default()
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Jxl,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "JXL transcode failed: {:?}", res);
    assert!(output_path.exists());
    assert!(std::fs::metadata(&output_path).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_avif_y4m_c444_stream_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input.png");
    let output_path = temp_dir.path().join("output.avif");

    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([255, 0, 0, 255]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        engine_params: serde_json::json!({
            "yuv": "444"
        }),
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::DisplayP3,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt601,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: compute_types::ChromaSubsampling::Yuv444,
            ..Default::default()
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Avif,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "AVIF Y4M C444 transcode failed: {:?}", res);
    assert!(output_path.exists());
    assert!(std::fs::metadata(&output_path).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_jpeg_to_jxl_direct_brunsli_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.cjxl_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input.jpg");
    let output_path = temp_dir.path().join("output.jxl");

    let img = image::DynamicImage::ImageRgb8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgb([200, 100, 50]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Jpeg)
        .unwrap();

    let params = Some(Params {
        engine_params: serde_json::json!({
            "lossless": true
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Jxl,
        &params,
        Some(Format::Jpeg),
        &[],
    )
    .await;
    assert!(res.is_ok(), "Direct Brunsli transcode failed: {:?}", res);
    assert!(output_path.exists());
    assert!(std::fs::metadata(&output_path).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_jpeg_to_jxl_bypass_without_extension() {
    let tools = ToolCapabilities::probe().await;
    if !tools.cjxl_available || !tools.djxl_available {
        eprintln!("cjxl/djxl unavailable, skipping extension-less bypass test");
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    // The worker materialises S3 keys verbatim; a key without an extension must
    // still trigger the direct JPEG→JXL bypass, so detection has to use bytes.
    let input_path = temp_dir.path().join("no_extension_input.tmp");
    let output_path = temp_dir.path().join("output.jxl");

    let img = image::DynamicImage::ImageRgb8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgb([200, 100, 50]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Jpeg)
        .unwrap();

    let probe = super::detect::InputProbe::from_file(&input_path, None).await;
    assert!(
        probe.is_jpeg(),
        "jpeg magic bytes must be detected without a .jpg extension"
    );

    let params = Some(Params {
        engine_params: serde_json::json!({
            "lossless": true
        }),
        ..Default::default()
    });

    // No declared format either: the bytes alone must drive the pipeline.
    let res = run_transcode(&input_path, &output_path, Format::Jxl, &params, None, &[]).await;
    assert!(res.is_ok(), "extension-less bypass failed: {:?}", res);
    assert!(std::fs::metadata(&output_path).unwrap().len() > 0);

    let decoded_path = temp_dir.path().join("decoded.png");
    let status = tokio::process::Command::new("djxl")
        .arg(&output_path)
        .arg(&decoded_path)
        .status()
        .await
        .unwrap();
    assert!(status.success(), "djxl failed to decode the bypass output");
}

#[tokio::test]
async fn test_e2e_avif_y4m_c444p10_stream_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input_16.png");
    let output_path = temp_dir.path().join("output_10bit.avif");

    let img = image::DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([65535, 32768, 0, 65535]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        engine_params: serde_json::json!({
            "yuv": "444",
            "depth": 10
        }),
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::Bt2020,
            transfer: TransferCharacteristics::Pq,
            matrix: MatrixCoefficients::Bt2020Ncl,
            range: ColorRange::Full,
            nominal_bit_depth: 10,
            chroma: compute_types::ChromaSubsampling::Yuv444,
            ..Default::default()
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Avif,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(
        res.is_ok(),
        "10-bit AVIF Y4M C444p10 transcode failed: {:?}",
        res
    );
    assert!(output_path.exists());
    assert!(std::fs::metadata(&output_path).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_avif_y4m_c420_stream_and_avifdec_y4m_roundtrip() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input.png");
    let avif_path = temp_dir.path().join("output_420.avif");
    let roundtrip_path = temp_dir.path().join("roundtrip.png");

    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([180, 90, 45, 255]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        engine_params: serde_json::json!({
            "yuv": "420"
        }),
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::Srgb,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: compute_types::ChromaSubsampling::Yuv420,
            ..Default::default()
        }),
        ..Default::default()
    });

    // Step 1: Encode PNG -> AVIF (via pure uncompressed Y4M C420 stdin streaming)
    let res = run_transcode(
        &input_path,
        &avif_path,
        Format::Avif,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "AVIF 420 encode failed: {:?}", res);
    assert!(avif_path.exists());

    // Step 2: Decode AVIF -> PNG (via avifdec uncompressed Y4M planar decode + read_y4m_to_image)
    let res2 = run_transcode(
        &avif_path,
        &roundtrip_path,
        Format::Png,
        &None,
        Some(Format::Avif),
        &[],
    )
    .await;
    assert!(res2.is_ok(), "AVIF y4m decode roundtrip failed: {:?}", res2);
    assert!(roundtrip_path.exists());
    assert!(std::fs::metadata(&roundtrip_path).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_avif_y4m_c422_stream_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input.png");
    let avif_path = temp_dir.path().join("output_422.avif");
    let roundtrip_path = temp_dir.path().join("roundtrip.png");

    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([120, 200, 80, 255]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        engine_params: serde_json::json!({
            "yuv": "422"
        }),
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::Srgb,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: compute_types::ChromaSubsampling::Yuv422,
            ..Default::default()
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &avif_path,
        Format::Avif,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "AVIF 422 encode failed: {:?}", res);
    assert!(avif_path.exists());

    let res2 = run_transcode(
        &avif_path,
        &roundtrip_path,
        Format::Png,
        &None,
        Some(Format::Avif),
        &[],
    )
    .await;
    assert!(res2.is_ok(), "AVIF 422 decode failed: {:?}", res2);
    assert!(roundtrip_path.exists());
}

#[tokio::test]
async fn test_e2e_heic_encoding_and_roundtrip() {
    let tools = ToolCapabilities::probe().await;
    if !tools.heif_enc_available || !tools.heif_dec_available {
        eprintln!("heif-enc/heif-dec unavailable, skipping HEIC e2e test");
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let png_input = temp_dir.path().join("heic_in.png");
    let heic_out = temp_dir.path().join("heic_out.heic");
    let png_roundtrip = temp_dir.path().join("heic_roundtrip.png");

    let mut img = image::RgbaImage::new(16, 16);
    for (x, y, px) in img.enumerate_pixels_mut() {
        *px = image::Rgba([(x * 15) as u8, (y * 15) as u8, 120, 255]);
    }
    image::DynamicImage::ImageRgba8(img)
        .save_with_format(&png_input, image::ImageFormat::Png)
        .unwrap();

    // 1. Transcode PNG -> HEIC (invoking encode_heic via heif-enc)
    let res = run_transcode(
        &png_input,
        &heic_out,
        Format::Heic,
        &None,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "PNG -> HEIC encode failed: {:?}", res);
    assert!(heic_out.exists());
    assert!(std::fs::metadata(&heic_out).unwrap().len() > 0);

    // 2. Transcode HEIC -> PNG (invoking decode via heif-dec)
    let res2 = run_transcode(
        &heic_out,
        &png_roundtrip,
        Format::Png,
        &None,
        Some(Format::Heic),
        &[],
    )
    .await;
    assert!(res2.is_ok(), "HEIC -> PNG decode failed: {:?}", res2);
    assert!(png_roundtrip.exists());
    let decoded = image::open(&png_roundtrip).unwrap().to_rgba8();
    assert_eq!(decoded.width(), 16);
    assert_eq!(decoded.height(), 16);
}

/// heif-dec's Y4M output is 8-bit 4:2:0 without a range tag; HEIC must keep its
/// depth and full-range levels on both the pixel path and the AVIF target.
#[tokio::test]
async fn test_e2e_10bit_heic_keeps_depth_and_range() {
    let tools = ToolCapabilities::probe().await;
    if !tools.heif_dec_available || !tools.heif_enc_available || !tools.avifdec_available {
        eprintln!("heif-enc/heif-dec/avifdec unavailable, skipping 10-bit HEIC test");
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let png_input = temp_dir.path().join("deep.png");
    let heic_file = temp_dir.path().join("deep.heic");
    let png_out = temp_dir.path().join("deep_out.png");
    let avif_out = temp_dir.path().join("deep_out.avif");
    let avif_png = temp_dir.path().join("deep_avif.png");

    // Dark-to-light ramp whose ends sit away from 0/65535, so a limited-range
    // misread would visibly clip them.
    let ramp = image::ImageBuffer::from_fn(32, 32, |x, _| {
        let v = 4096 + (x as u16) * 1800;
        image::Rgb([v, v, v])
    });
    image::DynamicImage::ImageRgb16(ramp)
        .save_with_format(&png_input, image::ImageFormat::Png)
        .unwrap();
    let status = tokio::process::Command::new("heif-enc")
        .args(["-b", "10", "-q", "95"])
        .arg(&png_input)
        .arg("-o")
        .arg(&heic_file)
        .status()
        .await
        .unwrap();
    assert!(status.success());

    let darkest = |img: &image::DynamicImage| img.to_rgb16().pixels().map(|p| p[0]).min().unwrap();

    run_transcode(&heic_file, &png_out, Format::Png, &None, Some(Format::Heic), &[])
        .await
        .unwrap();
    let decoded = image::open(&png_out).unwrap();
    assert!(compute_engine::is_16bit_image(&decoded), "HEIC decode lost depth");
    assert!(darkest(&decoded) > 2048, "HEIC decode clipped the dark end");

    run_transcode(&heic_file, &avif_out, Format::Avif, &None, Some(Format::Heic), &[])
        .await
        .unwrap();
    let info = tokio::process::Command::new("avifdec")
        .arg("--info")
        .arg(&avif_out)
        .output()
        .await
        .unwrap();
    let info = String::from_utf8_lossy(&info.stdout);
    assert!(info.contains("Bit Depth      : 10"), "HEIC -> AVIF lost depth:\n{info}");
    let status = tokio::process::Command::new("avifdec")
        .args(["-d", "16"])
        .arg(&avif_out)
        .arg(&avif_png)
        .status()
        .await
        .unwrap();
    assert!(status.success());
    let roundtrip = image::open(&avif_png).unwrap();
    assert!(darkest(&roundtrip) > 2048, "HEIC -> AVIF clipped the dark end");
}

/// `--sharpyuv` only works on RGB input, so sharp 4:2:0 goes through PNG and
/// must still honor the requested layout; 16-bit pixels (which avifenc builds
/// without libyuv cannot sharpen) must not fail the request.
#[tokio::test]
async fn test_e2e_sharp_yuv_avif_keeps_requested_layout() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        eprintln!("avifenc/avifdec unavailable, skipping sharp YUV test");
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();
    let rgb8 = temp_dir.path().join("sharp8.png");
    let rgba16 = temp_dir.path().join("sharp16.png");
    image::DynamicImage::ImageRgb8(image::ImageBuffer::from_fn(16, 16, |x, y| {
        if (x + y) % 2 == 0 { image::Rgb([255u8, 0, 0]) } else { image::Rgb([0, 0, 255]) }
    }))
    .save_with_format(&rgb8, image::ImageFormat::Png)
    .unwrap();
    image::DynamicImage::ImageRgba16(image::ImageBuffer::from_fn(16, 16, |x, _| {
        image::Rgba([60_000u16, 1_000, 1_000, if x < 8 { 30_000 } else { 65_535 }])
    }))
    .save_with_format(&rgba16, image::ImageFormat::Png)
    .unwrap();

    let params = |depth: u8| {
        Some(Params {
            engine: Some("libavif:avif".into()),
            engine_params: serde_json::json!({ "sharp_yuv": true }),
            color: Some(ColorEncoding {
                nominal_bit_depth: depth,
                chroma: compute_types::ChromaSubsampling::Yuv420,
                ..Default::default()
            }),
            ..Default::default()
        })
    };
    for (input, depth) in [(&rgb8, 8u8), (&rgba16, 10)] {
        let out = input.with_extension("avif");
        run_transcode(input, &out, Format::Avif, &params(depth), Some(Format::Png), &[])
            .await
            .unwrap();
        let info = tokio::process::Command::new("avifdec")
            .arg("--info")
            .arg(&out)
            .output()
            .await
            .unwrap();
        let info = String::from_utf8_lossy(&info.stdout);
        assert!(info.contains(&format!("Bit Depth      : {depth}")), "{info}");
        assert!(info.contains("Format         : YUV420"), "{info}");
    }
}

#[tokio::test]
async fn test_e2e_heic_decode_if_available() {
    let tools = ToolCapabilities::probe().await;
    if !tools.heif_dec_available {
        return;
    }

    // Check if heif-enc is available to create test input
    let heif_enc_available = tokio::process::Command::new("heif-enc")
        .arg("-v")
        .output()
        .await
        .map(|o| o.status.success())
        .unwrap_or(false);

    if !heif_enc_available {
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let png_input = temp_dir.path().join("input.png");
    let heic_file = temp_dir.path().join("test.heic");
    let roundtrip_png = temp_dir.path().join("roundtrip.png");

    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([200, 100, 50, 255]),
    ));
    img.save_with_format(&png_input, image::ImageFormat::Png)
        .unwrap();

    // Encode PNG to HEIC using heif-enc
    let enc_status = tokio::process::Command::new("heif-enc")
        .arg(&png_input)
        .arg("-o")
        .arg(&heic_file)
        .status()
        .await
        .unwrap();
    assert!(enc_status.success());
    assert!(heic_file.exists());

    // Decode HEIC to PNG via run_transcode (heif-dec -> 16-bit PNG)
    let res = run_transcode(
        &heic_file,
        &roundtrip_png,
        Format::Png,
        &None,
        Some(Format::Heic),
        &[],
    )
    .await;
    assert!(res.is_ok(), "HEIC decode via heif-dec failed: {:?}", res);
    assert!(roundtrip_png.exists());
    assert!(std::fs::metadata(&roundtrip_png).unwrap().len() > 0);
}

#[tokio::test]
async fn test_e2e_avif_alpha_encoding_survives_via_png_path() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        eprintln!("avifenc/avifdec unavailable, skipping alpha encoding test");
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let png_input = temp_dir.path().join("alpha_in.png");
    let avif_file = temp_dir.path().join("alpha_encoded.avif");
    let out_png = temp_dir.path().join("alpha_out.png");

    let mut img = image::RgbaImage::new(8, 8);
    for (x, _y, px) in img.enumerate_pixels_mut() {
        *px = image::Rgba([0, 255, 100, if x < 4 { 100 } else { 255 }]);
    }
    image::DynamicImage::ImageRgba8(img)
        .save_with_format(&png_input, image::ImageFormat::Png)
        .unwrap();

    // Transcode PNG with alpha -> AVIF (encode_avif via PNG path)
    let res = run_transcode(
        &png_input,
        &avif_file,
        Format::Avif,
        &None,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(
        res.is_ok(),
        "PNG -> AVIF encode with alpha failed: {:?}",
        res
    );

    let bytes = std::fs::read(&avif_file).unwrap();
    assert!(
        compute_engine::probe_alpha_channel(&bytes),
        "avifenc must preserve alpha channel in output"
    );

    // Decode back to PNG to verify alpha preserved
    let res2 = run_transcode(
        &avif_file,
        &out_png,
        Format::Png,
        &None,
        Some(Format::Avif),
        &[],
    )
    .await;
    assert!(res2.is_ok(), "AVIF -> PNG decode failed: {:?}", res2);

    let out = image::open(&out_png).unwrap().to_rgba8();
    let left = out.get_pixel(0, 0)[3] as i16;
    let right = out.get_pixel(7, 0)[3] as i16;
    assert!(
        (left - 100).abs() <= 5,
        "semi-transparent half must survive, got alpha {}",
        left
    );
    assert_eq!(right, 255, "opaque half must stay opaque");
}

#[tokio::test]
async fn test_e2e_avif_alpha_survives_the_planar_path() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        eprintln!("avifenc/avifdec unavailable, skipping alpha e2e test");
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let png_input = temp_dir.path().join("alpha_in.png");
    let avif_file = temp_dir.path().join("alpha.avif");
    let out_png = temp_dir.path().join("alpha_out.png");

    // The left half is semi-transparent so the alpha plane cannot be dismissed
    // as "fully opaque, safe to drop".
    let mut img = image::RgbaImage::new(8, 8);
    for (x, _y, px) in img.enumerate_pixels_mut() {
        *px = image::Rgba([255, 0, 0, if x < 4 { 128 } else { 255 }]);
    }
    image::DynamicImage::ImageRgba8(img)
        .save_with_format(&png_input, image::ImageFormat::Png)
        .unwrap();

    let status = tokio::process::Command::new("avifenc")
        .arg(&png_input)
        .arg(&avif_file)
        .status()
        .await
        .unwrap();
    assert!(status.success());

    let bytes = std::fs::read(&avif_file).unwrap();
    assert!(
        compute_engine::probe_alpha_channel(&bytes),
        "avifenc must declare an alpha auxiliary plane for this fixture"
    );

    // Y4M cannot carry alpha, so the decoder has to fall back to a container
    // that can; without that fallback the output would come back fully opaque.
    let res = run_transcode(
        &avif_file,
        &out_png,
        Format::Png,
        &None,
        Some(Format::Avif),
        &[],
    )
    .await;
    assert!(res.is_ok(), "alpha transcode failed: {:?}", res);

    let out = image::open(&out_png).unwrap().to_rgba8();
    let left = out.get_pixel(0, 0)[3] as i16;
    let right = out.get_pixel(7, 0)[3] as i16;
    assert!(
        (left - 128).abs() <= 2,
        "semi-transparent half must survive, got alpha {}",
        left
    );
    assert_eq!(right, 255, "opaque half must stay opaque");
}

#[test]
fn test_native_cicp_carries_the_source_matrix() {
    // The planes keep the source matrix, so a BT.601 source must not be
    // relabelled as BT.709 — the decoder would undo the wrong matrix.
    let bt601 = compute_engine::Nclx {
        primaries: 1,
        transfer: 13,
        matrix: 6,
        full_range: true,
    };
    assert_eq!(
        super::color::resolve_native_cicp(Some(bt601), &None, false).to_arg_string(),
        "1/13/6"
    );

    // An embedded profile governs primaries/transfer; the matrix still has to be
    // declared because it is not carried by the profile.
    assert_eq!(
        super::color::resolve_native_cicp(Some(bt601), &None, true).to_arg_string(),
        "2/2/6"
    );

    // Unknown/unspecified matrices fall back to the same default the pixel
    // pipeline uses, so the tag always matches how the planes were read.
    let unspecified = compute_engine::Nclx {
        primaries: 2,
        transfer: 2,
        matrix: 2,
        full_range: true,
    };
    assert_eq!(
        super::color::resolve_native_cicp(Some(unspecified), &None, false).to_arg_string(),
        "2/2/1"
    );
    assert_eq!(
        super::color::resolve_native_cicp(None, &None, false).to_arg_string(),
        "1/13/1"
    );
}

#[tokio::test]
async fn test_e2e_native_yuv_passthrough_for_avif() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifdec_available || !tools.avifenc_available {
        eprintln!("avif tools unavailable, skipping native path test");
        return;
    }

    let fixture = Path::new("../../../scripts/fixtures/quality/avif_8bit_420.avif");
    if !fixture.exists() {
        eprintln!("fixture unavailable, skipping native path test");
        return;
    }

    // Matching layout: the decoder's planes can be forwarded untouched.
    let probe = super::detect::InputProbe::from_file(fixture, Some(Format::Avif)).await;
    assert!(probe.is_avif());
    let native = super::native::try_native_path(fixture, probe, Format::Avif, &None, &[], &tools)
        .await
        .expect("AVIF → AVIF with a matching layout should take the native path");
    assert_eq!(
        native.representation.chroma,
        compute_types::ChromaSubsampling::Yuv420
    );
    assert_eq!(native.representation.depth, 8);

    // Any edit rewrites pixels, which invalidates the premise.
    let edited = super::native::try_native_path(
        fixture,
        probe,
        Format::Avif,
        &None,
        &[ImageEditOp::Grayscale],
        &tools,
    )
    .await;
    assert!(
        edited.is_none(),
        "edits must fall back to the pixel pipeline"
    );

    // JXL takes RGB, so a YUV hand-off cannot serve it.
    let to_jxl =
        super::native::try_native_path(fixture, probe, Format::Jxl, &None, &[], &tools).await;
    assert!(
        to_jxl.is_none(),
        "JXL takes RGB, so the native path cannot serve it"
    );

    // Asking for a different subsampling needs resampling, not a hand-off.
    let resampled = super::native::try_native_path(
        fixture,
        probe,
        Format::Avif,
        &Some(Params {
            color: Some(ColorEncoding {
                chroma: compute_types::ChromaSubsampling::Yuv444,
                ..Default::default()
            }),
            ..Default::default()
        }),
        &[],
        &tools,
    )
    .await;
    assert!(
        resampled.is_none(),
        "a different chroma layout needs resampling"
    );
}

#[tokio::test]
async fn test_e2e_native_passthrough_round_trips_avif() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifdec_available || !tools.avifenc_available {
        eprintln!("avif tools unavailable, skipping native round trip test");
        return;
    }

    let fixture = Path::new("../../../scripts/fixtures/quality/avif_8bit_420.avif");
    if !fixture.exists() {
        eprintln!("fixture unavailable, skipping native round trip test");
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();
    let out = temp_dir.path().join("native_out.avif");

    let res = run_transcode(fixture, &out, Format::Avif, &None, Some(Format::Avif), &[]).await;
    assert!(res.is_ok(), "native AVIF transcode failed: {:?}", res);
    assert!(out.exists());

    // The output must still decode with the same layout it went in with.
    let decoded = temp_dir.path().join("decoded.y4m");
    let status = tokio::process::Command::new("avifdec")
        .arg(&out)
        .arg(&decoded)
        .status()
        .await
        .unwrap();
    assert!(status.success());

    let layout =
        compute_engine::probe_y4m(&std::fs::read(&decoded).unwrap()).expect("valid Y4M output");
    assert_eq!(layout.width, 512);
    assert_eq!(layout.chroma, compute_types::ChromaSubsampling::Yuv420);
    assert_eq!(layout.depth, 8);
    assert_eq!(layout.range, ColorRange::Full);
}

#[tokio::test]
async fn test_e2e_tonemap_dither_pipeline() {
    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input_16.png");
    let output_path = temp_dir.path().join("output_8.png");

    // PQ/HLG transfer conversion is supported; gamut conversion is not.
    let img = image::DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([60000, 20000, 10000, 65535]),
    ));
    img.save_with_format(&input_path, image::ImageFormat::Png)
        .unwrap();

    let params = Some(Params {
        color: Some(ColorEncoding {
            color_space: None,
            primaries: ColorPrimaries::Srgb,
            transfer: TransferCharacteristics::Hlg,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: compute_types::ChromaSubsampling::Yuv444,
            tone_mapping: Some(ToneMappingOperator::AcesFilmic),
            dither: true,
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Png,
        &params,
        Some(Format::Png),
        &[],
    )
    .await;

    assert!(res.is_ok(), "Transcode failed: {:?}", res);
    assert!(output_path.exists());

    // Verify output is 8-bit image
    let out_img = image::open(&output_path).unwrap();
    assert_eq!(out_img.width(), 16);
    assert_eq!(out_img.height(), 16);
    assert!(matches!(out_img, image::DynamicImage::ImageRgba8(_)));
}

#[tokio::test]
async fn test_e2e_tonemapped_jxl_carries_sdr_transfer() {
    use jxl_oxide::color::{ColourEncoding, Primaries, TransferFunction};
    if !ToolCapabilities::probe().await.cjxl_available {
        eprintln!("cjxl unavailable, skipping tone-mapped JXL test");
        return;
    }
    let dir = tempfile::tempdir().unwrap();
    let input = dir.path().join("hdr.png");
    let output = dir.path().join("sdr.jxl");
    image::DynamicImage::ImageRgb16(image::ImageBuffer::from_pixel(
        8,
        8,
        image::Rgb([40_000u16, 30_000, 20_000]),
    ))
    .save(&input)
    .unwrap();
    let params = Some(Params {
        color: Some(ColorEncoding {
            color_space: Some(compute_types::ColorSpace::Bt2020Pq),
            nominal_bit_depth: 10,
            tone_mapping: Some(ToneMappingOperator::AcesFilmic),
            ..Default::default()
        }),
        engine_params: serde_json::json!({"lossless": true}),
        ..Default::default()
    });
    run_transcode(&input, &output, Format::Jxl, &params, None, &[])
        .await
        .unwrap();
    let decoded = jxl_oxide::JxlImage::builder().open(&output).unwrap();
    let ColourEncoding::Enum(color) = &decoded.image_header().metadata.colour_encoding else {
        panic!("processed JXL must use an explicit output color encoding");
    };
    assert_eq!(color.tf, TransferFunction::Srgb);
    assert_eq!(color.primaries, Primaries::Bt2100);
    let mut expected = image::open(&input).unwrap();
    compute_engine::apply_tone_mapping_to_image(
        &mut expected,
        ToneMappingOperator::AcesFilmic,
        TransferCharacteristics::Pq,
        TransferCharacteristics::Srgb,
    );
    // Render in the file's native encoding, avoiding an implicit gamut conversion.
    let rendered = decoded.render_frame(0).unwrap();
    let pixels = rendered.image_all_channels();
    let want = expected.to_rgb16().get_pixel(0, 0).0;
    for (actual, expected) in pixels.buf()[..3].iter().zip(want) {
        assert!((actual * 65_535.0 - expected as f32).abs() < 4.0);
    }
}

#[tokio::test]
async fn test_e2e_avif_rejects_conflicting_cicp_for_both_paths() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available || !tools.avifdec_available {
        eprintln!("avif tools unavailable, skipping CICP conflict test");
        return;
    }
    let dir = tempfile::tempdir().unwrap();
    let input = dir.path().join("source.png");
    let native_input = dir.path().join("source.avif");
    let output = dir.path().join("bad.avif");
    image::DynamicImage::ImageRgb8(image::ImageBuffer::from_pixel(
        8,
        8,
        image::Rgb([220, 80, 40]),
    ))
    .save(&input)
    .unwrap();
    run_transcode(&input, &native_input, Format::Avif, &None, None, &[])
        .await
        .unwrap();
    let params = Some(Params {
        engine_params: serde_json::json!({
            "cicp": {"primaries": 1, "transfer": 13, "matrix": 6}
        }),
        ..Default::default()
    });
    for input in [&input, &native_input] {
        let err = run_transcode(input, &output, Format::Avif, &params, None, &[])
            .await
            .unwrap_err();
        assert!(
            err.to_string().contains("conflicts with output pixels"),
            "{err}"
        );
        assert!(!output.exists());
    }

    // Selecting BT.601 for both pixels and metadata is valid.
    let consistent = Some(Params {
        color: Some(ColorEncoding {
            matrix: MatrixCoefficients::Bt601,
            ..Default::default()
        }),
        engine_params: serde_json::json!({
            "quality": 100,
            "cicp": {"primaries": 1, "transfer": 13, "matrix": 6}
        }),
        ..Default::default()
    });
    run_transcode(&input, &output, Format::Avif, &consistent, None, &[])
        .await
        .unwrap();
    let decoded_path = dir.path().join("decoded.png");
    let mut cmd = tokio::process::Command::new("avifdec");
    cmd.arg(&output).arg(&decoded_path);
    super::process::run_checked(&mut cmd, "avifdec")
        .await
        .unwrap();
    let decoded = image::open(decoded_path).unwrap().to_rgb8();
    for (actual, expected) in decoded.get_pixel(0, 0).0.into_iter().zip([220u8, 80, 40]) {
        assert!(actual.abs_diff(expected) <= 2, "{actual} != {expected}");
    }
}

#[tokio::test]
async fn test_e2e_icc_injection_avif_and_jxl() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available {
        eprintln!("avifenc not available, skipping ICC injection test");
        return;
    }

    let icc_source_path = std::path::Path::new("/System/Library/ColorSync/Profiles/Display P3.icc");
    if !icc_source_path.exists() {
        eprintln!("System ICC profile not available, skipping ICC injection test");
        return;
    }
    let original_icc = std::fs::read(icc_source_path).unwrap();

    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input_p3.jpg");
    let output_avif = temp_dir.path().join("output.avif");
    let output_avif_stripped = temp_dir.path().join("output_stripped.avif");

    // Construct synthetic JPEG with APP2 ICC profile marker
    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([220, 80, 40, 255]),
    ));
    let mut cursor = std::io::Cursor::new(Vec::new());
    img.write_to(&mut cursor, image::ImageFormat::Jpeg).unwrap();
    let raw_jpeg = cursor.into_inner();

    let mut jpeg_with_icc = Vec::new();
    jpeg_with_icc.extend_from_slice(&raw_jpeg[..2]); // SOI

    let app2_marker = 0xFFE2u16;
    let payload_len = (16 + original_icc.len()) as u16;
    jpeg_with_icc.extend_from_slice(&app2_marker.to_be_bytes());
    jpeg_with_icc.extend_from_slice(&payload_len.to_be_bytes());
    jpeg_with_icc.extend_from_slice(b"ICC_PROFILE\0");
    jpeg_with_icc.push(1); // seq
    jpeg_with_icc.push(1); // total
    jpeg_with_icc.extend_from_slice(&original_icc);
    jpeg_with_icc.extend_from_slice(&raw_jpeg[2..]);

    std::fs::write(&input_path, &jpeg_with_icc).unwrap();

    // 1. Transcode to AVIF with default profile policy (Keep)
    let res = run_transcode(
        &input_path,
        &output_avif,
        Format::Avif,
        &None,
        Some(Format::Jpeg),
        &[],
    )
    .await;
    assert!(res.is_ok(), "AVIF transcode failed: {:?}", res);
    assert!(output_avif.exists());

    let avif_bytes = std::fs::read(&output_avif).unwrap();
    let extracted_icc = compute_engine::extract_icc_from_memory(&avif_bytes);
    assert!(
        extracted_icc.is_some(),
        "AVIF output should preserve embedded ICC profile"
    );
    assert_eq!(
        extracted_icc.unwrap(),
        original_icc,
        "Preserved ICC bytes in AVIF must match original source ICC"
    );

    // 2. Transcode to AVIF with ProfilePolicy::Strip
    let strip_params = Some(Params {
        profile_policy: Some(ProfilePolicy::Strip),
        ..Default::default()
    });
    let res_strip = run_transcode(
        &input_path,
        &output_avif_stripped,
        Format::Avif,
        &strip_params,
        Some(Format::Jpeg),
        &[],
    )
    .await;
    assert!(
        res_strip.is_ok(),
        "Stripped AVIF transcode failed: {:?}",
        res_strip
    );
    assert!(output_avif_stripped.exists());

    let stripped_bytes = std::fs::read(&output_avif_stripped).unwrap();
    let stripped_icc = compute_engine::extract_icc_from_memory(&stripped_bytes);
    assert!(
        stripped_icc.is_none(),
        "Stripped AVIF output should NOT contain any ICC profile"
    );

    // 3. Transcode to JXL if cjxl available
    if tools.cjxl_available {
        let output_jxl = temp_dir.path().join("output.jxl");
        let res_jxl = run_transcode(
            &input_path,
            &output_jxl,
            Format::Jxl,
            &None,
            Some(Format::Jpeg),
            &[],
        )
        .await;
        assert!(res_jxl.is_ok(), "JXL transcode failed: {:?}", res_jxl);
        assert!(output_jxl.exists());
    }
}

#[tokio::test]
async fn test_e2e_auto_chroma_subsampling() {
    let tools = ToolCapabilities::probe().await;
    if !tools.avifenc_available {
        return;
    }

    let temp_dir = tempfile::tempdir().unwrap();

    // 1. UI Checkerboard (sharp color contrast)
    let mut ui_img = image::ImageBuffer::new(32, 32);
    for y in 0..32 {
        for x in 0..32 {
            if (x + y) % 2 == 0 {
                ui_img.put_pixel(x, y, image::Rgba([255, 0, 0, 255]));
            } else {
                ui_img.put_pixel(x, y, image::Rgba([0, 255, 255, 255]));
            }
        }
    }
    let ui_dyn = image::DynamicImage::ImageRgba8(ui_img);
    assert_eq!(
        compute_engine::detect_optimal_chroma_subsampling(&ui_dyn),
        compute_types::ChromaSubsampling::Yuv444
    );

    let ui_path = temp_dir.path().join("ui.png");
    ui_dyn
        .save_with_format(&ui_path, image::ImageFormat::Png)
        .unwrap();

    let ui_avif = temp_dir.path().join("ui_auto.avif");
    let auto_params = Some(Params {
        engine_params: serde_json::json!({
            "yuv": "auto",
            "speed": 8
        }),
        ..Default::default()
    });

    let res = run_transcode(
        &ui_path,
        &ui_avif,
        Format::Avif,
        &auto_params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(
        res.is_ok(),
        "Auto chroma transcode for UI failed: {:?}",
        res
    );
    assert!(ui_avif.exists());

    // 2. Smooth gradient (photographic)
    let mut grad_img = image::ImageBuffer::new(64, 64);
    for y in 0..64 {
        for x in 0..64 {
            let v = ((x + y) * 255 / 128) as u8;
            grad_img.put_pixel(x, y, image::Rgba([v, v, v, 255]));
        }
    }
    let grad_dyn = image::DynamicImage::ImageRgba8(grad_img);
    assert_eq!(
        compute_engine::detect_optimal_chroma_subsampling(&grad_dyn),
        compute_types::ChromaSubsampling::Yuv420
    );

    let grad_path = temp_dir.path().join("grad.png");
    grad_dyn
        .save_with_format(&grad_path, image::ImageFormat::Png)
        .unwrap();

    let grad_avif = temp_dir.path().join("grad_auto.avif");
    let res_grad = run_transcode(
        &grad_path,
        &grad_avif,
        Format::Avif,
        &auto_params,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(
        res_grad.is_ok(),
        "Auto chroma transcode for gradient failed: {:?}",
        res_grad
    );
    assert!(grad_avif.exists());
}

#[test]
fn test_resolve_output_cicp_matrix_tracks_engine() {
    use compute_engine::Nclx;
    use compute_types::{
        ColorEncoding, ColorPrimaries, MatrixCoefficients, TransferCharacteristics,
    };

    // No color params + preserved source ICC: matrix must still be BT.709 so
    // the decoder reverses the engine's RGB→YUV conversion; primaries/transfer
    // stay unspecified and are governed by the ICC.
    let cicp = resolve_output_cicp(&None, true, None);
    assert_eq!(cicp, Cicp::new(2, 2, 1));

    // No color params, no ICC, SDR source: decoded samples are treated as sRGB.
    let cicp = resolve_output_cicp(&None, false, None);
    assert_eq!(cicp, Cicp::new(1, 13, 1));

    // No color params, no ICC, but an HDR (PQ) source: the samples keep their
    // transfer function — the engine does not tone map unless asked, so an
    // sRGB label would be factually wrong. Primaries are inherited too.
    let cicp = resolve_output_cicp(
        &None,
        false,
        Some(Nclx {
            primaries: 9,
            transfer: 16,
            matrix: 9,
            full_range: true,
        }),
    );
    assert_eq!(cicp, Cicp::new(9, 16, 1));

    // An HLG source is inherited the same way.
    let cicp = resolve_output_cicp(
        &None,
        false,
        Some(Nclx {
            primaries: 9,
            transfer: 18,
            matrix: 9,
            full_range: true,
        }),
    );
    assert_eq!(cicp, Cicp::new(9, 18, 1));

    // An unspecified source transfer must not be promoted to a claim; the
    // SDR baseline applies instead.
    let cicp = resolve_output_cicp(
        &None,
        false,
        Some(Nclx {
            primaries: 2,
            transfer: 2,
            matrix: 6,
            full_range: true,
        }),
    );
    assert_eq!(cicp, Cicp::new(1, 13, 1));

    // Explicit color encoding with an embedded source ICC: the matrix still
    // tracks the engine, while primaries/transfer stay unspecified so the
    // profile governs instead of contradicting a hard-coded tag.
    let params = Some(Params {
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::DisplayP3,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Unspecified,
            ..Default::default()
        }),
        ..Default::default()
    });
    assert_eq!(resolve_output_cicp(&params, true, None), Cicp::new(2, 2, 1));

    // Same explicit encoding without a source ICC: the requested target is
    // advertised so untagged samples are decoded in the intended space.
    assert_eq!(
        resolve_output_cicp(&params, false, None),
        Cicp::new(12, 13, 1)
    );

    let params = Some(Params {
        color: Some(ColorEncoding {
            primaries: ColorPrimaries::Bt2020,
            transfer: TransferCharacteristics::Pq,
            matrix: MatrixCoefficients::Bt2020Ncl,
            ..Default::default()
        }),
        ..Default::default()
    });
    assert_eq!(
        resolve_output_cicp(&params, false, None),
        Cicp::new(9, 16, 9)
    );
}

#[test]
fn test_color_space_couples_primaries_and_transfer() {
    use compute_types::{ColorEncoding, ColorSpace};

    // The coupled color_space overrides the legacy standalone fields.
    let params = Some(Params {
        color: Some(ColorEncoding {
            color_space: Some(ColorSpace::Bt2020Pq),
            primaries: ColorPrimaries::Srgb,
            transfer: TransferCharacteristics::Srgb,
            ..Default::default()
        }),
        ..Default::default()
    });
    assert_eq!(
        resolve_output_cicp(&params, false, None),
        Cicp::new(9, 16, 1)
    );
}

#[tokio::test]
async fn test_e2e_p3_wide_gamut_preserved_avif_and_jxl() {
    let tools = ToolCapabilities::probe().await;
    let input = std::path::Path::new("../../../scripts/fixtures/external/webkit_logo_p3.png");
    if !input.exists() {
        eprintln!("P3 fixture unavailable, skipping wide-gamut e2e test");
        return;
    }
    let temp_dir = tempfile::tempdir().unwrap();

    // AVIF with no explicit color pipeline: the container must still carry a
    // CICP whose matrix matches the engine, otherwise avifenc defaults to
    // BT.601 and the whole image darkens (the reported "dull" P3 output).
    if tools.avifenc_available && tools.avifdec_available {
        let out = temp_dir.path().join("logo.avif");
        run_transcode(input, &out, Format::Avif, &None, Some(Format::Png), &[])
            .await
            .unwrap();
        let decoded = temp_dir.path().join("logo_avif.png");
        let status = tokio::process::Command::new("avifdec")
            .arg(&out)
            .arg(&decoded)
            .status()
            .await
            .unwrap();
        assert!(status.success());
        let img = image::open(&decoded).unwrap().to_rgb8();
        let corner = img.get_pixel(50, 50)[0] as f32 / 255.0;
        let center = img.get_pixel(500, 500)[0] as f32 / 255.0;
        assert!(corner > 0.98, "P3 red background dimmed to {}", corner);
        assert!(
            corner - center > 0.01,
            "hidden logo contrast lost: corner={} center={}",
            corner,
            center
        );
    }

    // JXL must describe the source color space with cjxl's native encoding
    // rather than handing the raw Photoshop ICC to `icc_pathname`, which
    // flattens the subtle logo detail at lossy distances.
    if tools.cjxl_available && tools.djxl_available {
        let out = temp_dir.path().join("logo.jxl");
        let params = Some(Params {
            engine_params: serde_json::json!({ "quality": 90 }),
            ..Default::default()
        });
        run_transcode(input, &out, Format::Jxl, &params, Some(Format::Png), &[])
            .await
            .unwrap();
        let decoded = temp_dir.path().join("logo_jxl.png");
        let status = tokio::process::Command::new("djxl")
            .arg(&out)
            .arg(&decoded)
            .status()
            .await
            .unwrap();
        assert!(status.success());
        let img = image::open(&decoded).unwrap().to_rgb8();
        let corner = img.get_pixel(50, 50)[0] as f32;
        let center = img.get_pixel(500, 500)[0] as f32;
        assert!(
            corner - center > 1.0,
            "hidden logo contrast lost in JXL: corner={} center={}",
            corner,
            center
        );
    }
}

#[test]
fn test_cwebp_uses_metadata_flag_not_icc_profile() {
    // `cwebp` errors out on `-icc_profile`; the profile must travel via
    // `-metadata icc` plus an ICC-injected intermediate file.
    let input = Path::new("in.png");
    let output = Path::new("out.webp");

    let with_icc = build_cwebp_args(input, output, 80, false, None, true);
    assert!(
        !with_icc
            .iter()
            .any(|a| a.to_string_lossy() == "-icc_profile"),
        "cwebp rejects -icc_profile: {with_icc:?}"
    );
    let metadata_pos = with_icc
        .iter()
        .position(|a| a.to_string_lossy() == "-metadata")
        .expect("expected a -metadata flag");
    assert_eq!(with_icc[metadata_pos + 1].to_string_lossy(), "icc");

    let without_icc = build_cwebp_args(input, output, 80, false, None, false);
    assert!(
        !without_icc
            .iter()
            .any(|a| a.to_string_lossy() == "-metadata"),
        "no ICC means no -metadata flag: {without_icc:?}"
    );
}

#[tokio::test]
async fn test_e2e_webp_preserves_icc() {
    let tools = ToolCapabilities::probe().await;
    if !tools.cwebp_available {
        eprintln!("cwebp not available, skipping WebP ICC test");
        return;
    }

    let icc_source = std::path::Path::new("/System/Library/ColorSync/Profiles/Display P3.icc");
    if !icc_source.exists() {
        eprintln!("system ICC profile unavailable, skipping WebP ICC test");
        return;
    }
    let original_icc = std::fs::read(icc_source).unwrap();

    let temp_dir = tempfile::tempdir().unwrap();
    let input_path = temp_dir.path().join("input_p3.png");
    let output_path = temp_dir.path().join("output.webp");

    // A PNG carrying the P3 profile; the engine extracts it from the source
    // bytes and must re-inject it into the WebP output.
    let img = image::DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
        16,
        16,
        image::Rgba([220, 80, 40, 255]),
    ));
    let mut cursor = std::io::Cursor::new(Vec::new());
    img.write_to(&mut cursor, image::ImageFormat::Png).unwrap();
    let tagged = compute_engine::inject_icc_into_png(&cursor.into_inner(), &original_icc);
    std::fs::write(&input_path, tagged).unwrap();

    let res = run_transcode(
        &input_path,
        &output_path,
        Format::Webp,
        &None,
        Some(Format::Png),
        &[],
    )
    .await;
    assert!(res.is_ok(), "WebP transcode failed: {:?}", res);

    let webp_bytes = std::fs::read(&output_path).unwrap();
    let extracted = compute_engine::extract_icc_from_memory(&webp_bytes)
        .expect("WebP output should carry the source ICC profile");
    assert_eq!(
        extracted, original_icc,
        "WebP ICC must match the source bytes"
    );
}
