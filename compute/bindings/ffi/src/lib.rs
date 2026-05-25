use compute_types::Format;
use std::os::raw::{c_char, c_uchar};

// FFI Bindings for Flutter/SwiftUI
// This library provides a C-compatible interface to the core transcoding engine.

/// Returns the version of the transcoder engine.
///
/// The returned string is statically allocated and should not be freed by the caller.
#[unsafe(no_mangle)]
pub extern "C" fn transcoder_version() -> *const c_char {
    const VERSION: &[u8] = concat!(env!("CARGO_PKG_VERSION"), "\0").as_bytes();
    VERSION.as_ptr() as *const c_char
}

/// Transcodes an image from one format to another.
///
/// # Safety
/// - `data` must be a valid pointer to a buffer of `len` bytes.
/// - `out_len` must be a valid pointer to a `size_t` where the result length will be stored.
/// - The caller must free the returned buffer using `transcoder_free_buffer`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn transcoder_process(
    data: *const c_uchar,
    len: usize,
    target_format_code: i32,
    out_len: *mut usize,
) -> *mut c_uchar {
    let input = unsafe { std::slice::from_raw_parts(data, len) };

    // Map i32 to Format enum (Simplified example)
    let target_format = match target_format_code {
        0 => Format::Jpeg,
        1 => Format::Png,
        2 => Format::Webp,
        3 => Format::Avif,
        _ => return std::ptr::null_mut(),
    };

    match compute_engine::process(input, target_format, None) {
        Ok(result) => {
            unsafe { *out_len = result.len() };
            let mut boxed_slice = result.into_boxed_slice();
            let ptr = boxed_slice.as_mut_ptr();
            std::mem::forget(boxed_slice);
            ptr
        }
        Err(_) => std::ptr::null_mut(),
    }
}

/// Frees a buffer allocated by the transcoder.
///
/// # Safety
/// - `ptr` must have been returned by a transcoder function and not yet freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn transcoder_free_buffer(ptr: *mut c_uchar, len: usize) {
    if !ptr.is_null() {
        unsafe {
            drop(Box::from_raw(std::slice::from_raw_parts_mut(ptr, len)));
        }
    }
}
