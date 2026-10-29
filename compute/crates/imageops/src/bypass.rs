//! Bit-exact JPEG → JXL recompression through `cjxl`.
//!
//! cjxl keeps the original JPEG's DCT coefficients, at 10–30% less size. With
//! metadata kept, it also stores reconstruction data so `djxl` restores the
//! JPEG byte for byte. libvips can only re-encode decoded pixels, so this is
//! the one case handled outside it.
//!
//! An UltraHDR gain map survives only inside the reconstruction data: a JXL
//! viewer shows the SDR base, and `djxl` restores the HDR JPEG.

use crate::{Error, Output};
use compute_types::ImageOptions;
use compute_types::image_options::{ColorIntent, DynamicRange, Metadata, OutputFormat};
use rs_vips::VipsImage;
use std::process::Command;

/// Returns `None` when the request is not a pure recompression. cjxl is a
/// required worker dependency, so a missing binary is an error.
pub fn jpeg_to_jxl(
    input: &[u8],
    source: &VipsImage,
    loader: &str,
    has_gain_map: bool,
    options: &ImageOptions,
) -> Result<Option<Output>, Error> {
    // UltraHDR JPEGs load through `uhdrload`.
    let jpeg = matches!(loader, "jpegload_buffer" | "uhdrload_buffer");
    let pure = jpeg
        && options.format == OutputFormat::Jxl
        && options.lossless
        && options.resize.is_none()
        && options.color == ColorIntent::Keep;
    if !pure {
        return Ok(None);
    }
    // Reconstruction data carries every JPEG marker, the gain map included,
    // so it is kept only when nothing is to be dropped. Without it cjxl still
    // transcodes the DCT coefficients and moves the EXIF orientation into the
    // codestream header, but the original JPEG can no longer be restored.
    let strip = options.metadata == Metadata::Strip;
    let reconstruct = !strip && !(has_gain_map && options.dynamic_range == DynamicRange::Sdr);

    let dir = tempfile::tempdir().map_err(|e| Error::Failed(format!("temp dir: {e}")))?;
    let (src, dst) = (dir.path().join("in.jpg"), dir.path().join("out.jxl"));
    std::fs::write(&src, input).map_err(|e| Error::Failed(format!("write input: {e}")))?;
    let mut cmd = Command::new("cjxl");
    cmd.arg("--lossless_jpeg=1");
    if !reconstruct {
        cmd.arg("--allow_jpeg_reconstruction=0");
    }
    if strip {
        for kind in ["exif", "xmp", "jumbf"] {
            cmd.arg("-x").arg(format!("strip={kind}"));
        }
    }
    let run = cmd.arg(&src).arg(&dst).output();
    let out = match run {
        Ok(out) => out,
        Err(e) => return Err(Error::Failed(format!("cjxl: {e}"))),
    };
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(Error::Failed(format!("cjxl: {}", stderr.trim())));
    }
    let bytes = std::fs::read(&dst).map_err(|e| Error::Failed(format!("read output: {e}")))?;

    // The JPEG keeps its EXIF orientation, so report the displayed size.
    let (w, h) = (source.get_width() as u32, source.get_height() as u32);
    let rotated = source
        .get_int("orientation")
        .is_ok_and(|o| (5..=8).contains(&o));
    let (width, height) = if rotated { (h, w) } else { (w, h) };
    Ok(Some(Output {
        bytes,
        mime: "image/jxl",
        width,
        height,
    }))
}

/// Fails when `cjxl` cannot be run; the worker calls this at startup.
pub fn check_cjxl() -> Result<(), Error> {
    match Command::new("cjxl").arg("--version").output() {
        Ok(out) if out.status.success() => Ok(()),
        Ok(out) => Err(Error::Failed(format!(
            "cjxl --version: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ))),
        Err(e) => Err(Error::Failed(format!("cjxl: {e}"))),
    }
}
