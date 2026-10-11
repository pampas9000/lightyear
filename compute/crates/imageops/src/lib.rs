//! Applies an [`ImageOptions`] output intent to an encoded image with libvips.
//!
//! Decoding, EXIF auto-rotation, resizing, color conversion, metadata handling
//! and encoding are all libvips; this crate only maps the options onto it and
//! guards the cases libvips would get wrong (HDR labels) or that need limits.
//! The one external tool is `cjxl`, for bit-exact JPEG → JXL recompression.

mod bypass;
mod encode;
mod probe;

pub use compute_types::ImageOptions;
use compute_types::image_options::{ColorIntent, Fit, Resize};
use rs_vips::enums::BandFormat;
use rs_vips::{
    Vips, VipsImage,
    voption::{Setter, VOption},
};
use std::sync::OnceLock;

/// Bounds a single job; anything larger is rejected before full decoding.
#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub max_pixels: u64,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            max_pixels: 100_000_000,
        }
    }
}

#[derive(Debug)]
pub struct Output {
    pub bytes: Vec<u8>,
    pub mime: &'static str,
    pub width: u32,
    pub height: u32,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Error {
    /// The options themselves are invalid.
    Invalid(String),
    /// The input cannot be processed as requested (unknown format, too large,
    /// HDR that would be mislabeled).
    Unsupported(String),
    /// libvips or cjxl failed while processing a supported request.
    Failed(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Invalid(m) => write!(f, "invalid options: {m}"),
            Self::Unsupported(m) => write!(f, "unsupported input: {m}"),
            Self::Failed(m) => write!(f, "processing failed: {m}"),
        }
    }
}

impl std::error::Error for Error {}

/// Wraps a libvips failure with the library's own error text.
pub(crate) fn vips_error(context: &str, err: rs_vips::error::Error) -> Error {
    let detail = Vips::error_buffer().unwrap_or_default();
    Vips::error_clear();
    Error::Failed(format!("{context}: {err} {}", detail.trim()))
}

/// Large enough to never constrain a side the request leaves open.
const UNBOUNDED: i32 = 10_000_000;

fn init() {
    static INIT: OnceLock<()> = OnceLock::new();
    INIT.get_or_init(|| {
        Vips::init("compute-imageops").expect("libvips failed to initialize");
        // Inputs are untrusted uploads: refuse the loaders libvips marks
        // unsafe (PDF, SVG, OpenEXR, FITS, …). libjxl is on that list too, but
        // JPEG XL is a supported format, so its loader and saver are re-allowed.
        unsafe {
            rs_vips::bindings::vips_block_untrusted_set(1);
            for class in [c"VipsForeignLoadJxl", c"VipsForeignSaveJxl"] {
                rs_vips::bindings::vips_operation_block_set(class.as_ptr(), 0);
            }
        }
        Vips::cache_set_max(0);
    });
}

/// Processes `input` according to `options`.
pub fn process(input: &[u8], options: &ImageOptions, limits: &Limits) -> Result<Output, Error> {
    let options = options
        .clone()
        .canonicalize()
        .map_err(|e| Error::Invalid(e.0))?;
    init();

    // Header only: libvips decodes lazily, so size checks happen before pixels.
    let source = VipsImage::new_from_buffer(input, "")
        .map_err(|_| Error::Unsupported("unrecognized or corrupt image".into()))?;
    let pixels = source.get_width() as u64 * source.get_height() as u64;
    if pixels > limits.max_pixels {
        return Err(Error::Unsupported(format!(
            "{pixels} pixels exceeds the limit of {}",
            limits.max_pixels
        )));
    }
    let loader = source.get_string("vips-loader").unwrap_or_default();
    if probe::has_hdr_transfer(input, &loader) {
        // libvips decodes PQ/HLG code values but writes them back as sRGB.
        return Err(Error::Unsupported(
            "HDR (PQ/HLG) sources are not supported yet".into(),
        ));
    }
    let has_gain_map = source.get_typeof("gainmap-data").unwrap_or(0) != 0;

    if let Some(output) = bypass::jpeg_to_jxl(input, &source, &loader, &options)? {
        return Ok(output);
    }

    let image = decode(input, &source, &options)?;
    let bytes = encode::save(&image, &options, has_gain_map)?;
    Ok(Output {
        bytes,
        mime: encode::mime(options.format),
        width: image.get_width() as u32,
        height: image.get_height() as u32,
    })
}

