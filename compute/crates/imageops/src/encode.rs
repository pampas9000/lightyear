//! Maps `ImageOptions` onto the libvips savers.

use crate::{Error, vips_error};
use compute_types::ImageOptions;
use compute_types::image_options::{Chroma, DynamicRange, Metadata, OutputFormat};
use rs_vips::VipsImage;
use rs_vips::enums::{BandFormat, ForeignKeep};
use rs_vips::voption::{Setter, VOption};

pub fn mime(format: OutputFormat) -> &'static str {
    match format {
        OutputFormat::Avif => "image/avif",
        OutputFormat::Heic => "image/heic",
        OutputFormat::Jxl => "image/jxl",
        OutputFormat::Webp => "image/webp",
        OutputFormat::Jpeg => "image/jpeg",
        OutputFormat::Png => "image/png",
    }
}

/// Spreads the shared 1–10 effort over an encoder's own range.
fn effort(options: &ImageOptions, low: i32, high: i32) -> i32 {
    let e = options.effort.unwrap_or(5).clamp(1, 10) as i32;
    low + ((e - 1) * (high - low) + 4) / 9
}

fn keep(options: &ImageOptions, gain_map: bool) -> i32 {
    // The ICC profile is governed by `color`: either the source's, or sRGB.
    let mut flags = ForeignKeep::Icc as i32;
    if options.metadata == Metadata::Keep {
        flags |= ForeignKeep::Exif as i32 | ForeignKeep::Xmp as i32 | ForeignKeep::Iptc as i32;
    }
    if gain_map {
        flags |= ForeignKeep::Gainmap as i32;
    }
    flags
}

fn is_deep(image: &VipsImage) -> bool {
    matches!(
        image.get_format(),
        Ok(BandFormat::Ushort | BandFormat::Float)
    )
}

pub fn save(
    image: &VipsImage,
    options: &ImageOptions,
    has_gain_map: bool,
) -> Result<Vec<u8>, Error> {
    let quality = options.quality.map(i32::from);
    let keep_gain_map = has_gain_map && options.dynamic_range == DynamicRange::Keep;
    let mut opts = VOption::new().set("keep", keep(options, keep_gain_map));
    if let Some(q) = quality {
        opts = opts.set("Q", q);
    }
    let result = match options.format {
        OutputFormat::Avif | OutputFormat::Heic => {
            let compression = if options.format == OutputFormat::Avif {
                "av1"
            } else {
                "hevc"
            };
            let depth = options
                .bit_depth
                .map(i32::from)
                .unwrap_or(if is_deep(image) { 10 } else { 8 });
            let subsample = match options.chroma {
                Some(Chroma::Yuv420) => "on",
                Some(Chroma::Yuv444) => "off",
                None => "auto",
            };
            image.heifsave_buffer_with_opts(
                opts.set("compression", compression)
                    .set("lossless", options.lossless)
                    .set("effort", effort(options, 0, 9))
                    .set("bitdepth", depth)
                    .set("subsample_mode", subsample),
            )
        }
        OutputFormat::Jxl => image.jxlsave_buffer_with_opts(
            opts.set("lossless", options.lossless)
                .set("effort", effort(options, 1, 9)),
        ),
        OutputFormat::Webp => image.webpsave_buffer_with_opts(
            opts.set("lossless", options.lossless)
                .set("effort", effort(options, 0, 6)),
        ),
        OutputFormat::Png => image.pngsave_buffer_with_opts(
            opts.set("compression", effort(options, 0, 9))
                .set("bitdepth", if is_deep(image) { 16 } else { 8 }),
        ),
        // A gain map survives only as UltraHDR JPEG.
        OutputFormat::Jpeg if keep_gain_map => image.uhdrsave_buffer_with_opts(opts),
        OutputFormat::Jpeg => {
            let subsample = match options.chroma {
                Some(Chroma::Yuv420) => "on",
                Some(Chroma::Yuv444) => "off",
                None => "auto",
            };
            image.jpegsave_buffer_with_opts(
                opts.set("subsample_mode", subsample)
                    .set("optimize_coding", true)
                    // JPEG has no alpha; flatten onto white instead of black.
                    .set("background", &[255.0][..]),
            )
        }
    };
    result.map_err(|e| vips_error("encode", e))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn effort_spans_each_encoder_range() {
        let with = |e| ImageOptions {
            effort: Some(e),
            ..serde_json::from_str(r#"{"format":"avif"}"#).unwrap()
        };
        assert_eq!(effort(&with(1), 0, 9), 0);
        assert_eq!(effort(&with(10), 0, 9), 9);
        assert_eq!(effort(&with(5), 0, 9), 4);
        assert_eq!(effort(&with(1), 1, 9), 1);
        assert_eq!(effort(&with(10), 1, 9), 9);
        assert_eq!(effort(&with(10), 0, 6), 6);
    }
}
