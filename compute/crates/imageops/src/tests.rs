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
    process(input, &opts(json), &Limits::default()).unwrap_or_else(|e| panic!("{json}: {e}"))
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
fn untrusted_loaders_are_blocked() {
    let svg = br#"<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"/>"#;
    assert!(matches!(
        process(svg, &opts(r#"{"format":"png"}"#), &Limits::default()),
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
            &Limits::default(),
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
    let tiny = Limits { max_pixels: 1000 };
    assert!(matches!(
        process(&photo, &opts(r#"{"format":"png"}"#), &tiny),
        Err(Error::Unsupported(_))
    ));
    assert!(matches!(
        process(
            b"not an image",
            &opts(r#"{"format":"png"}"#),
            &Limits::default()
        ),
        Err(Error::Unsupported(_))
    ));
    assert!(matches!(
        process(
            &photo,
            &opts(r#"{"format":"png","quality":50}"#),
            &Limits::default()
        ),
        Err(Error::Invalid(_))
    ));
}

#[test]
fn jpeg_to_jxl_lossless_is_bit_exact() {
    let jpeg = fixture("quality/jpeg_420_90.jpg");
    let out = run(
        &jpeg,
        r#"{"format":"jxl","lossless":true,"metadata":"keep"}"#,
    );
    assert_eq!(out.mime, "image/jxl");
    let dir = tempfile::tempdir().unwrap();
    let (jxl, back) = (dir.path().join("a.jxl"), dir.path().join("back.jpg"));
    std::fs::write(&jxl, &out.bytes).unwrap();
    // cjxl and djxl are development dependencies: a missing tool fails here
    // rather than quietly testing the pixel fallback.
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
    assert_eq!(
        std::fs::read(&back).unwrap(),
        jpeg,
        "JPEG not reconstructed bit-exactly (is cjxl installed?)"
    );
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
                let result = process(&input, &opts(&json), &Limits::default());
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
