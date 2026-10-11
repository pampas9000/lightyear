use compute_engine::edit::ImageEditOp;
use compute_types::{ChromaSubsampling, ColorEncoding, Format, TransferCharacteristics};
use std::sync::Once;
use wasm_bindgen::prelude::*;

static INIT: Once = Once::new();

fn init_panic_hook() {
    INIT.call_once(|| {
        console_error_panic_hook::set_once();
    });
}

/// Parses the optional `ColorEncoding` JSON once so both the edit pass and the
/// output pass agree on the source transfer characteristics.
fn parse_color_json(color_json: &Option<String>) -> Option<ColorEncoding> {
    color_json
        .as_ref()
        .and_then(|json| serde_json::from_str(json).ok())
}

/// Applies the edit operations in linear light, linearizing with the *source*
/// transfer function rather than assuming sRGB.
///
/// Resize and blur are linear operations, so running them on encoded values
/// distorts highlights and darkens antialiased edges; the server has always
/// linearized with the real transfer and the browser sandbox must match it.
fn apply_edits(
    img: &mut image::DynamicImage,
    ops: &[ImageEditOp],
    color: &Option<ColorEncoding>,
) -> Result<(), JsValue> {
    let transfer = color
        .as_ref()
        .map(ColorEncoding::resolved_transfer)
        .unwrap_or(TransferCharacteristics::Srgb);

    compute_engine::edit::apply_pipeline_with_transfer(img, ops, transfer)
        .map_err(|e| JsValue::from_str(&format!("Failed to apply image pipeline: {}", e)))
}

fn parse_target_format(format: &str) -> Result<Format, JsValue> {
    match format.to_lowercase().as_str() {
        "jxl" => Ok(Format::Jxl),
        "avif" => Ok(Format::Avif),
        "webp" => Ok(Format::Webp),
        "heic" => Ok(Format::Heic),
        "jfif" | "jpg" | "jpeg" => Ok(Format::Jpeg),
        "png" => Ok(Format::Png),
        _ => Err(JsValue::from_str(&format!(
            "Unsupported format: {}",
            format
        ))),
    }
}

fn parse_edit_ops(ops_json: &str) -> Result<Vec<ImageEditOp>, JsValue> {
    serde_json::from_str(ops_json)
        .map_err(|e| JsValue::from_str(&format!("Invalid operations JSON: {}", e)))
}

#[wasm_bindgen]
pub fn browser_convert(data: &[u8], format: &str, quality: u8) -> Result<Vec<u8>, JsValue> {
    browser_convert_advanced(data, format, quality, None)
}

/// Applies the optional color-managed output pipeline (tone mapping and dithering).
///
/// Pixels are never rewritten between primaries here; the browser-side encoders
/// cannot emit an ICC profile, so wide-gamut sources are carried untagged (the
/// UI warns about this).
fn apply_requested_color(img: &mut image::DynamicImage, color: &Option<ColorEncoding>) {
    if let Some(color) = color {
        compute_engine::apply_output_color_pipeline(
            img,
            color,
            compute_engine::OutputColorSpec {
                source_transfer: color.resolved_transfer(),
                target_transfer: TransferCharacteristics::Srgb,
                bit_depth: color.nominal_bit_depth,
            },
        );
    }
}

/// Advanced image conversion in WASM with professional color pipeline:
/// supports color primaries conversion, transfer characteristics dynamic linearization,
/// Blue Noise spatial dithering, tone mapping, and raw 16-bit preservation.
#[wasm_bindgen]
pub fn browser_convert_advanced(
    data: &[u8],
    format: &str,
    quality: u8,
    color_json: Option<String>,
) -> Result<Vec<u8>, JsValue> {
    init_panic_hook();
    let target_format = parse_target_format(format)?;

    let mut img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to load image: {}", e)))?;

    apply_requested_color(&mut img, &parse_color_json(&color_json));

    match compute_engine::encode(&img, target_format, quality) {
        Ok(result) => Ok(result),
        Err(e) => Err(JsValue::from_str(&format!("Encoding failed: {}", e))),
    }
}

