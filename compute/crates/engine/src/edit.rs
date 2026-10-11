use crate::color::{eotf_to_linear, oetf_from_linear};
use anyhow::{Result, bail};
use compute_types::TransferCharacteristics;
use fast_image_resize::{FilterType as FirFilterType, ResizeAlg, ResizeOptions, Resizer};
use image::{DynamicImage, ImageBuffer, Rgba, imageops::FilterType};

pub use compute_types::ImageEditOp;

/// Maps the caller's filter name onto a `fast_image_resize` algorithm.
///
/// `triangle` names the linear (bilinear) kernel; any unknown name keeps the
/// engine's long-standing Lanczos3 default.
fn resize_algorithm(filter: &str) -> ResizeAlg {
    match filter.to_lowercase().as_str() {
        "nearest" => ResizeAlg::Nearest,
        "triangle" => ResizeAlg::Convolution(FirFilterType::Bilinear),
        "catmullrom" | "catmull-rom" => ResizeAlg::Convolution(FirFilterType::CatmullRom),
        "gaussian" => ResizeAlg::Convolution(FirFilterType::Gaussian),
        _ => ResizeAlg::Convolution(FirFilterType::Lanczos3),
    }
}

/// Aspect-preserving "fit inside a constraint box" dimensions.
///
/// `ImageEditOp::Resize` keeps its historical semantics: `width`/`height` are a
/// bounding box, not a forced target size (the web client builds its "max
/// constraint box" on exactly this behaviour). This mirrors what
/// `image::DynamicImage::resize` computed before the resampling kernel moved to
/// `fast_image_resize`.
fn fit_dimensions(current: (u32, u32), bounds: (u32, u32)) -> (u32, u32) {
    if current.0 == 0 || current.1 == 0 {
        return (bounds.0.max(1), bounds.1.max(1));
    }

    let ratio = f64::min(
        f64::from(bounds.0) / f64::from(current.0),
        f64::from(bounds.1) / f64::from(current.1),
    );
    let width = ((f64::from(current.0) * ratio).round() as u32).max(1);
    let height = ((f64::from(current.1) * ratio).round() as u32).max(1);
    (width, height)
}

/// Converts a DynamicImage to a linear-light premultiplied RGBA f32 ImageBuffer using specified transfer characteristics.
pub fn to_premultiplied_linear_f32_with_transfer(
    img: &DynamicImage,
    transfer: TransferCharacteristics,
) -> ImageBuffer<Rgba<f32>, Vec<f32>> {
    let width = img.width();
    let height = img.height();

    match img {
        DynamicImage::ImageRgba16(_)
        | DynamicImage::ImageRgb16(_)
        | DynamicImage::ImageLuma16(_)
        | DynamicImage::ImageLumaA16(_) => {
            let mut out = ImageBuffer::new(width, height);
            for (x, y, pixel) in img.to_rgba16().enumerate_pixels() {
                let alpha = pixel[3] as f32 / 65535.0;
                let r_lin = eotf_to_linear(pixel[0] as f32 / 65535.0, transfer);
                let g_lin = eotf_to_linear(pixel[1] as f32 / 65535.0, transfer);
                let b_lin = eotf_to_linear(pixel[2] as f32 / 65535.0, transfer);
                out.put_pixel(
                    x,
                    y,
                    Rgba([r_lin * alpha, g_lin * alpha, b_lin * alpha, alpha]),
                );
            }
            out
        }
        _ => {
            let rgba8 = img.to_rgba8();
            let mut out = ImageBuffer::new(width, height);
            for (x, y, pixel) in rgba8.enumerate_pixels() {
                let alpha = pixel[3] as f32 / 255.0;
                let r_lin = eotf_to_linear(pixel[0] as f32 / 255.0, transfer);
                let g_lin = eotf_to_linear(pixel[1] as f32 / 255.0, transfer);
                let b_lin = eotf_to_linear(pixel[2] as f32 / 255.0, transfer);
                out.put_pixel(
                    x,
                    y,
                    Rgba([r_lin * alpha, g_lin * alpha, b_lin * alpha, alpha]),
                );
            }
            out
        }
    }
}

/// Converts a DynamicImage to a linear-light premultiplied RGBA f32 ImageBuffer defaulting to sRGB.
pub fn to_premultiplied_linear_f32(img: &DynamicImage) -> ImageBuffer<Rgba<f32>, Vec<f32>> {
    to_premultiplied_linear_f32_with_transfer(img, TransferCharacteristics::Srgb)
}

