pub mod color;
pub mod dither;
pub mod edit;
pub mod tonemap;

use anyhow::{Result, bail};
use compute_types::Format;
use image::DynamicImage;
use std::io::Cursor;

pub use color::{
    IccColorSpace, Nclx, OutputColorSpec, Y4mLayout, apply_output_color_pipeline,
    classify_icc_color_space, detect_optimal_chroma_subsampling, extract_exif_orientation,
    extract_icc_from_memory, hlg_ootf, inject_icc_into_jpeg, inject_icc_into_png,
    probe_alpha_channel, probe_nclx, probe_y4m, read_y4m_to_image, rgb_to_yuv420_planar,
    rgb_to_yuv420_planar_10bit, rgb_to_yuv422_planar, rgb_to_yuv422_planar_10bit,
    rgb_to_yuv444_planar, rgb_to_yuv444_planar_10bit, write_pam_rgba, write_y4m_c420,
    write_y4m_c420_p10, write_y4m_c422, write_y4m_c422_p10, write_y4m_c444, write_y4m_c444_p10,
};
pub use dither::{dither_rgb16_to_rgb8, dither_rgba16_to_rgba8, quantize_16bit_to_8bit};
pub use edit::{ImageEditOp, apply_pipeline, apply_pipeline_with_transfer};
pub use tonemap::{apply_tone_mapping_rgb, apply_tone_mapping_to_image, tonemap_scalar};

/// Whether integer pixel samples need a 16-bit working buffer.
pub fn is_16bit_image(img: &DynamicImage) -> bool {
    matches!(
        img,
        DynamicImage::ImageRgb16(_)
            | DynamicImage::ImageRgba16(_)
            | DynamicImage::ImageLuma16(_)
            | DynamicImage::ImageLumaA16(_)
    )
}

fn is_jxl(data: &[u8]) -> bool {
    if data.len() < 2 {
        return false;
    }
    if data[0] == 0xFF && data[1] == 0x0A {
        return true;
    }
    if data.len() >= 12
        && &data[0..12]
            == &[
                0x00, 0x00, 0x00, 0x0C, 0x4A, 0x58, 0x4C, 0x20, 0x0D, 0x0A, 0x87, 0x0A,
            ]
    {
        return true;
    }
    false
}

pub fn load_from_memory(data: &[u8]) -> Result<DynamicImage> {
    let mut img = if is_jxl(data) {
        use jxl_oxide::integration::JxlDecoder;
        let decoder = JxlDecoder::new(Cursor::new(data))
            .map_err(|e| anyhow::anyhow!("Failed to initialize JXL decoder: {}", e))?;
        DynamicImage::from_decoder(decoder)
            .map_err(|e| anyhow::anyhow!("Failed to decode JXL image: {}", e))?
    } else {
        image::load_from_memory(data)?
    };

    apply_exif_orientation(&mut img, data);
    Ok(img)
}

/// Rotates/flips the image according to the container's EXIF orientation tag.
///
/// Phone photos are routinely stored sideways with the rotation recorded only in
/// EXIF. Since transcoding drops the tag, the rotation has to be baked into the
/// pixels or the output ships visibly rotated. Returns `true` when a transform
/// was applied.
///
/// Not used on the JPEG→JXL direct-bypass path, which reconstructs the original
/// JPEG bit-exactly and therefore keeps its EXIF (and orientation) intact.
pub fn apply_exif_orientation(img: &mut DynamicImage, data: &[u8]) -> bool {
    let Some(raw) = color::extract_exif_orientation(data) else {
        return false;
    };
    let Some(orientation) = image::metadata::Orientation::from_exif(raw) else {
        return false;
    };
    if orientation == image::metadata::Orientation::NoTransforms {
        return false;
    }
    img.apply_orientation(orientation);
    true
}

