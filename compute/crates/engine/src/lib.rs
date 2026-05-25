use anyhow::{bail, Result};
use compute_types::{EncodeOptions, Format};
use std::io::Cursor;

pub fn process(
    data: &[u8],
    target_format: Format,
    _options: Option<EncodeOptions>,
) -> Result<Vec<u8>> {
    // Platform checking via cfg macro
    cfg_if::cfg_if! {
        if #[cfg(target_arch = "wasm32")] {
            // WASM Specifics (e.g., single threaded encoder setting)
            // Example: ravif::Encoder::new().with_threads(1);
        } else {
            // Server Specifics (e.g., multi threaded encoder setting)
            // Example: ravif::Encoder::new().with_threads(4);
        }
    }

    let img = image::load_from_memory(data)?;
    let mut out_buffer = Cursor::new(Vec::new());

    match target_format {
        Format::Webp => {
            img.write_to(&mut out_buffer, image::ImageFormat::WebP)?;
            Ok(out_buffer.into_inner())
        }
        Format::Jpeg => {
            img.write_to(&mut out_buffer, image::ImageFormat::Jpeg)?;
            Ok(out_buffer.into_inner())
        }
        Format::Png => {
            img.write_to(&mut out_buffer, image::ImageFormat::Png)?;
            Ok(out_buffer.into_inner())
        }
        Format::Avif => {
            img.write_to(&mut out_buffer, image::ImageFormat::Avif)?;
            Ok(out_buffer.into_inner())
        }

        // Wait for specific pure-rust implementations to be added for JXL, AVIF, etc.
        _ => bail!(
            "Format {:?} currently not supported via pure rust portable engine",
            target_format
        ),
    }
}
