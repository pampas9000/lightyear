//! Bit-exact JPEG → JXL recompression through `cjxl`.
//!
//! cjxl keeps the original JPEG's DCT coefficients and metadata, so the JPEG can
//! be reconstructed byte for byte, at roughly 20% less size. libvips can only
//! re-encode decoded pixels, so this is the one case handled outside it.

use crate::{Error, Output};
use compute_types::ImageOptions;
use compute_types::image_options::{ColorIntent, Metadata, OutputFormat};
use rs_vips::VipsImage;
use std::process::Command;

/// Returns `None` when the request is not a pure recompression, or when cjxl is
/// not installed (libvips then encodes the decoded pixels losslessly instead).
pub fn jpeg_to_jxl(
    input: &[u8],
    source: &VipsImage,
    loader: &str,
    options: &ImageOptions,
) -> Result<Option<Output>, Error> {
    // UltraHDR JPEGs load through `uhdrload`; cjxl would drop their gain map.
    let pure = loader == "jpegload_buffer"
        && options.format == OutputFormat::Jxl
        && options.lossless
        && options.resize.is_none()
        && options.color == ColorIntent::Keep
        && options.metadata == Metadata::Keep;
    if !pure {
        return Ok(None);
    }

    let dir = tempfile::tempdir().map_err(|e| Error::Failed(format!("temp dir: {e}")))?;
    let (src, dst) = (dir.path().join("in.jpg"), dir.path().join("out.jxl"));
    std::fs::write(&src, input).map_err(|e| Error::Failed(format!("write input: {e}")))?;
    let run = Command::new("cjxl")
        .arg("--lossless_jpeg=1")
        .arg(&src)
        .arg(&dst)
        .output();
    let out = match run {
        Ok(out) => out,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
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
