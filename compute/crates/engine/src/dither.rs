use image::{DynamicImage, ImageBuffer, Rgb, Rgba};

pub const BLUE_NOISE_64X64: &[u8; 4096] = include_bytes!("blue_noise_64.bin");

/// Downsamples a 16-bit image to 8-bit, optionally applying blue-noise dithering
/// to break up the quantization banding.
pub fn quantize_16bit_to_8bit(img: &DynamicImage, dither: bool) -> DynamicImage {
    let width = img.width();
    let height = img.height();

    match img {
        DynamicImage::ImageRgba16(rgba16) => {
            let out_buf = dither_rgba16_to_rgba8(width, height, rgba16.as_raw(), dither);
            let img_buf = ImageBuffer::<Rgba<u8>, _>::from_raw(width, height, out_buf)
                .expect("Buffer dimension mismatch");
            DynamicImage::ImageRgba8(img_buf)
        }
        DynamicImage::ImageRgb16(rgb16) => {
            let out_buf = dither_rgb16_to_rgb8(width, height, rgb16.as_raw(), dither);
            let img_buf = ImageBuffer::<Rgb<u8>, _>::from_raw(width, height, out_buf)
                .expect("Buffer dimension mismatch");
            DynamicImage::ImageRgb8(img_buf)
        }
        _ => img.clone(),
    }
}

/// Rounds a 16-bit sample to 8-bit without dithering.
#[inline]
fn round_u16_to_u8(val: u16) -> u8 {
    ((val as u32 * 255 + 32895) / 65535) as u8
}

/// Quantizes a 16-bit sample to 8-bit, adding one LSB when the blue-noise
/// threshold falls below the truncated remainder.
#[inline]
fn quantize_with_blue_noise(val: u16, threshold: u32) -> u8 {
    let base = val as u32 / 257;
    let rem = val as u32 % 257;
    (base + u32::from(rem >= threshold)).min(255) as u8
}

/// Dithers RGB 16-bit interleaved buffer [R, G, B, ...] to 8-bit.
pub fn dither_rgb16_to_rgb8(width: u32, height: u32, data: &[u16], dither: bool) -> Vec<u8> {
    let w = width as usize;
    let h = height as usize;
    let mut out = vec![0u8; w * h * 3];

    if !dither {
        for (src, dst) in data.chunks_exact(3).zip(out.chunks_exact_mut(3)) {
            dst[0] = round_u16_to_u8(src[0]);
            dst[1] = round_u16_to_u8(src[1]);
            dst[2] = round_u16_to_u8(src[2]);
        }
        return out;
    }

    for y in 0..h {
        let bn_y = (y % 64) * 64;
        for x in 0..w {
            let threshold = (BLUE_NOISE_64X64[bn_y + (x % 64)] as u32 * 257) / 256;
            let idx = (y * w + x) * 3;
            out[idx] = quantize_with_blue_noise(data[idx], threshold);
            out[idx + 1] = quantize_with_blue_noise(data[idx + 1], threshold);
            out[idx + 2] = quantize_with_blue_noise(data[idx + 2], threshold);
        }
    }

    out
}

/// Dithers RGBA 16-bit interleaved buffer [R, G, B, A, ...] to 8-bit.
/// Alpha is quantized directly without dithering.
pub fn dither_rgba16_to_rgba8(width: u32, height: u32, data: &[u16], dither: bool) -> Vec<u8> {
    let w = width as usize;
    let h = height as usize;
    let mut out = vec![0u8; w * h * 4];

    if !dither {
        for (src, dst) in data.chunks_exact(4).zip(out.chunks_exact_mut(4)) {
            dst[0] = round_u16_to_u8(src[0]);
            dst[1] = round_u16_to_u8(src[1]);
            dst[2] = round_u16_to_u8(src[2]);
            dst[3] = round_u16_to_u8(src[3]);
        }
        return out;
    }

    for y in 0..h {
        let bn_y = (y % 64) * 64;
        for x in 0..w {
            let threshold = (BLUE_NOISE_64X64[bn_y + (x % 64)] as u32 * 257) / 256;
            let idx = (y * w + x) * 4;
            out[idx] = quantize_with_blue_noise(data[idx], threshold);
            out[idx + 1] = quantize_with_blue_noise(data[idx + 1], threshold);
            out[idx + 2] = quantize_with_blue_noise(data[idx + 2], threshold);
            out[idx + 3] = round_u16_to_u8(data[idx + 3]);
        }
    }

    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dithering_algorithms_execution() {
        let width = 64;
        let height = 64;
        // Construct smooth 16-bit gradient
        let mut gradient = Vec::with_capacity((width * height * 4) as usize);
        for y in 0..height {
            for x in 0..width {
                let v = ((x as u32 + y as u32) * 65535 / 128) as u16;
                gradient.push(v);
                gradient.push(v);
                gradient.push(v);
                gradient.push(65535);
            }
        }

        for dither in [false, true] {
            let dithered = dither_rgba16_to_rgba8(width, height, &gradient, dither);
            assert_eq!(dithered.len(), (width * height * 4) as usize);
            assert!(dithered.iter().any(|&val| val > 0));
        }
    }
}
