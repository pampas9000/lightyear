use anyhow::Result;
use compute_types::{EncodeOptions, Format};

/** Encode with ffmpeg */
pub fn ffmpeg_encode(
    _data: &[u8],
    _target_format: Format,
    _options: Option<EncodeOptions>,
) -> Result<Vec<u8>> {
    // This is a placeholder for FFI server-side exclusive codes like ffmpeg-next bindings
    // Since this crate will not be compiled into WASM, we can safely use thread-blocking components
    // or heavy C-bindings here.

    unimplemented!("Server-side exclusive C bindings are not implemented yet.");
}
