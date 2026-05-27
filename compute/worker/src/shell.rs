use std::ffi::OsString;
use std::path::Path;
use anyhow::{anyhow, Result, bail};
use compute_types::{Format, Params, LibavifParams, LibjxlParams, YuvFormat};

/// Builds the argument list for the `avifenc` CLI command.
pub fn build_avifenc_args(input: &Path, output: &Path, params: &LibavifParams) -> Vec<OsString> {
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
    if let Some(ref adv) = params.advanced {
        if let Some(sh) = adv.sharpness {
            args.push(OsString::from("-a"));
            args.push(OsString::from(format!("c:sharpness={}", sh)));
        }
        if let Some(c_sh) = adv.color_sharpness {
            args.push(OsString::from("-a"));
            args.push(OsString::from(format!("c:sharpness={}", c_sh)));
        }
        if let Some(a_sh) = adv.alpha_sharpness {
            args.push(OsString::from("-a"));
            args.push(OsString::from(format!("a:sharpness={}", a_sh)));
        }
    }
    args.push(input.as_os_str().to_os_string());
    args.push(output.as_os_str().to_os_string());
    args
}

/// Builds the argument list for the `cjxl` CLI command.
pub fn build_cjxl_args(
    input: &Path,
    output: &Path,
    params: &LibjxlParams,
    input_format: Option<Format>,
) -> Vec<OsString> {
    let mut args = Vec::new();

    // Check if the input is a JPEG file based on input_format or fallback extension
    let is_jpeg = input_format == Some(Format::Jpeg) || {
        input.extension()
            .map(|ext| ext.to_string_lossy().to_lowercase())
            .map(|ext| ext == "jpg" || ext == "jpeg")
            .unwrap_or(false)
    };

    // Determine if we should perform lossless reconstruction for JPEG
    let should_do_jpeg_reconstruction = is_jpeg && (
        params.lossless.unwrap_or(false) || params.jpeg_reconstruction.unwrap_or(false)
    );

    if should_do_jpeg_reconstruction {
        // For JPEG lossless reconstruction, do NOT pass --distance or --quality.
        // cjxl automatically performs lossless Brunsli transcoding when input is JPEG and distance/quality are omitted.
    } else if params.lossless.unwrap_or(false) {
        // For non-JPEG inputs in lossless mode, enforce --distance 0 (pixel-level modular lossless)
        args.push(OsString::from("--distance"));
        args.push(OsString::from("0"));
    } else {
        // Standard lossy mode
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
    args.push(input.as_os_str().to_os_string());
    args.push(output.as_os_str().to_os_string());
    args
}

/// Executes external command-line tools (or portable fallbacks) to perform local transcoding.
pub async fn run_transcode(
    input_path: &Path,
    output_path: &Path,
    target_format: Format,
    params_val: &Option<Params>,
    input_format: Option<Format>,
) -> Result<()> {
    match target_format {
        Format::Avif => {
            let avif_params = if let Some(params) = params_val {
                params.parse_libavif_params()
                    .map_err(|e| anyhow!("Failed to parse LibavifParams: {}", e))?
            } else {
                LibavifParams::default()
            };

            let args = build_avifenc_args(input_path, output_path, &avif_params);
            
            // Execute avifenc CLI
            let mut cmd = tokio::process::Command::new("avifenc");
            cmd.args(&args);
            
            println!("[Shell] Running command: avifenc {:?}", args);
            match cmd.output().await {
                Ok(output) => {
                    if !output.status.success() {
                        let stderr = String::from_utf8_lossy(&output.stderr);
                        let truncated_stderr = truncate_str(&stderr, 4096);
                        bail!("avifenc failed: {}", truncated_stderr.trim());
                    }
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    bail!("avifenc binary not found in PATH");
                }
                Err(e) => {
                    bail!("Failed to execute avifenc: {}", e);
                }
            }
        }
        Format::Jxl => {
            let jxl_params = if let Some(params) = params_val {
                params.parse_libjxl_params()
                    .map_err(|e| anyhow!("Failed to parse LibjxlParams: {}", e))?
            } else {
                LibjxlParams::default()
            };

            let args = build_cjxl_args(input_path, output_path, &jxl_params, input_format);

            // Execute cjxl CLI
            let mut cmd = tokio::process::Command::new("cjxl");
            cmd.args(&args);

            println!("[Shell] Running command: cjxl {:?}", args);
            match cmd.output().await {
                Ok(output) => {
                    if !output.status.success() {
                        let stderr = String::from_utf8_lossy(&output.stderr);
                        let truncated_stderr = truncate_str(&stderr, 4096);
                        bail!("cjxl failed: {}", truncated_stderr.trim());
                    }
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    bail!("cjxl binary not found in PATH");
                }
                Err(e) => {
                    bail!("Failed to execute cjxl: {}", e);
                }
            }
        }
        Format::Webp | Format::Jpeg | Format::Png => {
            // Fall back to pure Rust memory-based transcoding
            println!("[Shell] Falling back to pure Rust memory transcoding for format {:?}", target_format);
            let input_bytes = tokio::fs::read(input_path).await
                .map_err(|e| anyhow!("Failed to read input file: {}", e))?;
            
            let output_bytes = compute_engine::process(&input_bytes, target_format, None)
                .map_err(|e| anyhow!("compute_engine failed: {}", e))?;
            
            tokio::fs::write(output_path, output_bytes).await
                .map_err(|e| anyhow!("Failed to write output file: {}", e))?;
        }
        _ => {
            bail!("Format {:?} currently not supported via worker transcoding", target_format);
        }
    }

    Ok(())
}

pub async fn ffmpeg_cli_execute(_job: &compute_types::Job) -> Result<Vec<u8>> {
    bail!("ffmpeg_cli_execute fallback is not implemented.")
}


fn truncate_str(s: &str, max_len: usize) -> &str {
    if s.len() <= max_len {
        s
    } else {
        // Truncate safely at char boundary
        let mut index = max_len;
        while !s.is_char_boundary(index) {
            index -= 1;
        }
        &s[..index]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::LibavifAdvancedParams;
    use std::path::Path;

    #[test]
    fn test_build_avifenc_args() {
        let input = Path::new("input.png");
        let output = Path::new("output.avif");
        let params = LibavifParams {
            quality: Some(85),
            alpha_quality: Some(90),
            speed: Some(6),
            jobs: Some(4),
            sharp_yuv: Some(true),
            yuv: Some(YuvFormat::Yuv420),
            advanced: Some(LibavifAdvancedParams {
                sharpness: Some(3),
                color_sharpness: Some(2),
                alpha_sharpness: Some(0),
            }),
        };

        let args = build_avifenc_args(input, output, &params);
        
        assert!(args.contains(&OsString::from("--qcolor")));
        assert!(args.contains(&OsString::from("85")));
        assert!(args.contains(&OsString::from("--qalpha")));
        assert!(args.contains(&OsString::from("90")));
        assert!(args.contains(&OsString::from("--speed")));
        assert!(args.contains(&OsString::from("6")));
        assert!(args.contains(&OsString::from("--jobs")));
        assert!(args.contains(&OsString::from("4")));
        assert!(args.contains(&OsString::from("--sharpyuv")));
        assert!(args.contains(&OsString::from("--yuv")));
        assert!(args.contains(&OsString::from("420")));
        
        // Advanced flags
        let adv_count = args.iter().filter(|x| *x == "-a").count();
        assert_eq!(adv_count, 3);
        assert!(args.contains(&OsString::from("c:sharpness=3")));
        assert!(args.contains(&OsString::from("c:sharpness=2")));
        assert!(args.contains(&OsString::from("a:sharpness=0")));

        // Trailing input and output
        assert_eq!(args.last().unwrap(), "output.avif");
        assert_eq!(&args[args.len() - 2], "input.png");
    }

    #[test]
    fn test_build_cjxl_args() {
        let input = Path::new("input.png");
        let output = Path::new("output.jxl");
        let params = LibjxlParams {
            distance: Some(1.0),
            quality: None,
            effort: Some(7),
            progressive: Some(true),
            ..Default::default()
        };

        let args = build_cjxl_args(input, output, &params, None);

        assert!(args.contains(&OsString::from("--distance")));
        assert!(args.contains(&OsString::from("1")));
        assert!(!args.contains(&OsString::from("--quality")));
        assert!(args.contains(&OsString::from("--effort")));
        assert!(args.contains(&OsString::from("7")));
        assert!(args.contains(&OsString::from("--progressive")));

        assert_eq!(args.last().unwrap(), "output.jxl");
        assert_eq!(&args[args.len() - 2], "input.png");
    }

    #[test]
    fn test_build_cjxl_args_jpeg_lossless() {
        let input = Path::new("input.jpg");
        let output = Path::new("output.jxl");
        let params = LibjxlParams {
            lossless: Some(true),
            effort: Some(7),
            ..Default::default()
        };

        let args = build_cjxl_args(input, output, &params, None);

        // JPEG lossless reconstruction should omit --distance and --quality entirely
        assert!(!args.contains(&OsString::from("--distance")));
        assert!(!args.contains(&OsString::from("--quality")));
        assert!(args.contains(&OsString::from("--effort")));

        assert_eq!(args.last().unwrap(), "output.jxl");
        assert_eq!(&args[args.len() - 2], "input.jpg");
    }

    #[test]
    fn test_build_cjxl_args_png_lossless() {
        let input = Path::new("input.png");
        let output = Path::new("output.jxl");
        let params = LibjxlParams {
            lossless: Some(true),
            effort: Some(7),
            ..Default::default()
        };

        let args = build_cjxl_args(input, output, &params, None);

        // PNG lossless should enforce --distance 0
        assert!(args.contains(&OsString::from("--distance")));
        assert!(args.contains(&OsString::from("0")));
        assert!(!args.contains(&OsString::from("--quality")));

        assert_eq!(args.last().unwrap(), "output.jxl");
        assert_eq!(&args[args.len() - 2], "input.png");
    }

    #[test]
    fn test_build_cjxl_args_jpeg_reconstruction_in_lossy_mode() {
        let input = Path::new("input.jpeg");
        let output = Path::new("output.jxl");
        let params = LibjxlParams {
            lossless: Some(false),
            jpeg_reconstruction: Some(true),
            distance: Some(1.5),
            effort: Some(7),
            ..Default::default()
        };

        let args = build_cjxl_args(input, output, &params, None);

        // Since jpeg_reconstruction is true and input is JPEG, it should omit --distance and --quality
        assert!(!args.contains(&OsString::from("--distance")));
        assert!(!args.contains(&OsString::from("--quality")));
        assert!(args.contains(&OsString::from("--effort")));
    }

    #[test]
    fn test_build_cjxl_args_jpeg_mime_without_extension() {
        let input = Path::new("input.tmp");
        let output = Path::new("output.jxl");
        let params = LibjxlParams {
            lossless: Some(true),
            effort: Some(7),
            ..Default::default()
        };

        let args = build_cjxl_args(input, output, &params, Some(Format::Jpeg));

        // JPEG lossless reconstruction should be triggered based on Format even without extension
        assert!(!args.contains(&OsString::from("--distance")));
        assert!(!args.contains(&OsString::from("--quality")));
        assert!(args.contains(&OsString::from("--effort")));
    }
}
