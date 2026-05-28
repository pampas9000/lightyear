pub mod edit;

use anyhow::{bail, Result};
use compute_types::{EncodeOptions, Format};
use image::DynamicImage;
use std::io::Cursor;

pub fn process(
    data: &[u8],
    target_format: Format,
    options: Option<EncodeOptions>,
) -> Result<Vec<u8>> {
    // Platform checking via cfg macro
    cfg_if::cfg_if! {
        if #[cfg(target_arch = "wasm32")] {
            // WASM Specifics (e.g., single threaded encoder setting)
        } else {
            // Server Specifics (e.g., multi threaded encoder setting)
        }
    }

    let img = image::load_from_memory(data)?;
    let quality = options.map(|o| o.quality).unwrap_or(80);

    encode(&img, target_format, quality)
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
        _ => bail!(
            "Format {:?} currently not supported via pure rust portable engine",
            format
        ),
    }
}

