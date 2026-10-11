use crate::color::{eotf_to_sdr_relative, oetf_from_linear};
use compute_types::{ToneMappingOperator, TransferCharacteristics};
use image::DynamicImage;

/// Evaluates the tone mapping curve on a single linear luminance/intensity
/// scalar expressed relative to SDR diffuse white (1.0 = diffuse white).
#[inline]
pub fn tonemap_scalar(x: f32, op: ToneMappingOperator) -> f32 {
    let x = x.max(0.0);
    match op {
        ToneMappingOperator::None => x.clamp(0.0, 1.0),
        ToneMappingOperator::AcesFilmic => {
            // Narkowicz fit of the ACES filmic curve
            let a = 2.51;
            let b = 0.03;
            let c = 2.43;
            let d = 0.59;
            let e = 0.14;
            let num = x * (a * x + b);
            let den = x * (c * x + d) + e;
            if den <= 0.0 {
                0.0
            } else {
                (num / den).clamp(0.0, 1.0)
            }
        }
    }
}

/// Applies tone mapping to linear RGB values using luminance preservation
/// to prevent chromaticity/hue shifts in bright highlights.
#[inline]
pub fn apply_tone_mapping_rgb_luminance(
    r: f32,
    g: f32,
    b: f32,
    op: ToneMappingOperator,
) -> (f32, f32, f32) {
    if op == ToneMappingOperator::None {
        return (r.clamp(0.0, 1.0), g.clamp(0.0, 1.0), b.clamp(0.0, 1.0));
    }

    // ITU-R BT.709 linear luminance weights
    let lum = 0.2126 * r.max(0.0) + 0.7152 * g.max(0.0) + 0.0722 * b.max(0.0);
    if lum <= 1e-6 {
        return (0.0, 0.0, 0.0);
    }

    let mapped_lum = tonemap_scalar(lum, op);
    let scale = mapped_lum / lum;

    (
        (r * scale).clamp(0.0, 1.0),
        (g * scale).clamp(0.0, 1.0),
        (b * scale).clamp(0.0, 1.0),
    )
}

/// Applies tone mapping to linear RGB values per channel independently.
#[inline]
pub fn apply_tone_mapping_rgb_per_channel(
    r: f32,
    g: f32,
    b: f32,
    op: ToneMappingOperator,
) -> (f32, f32, f32) {
    (
        tonemap_scalar(r, op),
        tonemap_scalar(g, op),
        tonemap_scalar(b, op),
    )
}

/// Default tone mapping helper using luminance preservation.
#[inline]
pub fn apply_tone_mapping_rgb(r: f32, g: f32, b: f32, op: ToneMappingOperator) -> (f32, f32, f32) {
    apply_tone_mapping_rgb_luminance(r, g, b, op)
}