/// Converts a linear-light premultiplied RGBA f32 ImageBuffer back to DynamicImage using specified transfer characteristics.
pub fn from_premultiplied_linear_f32_with_transfer(
    buf: &ImageBuffer<Rgba<f32>, Vec<f32>>,
    prefer_16bit: bool,
    transfer: TransferCharacteristics,
) -> DynamicImage {
    let width = buf.width();
    let height = buf.height();

    if prefer_16bit {
        let mut out = ImageBuffer::new(width, height);
        for (x, y, pixel) in buf.enumerate_pixels() {
            let alpha = pixel[3];
            if alpha > 1e-6 {
                let r_lin = (pixel[0] / alpha).clamp(0.0, 1.0);
                let g_lin = (pixel[1] / alpha).clamp(0.0, 1.0);
                let b_lin = (pixel[2] / alpha).clamp(0.0, 1.0);
                let r_enc =
                    (oetf_from_linear(r_lin, transfer) * 65535.0 + 0.5).clamp(0.0, 65535.0) as u16;
                let g_enc =
                    (oetf_from_linear(g_lin, transfer) * 65535.0 + 0.5).clamp(0.0, 65535.0) as u16;
                let b_enc =
                    (oetf_from_linear(b_lin, transfer) * 65535.0 + 0.5).clamp(0.0, 65535.0) as u16;
                let a_u16 = (alpha * 65535.0 + 0.5).clamp(0.0, 65535.0) as u16;
                out.put_pixel(x, y, Rgba([r_enc, g_enc, b_enc, a_u16]));
            } else {
                out.put_pixel(x, y, Rgba([0, 0, 0, 0]));
            }
        }
        DynamicImage::ImageRgba16(out)
    } else {
        let mut out = ImageBuffer::new(width, height);
        for (x, y, pixel) in buf.enumerate_pixels() {
            let alpha = pixel[3];
            if alpha > 1e-6 {
                let r_lin = (pixel[0] / alpha).clamp(0.0, 1.0);
                let g_lin = (pixel[1] / alpha).clamp(0.0, 1.0);
                let b_lin = (pixel[2] / alpha).clamp(0.0, 1.0);
                let r_enc =
                    (oetf_from_linear(r_lin, transfer) * 255.0 + 0.5).clamp(0.0, 255.0) as u8;
                let g_enc =
                    (oetf_from_linear(g_lin, transfer) * 255.0 + 0.5).clamp(0.0, 255.0) as u8;
                let b_enc =
                    (oetf_from_linear(b_lin, transfer) * 255.0 + 0.5).clamp(0.0, 255.0) as u8;
                let a_u8 = (alpha * 255.0 + 0.5).clamp(0.0, 255.0) as u8;
                out.put_pixel(x, y, Rgba([r_enc, g_enc, b_enc, a_u8]));
            } else {
                out.put_pixel(x, y, Rgba([0, 0, 0, 0]));
            }
        }
        DynamicImage::ImageRgba8(out)
    }
}

/// Converts a linear-light premultiplied RGBA f32 ImageBuffer back to sRGB DynamicImage.
pub fn from_premultiplied_linear_f32(
    buf: &ImageBuffer<Rgba<f32>, Vec<f32>>,
    prefer_16bit: bool,
) -> DynamicImage {
    from_premultiplied_linear_f32_with_transfer(buf, prefer_16bit, TransferCharacteristics::Srgb)
}

