use anyhow::{bail, Result};
use image::{imageops::FilterType, DynamicImage};

#[derive(Debug, Clone)]
pub enum ImageEditOp {
    Resize {
        width: u32,
        height: u32,
        filter: String,
    },
    Rotate {
        degree: u32,
    },
    Flip {
        direction: String,
    },
    Grayscale,
    Blur {
        sigma: f32,
    },
    Adjust {
        brightness: i32,
        contrast: f32,
    },
}

/// Applies a list of image editing operations to a DynamicImage.
pub fn apply_pipeline(img: &mut DynamicImage, ops: &[ImageEditOp]) -> Result<()> {
    for op in ops {
        match op {
            ImageEditOp::Resize { width, height, filter } => {
                let filter_type = match filter.to_lowercase().as_str() {
                    "nearest" => FilterType::Nearest,
                    "triangle" => FilterType::Triangle,
                    "catmullrom" | "catmull-rom" => FilterType::CatmullRom,
                    "gaussian" => FilterType::Gaussian,
                    "lanczos3" => FilterType::Lanczos3,
                    _ => FilterType::Lanczos3,
                };
                *img = img.resize(*width, *height, filter_type);
            }
            ImageEditOp::Rotate { degree } => {
                *img = match degree {
                    90 => img.rotate90(),
                    180 => img.rotate180(),
                    270 => img.rotate270(),
                    other => bail!("Unsupported rotation degree: {}. Use 90, 180, or 270", other),
                };
            }
            ImageEditOp::Flip { direction } => {
                *img = match direction.to_lowercase().as_str() {
                    "horizontal" | "h" => img.fliph(),
                    "vertical" | "v" => img.flipv(),
                    other => bail!("Unsupported flip direction: {}. Use 'horizontal' or 'vertical'", other),
                };
            }
            ImageEditOp::Grayscale => {
                *img = img.grayscale();
            }
            ImageEditOp::Blur { sigma } => {
                if *sigma > 0.0 {
                    *img = img.blur(*sigma);
                }
            }
            ImageEditOp::Adjust { brightness, contrast } => {
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