/// Decodes an image into raw RGBA 8-bit pixels or 16-bit container pixels with width/height metadata.
#[wasm_bindgen]
pub fn browser_decode_raw_pixels(data: &[u8]) -> Result<JsValue, JsValue> {
    init_panic_hook();
    let img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to decode image: {}", e)))?;

    let width = img.width();
    let height = img.height();
    let is_16bit = compute_engine::is_16bit_image(&img);

    let optimal_chroma = match compute_engine::detect_optimal_chroma_subsampling(&img) {
        ChromaSubsampling::Yuv444 => "444",
        ChromaSubsampling::Yuv422 => "422",
        ChromaSubsampling::Yuv420 => "420",
        _ => "444",
    };

    let rgba_bytes: Vec<u8> = if is_16bit {
        let rgba16 = img.to_rgba16();
        bytemuck::cast_slice::<u16, u8>(rgba16.as_raw()).to_vec()
    } else {
        img.to_rgba8().into_raw()
    };

    build_pixel_response(width, height, is_16bit, Some(optimal_chroma), &rgba_bytes)
}

/// Builds a JS object exposing `pixels` as a real `Uint8Array` (has `.buffer` for zero-copy transfer).
fn build_pixel_response(
    width: u32,
    height: u32,
    is_16bit: bool,
    optimal_chroma: Option<&str>,
    rgba_bytes: &[u8],
) -> Result<JsValue, JsValue> {
    let obj = js_sys::Object::new();

    js_sys::Reflect::set(&obj, &JsValue::from_str("width"), &JsValue::from(width))
        .map_err(|_| JsValue::from_str("Failed to set width"))?;
    js_sys::Reflect::set(&obj, &JsValue::from_str("height"), &JsValue::from(height))
        .map_err(|_| JsValue::from_str("Failed to set height"))?;
    js_sys::Reflect::set(
        &obj,
        &JsValue::from_str("is_16bit"),
        &JsValue::from(is_16bit),
    )
    .map_err(|_| JsValue::from_str("Failed to set is_16bit"))?;

    if let Some(chroma) = optimal_chroma {
        js_sys::Reflect::set(
            &obj,
            &JsValue::from_str("optimal_chroma"),
            &JsValue::from_str(chroma),
        )
        .map_err(|_| JsValue::from_str("Failed to set optimal_chroma"))?;
    }

    let pixels = js_sys::Uint8Array::from(rgba_bytes);
    js_sys::Reflect::set(&obj, &JsValue::from_str("pixels"), &pixels)
        .map_err(|_| JsValue::from_str("Failed to set pixels"))?;

    Ok(obj.into())
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
    color_json: Option<String>,
) -> Result<Vec<u8>, JsValue> {
    init_panic_hook();
    let parsed_ops = parse_edit_ops(ops_json)?;

    // 2. Load image from raw bytes
    let mut img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to load image: {}", e)))?;

    // 3. Apply the edit pipeline in linear light, using the source transfer
    let color = parse_color_json(&color_json);
    apply_edits(&mut img, &parsed_ops, &color)?;

    // 4. Apply optional color-managed output pipeline
    apply_requested_color(&mut img, &color);

    let target_format = parse_target_format(format)?;

    match compute_engine::encode(&img, target_format, quality) {
        Ok(result) => Ok(result),
        Err(e) => Err(JsValue::from_str(&format!("Encoding failed: {}", e))),
    }
}

/// Applies image editing pipeline and exports raw RGBA bytes with dimensions,
/// specifically designed for feeding into external WebAssembly encoders like @jsquash/jxl.
#[wasm_bindgen]
pub fn browser_edit_to_raw_rgba(
    data: &[u8],
    ops_json: &str,
    color_json: Option<String>,
) -> Result<JsValue, JsValue> {
    init_panic_hook();
    let parsed_ops = parse_edit_ops(ops_json)?;

    let mut img = compute_engine::load_from_memory(data)
        .map_err(|e| JsValue::from_str(&format!("Failed to load image: {}", e)))?;

    let color = parse_color_json(&color_json);
    apply_edits(&mut img, &parsed_ops, &color)?;

    apply_requested_color(&mut img, &color);

    let width = img.width();
    let height = img.height();
    let rgba8 = img.to_rgba8().into_raw();

    build_pixel_response(width, height, false, None, &rgba8)
}
