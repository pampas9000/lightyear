//! Maps `ImageOptions` onto the libvips savers.

use crate::{AvifEncoder, Error, vips_error};
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
pub(crate) fn spread(effort: u8, low: i32, high: i32) -> i32 {
    let e = effort.clamp(1, 10) as i32;
    low + ((e - 1) * (high - low) + 4) / 9
}

/// Sets an encoder's effort option only when the request gives one, so an
/// unset effort means that encoder's own default.
fn effort<'a>(
    opts: VOption<'a>,
    name: &'a str,
    options: &ImageOptions,
    low: i32,
    high: i32,
) -> VOption<'a> {
    match options.effort {
        Some(e) => opts.set(name, spread(e, low, high)),
        None => opts,
    }
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

/// AVIF/HEIC depth: as requested, else 10 for deeper sources and 8 otherwise.
fn heif_depth(image: &VipsImage, options: &ImageOptions) -> i32 {
    options
        .bit_depth
        .map(i32::from)
        .unwrap_or(if is_deep(image) { 10 } else { 8 })
}

pub fn save(
    image: &VipsImage,
    options: &ImageOptions,
    has_gain_map: bool,
    avif_encoder: AvifEncoder,
) -> Result<Vec<u8>, Error> {
    let quality = options.quality.map(i32::from);
    let keep_gain_map = has_gain_map && options.dynamic_range == DynamicRange::Keep;
    let mut opts = VOption::new().set("keep", keep(options, keep_gain_map));
    if let Some(q) = quality {
        opts = opts.set("Q", q);
    }
    let result = match options.format {
        OutputFormat::Avif if avif_encoder == AvifEncoder::Libavif => {
            return crate::avif::save(image, options, heif_depth(image, options));
        }
        OutputFormat::Avif | OutputFormat::Heic => {
            let compression = if options.format == OutputFormat::Avif {
                "av1"
            } else {
                "hevc"
            };
            let subsample = match options.chroma {
                Some(Chroma::Yuv420) => "on",
                Some(Chroma::Yuv444) => "off",
                None => "auto",
            };
            image.heifsave_buffer_with_opts(effort(
                opts.set("compression", compression)
                    .set("lossless", options.lossless)
                    .set("bitdepth", heif_depth(image, options))
                    .set("subsample_mode", subsample),
                "effort",
                options,
                0,
                9,
            ))
        }
        OutputFormat::Jxl => image.jxlsave_buffer_with_opts(effort(
            opts.set("lossless", options.lossless),
            "effort",
            options,
            1,
            9,
        )),
        OutputFormat::Webp => image.webpsave_buffer_with_opts(effort(
            opts.set("lossless", options.lossless),
            "effort",
            options,
            0,
            6,
        )),
        OutputFormat::Png => image.pngsave_buffer_with_opts(effort(
            opts.set("bitdepth", if is_deep(image) { 16 } else { 8 }),
            "compression",
            options,
            0,
            9,
        )),
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
        assert_eq!(spread(1, 0, 9), 0);
        assert_eq!(spread(10, 0, 9), 9);
        assert_eq!(spread(5, 0, 9), 4);
        assert_eq!(spread(1, 1, 9), 1);
        assert_eq!(spread(10, 1, 9), 9);
        assert_eq!(spread(10, 0, 6), 6);
        assert_eq!(spread(5, 0, 10), 4);
    }
}
