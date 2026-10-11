use super::color::OutputColor;
use compute_engine::{ImageEditOp, apply_output_color_pipeline};
use compute_types::Params;
use image::DynamicImage;

/// Edits use the same resolved source transfer as the output color pipeline.
pub fn apply_edits(
    img: &mut DynamicImage,
    ops: &[ImageEditOp],
    output: &OutputColor,
) -> anyhow::Result<()> {
    if ops.is_empty() {
        return Ok(());
    }

    compute_engine::edit::apply_pipeline_with_transfer(img, ops, output.spec.source_transfer)?;
    Ok(())
}

/// Applies tone mapping and dithering for the target format.
pub fn apply_color_transforms(
    img: &mut DynamicImage,
    params: &Option<Params>,
    output: &OutputColor,
) {
    let Some(color) = params.as_ref().and_then(|p| p.color.as_ref()) else {
        return;
    };

    apply_output_color_pipeline(img, color, output.spec);
}
