use super::*;
use rs_vips::voption::{Setter, VOption};

fn fixture(path: &str) -> Vec<u8> {
    init();
    let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../../../scripts/fixtures/");
    std::fs::read(format!("{root}{path}")).unwrap_or_else(|e| panic!("{path}: {e}"))
}

fn opts(json: &str) -> ImageOptions {
    serde_json::from_str(json).unwrap()
}

fn run(input: &[u8], json: &str) -> Output {
    process(input, &opts(json), &Settings::default()).unwrap_or_else(|e| panic!("{json}: {e}"))
}

fn reload(output: &Output) -> VipsImage {
    VipsImage::new_from_buffer(&output.bytes, "").unwrap()
}

fn has_field(image: &VipsImage, name: &str) -> bool {
    image.get_typeof(name).unwrap_or(0) != 0
}

/// A JPEG whose EXIF says "rotate 90° clockwise" (orientation 6).
fn sideways_jpeg(width: i32, height: i32) -> Vec<u8> {
    init();
    let mut image =
        VipsImage::black_with_opts(width, height, VOption::new().set("bands", 3)).unwrap();
    image.set_int("orientation", 6).unwrap();
    image
        .jpegsave_buffer_with_opts(
            VOption::new().set("keep", rs_vips::enums::ForeignKeep::Exif as i32),
        )
        .unwrap()
}

/// A 16-bit RGB PNG (1024 × 684).
fn deep_png() -> Vec<u8> {
    fixture("external/aom_weld_16bit.png")
}

