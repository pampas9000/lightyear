pub mod edit;

use anyhow::{bail, Result};
use compute_types::{EncodeOptions, Format};
use image::DynamicImage;
use std::io::Cursor;

fn is_jxl(data: &[u8]) -> bool {
    if data.len() < 2 {
        return false;
    }
    if data[0] == 0xFF && data[1] == 0x0A {
        return true;
    }
    if data.len() >= 12
        && &data[0..12] == &[0x00, 0x00, 0x00, 0x0C, 0x4A, 0x58, 0x4C, 0x20, 0x0D, 0x0A, 0x87, 0x0A]
    {
        return true;
    }
    false
}

pub fn load_from_memory(data: &[u8]) -> Result<DynamicImage> {
    if is_jxl(data) {
        use jxl_oxide::integration::JxlDecoder;
        let decoder = JxlDecoder::new(Cursor::new(data))
            .map_err(|e| anyhow::anyhow!("Failed to initialize JXL decoder: {}", e))?;
        let img = DynamicImage::from_decoder(decoder)
            .map_err(|e| anyhow::anyhow!("Failed to decode JXL image: {}", e))?;
        Ok(img)
    } else if matches!(image::guess_format(data), Ok(image::ImageFormat::Avif)) {
        use zenpixels_convert::ext::PixelBufferConvertTypedExt;
        let decoded = zenavif::decode(data)?;
        let w = decoded.width();
        let h = decoded.height();

        let rgba_buffer = decoded.to_rgba8();
        let rgba_pixels = rgba_buffer.erase().into_contiguous_pixels::<rgb::Rgba<u8>>()
            .ok_or_else(|| anyhow::anyhow!("Failed to convert AVIF to contiguous pixels"))?;

        let raw_bytes = bytemuck::cast_vec::<rgb::Rgba<u8>, u8>(rgba_pixels);
        let img_buffer = image::ImageBuffer::<image::Rgba<u8>, Vec<u8>>::from_raw(w, h, raw_bytes)
            .ok_or_else(|| anyhow::anyhow!("Failed to construct ImageBuffer from AVIF pixels"))?;
        
        Ok(image::DynamicImage::ImageRgba8(img_buffer))
    } else {
        Ok(image::load_from_memory(data)?)
    }
}

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

    let img = load_from_memory(data)?;
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
        Format::Jxl => {
            use zune_core::bit_depth::BitDepth;
            use zune_core::colorspace::ColorSpace;
            use zune_core::options::EncoderOptions;
            use zune_jpegxl::JxlSimpleEncoder;

            let width = img.width() as usize;
            let height = img.height() as usize;

            let (colorspace, bit_depth, raw_pixels) = match img {
                DynamicImage::ImageLuma8(i) => (ColorSpace::Luma, BitDepth::Eight, i.as_raw().clone()),
                DynamicImage::ImageLumaA8(i) => (ColorSpace::LumaA, BitDepth::Eight, i.as_raw().clone()),
                DynamicImage::ImageRgb8(i) => (ColorSpace::RGB, BitDepth::Eight, i.as_raw().clone()),
                DynamicImage::ImageRgba8(i) => (ColorSpace::RGBA, BitDepth::Eight, i.as_raw().clone()),
                DynamicImage::ImageLuma16(i) => {
                    let bytes = bytemuck::cast_slice::<u16, u8>(i.as_raw()).to_vec();
                    (ColorSpace::Luma, BitDepth::Sixteen, bytes)
                }
                DynamicImage::ImageLumaA16(i) => {
                    let bytes = bytemuck::cast_slice::<u16, u8>(i.as_raw()).to_vec();
                    (ColorSpace::LumaA, BitDepth::Sixteen, bytes)
                }
                DynamicImage::ImageRgb16(i) => {
                    let bytes = bytemuck::cast_slice::<u16, u8>(i.as_raw()).to_vec();
                    (ColorSpace::RGB, BitDepth::Sixteen, bytes)
                }
                DynamicImage::ImageRgba16(i) => {
                    let bytes = bytemuck::cast_slice::<u16, u8>(i.as_raw()).to_vec();
                    (ColorSpace::RGBA, BitDepth::Sixteen, bytes)
                }
                other => {
                    let rgba8 = other.to_rgba8();
                    (ColorSpace::RGBA, BitDepth::Eight, rgba8.as_raw().clone())
                }
            };

            let options = EncoderOptions::new(width, height, colorspace, bit_depth)
                .set_quality(quality);
            let encoder = JxlSimpleEncoder::new(&raw_pixels, options);
            let mut encoded_bytes = Vec::new();
            encoder.encode(&mut encoded_bytes)
                .map_err(|e| anyhow::anyhow!("Failed to encode JXL: {:?}", e))?;
            
            Ok(encoded_bytes)
        }
        _ => bail!(
            "Format {:?} currently not supported via pure rust portable engine",
            format
        ),
    }
}

