use compute_types::{EncodeOptions, Format};
use wasm_bindgen::prelude::*;

#[wasm_bindgen]
pub fn browser_convert(data: &[u8], format: &str, quality: u8) -> Result<Vec<u8>, JsValue> {
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