#[test]
fn inside_resize_keeps_aspect_and_never_upscales_by_default() {
    let photo = fixture("hd_benchmark/landscape_mountains_4k.jpg");
    let out = run(
        &photo,
        r#"{"format":"avif","resize":{"width":800,"height":800}}"#,
    );
    assert_eq!(out.mime, "image/avif");
    assert_eq!(out.width.max(out.height), 800);
    assert!(out.width.min(out.height) < 800);
    assert_eq!(
        reload(&out).get_string("vips-loader").unwrap(),
        "heifload_buffer"
    );

    let small = fixture("quality/checker_rb_444.png"); // 512 × 512
    assert_eq!(
        run(&small, r#"{"format":"png","resize":{"width":1024}}"#).width,
        512
    );
    assert_eq!(
        run(
            &small,
            r#"{"format":"png","resize":{"width":1024,"enlarge":true}}"#
        )
        .width,
        1024
    );
}

#[test]
fn cover_crops_and_fill_stretches_to_the_box() {
    let photo = fixture("hd_benchmark/landscape_mountains_4k.jpg");
    let cover = run(
        &photo,
        r#"{"format":"webp","resize":{"width":300,"height":300,"fit":"cover"}}"#,
    );
    assert_eq!((cover.width, cover.height), (300, 300));
    let fill = run(
        &photo,
        r#"{"format":"jpeg","resize":{"width":120,"height":300,"fit":"fill"}}"#,
    );
    assert_eq!((fill.width, fill.height), (120, 300));
}

#[test]
fn exif_orientation_is_baked_into_pixels() {
    let out = run(&sideways_jpeg(40, 20), r#"{"format":"png"}"#);
    assert_eq!((out.width, out.height), (20, 40));
}

#[test]
fn metadata_strip_removes_exif_and_keep_retains_it() {
    let input = sideways_jpeg(40, 20);
    assert!(!has_field(
        &reload(&run(&input, r#"{"format":"jpeg"}"#)),
        "exif-data"
    ));
    let kept = reload(&run(&input, r#"{"format":"jpeg","metadata":"keep"}"#));
    assert!(has_field(&kept, "exif-data"));
    // The orientation was applied, so it must not be applied twice.
    assert!(kept.get_int("orientation").is_err() || kept.get_int("orientation").unwrap() == 1);
}

#[test]
fn color_keep_carries_the_source_profile_and_srgb_converts() {
    let p3 = fixture("external/webkit_iceland_p3.jpg");
    let source_icc = VipsImage::new_from_buffer(&p3, "")
        .unwrap()
        .get_blob("icc-profile-data")
        .unwrap();
    let kept = reload(&run(&p3, r#"{"format":"webp"}"#));
    assert_eq!(kept.get_blob("icc-profile-data").unwrap(), source_icc);

    let converted = reload(&run(&p3, r#"{"format":"webp","color":"srgb"}"#));
    let icc = converted.get_blob("icc-profile-data").unwrap();
    assert_ne!(icc, source_icc);
    // The description is stored as UTF-16 (`mluc`).
    let utf16: Vec<u8> = "sRGB".bytes().flat_map(|b| [0, b]).collect();
    assert!(
        icc.windows(utf16.len()).any(|w| w == utf16),
        "not an sRGB profile"
    );
}

#[test]
fn avif_depth_follows_the_source_unless_requested() {
    let deep = deep_png();
    let follow = reload(&run(&deep, r#"{"format":"avif"}"#));
    assert_eq!(follow.get_int("bits-per-sample").unwrap(), 10);
    let shallow = reload(&run(&deep, r#"{"format":"avif","bit_depth":8}"#));
    assert_eq!(shallow.get_int("bits-per-sample").unwrap(), 8);
    let png = reload(&run(&deep, r#"{"format":"png"}"#));
    assert_eq!(
        png.get_format().unwrap(),
        rs_vips::enums::BandFormat::Ushort
    );
}

fn mean(image: &VipsImage) -> f64 {
    image.avg().unwrap()
}

#[test]
fn twelve_bit_avif_and_heic_keep_their_brightness() {
    let deep = deep_png();
    let source = mean(&VipsImage::new_from_buffer(&deep, "").unwrap());
    for format in ["avif", "heic"] {
        let out = reload(&run(
            &deep,
            &format!(r#"{{"format":"{format}","bit_depth":12}}"#),
        ));
        assert_eq!(out.get_int("bits-per-sample").unwrap(), 12, "{format}");
        // Loaded back on the 16-bit scale.
        let ratio = mean(&out) / source;
        assert!((0.97..1.03).contains(&ratio), "{format}: {ratio}");
    }
    let libvips = Settings {
        avif_encoder: AvifEncoder::Libvips,
        ..Settings::default()
    };
    let out = process(
        &deep,
        &opts(r#"{"format":"avif","bit_depth":12}"#),
        &libvips,
    )
    .unwrap();
    assert_eq!(reload(&out).get_int("bits-per-sample").unwrap(), 12);
}

#[test]
fn jxl_depth_follows_the_source_unless_requested() {
    use rs_vips::enums::BandFormat;
    let deep = deep_png();
    let source = mean(&VipsImage::new_from_buffer(&deep, "").unwrap());
    let follow = reload(&run(&deep, r#"{"format":"jxl"}"#));
    assert_eq!(follow.get_format().unwrap(), BandFormat::Ushort);
    let shallow = reload(&run(&deep, r#"{"format":"jxl","bit_depth":8}"#));
    assert_eq!(shallow.get_format().unwrap(), BandFormat::Uchar);
    assert_eq!(shallow.get_int("bits-per-sample").unwrap(), 8);
    let ratio = mean(&shallow) * 257.0 / source;
    assert!((0.97..1.03).contains(&ratio), "16 to 8: {ratio}");

    let photo = fixture("quality/jpeg_420_90.jpg");
    let source = mean(&VipsImage::new_from_buffer(&photo, "").unwrap());
    let widened = reload(&run(&photo, r#"{"format":"jxl","bit_depth":16}"#));
    assert_eq!(widened.get_format().unwrap(), BandFormat::Ushort);
    assert_eq!(widened.get_int("bits-per-sample").unwrap(), 16);
    let ratio = mean(&widened) / 257.0 / source;
    assert!((0.97..1.03).contains(&ratio), "8 to 16: {ratio}");

    // Lossless JPEG to JXL is a bit-exact recompression only at 8 bits.
    let recompressed = run(&photo, r#"{"format":"jxl","lossless":true,"bit_depth":8}"#);
    assert_eq!(
        reload(&recompressed).get_format().unwrap(),
        BandFormat::Uchar
    );
    let widened = run(&photo, r#"{"format":"jxl","lossless":true,"bit_depth":16}"#);
    assert_eq!(reload(&widened).get_format().unwrap(), BandFormat::Ushort);
}

#[test]
fn float_jxl_stays_float_unless_a_depth_is_requested() {
    use rs_vips::enums::{BandFormat, Interpretation};
    init();
    // Linear 0.5, which is 0.7354 once sRGB-encoded.
    let float = VipsImage::black_with_opts(64, 64, VOption::new().set("bands", 3))
        .unwrap()
        .linear(&[0.0], &[0.5])
        .unwrap()
        .copy_with_opts(VOption::new().set("interpretation", Interpretation::Scrgb as i32))
        .unwrap()
        .jxlsave_buffer_with_opts(VOption::new().set("lossless", true))
        .unwrap();
    let source = VipsImage::new_from_buffer(&float, "").unwrap();
    assert_eq!(source.get_format().unwrap(), BandFormat::Float);

    let follow = reload(&run(&float, r#"{"format":"jxl"}"#));
    assert_eq!(follow.get_format().unwrap(), BandFormat::Float);
    assert!((mean(&follow) - 0.5).abs() < 0.01, "{}", mean(&follow));

    let sixteen = reload(&run(&float, r#"{"format":"jxl","bit_depth":16}"#));
    assert_eq!(sixteen.get_format().unwrap(), BandFormat::Ushort);
    let level = mean(&sixteen) / 65535.0;
    assert!((level - 0.7354).abs() < 0.01, "16-bit: {level}");

    let eight = reload(&run(&float, r#"{"format":"jxl","bit_depth":8}"#));
    assert_eq!(eight.get_format().unwrap(), BandFormat::Uchar);
    let level = mean(&eight) / 255.0;
    assert!((level - 0.7354).abs() < 0.01, "8-bit: {level}");
}

#[test]
fn deep_sources_get_the_same_geometry_and_stay_deep() {
    let deep = deep_png(); // 1024 × 684
    let ushort =
        |out: &Output| reload(out).get_format().unwrap() == rs_vips::enums::BandFormat::Ushort;
    let inside = run(&deep, r#"{"format":"png","resize":{"width":512}}"#);
    assert_eq!((inside.width, inside.height), (512, 342));
    assert!(ushort(&inside));
    let cover = run(
        &deep,
        r#"{"format":"png","resize":{"width":300,"height":300,"fit":"cover"}}"#,
    );
    assert_eq!((cover.width, cover.height), (300, 300));
    let fill = run(
        &deep,
        r#"{"format":"png","resize":{"width":200,"height":100,"fit":"fill"}}"#,
    );
    assert_eq!((fill.width, fill.height), (200, 100));
    let small = run(&deep, r#"{"format":"png","resize":{"width":4000}}"#);
    assert_eq!(small.width, 1024);
    let srgb = run(&deep, r#"{"format":"png","color":"srgb"}"#);
    assert!(ushort(&srgb), "sRGB conversion dropped depth");
}

#[test]
fn alpha_survives_and_jpeg_flattens_onto_white() {
    let alpha = fixture("quality/avif_8bit_420_alpha.avif");
    for format in ["webp", "png", "avif", "jxl"] {
        let out = reload(&run(
            &alpha,
            &format!(r#"{{"format":"{format}","lossless":true}}"#),
        ));
        assert_eq!(out.get_bands(), 4, "{format}");
    }
    assert_eq!(reload(&run(&alpha, r#"{"format":"jpeg"}"#)).get_bands(), 3);
}

#[test]
fn jpeg_chroma_and_quality_are_applied() {
    let photo = fixture("quality/jpeg_420_90.jpg");
    let full = reload(&run(&photo, r#"{"format":"jpeg","chroma":"444"}"#));
    assert_eq!(full.get_string("jpeg-chroma-subsample").unwrap(), "4:4:4");
    let sub = reload(&run(&photo, r#"{"format":"jpeg","chroma":"420"}"#));
    assert_eq!(sub.get_string("jpeg-chroma-subsample").unwrap(), "4:2:0");

    let low = run(&photo, r#"{"format":"webp","quality":30}"#).bytes.len();
    let high = run(&photo, r#"{"format":"webp","quality":95}"#).bytes.len();
    assert!(low < high, "quality ignored: {low} vs {high}");
}

#[test]
fn jxl_distance_is_applied() {
    let photo = fixture("quality/gradient_sunset_8bit.png");
    let near = run(&photo, r#"{"format":"jxl","distance":0.5,"effort":3}"#).bytes;
    let far = run(&photo, r#"{"format":"jxl","distance":4,"effort":3}"#).bytes;
    assert!(far.len() < near.len(), "distance ignored: {} vs {}", far.len(), near.len());
    // libvips maps Q80 to distance 1.9, so both requests encode identically.
    let by_distance = run(&photo, r#"{"format":"jxl","distance":1.9,"effort":3}"#).bytes;
    let by_quality = run(&photo, r#"{"format":"jxl","quality":80,"effort":3}"#).bytes;
    assert_eq!(by_distance, by_quality);
}

#[test]
fn every_format_encodes() {
    let photo = fixture("quality/gradient_sunset_8bit.png");
    for (format, loader) in [
        ("avif", "heifload_buffer"),
        ("heic", "heifload_buffer"),
        ("jxl", "jxlload_buffer"),
        ("webp", "webpload_buffer"),
        ("jpeg", "jpegload_buffer"),
        ("png", "pngload_buffer"),
    ] {
        let out = run(&photo, &format!(r#"{{"format":"{format}"}}"#));
        assert_eq!(
            reload(&out).get_string("vips-loader").unwrap(),
            loader,
            "{format}"
        );
    }
    let heic = reload(&run(&photo, r#"{"format":"heic"}"#));
    assert_eq!(heic.get_string("heif-compression").unwrap(), "hevc");
}

#[test]
fn avif_carries_profile_and_metadata_and_lossless_is_exact() {
    let p3 = fixture("external/webkit_iceland_p3.jpg");
    let source_icc = VipsImage::new_from_buffer(&p3, "")
        .unwrap()
        .get_blob("icc-profile-data")
        .unwrap();
    let kept = reload(&run(&p3, r#"{"format":"avif"}"#));
    assert_eq!(kept.get_blob("icc-profile-data").unwrap(), source_icc);

    let sideways = sideways_jpeg(40, 20);
    assert!(!has_field(
        &reload(&run(&sideways, r#"{"format":"avif"}"#)),
        "exif-data"
    ));
    let out = run(&sideways, r#"{"format":"avif","metadata":"keep"}"#);
    assert_eq!((out.width, out.height), (20, 40));
    let kept = reload(&out);
    assert!(has_field(&kept, "exif-data"));
    // The orientation was applied, so it must not be applied twice.
    assert!(kept.get_int("orientation").is_err() || kept.get_int("orientation").unwrap() == 1);
    assert_eq!((kept.get_width(), kept.get_height()), (20, 40));

    let checker = fixture("quality/checker_rb_444.png");
    let lossless = reload(&run(&checker, r#"{"format":"avif","lossless":true}"#));
    let source = VipsImage::new_from_buffer(&checker, "").unwrap();
    let diff = source
        .subtract(&lossless)
        .unwrap()
        .abs()
        .unwrap()
        .max()
        .unwrap();
    assert_eq!(diff, 0.0);
}

#[test]
fn avif_encoder_setting_picks_the_library() {
    let input = fixture("quality/checker_rb_444.png");
    let encode = |avif_encoder| {
        let settings = Settings {
            avif_encoder,
            ..Settings::default()
        };
        process(&input, &opts(r#"{"format":"avif"}"#), &settings).unwrap()
    };
    // libavif names its primary item "Color" in the `infe` box; libheif
    // leaves it unnamed.
    let names_libavif = |out: &Output| out.bytes.windows(10).any(|w| w == b"av01Color\0");
    let libavif = encode(AvifEncoder::Libavif);
    let libvips = encode(AvifEncoder::Libvips);
    assert!(names_libavif(&libavif));
    assert!(!names_libavif(&libvips));
    for out in [&libavif, &libvips] {
        assert_eq!(out.mime, "image/avif");
        assert_eq!(reload(out).get_string("heif-compression").unwrap(), "av1");
    }
    assert_eq!("libvips".parse(), Ok(AvifEncoder::Libvips));
    assert!("aom".parse::<AvifEncoder>().is_err());
}

#[test]
fn untrusted_loaders_are_blocked() {
    let svg = br#"<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"/>"#;
    assert!(matches!(
        process(svg, &opts(r#"{"format":"png"}"#), &Settings::default()),
        Err(Error::Unsupported(_))
    ));
}

#[test]
fn hdr_transfer_sources_are_rejected_instead_of_mislabeled() {
    for path in [
        "quality/hdr_pq_10bit.avif",
        "quality/hdr_hlg_10bit.avif",
        "external/aom_seine_hdr_rec2020.avif",
    ] {
        let err = process(
            &fixture(path),
            &opts(r#"{"format":"avif"}"#),
            &Settings::default(),
        )
        .unwrap_err();
        assert!(matches!(err, Error::Unsupported(_)), "{path}: {err}");
    }
}

#[test]
fn ultrahdr_gain_map_is_kept_for_jpeg_and_dropped_for_sdr() {
    let ultra = fixture("external/aom_apple_gainmap_new.jpg");
    let kept = reload(&run(&ultra, r#"{"format":"jpeg","resize":{"width":200}}"#));
    assert!(has_field(&kept, "gainmap-data"));
    let sdr = reload(&run(&ultra, r#"{"format":"jpeg","dynamic_range":"sdr"}"#));
    assert!(!has_field(&sdr, "gainmap-data"));
    // Other formats get the SDR base image.
    assert_eq!(run(&ultra, r#"{"format":"webp"}"#).mime, "image/webp");
}

#[test]
fn oversized_and_unreadable_inputs_are_unsupported() {
    let photo = fixture("quality/checker_rb_444.png");
    let tiny = Settings {
        max_pixels: 1000,
        ..Settings::default()
    };
    assert!(matches!(
        process(&photo, &opts(r#"{"format":"png"}"#), &tiny),
        Err(Error::Unsupported(_))
    ));
    assert!(matches!(
        process(
            b"not an image",
            &opts(r#"{"format":"png"}"#),
            &Settings::default()
        ),
        Err(Error::Unsupported(_))
    ));
    assert!(matches!(
        process(
            &photo,
            &opts(r#"{"format":"png","quality":50}"#),
            &Settings::default()
        ),
        Err(Error::Invalid(_))
    ));
}

/// Restores a JPEG from a JXL's reconstruction data with `djxl`.
fn djxl_jpeg(jxl_bytes: &[u8]) -> Vec<u8> {
    let dir = tempfile::tempdir().unwrap();
    let (jxl, back) = (dir.path().join("a.jxl"), dir.path().join("back.jpg"));
    std::fs::write(&jxl, jxl_bytes).unwrap();
    // djxl is a development dependency: a missing tool fails here.
    let djxl = std::process::Command::new("djxl")
        .arg(&jxl)
        .arg(&back)
        .output()
        .expect("djxl (libjxl tools) is required");
    assert!(
        djxl.status.success(),
        "djxl: {}",
        String::from_utf8_lossy(&djxl.stderr)
    );
    std::fs::read(&back).unwrap()
}

/// Whether a JXL container carries JPEG reconstruction data (`jbrd` box).
fn has_jbrd(jxl: &[u8]) -> bool {
    jxl.windows(4).any(|w| w == b"jbrd")
}

#[test]
fn jpeg_to_jxl_lossless_is_bit_exact() {
    let jpeg = fixture("quality/jpeg_420_90.jpg");
    let out = run(
        &jpeg,
        r#"{"format":"jxl","lossless":true,"metadata":"keep"}"#,
    );
    assert_eq!(out.mime, "image/jxl");
    assert_eq!(
        djxl_jpeg(&out.bytes),
        jpeg,
        "JPEG not reconstructed bit-exactly"
    );
}

#[test]
fn jpeg_to_jxl_lossless_with_stripped_metadata_transcodes_coefficients() {
    let jpeg = fixture("external/webkit_iceland_p3.jpg");
    let out = run(&jpeg, r#"{"format":"jxl","lossless":true}"#);
    // Re-encoded pixels would be larger than the JPEG; transcoding is smaller.
    assert!(out.bytes.len() < jpeg.len());
    assert!(!has_jbrd(&out.bytes));
    let jxl = reload(&out);
    assert!(!has_field(&jxl, "exif-data"));
    assert!(has_field(&jxl, "icc-profile-data"));
}

#[test]
fn ultrahdr_to_jxl_lossless_keeps_gain_map_unless_sdr() {
    let ultra = fixture("external/aom_apple_gainmap_new.jpg");
    let out = run(
        &ultra,
        r#"{"format":"jxl","lossless":true,"metadata":"keep"}"#,
    );
    // The gain map lives in the reconstruction data, so the restored JPEG is
    // the original UltraHDR file.
    assert_eq!(
        djxl_jpeg(&out.bytes),
        ultra,
        "UltraHDR not reconstructed bit-exactly"
    );

    // `dynamic_range=sdr` drops the gain map with the reconstruction data.
    let sdr = run(
        &ultra,
        r#"{"format":"jxl","lossless":true,"metadata":"keep","dynamic_range":"sdr"}"#,
    );
    assert!(!has_jbrd(&sdr.bytes));
    assert!(sdr.bytes.len() < ultra.len());
}

/// Every fixture × every format; only CICP-tagged HDR sources may fail. Slow in
/// debug builds, so run with `task test:fixtures`.
#[test]
#[ignore]
fn fixture_sweep() {
    const REJECTED: [&str; 4] = [
        "quality/hdr_pq_10bit.avif",
        "quality/hdr_hlg_10bit.avif",
        "external/aom_seine_hdr_rec2020.avif",
        // PQ base image (sRGB primaries) with an SDR gain map alternate.
        "external/aom_seine_hdr_gainmap_srgb.avif",
    ];
    let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../../../scripts/fixtures");
    let mut unexpected = Vec::new();
    for dir in ["quality", "external", "hd_benchmark"] {
        let mut entries: Vec<_> = std::fs::read_dir(format!("{root}/{dir}"))
            .unwrap()
            .flatten()
            .filter(|e| !e.file_name().to_string_lossy().starts_with('.'))
            .collect();
        entries.sort_by_key(|e| e.path());
        for entry in entries {
            let name = format!("{dir}/{}", entry.file_name().to_string_lossy());
            let input = std::fs::read(entry.path()).unwrap();
            for format in ["avif", "heic", "jxl", "webp", "jpeg", "png"] {
                let json = format!(r#"{{"format":"{format}","resize":{{"width":1024}}}}"#);
                let result = process(&input, &opts(&json), &Settings::default());
                match (REJECTED.contains(&name.as_str()), result) {
                    (false, Ok(_)) | (true, Err(Error::Unsupported(_))) => {}
                    (_, result) => {
                        unexpected.push(format!("{name} -> {format}: {:?}", result.err()))
                    }
                }
            }
        }
    }
    assert!(unexpected.is_empty(), "{}", unexpected.join("\n"));
}

#[test]
fn prepare_and_encode_match_process_byte_for_byte() {
    let cases = [
        (
            fixture("hd_benchmark/landscape_mountains_4k.jpg"),
            r#"{"format":"avif","quality":68,"resize":{"width":640},"metadata":"keep"}"#,
        ),
        (
            deep_png(),
            r#"{"format":"avif","quality":75,"bit_depth":10,"color":"srgb"}"#,
        ),
        (
            deep_png(),
            r#"{"format":"jxl","distance":1.5,"bit_depth":16}"#,
        ),
        (
            fixture("external/aom_apple_gainmap_new.jpg"),
            r#"{"format":"jpeg","quality":82,"dynamic_range":"keep","metadata":"keep"}"#,
        ),
        (
            fixture("quality/checker_rb_444.png"),
            r#"{"format":"webp","quality":75}"#,
        ),
    ];
    let settings = Settings::default();
    for (input, spec) in cases {
        let o = opts(spec);
        let direct = process(&input, &o, &settings).unwrap();
        let prepared = prepare(&input, &o, &settings).unwrap();
        let split = encode(&prepared, &o, &settings).unwrap();
        assert_eq!(split.mime, direct.mime, "{spec}");
        assert_eq!((split.width, split.height), (direct.width, direct.height), "{spec}");
        assert_eq!(split.bytes, direct.bytes, "{spec}: bytes differ");
    }
}

#[test]
fn explicit_chroma_matches_auto_cutoff_at_quality_90() {
    let input = fixture("quality/checker_rb_444.png");
    let settings = Settings::default();
    for format in ["avif", "heic", "jpeg"] {
        for (q, expected_chroma) in [(89, "420"), (90, "444")] {
            let auto_opts = opts(&format!(r#"{{"format":"{format}","quality":{q}}}"#));
            let explicit_opts = opts(&format!(
                r#"{{"format":"{format}","quality":{q},"chroma":"{expected_chroma}"}}"#
            ));
            let a = process(&input, &auto_opts, &settings).unwrap();
            let b = process(&input, &explicit_opts, &settings).unwrap();
            assert_eq!(a.bytes, b.bytes, "{format} Q{q} chroma={expected_chroma}");
        }
    }
}
