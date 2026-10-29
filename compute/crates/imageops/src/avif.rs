//! AVIF through libavif (aom) rather than libvips `heifsave`.
//!
//! `heifsave` hard-codes aom's intra block copy off, which makes screenshots
//! 13–40% larger at the same quality (docs/engineering-notes/encoder-comparison.md).
//! libvips still prepares the pixels, exactly as its savers would; only the
//! final encode is libavif's, with libavif's defaults.

use crate::{Error, vips_error};
use compute_types::ImageOptions;
use compute_types::image_options::{Chroma, Metadata};
use rs_vips::VipsImage;
use rs_vips::bindings;
use rs_vips::enums::{BandFormat, Interpretation};
use rs_vips::voption::{Setter, VOption};
use std::ffi::{c_char, c_int};

#[repr(C)]
struct Input {
    pixels: *const u8,
    width: u32,
    height: u32,
    row_bytes: u32,
    channels: u32,
    pixel_depth: u32,
    depth: u32,
    yuv444: c_int,
    lossless: c_int,
    quality: c_int,
    speed: c_int,
    threads: c_int,
    icc: *const u8,
    icc_len: usize,
    exif: *mut u8,
    exif_len: usize,
    xmp: *const u8,
    xmp_len: usize,
}

unsafe extern "C" {
    fn ly_avif_encode(
        input: *const Input,
        out: *mut *mut u8,
        out_len: *mut usize,
        err: *mut c_char,
        err_len: usize,
    ) -> c_int;
    fn ly_avif_free(data: *mut u8);
}

/// libavif's default speed (`avifenc -s 6`); its range is 0 (slowest) – 10.
const DEFAULT_SPEED: i32 = 6;

/// Spreads the shared 1–10 effort over libavif's speed 10–0.
fn speed(options: &ImageOptions) -> i32 {
    options
        .effort
        .map_or(DEFAULT_SPEED, |e| 10 - crate::encode::spread(e, 0, 10))
}

pub fn save(image: &VipsImage, options: &ImageOptions, depth: i32) -> Result<Vec<u8>, Error> {
    let image = ready(image)?;
    let channels = image.get_bands() as u32;
    let pixel_depth = if matches!(image.get_format(), Ok(BandFormat::Ushort)) {
        16
    } else {
        8
    };
    let width = image.get_width() as u32;
    let height = image.get_height() as u32;
    let pixels = Pixels::render(&image)?;

    // The profile always follows the pixels (`color` decides which one it is),
    // but one that does not describe RGB, such as a grey source's, is dropped.
    let icc = image
        .get_blob("icc-profile-data")
        .ok()
        .filter(|icc| compatible_profile(&image, icc));
    let keep = options.metadata == Metadata::Keep;
    let mut exif = keep.then(|| image.get_blob("exif-data").ok()).flatten();
    let xmp = keep.then(|| image.get_blob("xmp-data").ok()).flatten();
    let quality = options.quality.map_or(60, i32::from);
    // libvips' rule for an unspecified chroma, kept for every format.
    let yuv444 = match options.chroma {
        Some(Chroma::Yuv444) => true,
        Some(Chroma::Yuv420) => false,
        None => quality >= 90,
    };

    let (icc_ptr, icc_len) = slice_parts(icc.as_deref());
    let (xmp_ptr, xmp_len) = slice_parts(xmp.as_deref());
    let (exif_ptr, exif_len) = match exif.as_mut() {
        Some(e) => (e.as_mut_ptr(), e.len()),
        None => (std::ptr::null_mut(), 0),
    };
    let input = Input {
        pixels: pixels.data,
        width,
        height,
        row_bytes: width * channels * pixel_depth / 8,
        channels,
        pixel_depth,
        depth: depth as u32,
        yuv444: yuv444 as c_int,
        lossless: options.lossless as c_int,
        quality,
        speed: speed(options),
        threads: std::thread::available_parallelism().map_or(1, |n| n.get() as c_int),
        icc: icc_ptr,
        icc_len,
        exif: exif_ptr,
        exif_len,
        xmp: xmp_ptr,
        xmp_len,
    };
    let mut out = std::ptr::null_mut();
    let mut out_len = 0;
    let mut err = [0 as c_char; 512];
    let status =
        unsafe { ly_avif_encode(&input, &mut out, &mut out_len, err.as_mut_ptr(), err.len()) };
    if status != 0 {
        let message = unsafe { std::ffi::CStr::from_ptr(err.as_ptr()) };
        return Err(Error::Failed(format!(
            "libavif {}",
            message.to_string_lossy()
        )));
    }
    let bytes = unsafe { std::slice::from_raw_parts(out, out_len) }.to_vec();
    unsafe { ly_avif_free(out) };
    Ok(bytes)
}

fn slice_parts(data: Option<&[u8]>) -> (*const u8, usize) {
    data.map_or((std::ptr::null(), 0), |d| (d.as_ptr(), d.len()))
}

/// The conversions libvips applies before an RGB + alpha saver such as
/// `heifsave` (`vips__foreign_convert_saveable`): CMYK through its profile,
/// everything else to sRGB or RGB16, at most one alpha, 8 or 16 bits.
fn ready(image: &VipsImage) -> Result<VipsImage, Error> {
    let failed = |e| vips_error("prepare pixels", e);
    let sixteen = matches!(image.get_format(), Ok(BandFormat::Ushort));
    let mut image = image.clone();
    if matches!(image.guess_interpretation(), Ok(Interpretation::Cmyk)) && image.get_bands() >= 4 {
        image = image
            .icc_import_with_opts(
                VOption::new()
                    .set("pcs", rs_vips::enums::PCS::Xyz as i32)
                    .set("embedded", true)
                    .set("input_profile", "cmyk"),
            )
            .map_err(failed)?;
    }
    image = image
        .colourspace(if sixteen {
            Interpretation::Rgb16
        } else {
            Interpretation::Srgb
        })
        .map_err(failed)?;
    if image.get_bands() > 4 {
        image = image
            .extract_band_with_opts(0, VOption::new().set("n", 4))
            .map_err(failed)?;
    }
    let format = match image.get_format() {
        Ok(BandFormat::Uchar | BandFormat::Char) => BandFormat::Uchar,
        _ => BandFormat::Ushort,
    };
    if image.get_format().ok() != Some(format) {
        image = image.cast(format).map_err(failed)?;
    }
    Ok(image)
}

/// The rendered pixels, owned by libvips; this is where decoding errors surface.
struct Pixels {
    data: *const u8,
}

impl Pixels {
    fn render(image: &VipsImage) -> Result<Self, Error> {
        let mut len = 0;
        let data = unsafe { bindings::vips_image_write_to_memory(image.as_mut_ptr(), &mut len) };
        if data.is_null() {
            let detail = rs_vips::Vips::error_buffer().unwrap_or_default();
            rs_vips::Vips::error_clear();
            return Err(Error::Failed(format!("decode: {}", detail.trim())));
        }
        Ok(Self { data: data.cast() })
    }
}

impl Drop for Pixels {
    fn drop(&mut self) {
        unsafe { bindings::g_free(self.data as *mut _) };
    }
}

fn compatible_profile(image: &VipsImage, icc: &[u8]) -> bool {
    unsafe {
        bindings::vips_icc_is_compatible_profile(image.as_mut_ptr(), icc.as_ptr().cast(), icc.len())
            != 0
    }
}