/// Applies a list of image editing operations to a DynamicImage using specified transfer characteristics for linear light calculations.
pub fn apply_pipeline_with_transfer(
    img: &mut DynamicImage,
    ops: &[ImageEditOp],
    transfer: TransferCharacteristics,
) -> Result<()> {
    for op in ops {
        match op {
            ImageEditOp::Resize {
                width,
                height,
                filter,
            } => {
                let algorithm = resize_algorithm(filter);
                let (target_width, target_height) =
                    fit_dimensions((img.width(), img.height()), (*width, *height));

                if (target_width, target_height) == (img.width(), img.height()) {
                    // The constraint box already contains the image.
                } else if algorithm == ResizeAlg::Nearest {
                    // Nearest is a pure pixel copy: the linear-light round trip
                    // would only add transfer-function quantization noise, so
                    // keep the cheap non-linear path.
                    *img = img.resize_exact(target_width, target_height, FilterType::Nearest);
                } else {
                    let is_16bit = crate::is_16bit_image(img);
                    let linear_buf = to_premultiplied_linear_f32_with_transfer(img, transfer);
                    let mut resized =
                        ImageBuffer::<Rgba<f32>, Vec<f32>>::new(target_width, target_height);

                    // The buffer is already premultiplied, so the resizer must
                    // not apply alpha a second time.
                    let options = ResizeOptions::new().resize_alg(algorithm).use_alpha(false);
                    Resizer::new()
                        .resize(&linear_buf, &mut resized, Some(&options))
                        .map_err(|e| {
                            anyhow::anyhow!(
                                "resize to {}x{} failed: {}",
                                target_width,
                                target_height,
                                e
                            )
                        })?;

                    *img =
                        from_premultiplied_linear_f32_with_transfer(&resized, is_16bit, transfer);
                }
            }
            ImageEditOp::Rotate { degree } => {
                *img = match degree {
                    90 => img.rotate90(),
                    180 => img.rotate180(),
                    270 => img.rotate270(),
                    other => bail!(
                        "Unsupported rotation degree: {}. Use 90, 180, or 270",
                        other
                    ),
                };
            }
            ImageEditOp::Flip { direction } => {
                *img = match direction.to_lowercase().as_str() {
                    "horizontal" | "h" => img.fliph(),
                    "vertical" | "v" => img.flipv(),
                    other => bail!(
                        "Unsupported flip direction: {}. Use 'horizontal' or 'vertical'",
                        other
                    ),
                };
            }
            ImageEditOp::Grayscale => {
                *img = img.grayscale();
            }
            ImageEditOp::Blur { sigma } => {
                if *sigma > 0.0 {
                    let is_16bit = crate::is_16bit_image(img);
                    let linear_buf = to_premultiplied_linear_f32_with_transfer(img, transfer);
                    let linear_dyn = DynamicImage::ImageRgba32F(linear_buf);
                    let blurred_linear = linear_dyn.blur(*sigma);
                    if let DynamicImage::ImageRgba32F(blurred_buf) = blurred_linear {
                        *img = from_premultiplied_linear_f32_with_transfer(
                            &blurred_buf,
                            is_16bit,
                            transfer,
                        );
                    } else {
                        *img = img.blur(*sigma);
                    }
                }
            }
            ImageEditOp::Adjust {
                brightness,
                contrast,
            } => {
                let mut temp = img.clone();
                if *brightness != 0 {
                    temp = temp.brighten(*brightness);
                }
                if *contrast != 0.0 {
                    temp = temp.adjust_contrast(*contrast);
                }
                *img = temp;
            }
        }
    }
    Ok(())
}

