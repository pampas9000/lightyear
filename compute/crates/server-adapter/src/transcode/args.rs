use compute_types::{LibavifParams, LibjxlParams, YuvFormat};
use std::ffi::OsString;
use std::path::Path;

/// Builds common option arguments for avifenc CLI.
pub fn build_avifenc_options(params: &LibavifParams) -> Vec<OsString> {
    let mut args = Vec::new();
    if let Some(q) = params.quality {
        args.push(OsString::from("--qcolor"));
        args.push(OsString::from(q.to_string()));
    }
    if let Some(aq) = params.alpha_quality {
        args.push(OsString::from("--qalpha"));
        args.push(OsString::from(aq.to_string()));
    }
    if let Some(s) = params.speed {
        args.push(OsString::from("--speed"));
        args.push(OsString::from(s.to_string()));
    }
    if let Some(j) = params.jobs {
        args.push(OsString::from("--jobs"));
        args.push(OsString::from(j.to_string()));
    }
    if let Some(true) = params.sharp_yuv {
        args.push(OsString::from("--sharpyuv"));
    }
    if let Some(depth) = params.depth {
        args.push(OsString::from("--depth"));
        args.push(OsString::from(depth.to_string()));
    }
    if let Some(yuv) = params.yuv {
        args.push(OsString::from("--yuv"));
        let yuv_str = match yuv {
            YuvFormat::Yuv444 => "444",
            YuvFormat::Yuv420 => "420",
            YuvFormat::Yuv422 => "422",
            YuvFormat::Auto => "auto",
        };
        args.push(OsString::from(yuv_str));
    }
    // The engine always writes full-range planes (Y4M is tagged
    // XCOLORRANGE=FULL), so the input range is locked here regardless of the
    // requested `color_range`; still images default to full range.
    args.push(OsString::from("-r"));
    args.push(OsString::from("full"));
    if let Some(ref cicp) = params.cicp {
        args.push(OsString::from("--cicp"));
        args.push(OsString::from(cicp.to_arg_string()));
    }
    if let Some(ref icc) = params.icc_path {
        args.push(OsString::from("--icc"));
        args.push(OsString::from(icc));
    }
    if let Some(target_size) = params.target_size {
        args.push(OsString::from("--target-size"));
        args.push(OsString::from(target_size.to_string()));
    }
    if let Some(ref adv) = params.advanced {
        // `sharpness` and `color_sharpness` are the same aom knob
        // (`c:sharpness`); emitting both would let the last one silently win, so
        // resolve them to a single effective value.
        if let Some(sh) = adv.color_sharpness.or(adv.sharpness) {
            args.push(OsString::from("-a"));
            args.push(OsString::from(format!("c:sharpness={}", sh)));
        }
        if let Some(a_sh) = adv.alpha_sharpness {
            args.push(OsString::from("-a"));
            args.push(OsString::from(format!("a:sharpness={}", a_sh)));
        }
    }
    args
}

/// Builds the argument list for `avifenc --stdin output.avif`.
pub fn build_avifenc_stdin_args(output: &Path, params: &LibavifParams) -> Vec<OsString> {
    let mut args = Vec::new();
    args.push(OsString::from("--stdin"));
    args.extend(build_avifenc_options(params));
    args.push(output.as_os_str().to_os_string());
    args
}

/// Builds the argument list for `avifenc input.png output.avif` with alpha support.
pub fn build_avifenc_file_args(
    input: &Path,
    output: &Path,
    params: &LibavifParams,
) -> Vec<OsString> {
    let mut args = Vec::new();
    args.extend(build_avifenc_options(params));
    args.push(input.as_os_str().to_os_string());
    args.push(output.as_os_str().to_os_string());
    args
}

/// Builds common option arguments for heif-enc CLI.
pub fn build_heif_enc_args(
    input: &Path,
    output: &Path,
    quality: u8,
    lossless: bool,
    has_alpha: bool,
    bit_depth: Option<u8>,
) -> Vec<OsString> {
    let mut args = Vec::new();
    if lossless {
        args.push(OsString::from("-L"));
    } else {
        args.push(OsString::from("-q"));
        args.push(OsString::from(quality.to_string()));
    }

    if !has_alpha {
        args.push(OsString::from("--no-alpha"));
    }

    if let Some(b) = bit_depth {
        if b >= 9 && b <= 16 {
            args.push(OsString::from("-b"));
            args.push(OsString::from(b.to_string()));
        }
    }

    args.push(input.as_os_str().to_os_string());
    args.push(OsString::from("-o"));
    args.push(output.as_os_str().to_os_string());
    args
}

///
/// `cwebp` has no stdin input mode, so callers must materialize the source
/// image (typically PNG) to a temporary path first.
pub fn build_cwebp_args(
    input: &Path,
    output: &Path,
    quality: u8,
    lossless: bool,
    method: Option<u8>,
    copy_icc: bool,
) -> Vec<OsString> {
    let mut args = Vec::new();
    args.push(OsString::from("-quiet"));

    if lossless {
        args.push(OsString::from("-lossless"));
    } else {
        args.push(OsString::from("-q"));
        args.push(OsString::from(quality.to_string()));
    }

    if let Some(m) = method {
        args.push(OsString::from("-m"));
        args.push(OsString::from(m.to_string()));
    }

    // `cwebp` has no `-icc_profile` option (it errors out on it); the only way
    // to carry a profile is `-metadata icc`, which copies the ICC **from the
    // input file** — so the caller must inject it into the intermediate first.
    if copy_icc {
        args.push(OsString::from("-metadata"));
        args.push(OsString::from("icc"));
    }

    args.push(input.as_os_str().to_os_string());
    args.push(OsString::from("-o"));
    args.push(output.as_os_str().to_os_string());
    args
}

/// Builds the argument list for the `cjxl` CLI command.
///
/// `is_jpeg` comes from the caller's magic-byte probe rather than the path, so
/// JPEG reconstruction is requested for extension-less or mislabelled inputs.
pub fn build_cjxl_args(
    input: &Path,
    output: &Path,
    params: &LibjxlParams,
    is_jpeg: bool,
) -> Vec<OsString> {
    let mut args = Vec::new();

    let should_do_jpeg_reconstruction = is_jpeg
        && (params.lossless.unwrap_or(false) || params.jpeg_reconstruction.unwrap_or(false));

    if should_do_jpeg_reconstruction {
        // Omit distance and quality for lossless JPEG reconstruction
    } else if params.lossless.unwrap_or(false) {
        args.push(OsString::from("--distance"));
        args.push(OsString::from("0"));
    } else {
        if let Some(d) = params.distance {
            args.push(OsString::from("--distance"));
            args.push(OsString::from(d.to_string()));
        }
        if let Some(q) = params.quality {
            args.push(OsString::from("--quality"));
            args.push(OsString::from(q.to_string()));
        }
    }

    if let Some(e) = params.effort {
        args.push(OsString::from("--effort"));
        args.push(OsString::from(e.to_string()));
    }
    if let Some(true) = params.progressive {
        args.push(OsString::from("--progressive"));
    }

    // Official dec-hints (-x key=value)
    if let Some(ref cs) = params.color_space {
        args.push(OsString::from("-x"));
        args.push(OsString::from(format!("color_space={}", cs)));
    }
    if let Some(ref icc) = params.icc_pathname {
        args.push(OsString::from("-x"));
        args.push(OsString::from(format!("icc_pathname={}", icc)));
    }

    args.push(input.as_os_str().to_os_string());
    args.push(output.as_os_str().to_os_string());
    args
}