/// A quality-aware encoder helper that converts a DynamicImage into bytes.
pub fn encode(img: &DynamicImage, format: Format, quality: u8) -> Result<Vec<u8>> {
    let mut out_buffer = Cursor::new(Vec::new());

    match format {
        Format::Webp => {
            use image::codecs::webp::WebPEncoder;
            let encoder = WebPEncoder::new_lossless(&mut out_buffer);
            img.write_with_encoder(encoder)?;
            Ok(out_buffer.into_inner())
        }
        Format::Jpeg => {
            use image::codecs::jpeg::JpegEncoder;
            let encoder = JpegEncoder::new_with_quality(&mut out_buffer, quality);
            img.write_with_encoder(encoder)?;
            Ok(out_buffer.into_inner())
        }
        Format::Png => {
            img.write_to(&mut out_buffer, image::ImageFormat::Png)?;
            Ok(out_buffer.into_inner())
        }
        Format::Avif => {
            use image::codecs::avif::AvifEncoder;
            // Speed 8 provides a highly optimized balance of speed and size for WebAssembly contexts.
            let encoder = AvifEncoder::new_with_speed_quality(&mut out_buffer, 8, quality);
            img.write_with_encoder(encoder)?;
            Ok(out_buffer.into_inner())
        }
        Format::Jxl => bail!(
            "JXL encoding is not supported via portable pure Rust engine. Use server worker CLI (cjxl) or WebAssembly @jsquash/jxl",
        ),
        _ => bail!(
            "Format {:?} currently not supported via pure rust portable engine",
            format
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::{MatrixCoefficients, TransferCharacteristics};
    use image::{GenericImageView, Rgba};

    #[test]
    fn test_srgb_linear_roundtrip() {
        for i in 0..=255 {
            let lin = color::srgb_u8_to_linear(i);
            let back = color::linear_to_srgb_u8(lin);
            assert_eq!(i, back, "Roundtrip failed for u8 value {}", i);
        }
    }

    #[test]
    fn test_pq_nits_roundtrip() {
        let nits_cases = [0.0, 100.0, 203.0, 1000.0, 4000.0, 10000.0];
        for &expected_nits in &nits_cases {
            let pq = color::linear_nits_to_pq(expected_nits);
            let nits = color::pq_to_linear_nits(pq);
            assert!(
                (nits - expected_nits).abs() < 1.0,
                "PQ roundtrip failed for {} nits, got {}",
                expected_nits,
                nits
            );
        }
    }

    #[test]
    fn test_bt709_linear_roundtrip() {
        for i in 0..=100 {
            let v = i as f32 / 100.0;
            let lin = color::bt709_to_linear_f32(v);
            let back = color::linear_to_bt709_f32(lin);
            assert!((v - back).abs() < 1e-4, "BT.709 roundtrip failed for {}", v);
        }
    }

    #[test]
    fn test_hlg_linear_roundtrip() {
        for i in 0..=100 {
            let v = i as f32 / 100.0;
            let lin = color::hlg_to_linear_f32(v);
            let back = color::linear_to_hlg_f32(lin);
            assert!((v - back).abs() < 1e-4, "HLG roundtrip failed for {}", v);
        }
    }

    #[test]
    fn test_y4m_420_chroma_is_upsampled_bilinearly() {
        // Left half pure red, right half pure blue. 4:2:0 collapses chroma to a
        // 2x2 plane, so the boundary column can only come from interpolating
        // between the two chroma samples.
        let mut img = image::ImageBuffer::new(4, 4);
        for y in 0..4 {
            for x in 0..4 {
                img.put_pixel(
                    x,
                    y,
                    if x < 2 {
                        Rgba([255, 0, 0, 255])
                    } else {
                        Rgba([0, 0, 255, 255])
                    },
                );
            }
        }
        let img = DynamicImage::ImageRgba8(img);

        let (y, u, v) = rgb_to_yuv420_planar(&img, MatrixCoefficients::Bt709);
        let y4m = write_y4m_c420(4, 4, &y, &u, &v);
        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        let rgba = decoded.to_rgba8();

        // Nearest-neighbour replication would leave exactly two values on this
        // row (pure red at x=0/1, pure blue at x=2/3). Interpolation must
        // produce at least one intermediate blue level.
        let blue: Vec<u8> = (0..4).map(|x| rgba.get_pixel(x, 0)[2]).collect();
        assert!(
            blue.iter().any(|&b| (10..=245).contains(&b)),
            "expected an interpolated chroma transition, got B = {:?}",
            blue
        );
        assert!(
            blue.windows(2).all(|pair| pair[1] >= pair[0]),
            "chroma must ramp monotonically from red to blue, got B = {:?}",
            blue
        );
    }

    #[test]
    fn test_y4m_identity_matrix_reconstructs_gbr() {
        // Identity (GBR) planes are not YUV: plane order is G, B, R. A decoder
        // that blindly applies a BT.709 matrix turns the image green.
        let mut y4m = b"YUV4MPEG2 W1 H1 F25:1 Ip A1:1 C444 XCOLORRANGE=FULL\nFRAME\n".to_vec();
        y4m.extend_from_slice(&[0, 0, 255]); // G=0, B=0, R=255

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Identity).unwrap();
        let px = decoded.to_rgba8().get_pixel(0, 0).0;
        assert_eq!(px[0], 255, "R must come from the third plane");
        assert_eq!(px[1], 0);
        assert_eq!(px[2], 0);
    }

    #[test]
    fn test_write_pam_rgba() {
        let img =
            DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(4, 4, Rgba([255, 0, 0, 255])));
        let pam = write_pam_rgba(&img);
        assert!(pam.starts_with(
            b"P7\nWIDTH 4\nHEIGHT 4\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n"
        ));
        assert_eq!(pam.len(), 65 + 4 * 4 * 4);
    }

    #[test]
    fn test_write_y4m_c444() {
        let img =
            DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(4, 4, Rgba([255, 0, 0, 255])));
        let (y, u, v) = rgb_to_yuv444_planar(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 16);
        assert_eq!(v.len(), 16);

        let y4m = write_y4m_c444(4, 4, &y, &u, &v);
        assert!(y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C444 XCOLORRANGE=FULL\nFRAME\n"));
        assert_eq!(y4m.len(), 58 + 16 * 3);
    }

    #[test]
    fn test_write_y4m_c444_p10() {
        let img = DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([65535, 0, 0, 65535]),
        ));
        let (y, u, v) = rgb_to_yuv444_planar_10bit(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 16);
        assert_eq!(v.len(), 16);

        // Max value must be strictly <= 1023
        for &val in &y {
            assert!(val <= 1023, "Y sample {} exceeded 1023", val);
        }
        for &val in &u {
            assert!(val <= 1023, "U sample {} exceeded 1023", val);
        }
        for &val in &v {
            assert!(val <= 1023, "V sample {} exceeded 1023", val);
        }

        let y4m = write_y4m_c444_p10(4, 4, &y, &u, &v);
        assert!(
            y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C444p10 XCOLORRANGE=FULL\nFRAME\n")
        );
        assert_eq!(y4m.len(), 61 + 16 * 3 * 2);
    }

    #[test]
    fn test_linear_light_resize_with_transfer() {
        let mut img = DynamicImage::ImageRgba8(image::ImageBuffer::from_fn(16, 16, |x, _y| {
            if x < 8 {
                Rgba([0, 0, 0, 255])
            } else {
                Rgba([255, 255, 255, 255])
            }
        }));

        let ops = vec![ImageEditOp::Resize {
            width: 8,
            height: 8,
            filter: "lanczos3".to_string(),
        }];

        let res = apply_pipeline_with_transfer(&mut img, &ops, TransferCharacteristics::Bt709);
        assert!(res.is_ok());
        assert_eq!(img.dimensions(), (8, 8));
    }

    #[test]
    fn test_y4m_c420_roundtrip() {
        let img = DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([200, 100, 50, 255]),
        ));
        let (y, u, v) = rgb_to_yuv420_planar(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 4);
        assert_eq!(v.len(), 4);

        let y4m = write_y4m_c420(4, 4, &y, &u, &v);
        assert!(
            y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C420jpeg XCOLORRANGE=FULL\nFRAME\n")
        );

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        assert_eq!(decoded.dimensions(), (4, 4));
        let p = decoded.to_rgba8().get_pixel(0, 0).0;
        // Tolerances for full-range roundtrip conversion
        assert!((p[0] as i16 - 200).abs() <= 2);
        assert!((p[1] as i16 - 100).abs() <= 2);
        assert!((p[2] as i16 - 50).abs() <= 2);
    }

    #[test]
    fn test_y4m_c420_p10_roundtrip() {
        let img = DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([50000, 25000, 12000, 65535]),
        ));
        let (y, u, v) = rgb_to_yuv420_planar_10bit(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 4);
        assert_eq!(v.len(), 4);

        for &val in &y {
            assert!(val <= 1023);
        }
        for &val in &u {
            assert!(val <= 1023);
        }
        for &val in &v {
            assert!(val <= 1023);
        }

        let y4m = write_y4m_c420_p10(4, 4, &y, &u, &v);
        assert!(
            y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C420p10 XCOLORRANGE=FULL\nFRAME\n")
        );

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        assert_eq!(decoded.dimensions(), (4, 4));
        if let DynamicImage::ImageRgba16(rgba16) = decoded {
            let p = rgba16.get_pixel(0, 0).0;
            // 10-bit precision corresponds to step ~ 64 in 16-bit space
            assert!((p[0] as i32 - 50000).abs() <= 200);
            assert!((p[1] as i32 - 25000).abs() <= 200);
            assert!((p[2] as i32 - 12000).abs() <= 200);
        } else {
            panic!("Expected ImageRgba16 from 10-bit Y4M");
        }
    }

    #[test]
    fn test_y4m_c422_roundtrip() {
        let img = DynamicImage::ImageRgba8(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([180, 120, 60, 255]),
        ));
        let (y, u, v) = rgb_to_yuv422_planar(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 8); // (4+1)/2 * 4 = 8
        assert_eq!(v.len(), 8);

        let y4m = write_y4m_c422(4, 4, &y, &u, &v);
        assert!(y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C422 XCOLORRANGE=FULL\nFRAME\n"));

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        assert_eq!(decoded.dimensions(), (4, 4));
        let p = decoded.to_rgba8().get_pixel(0, 0).0;
        assert!((p[0] as i16 - 180).abs() <= 2);
        assert!((p[1] as i16 - 120).abs() <= 2);
        assert!((p[2] as i16 - 60).abs() <= 2);
    }

    #[test]
    fn test_y4m_c422_p10_roundtrip() {
        let img = DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([45000, 30000, 15000, 65535]),
        ));
        let (y, u, v) = rgb_to_yuv422_planar_10bit(&img, MatrixCoefficients::Bt709);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 8);
        assert_eq!(v.len(), 8);

        for &val in &y {
            assert!(val <= 1023);
        }
        for &val in &u {
            assert!(val <= 1023);
        }
        for &val in &v {
            assert!(val <= 1023);
        }

        let y4m = write_y4m_c422_p10(4, 4, &y, &u, &v);
        assert!(
            y4m.starts_with(b"YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C422p10 XCOLORRANGE=FULL\nFRAME\n")
        );

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        assert_eq!(decoded.dimensions(), (4, 4));
        if let DynamicImage::ImageRgba16(rgba16) = decoded {
            let p = rgba16.get_pixel(0, 0).0;
            assert!((p[0] as i32 - 45000).abs() <= 200);
            assert!((p[1] as i32 - 30000).abs() <= 200);
            assert!((p[2] as i32 - 15000).abs() <= 200);
        } else {
            panic!("Expected ImageRgba16 from 10-bit Y4M 422");
        }
    }

    #[test]
    fn test_y4m_omitted_chroma_defaults_to_c420() {
        // Standard Y4M with omitted C tag (defaults to C420)
        let w = 4;
        let h = 4;
        let y_plane = vec![128u8; w * h];
        let u_plane = vec![128u8; (w / 2) * (h / 2)];
        let v_plane = vec![128u8; (w / 2) * (h / 2)];
        let mut y4m = format!("YUV4MPEG2 W{} H{} F25:1\nFRAME\n", w, h).into_bytes();
        y4m.extend_from_slice(&y_plane);
        y4m.extend_from_slice(&u_plane);
        y4m.extend_from_slice(&v_plane);

        let decoded = read_y4m_to_image(&y4m, MatrixCoefficients::Bt709).unwrap();
        assert_eq!(decoded.dimensions(), (4, 4));
    }

    #[test]
    fn test_tone_mapping_image_hlg_to_srgb() {
        use compute_types::ToneMappingOperator;

        let mut img = DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
            2,
            2,
            Rgba([50000, 30000, 20000, 65535]),
        ));

        apply_tone_mapping_to_image(
            &mut img,
            ToneMappingOperator::AcesFilmic,
            TransferCharacteristics::Hlg,
            TransferCharacteristics::Srgb,
        );

        if let DynamicImage::ImageRgba16(buf) = img {
            let p = buf.get_pixel(0, 0).0;
            assert!(p[0] > 0);
            assert!(p[1] > 0);
            assert!(p[2] > 0);
            assert_eq!(p[3], 65535);
        } else {
            panic!("Expected 16-bit image preserved");
        }
    }

    #[test]
    fn test_quantize_16bit_to_8bit_dither() {
        let img = DynamicImage::ImageRgba16(image::ImageBuffer::from_pixel(
            4,
            4,
            Rgba([32768, 16384, 8192, 65535]),
        ));

        for dither in [false, true] {
            let quantized = quantize_16bit_to_8bit(&img, dither);
            assert_eq!(quantized.dimensions(), (4, 4));
            if let DynamicImage::ImageRgba8(buf) = quantized {
                let p = buf.get_pixel(0, 0).0;
                assert!((p[0] as i16 - 128).abs() <= 2);
                assert!((p[1] as i16 - 64).abs() <= 2);
                assert!((p[2] as i16 - 32).abs() <= 2);
                assert_eq!(p[3], 255);
            } else {
                panic!("Expected ImageRgba8 output");
            }
        }
    }

    #[test]
    fn test_extract_icc_jpeg_and_png() {
        let icc = b"FAKE_ICC_PROFILE_DATA_FOR_TESTING";
        let img = DynamicImage::ImageRgb8(image::ImageBuffer::from_pixel(
            4,
            4,
            image::Rgb([10, 20, 30]),
        ));

        let mut jpeg = Cursor::new(Vec::new());
        img.write_to(&mut jpeg, image::ImageFormat::Jpeg).unwrap();
        let tagged_jpeg = inject_icc_into_jpeg(&jpeg.into_inner(), icc);
        assert_eq!(
            extract_icc_from_memory(&tagged_jpeg).as_deref(),
            Some(&icc[..]),
            "JPEG ICC roundtrip"
        );

        let mut png = Cursor::new(Vec::new());
        img.write_to(&mut png, image::ImageFormat::Png).unwrap();
        let tagged_png = inject_icc_into_png(&png.into_inner(), icc);
        assert_eq!(
            extract_icc_from_memory(&tagged_png).as_deref(),
            Some(&icc[..]),
            "PNG ICC roundtrip"
        );
    }

    #[test]
    fn test_extract_icc_webp() {
        let fake_icc_payload = b"WEBP_ICC_TEST_BYTES";
        let mut fake_webp = b"RIFF".to_vec();
        let total_size = (4 + 8 + fake_icc_payload.len() + (fake_icc_payload.len() % 2)) as u32;
        fake_webp.extend_from_slice(&total_size.to_le_bytes());
        fake_webp.extend_from_slice(b"WEBP");
        fake_webp.extend_from_slice(b"ICCP");
        fake_webp.extend_from_slice(&(fake_icc_payload.len() as u32).to_le_bytes());
        fake_webp.extend_from_slice(fake_icc_payload);
        if fake_icc_payload.len() % 2 != 0 {
            fake_webp.push(0);
        }

        let extracted = extract_icc_from_memory(&fake_webp).expect("Expected WebP ICC extraction");
        assert_eq!(extracted, fake_icc_payload);
    }

    fn build_matrix_icc(colorants: [[f32; 3]; 3]) -> Vec<u8> {
        let mut data = vec![0u8; 128];
        data.extend_from_slice(&3u32.to_be_bytes());
        let tags: [&[u8; 4]; 3] = [b"rXYZ", b"gXYZ", b"bXYZ"];
        let data_start = 128 + 4 + 3 * 12;
        for (i, sig) in tags.iter().enumerate() {
            data.extend_from_slice(*sig);
            data.extend_from_slice(&((data_start + i * 20) as u32).to_be_bytes());
            data.extend_from_slice(&20u32.to_be_bytes());
        }
        for colorant in colorants {
            data.extend_from_slice(b"XYZ ");
            data.extend_from_slice(&[0, 0, 0, 0]);
            for value in colorant {
                data.extend_from_slice(&((value * 65536.0).round() as i32).to_be_bytes());
            }
        }
        let size = data.len() as u32;
        data[0..4].copy_from_slice(&size.to_be_bytes());
        data
    }

    #[test]
    fn test_classify_icc_color_space() {
        let srgb = build_matrix_icc([
            [0.436066, 0.222488, 0.013916],
            [0.385147, 0.716873, 0.097076],
            [0.143066, 0.060608, 0.714096],
        ]);
        assert_eq!(classify_icc_color_space(&srgb), Some(IccColorSpace::Srgb));

        let p3 = build_matrix_icc([
            [0.515121, 0.241196, -0.001053],
            [0.291977, 0.692245, 0.041885],
            [0.157104, 0.066574, 0.784073],
        ]);
        assert_eq!(
            classify_icc_color_space(&p3),
            Some(IccColorSpace::DisplayP3)
        );
        assert_eq!(IccColorSpace::DisplayP3.cjxl_shorthand(), "DisplayP3");

        let unknown = build_matrix_icc([[0.9, 0.1, 0.0], [0.1, 0.9, 0.0], [0.0, 0.0, 0.9]]);
        assert_eq!(classify_icc_color_space(&unknown), None);

        // Real-world Photoshop-authored Display P3 profile from the WebKit suite.
        let fixture = std::path::Path::new("../../../scripts/fixtures/external/webkit_logo_p3.png");
        if fixture.exists() {
            let bytes = std::fs::read(fixture).unwrap();
            let icc = extract_icc_from_memory(&bytes).expect("fixture should carry an ICC");
            assert_eq!(
                classify_icc_color_space(&icc),
                Some(IccColorSpace::DisplayP3)
            );
        }
    }

    #[test]
    fn test_detect_optimal_chroma_subsampling() {
        use compute_types::ChromaSubsampling;

        // 1. Synthetic UI / Text image (sharp high-contrast red / blue checkerboard)
        let mut ui_img = image::ImageBuffer::new(32, 32);
        for y in 0..32 {
            for x in 0..32 {
                if (x + y) % 2 == 0 {
                    ui_img.put_pixel(x, y, Rgba([255, 0, 0, 255]));
                } else {
                    ui_img.put_pixel(x, y, Rgba([0, 0, 255, 255]));
                }
            }
        }
        let ui_dyn = DynamicImage::ImageRgba8(ui_img);
        let decision_ui = detect_optimal_chroma_subsampling(&ui_dyn);
        assert_eq!(decision_ui, ChromaSubsampling::Yuv444);

        // 2. Smooth natural gradient image
        let mut grad_img = image::ImageBuffer::new(64, 64);
        for y in 0..64 {
            for x in 0..64 {
                let v = ((x + y) * 255 / 128) as u8;
                grad_img.put_pixel(x, y, Rgba([v, v, v, 255]));
            }
        }
        let grad_dyn = DynamicImage::ImageRgba8(grad_img);
        let decision_grad = detect_optimal_chroma_subsampling(&grad_dyn);
        assert_eq!(decision_grad, ChromaSubsampling::Yuv420);
    }

    #[test]
    fn test_hlg_ootf_gamma() {
        // At 1000 nits, log2(1000/1000) = 0, gamma = 1.2
        let (r1, g1, b1) = hlg_ootf(0.5, 0.5, 0.5, 1000.0);
        assert!(r1 < 0.5); // (0.5)^1.2 < 0.5
        assert_eq!(r1, g1);
        assert_eq!(g1, b1);

        // At low nits (e.g. 203 nits SDR reference), gamma < 1.0, so values scale up
        let (r2, _, _) = hlg_ootf(0.5, 0.5, 0.5, 203.0);
        assert!(r2 > r1);
    }
}
