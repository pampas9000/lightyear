use crate::dither::quantize_16bit_to_8bit;
use crate::tonemap::apply_tone_mapping_to_image;
use compute_types::{ColorEncoding, ToneMappingOperator, TransferCharacteristics};
use image::DynamicImage;

/// Describes how the decoded pixels and the encoded output relate to color.
///
/// The engine never rewrites pixels between primaries; it only moves them along
/// the transfer axis (tone mapping / HDR-to-SDR) and quantizes for the output
/// bit depth. Primaries are carried by the container through ICC/CICP instead.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct OutputColorSpec {
    /// Transfer characteristics the source pixels are encoded with.
    pub source_transfer: TransferCharacteristics,
    /// Transfer characteristics the encoded output will be interpreted with.
    pub target_transfer: TransferCharacteristics,
    /// Bit depth of the encoded output; quantization applies at 8 bits or below.
    pub bit_depth: u8,
}

/// Applies the output color pipeline: tone mapping and dithering, in the order
/// required for correct results.
pub fn apply_output_color_pipeline(
    img: &mut DynamicImage,
    color: &ColorEncoding,
    output: OutputColorSpec,
) {
    // Tone mapping / transfer conversion: map the source transfer function to
    // the transfer the encoded output will be decoded with.
    if let Some(tmo) = color
        .tone_mapping
        .filter(|t| *t != ToneMappingOperator::None)
    {
        apply_tone_mapping_to_image(img, tmo, output.source_transfer, output.target_transfer);
    }

    // Quantization: 16-bit sources headed for an 8-bit output are dithered when
    // requested and truncated otherwise.
    if crate::is_16bit_image(img) && output.bit_depth <= 8 {
        *img = if color.dither {
            quantize_16bit_to_8bit(img, true)
        } else {
            DynamicImage::ImageRgba8(img.to_rgba8())
        };
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::{ImageBuffer, Rgba};

    fn spec(bit_depth: u8) -> OutputColorSpec {
        OutputColorSpec {
            source_transfer: TransferCharacteristics::Srgb,
            target_transfer: TransferCharacteristics::Srgb,
            bit_depth,
        }
    }

    #[test]
    fn dithers_16bit_source_for_8bit_output() {
        let color = ColorEncoding {
            dither: true,
            ..Default::default()
        };

        let mut img16 = DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
            4,
            4,
            Rgba([60000u16, 20000, 10000, 65535]),
        ));
        apply_output_color_pipeline(&mut img16, &color, spec(8));
        assert!(matches!(img16, DynamicImage::ImageRgba8(_)));

        let mut kept = DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
            4,
            4,
            Rgba([60000u16, 20000, 10000, 65535]),
        ));
        apply_output_color_pipeline(&mut kept, &color, spec(10));
        assert!(matches!(kept, DynamicImage::ImageRgba16(_)));
    }

    #[test]
    fn truncates_16bit_source_without_dithering() {
        let color = ColorEncoding::default();
        let mut img = DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
            4,
            4,
            Rgba([60000u16, 20000, 10000, 65535]),
        ));

        apply_output_color_pipeline(&mut img, &color, spec(8));

        assert!(matches!(img, DynamicImage::ImageRgba8(_)));
    }
}
