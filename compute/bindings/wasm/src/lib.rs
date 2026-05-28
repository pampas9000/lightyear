use compute_engine::edit::{apply_pipeline, ImageEditOp};
use compute_types::{EncodeOptions, Format};
use wasm_bindgen::prelude::*;
use std::sync::Once;

static INIT: Once = Once::new();

fn init_panic_hook() {
    INIT.call_once(|| {
        console_error_panic_hook::set_once();
    });
}

#[wasm_bindgen]
pub fn browser_convert(data: &[u8], format: &str, quality: u8) -> Result<Vec<u8>, JsValue> {
    init_panic_hook();
    let target_format = match format.to_lowercase().as_str() {
        "jxl" => Format::Jxl,
        "avif" => Format::Avif,
        "webp" => Format::Webp,
        "heic" => Format::Heic,
        // jfif, jpeg, jpg are all JPEG
        "jfif" | "jpg" | "jpeg" => Format::Jpeg,
        "png" => Format::Png,
        _ => {
            return Err(JsValue::from_str(&format!(
                "Unsupported format: {}",
                format
            )))
        }
    };

    let options = Some(EncodeOptions { quality });

    match compute_engine::process(data, target_format, options) {
        Ok(result) => Ok(result),
        Err(e) => Err(JsValue::from_str(&format!("Encoding failed: {}", e))),
    }
}

#[wasm_bindgen]
pub fn browser_get_metadata(data: &[u8]) -> Result<String, JsValue> {
    init_panic_hook();
    let img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to load image: {}", e)))?;

    let format_detected = match image::guess_format(data) {
        Ok(image::ImageFormat::Png) => "png",
        Ok(image::ImageFormat::Jpeg) => "jpeg",
        Ok(image::ImageFormat::WebP) => "webp",
        Ok(image::ImageFormat::Avif) => "avif",
        Ok(image::ImageFormat::Gif) => "gif",
        _ => "unknown",
    };

    let json = serde_json::json!({
        "width": img.width(),
        "height": img.height(),
        "format": format_detected,
        "size": data.len()
    });

    Ok(json.to_string())
}

#[wasm_bindgen]
pub fn browser_edit_image(
    data: &[u8],
    ops_json: &str,
    format: &str,
    quality: u8,
) -> Result<Vec<u8>, JsValue> {
    init_panic_hook();
    // 1. Parse JSON list of operations
    let ops_val: serde_json::Value = serde_json::from_str(ops_json)
        .map_err(|e| JsValue::from_str(&format!("Invalid operations JSON: {}", e)))?;

    let ops_arr = ops_val.as_array()
        .ok_or_else(|| JsValue::from_str("Operations must be a JSON array"))?;

    let mut parsed_ops = Vec::new();
    for op in ops_arr {
        let op_type = op.get("type").and_then(|v| v.as_str())
            .ok_or_else(|| JsValue::from_str("Operation missing 'type' field"))?;

        let parsed = match op_type {
            "resize" => {
                let width = op.get("width").and_then(|v| v.as_u64())
                    .ok_or_else(|| JsValue::from_str("Resize operation missing 'width'"))? as u32;
                let height = op.get("height").and_then(|v| v.as_u64())
                    .ok_or_else(|| JsValue::from_str("Resize operation missing 'height'"))? as u32;
                let filter = op.get("filter").and_then(|v| v.as_str()).unwrap_or("lanczos3").to_string();
                ImageEditOp::Resize { width, height, filter }
            }
            "rotate" => {
                let degree = op.get("degree").and_then(|v| v.as_u64())
                    .ok_or_else(|| JsValue::from_str("Rotate operation missing 'degree'"))? as u32;
                ImageEditOp::Rotate { degree }
            }
            "flip" => {
                let direction = op.get("direction").and_then(|v| v.as_str())
                    .ok_or_else(|| JsValue::from_str("Flip operation missing 'direction'"))?.to_string();
                ImageEditOp::Flip { direction }
            }
            "grayscale" => ImageEditOp::Grayscale,
            "blur" => {
                let sigma = op.get("sigma").and_then(|v| v.as_f64())
                    .ok_or_else(|| JsValue::from_str("Blur operation missing 'sigma'"))? as f32;
                ImageEditOp::Blur { sigma }
            }
            "adjust" => {
                let brightness = op.get("brightness").and_then(|v| v.as_i64()).unwrap_or(0) as i32;
                let contrast = op.get("contrast").and_then(|v| v.as_f64()).unwrap_or(0.0) as f32;
                ImageEditOp::Adjust { brightness, contrast }
            }
            other => return Err(JsValue::from_str(&format!("Unknown operation type: {}", other))),
        };
        parsed_ops.push(parsed);
    }

    // 2. Load image from raw bytes
    let mut img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to load image: {}", e)))?;

    // 3. Apply edit pipeline
    apply_pipeline(&mut img, &parsed_ops)
        .map_err(|e| JsValue::from_str(&format!("Failed to apply image pipeline: {}", e)))?;

    // 4. Encode image with specific format and quality
    let target_format = match format.to_lowercase().as_str() {
        "jxl" => Format::Jxl,
        "avif" => Format::Avif,
        "webp" => Format::Webp,
        "heic" => Format::Heic,
        "jfif" | "jpg" | "jpeg" => Format::Jpeg,
        "png" => Format::Png,
        _ => return Err(JsValue::from_str(&format!("Unsupported format: {}", format))),
    };

    match compute_engine::encode(&img, target_format, quality) {
        Ok(result) => Ok(result),
        Err(e) => Err(JsValue::from_str(&format!("Encoding failed: {}", e))),
    }
}