/// Applies tone mapping and transfer curve conversion to a DynamicImage in place.
pub fn apply_tone_mapping_to_image(
    img: &mut DynamicImage,
    op: ToneMappingOperator,
    source_transfer: TransferCharacteristics,
    target_transfer: TransferCharacteristics,
) {
    if op == ToneMappingOperator::None {
        return;
    }

    match img {
        DynamicImage::ImageRgba8(rgba) => {
            for pixel in rgba.pixels_mut() {
                let r_lin = eotf_to_sdr_relative(pixel[0] as f32 / 255.0, source_transfer);
                let g_lin = eotf_to_sdr_relative(pixel[1] as f32 / 255.0, source_transfer);
                let b_lin = eotf_to_sdr_relative(pixel[2] as f32 / 255.0, source_transfer);

                let (r_map, g_map, b_map) = apply_tone_mapping_rgb(r_lin, g_lin, b_lin, op);

                let r_out = (oetf_from_linear(r_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;
                let g_out = (oetf_from_linear(g_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;
                let b_out = (oetf_from_linear(b_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;

                pixel[0] = r_out;
                pixel[1] = g_out;
                pixel[2] = b_out;
            }
        }
        DynamicImage::ImageRgb8(rgb) => {
            for pixel in rgb.pixels_mut() {
                let r_lin = eotf_to_sdr_relative(pixel[0] as f32 / 255.0, source_transfer);
                let g_lin = eotf_to_sdr_relative(pixel[1] as f32 / 255.0, source_transfer);
                let b_lin = eotf_to_sdr_relative(pixel[2] as f32 / 255.0, source_transfer);

                let (r_map, g_map, b_map) = apply_tone_mapping_rgb(r_lin, g_lin, b_lin, op);

                let r_out = (oetf_from_linear(r_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;
                let g_out = (oetf_from_linear(g_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;
                let b_out = (oetf_from_linear(b_map, target_transfer) * 255.0 + 0.5)
                    .clamp(0.0, 255.0) as u8;

                pixel[0] = r_out;
                pixel[1] = g_out;
                pixel[2] = b_out;
            }
        }
        DynamicImage::ImageRgba16(rgba16) => {
            for pixel in rgba16.pixels_mut() {
                let r_lin = eotf_to_sdr_relative(pixel[0] as f32 / 65535.0, source_transfer);
                let g_lin = eotf_to_sdr_relative(pixel[1] as f32 / 65535.0, source_transfer);
                let b_lin = eotf_to_sdr_relative(pixel[2] as f32 / 65535.0, source_transfer);

                let (r_map, g_map, b_map) = apply_tone_mapping_rgb(r_lin, g_lin, b_lin, op);

                let r_out = (oetf_from_linear(r_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;
                let g_out = (oetf_from_linear(g_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;
                let b_out = (oetf_from_linear(b_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;

                pixel[0] = r_out;
                pixel[1] = g_out;
                pixel[2] = b_out;
            }
        }
        DynamicImage::ImageRgb16(rgb16) => {
            for pixel in rgb16.pixels_mut() {
                let r_lin = eotf_to_sdr_relative(pixel[0] as f32 / 65535.0, source_transfer);
                let g_lin = eotf_to_sdr_relative(pixel[1] as f32 / 65535.0, source_transfer);
                let b_lin = eotf_to_sdr_relative(pixel[2] as f32 / 65535.0, source_transfer);

                let (r_map, g_map, b_map) = apply_tone_mapping_rgb(r_lin, g_lin, b_lin, op);

                let r_out = (oetf_from_linear(r_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;
                let g_out = (oetf_from_linear(g_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;
                let b_out = (oetf_from_linear(b_map, target_transfer) * 65535.0 + 0.5)
                    .clamp(0.0, 65535.0) as u16;

                pixel[0] = r_out;
                pixel[1] = g_out;
                pixel[2] = b_out;
            }
        }
        _ => {
            let mut rgba_dyn = if matches!(
                img,
                DynamicImage::ImageLuma16(_) | DynamicImage::ImageLumaA16(_)
            ) {
                DynamicImage::ImageRgba16(img.to_rgba16())
            } else {
                DynamicImage::ImageRgba8(img.to_rgba8())
            };
            apply_tone_mapping_to_image(&mut rgba_dyn, op, source_transfer, target_transfer);
            *img = rgba_dyn;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_tonemap_monotonicity_and_bounds() {
        let operators = [ToneMappingOperator::None, ToneMappingOperator::AcesFilmic];

        let samples = [0.0, 0.1, 0.5, 1.0, 2.0, 4.0, 10.0, 50.0];

        for &op in &operators {
            let mut prev = -1.0f32;
            for &x in &samples {
                let mapped = tonemap_scalar(x, op);
                assert!(
                    mapped >= 0.0 && mapped <= 1.0,
                    "Operator {:?} produced {} outside [0, 1] for x={}",
                    op,
                    mapped,
                    x
                );
                assert!(
                    mapped >= prev,
                    "Operator {:?} is not monotonic: {} < {} for x={}",
                    op,
                    mapped,
                    prev,
                    x
                );
                prev = mapped;
            }
        }
    }

    #[test]
    fn test_apply_tone_mapping_rgb_luminance_preservation() {
        let op = ToneMappingOperator::AcesFilmic;
        // Bright saturated red (HDR highlight)
        let (r, g, b) = apply_tone_mapping_rgb(5.0, 0.2, 0.1, op);
        assert!(r <= 1.0 && g <= 1.0 && b <= 1.0);
        // Red channel should dominate without turning achromatic
        assert!(r > g * 2.0);
        assert!(r > b * 2.0);
    }
}