/// Decodes, auto-rotates, resizes and color-converts.
fn decode(input: &[u8], source: &VipsImage, options: &ImageOptions) -> Result<VipsImage, Error> {
    // `thumbnail` always produces 8-bit output, so deeper sources take the
    // explicit path to keep their precision.
    if matches!(source.get_format(), Ok(BandFormat::Uchar)) {
        thumbnail(input, options)
    } else {
        full_depth(input, options)
    }
}

/// One libvips call for 8-bit sources, which also lets JPEG and WebP shrink
/// while decoding.
fn thumbnail(input: &[u8], options: &ImageOptions) -> Result<VipsImage, Error> {
    let resize = options.resize.unwrap_or_default();
    let width = resize.width.map_or(UNBOUNDED, |w| w as i32);
    let height = resize.height.map_or(UNBOUNDED, |h| h as i32);
    let size = match (options.resize, resize.fit, resize.enlarge) {
        (None, ..) => "down",
        (Some(_), Fit::Fill, _) => "force",
        (Some(_), _, true) => "both",
        (Some(_), _, false) => "down",
    };
    let mut opts = VOption::new()
        .set("height", height)
        .set("size", size)
        .set("fail_on", "error");
    if resize.fit == Fit::Cover && options.resize.is_some() {
        opts = opts.set("crop", "centre");
    }
    if options.color == ColorIntent::Srgb {
        // Untagged sources are assumed to already be sRGB.
        opts = opts
            .set("input_profile", "srgb")
            .set("output_profile", "srgb");
    }
    VipsImage::thumbnail_buffer_with_opts(input, width, opts)
        .map_err(|e| vips_error("decode and resize", e))
}

/// The same geometry and color steps as [`thumbnail`], at the source's depth.
fn full_depth(input: &[u8], options: &ImageOptions) -> Result<VipsImage, Error> {
    let image = VipsImage::new_from_buffer(input, "fail_on=error")
        .and_then(|image| image.autorot())
        .map_err(|e| vips_error("decode", e))?;
    let image = match &options.resize {
        Some(resize) => resize_full_depth(image, resize)?,
        None => image,
    };
    if options.color != ColorIntent::Srgb {
        return Ok(image);
    }
    image
        .icc_transform_with_opts(
            "srgb",
            VOption::new()
                .set("input_profile", "srgb")
                .set("embedded", true)
                .set("depth", 16),
        )
        .map_err(|e| vips_error("convert to sRGB", e))
}

fn resize_full_depth(image: VipsImage, resize: &Resize) -> Result<VipsImage, Error> {
    let (iw, ih) = (image.get_width() as f64, image.get_height() as f64);
    let tw = resize.width.map_or(f64::INFINITY, f64::from);
    let th = resize.height.map_or(f64::INFINITY, f64::from);
    let failed = |e| vips_error("resize", e);
    if resize.fit == Fit::Fill {
        return image
            .resize_with_opts(tw / iw, VOption::new().set("vscale", th / ih))
            .map_err(failed);
    }
    let scale = if resize.fit == Fit::Cover {
        (tw / iw).max(th / ih)
    } else {
        (tw / iw).min(th / ih)
    };
    let scale = if resize.enlarge {
        scale
    } else {
        scale.min(1.0)
    };
    let image = if scale == 1.0 {
        image
    } else {
        image.resize(scale).map_err(failed)?
    };
    if resize.fit != Fit::Cover {
        return Ok(image);
    }
    let (w, h) = (image.get_width(), image.get_height());
    let (cw, ch) = ((tw as i32).min(w), (th as i32).min(h));
    image
        .extract_area((w - cw) / 2, (h - ch) / 2, cw, ch)
        .map_err(failed)
}

#[cfg(test)]
mod tests;