/// Applies a list of image editing operations to a DynamicImage defaulting to sRGB workspace.
pub fn apply_pipeline(img: &mut DynamicImage, ops: &[ImageEditOp]) -> Result<()> {
    apply_pipeline_with_transfer(img, ops, TransferCharacteristics::Srgb)
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::GenericImageView;

    #[test]
    fn filter_names_map_to_resize_algorithms() {
        assert_eq!(resize_algorithm("nearest"), ResizeAlg::Nearest);
        assert_eq!(
            resize_algorithm("triangle"),
            ResizeAlg::Convolution(FirFilterType::Bilinear)
        );
        assert_eq!(
            resize_algorithm("catmull-rom"),
            ResizeAlg::Convolution(FirFilterType::CatmullRom)
        );
        assert_eq!(
            resize_algorithm("gaussian"),
            ResizeAlg::Convolution(FirFilterType::Gaussian)
        );
        assert_eq!(
            resize_algorithm("lanczos3"),
            ResizeAlg::Convolution(FirFilterType::Lanczos3)
        );
        // Unknown names keep the engine's Lanczos3 default.
        assert_eq!(
            resize_algorithm("bogus"),
            ResizeAlg::Convolution(FirFilterType::Lanczos3)
        );
    }

    #[test]
    fn every_filter_name_resizes_within_the_constraint_box() {
        for filter in [
            "nearest",
            "triangle",
            "catmull-rom",
            "gaussian",
            "lanczos3",
            "bogus",
        ] {
            let source = ImageBuffer::from_fn(16, 16, |x, y| {
                Rgba([(x * 16) as u8, (y * 16) as u8, 128, 255])
            });

            // A non-matching box is fitted aspect-preservingly (4x4), while a
            // matching one is honored exactly.
            for (bounds, expected) in [((8, 4), (4, 4)), ((8, 8), (8, 8))] {
                let mut img = DynamicImage::ImageRgba8(source.clone());
                let ops = vec![ImageEditOp::Resize {
                    width: bounds.0,
                    height: bounds.1,
                    filter: filter.to_string(),
                }];
                apply_pipeline(&mut img, &ops).unwrap();
                assert_eq!(img.dimensions(), expected, "filter {filter}");
            }
        }
    }

    #[test]
    fn resize_inside_the_original_box_is_a_no_op() {
        let mut img = DynamicImage::ImageRgba8(ImageBuffer::from_fn(16, 16, |x, _| {
            Rgba([(x * 16) as u8, 0, 0, 255])
        }));
        let before = img.to_rgba8().into_raw();
        // The box's height already equals the image height, so the fitted
        // target is the source size and no resampling may happen.
        let ops = vec![ImageEditOp::Resize {
            width: 40,
            height: 16,
            filter: "lanczos3".to_string(),
        }];
        apply_pipeline(&mut img, &ops).unwrap();
        assert_eq!(img.dimensions(), (16, 16));
        assert_eq!(img.to_rgba8().into_raw(), before, "no resampling happened");
    }

    #[test]
    fn fit_dimensions_preserves_aspect_ratio_and_never_collapses() {
        assert_eq!(fit_dimensions((16, 16), (8, 4)), (4, 4));
        assert_eq!(fit_dimensions((16, 8), (8, 4)), (8, 4));
        assert_eq!(fit_dimensions((1000, 333), (500, 500)), (500, 167));
        assert_eq!(fit_dimensions((16, 16), (1, 1)), (1, 1));
        // Degenerate sources cannot yield a zero-sized target.
        assert_eq!(fit_dimensions((0, 16), (0, 0)), (1, 1));
    }

    #[test]
    fn resize_keeps_16bit_pixels_16bit() {
        let mut img = DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
            8,
            8,
            Rgba([50_000u16, 20_000, 10_000, 65_535]),
        ));
        let ops = vec![ImageEditOp::Resize {
            width: 4,
            height: 4,
            filter: "lanczos3".to_string(),
        }];
        apply_pipeline(&mut img, &ops).unwrap();
        assert!(matches!(img, DynamicImage::ImageRgba16(_)));
    }

    #[test]
    fn gray16_resize_keeps_fractional_precision() {
        let mut img =
            DynamicImage::ImageLuma16(ImageBuffer::from_pixel(8, 8, image::Luma([40_001u16])));
        apply_pipeline(
            &mut img,
            &[ImageEditOp::Resize {
                width: 4,
                height: 4,
                filter: "lanczos3".to_string(),
            }],
        )
        .unwrap();
        assert!(matches!(img, DynamicImage::ImageRgba16(_)));
        let sample = img.to_rgba16().get_pixel(0, 0)[0];
        assert!(
            sample.abs_diff(40_001) < 90,
            "16-bit resize must not round-trip through 8-bit samples: {sample}"
        );
    }

    #[test]
    fn blur_preserves_all_16bit_pixel_layouts() {
        let images = [
            DynamicImage::ImageRgb16(ImageBuffer::from_pixel(8, 8, image::Rgb([40_001u16; 3]))),
            DynamicImage::ImageRgba16(ImageBuffer::from_pixel(
                8,
                8,
                Rgba([40_001u16, 40_001, 40_001, 30_001]),
            )),
            DynamicImage::ImageLuma16(ImageBuffer::from_pixel(8, 8, image::Luma([40_001u16]))),
            DynamicImage::ImageLumaA16(ImageBuffer::from_pixel(
                8,
                8,
                image::LumaA([40_001u16, 30_001]),
            )),
        ];
        for mut img in images {
            let before = img.to_rgba16().get_pixel(0, 0).0;
            apply_pipeline(&mut img, &[ImageEditOp::Blur { sigma: 1.0 }]).unwrap();
            assert!(crate::is_16bit_image(&img));
            let after = img.to_rgba16().get_pixel(0, 0).0;
            for channel in 0..4 {
                assert!(
                    before[channel].abs_diff(after[channel]) <= 2,
                    "{before:?} -> {after:?}"
                );
            }
        }
    }

    #[test]
    fn premultiplied_alpha_prevents_transparent_pixels_from_bleeding() {
        // Left half opaque red, right half fully transparent black. Filtering
        // straight-alpha pixels would blend the transparent black in and darken
        // the boundary, and applying alpha twice would do the same.
        let mut img = DynamicImage::ImageRgba8(ImageBuffer::from_fn(8, 8, |x, _| {
            if x < 4 {
                Rgba([255, 0, 0, 255])
            } else {
                Rgba([0, 0, 0, 0])
            }
        }));
        let ops = vec![ImageEditOp::Resize {
            width: 4,
            height: 4,
            filter: "lanczos3".to_string(),
        }];
        apply_pipeline(&mut img, &ops).unwrap();

        let DynamicImage::ImageRgba8(out) = img else {
            panic!("expected an 8-bit RGBA output");
        };
        let opaque = out.get_pixel(0, 0).0;
        assert_eq!((opaque[0], opaque[3]), (255, 255), "opaque red survived");
        let boundary = out.get_pixel(1, 0).0;
        assert!(boundary[0] >= 200, "boundary must not darken: {boundary:?}");
        assert!(
            boundary[1] <= 8 && boundary[2] <= 8,
            "no dark fringes: {boundary:?}"
        );
        let transparent = out.get_pixel(3, 0).0;
        assert_eq!(transparent[3], 0, "transparent side stays transparent");
    }
}
