use compute_types::{ChromaSubsampling, ColorRange, MatrixCoefficients};
use image::DynamicImage;
use yuv::{
    YuvChromaSubsampling, YuvPlanarImage, YuvRange, YuvStandardMatrix, gb10_to_rgba10, gbr_to_rgba,
    i010_to_rgba10_bilinear, i210_to_rgba10_bilinear, i410_to_rgba10, yuv420_to_rgba_bilinear,
    yuv422_to_rgba_bilinear, yuv444_to_rgba,
};

/// Maps the engine's matrix enum onto the `yuv` crate's standard matrices.
fn to_standard_matrix(matrix: MatrixCoefficients) -> YuvStandardMatrix {
    match matrix {
        MatrixCoefficients::Bt601 => YuvStandardMatrix::Bt601,
        MatrixCoefficients::Bt2020Ncl => YuvStandardMatrix::Bt2020,
        MatrixCoefficients::Bt709
        | MatrixCoefficients::Identity
        | MatrixCoefficients::Unspecified => YuvStandardMatrix::Bt709,
    }
}

/// Chroma plane dimensions for a given subsampling, matching the `yuv` crate's
/// `div_ceil` convention so plane slices and strides agree with its checks.
fn chroma_dims(width: usize, height: usize, subsampling: YuvChromaSubsampling) -> (usize, usize) {
    let chroma_w = match subsampling {
        YuvChromaSubsampling::Yuv420 | YuvChromaSubsampling::Yuv422 => width.div_ceil(2),
        YuvChromaSubsampling::Yuv444 => width,
    };
    let chroma_h = match subsampling {
        YuvChromaSubsampling::Yuv420 => height.div_ceil(2),
        YuvChromaSubsampling::Yuv422 | YuvChromaSubsampling::Yuv444 => height,
    };
    (chroma_w, chroma_h)
}

/// Expands a 10-bit sample (`0..1023`) into the engine's 16-bit workspace
/// (`0..65535`) so white lands exactly on 65535.
#[inline]
fn scale_10bit_to_16bit(v: u16) -> u16 {
    ((v as u32 * 65535 + 511) / 1023) as u16
}

/// Matrix coefficients (Kr, Kb, Kg) for RGB to YUV matrix computation
#[inline]
pub fn get_matrix_kr_kb(matrix: MatrixCoefficients) -> (f32, f32) {
    match matrix {
        MatrixCoefficients::Bt601 => (0.2990, 0.1140),
        MatrixCoefficients::Bt2020Ncl => (0.2627, 0.0593),
        MatrixCoefficients::Bt709
        | MatrixCoefficients::Identity
        | MatrixCoefficients::Unspecified => (0.2126, 0.0722),
    }
}

/// Serializes DynamicImage into Netpbm PAM format (P7 RGB_ALPHA), supporting both 8-bit and 16-bit.
/// For 16-bit images, MAXVAL is 65535 and samples are written in Big-Endian (Netpbm standard).
pub fn write_pam_rgba(img: &DynamicImage) -> Vec<u8> {
    let width = img.width();
    let height = img.height();

    match img {
        DynamicImage::ImageRgba16(rgba16) => {
            let header = format!(
                "P7\nWIDTH {}\nHEIGHT {}\nDEPTH 4\nMAXVAL 65535\nTUPLTYPE RGB_ALPHA\nENDHDR\n",
                width, height
            );
            let mut out = Vec::with_capacity(header.len() + (width * height * 4 * 2) as usize);
            out.extend_from_slice(header.as_bytes());
            for pixel in rgba16.pixels() {
                out.extend_from_slice(&pixel[0].to_be_bytes());
                out.extend_from_slice(&pixel[1].to_be_bytes());
                out.extend_from_slice(&pixel[2].to_be_bytes());
                out.extend_from_slice(&pixel[3].to_be_bytes());
            }
            out
        }
        DynamicImage::ImageRgb16(rgb16) => {
            let header = format!(
                "P7\nWIDTH {}\nHEIGHT {}\nDEPTH 4\nMAXVAL 65535\nTUPLTYPE RGB_ALPHA\nENDHDR\n",
                width, height
            );
            let mut out = Vec::with_capacity(header.len() + (width * height * 4 * 2) as usize);
            out.extend_from_slice(header.as_bytes());
            for pixel in rgb16.pixels() {
                out.extend_from_slice(&pixel[0].to_be_bytes());
                out.extend_from_slice(&pixel[1].to_be_bytes());
                out.extend_from_slice(&pixel[2].to_be_bytes());
                out.extend_from_slice(&65535u16.to_be_bytes());
            }
            out
        }
        DynamicImage::ImageLuma16(_) | DynamicImage::ImageLumaA16(_) => {
            let header = format!(
                "P7\nWIDTH {}\nHEIGHT {}\nDEPTH 4\nMAXVAL 65535\nTUPLTYPE RGB_ALPHA\nENDHDR\n",
                width, height
            );
            let rgba = img.to_rgba16();
            let mut out = Vec::with_capacity(header.len() + (width * height * 8) as usize);
            out.extend_from_slice(header.as_bytes());
            for pixel in rgba.pixels() {
                for channel in pixel.0 {
                    out.extend_from_slice(&channel.to_be_bytes());
                }
            }
            out
        }
        _ => {
            let header = format!(
                "P7\nWIDTH {}\nHEIGHT {}\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n",
                width, height
            );
            let rgba = img.to_rgba8();
            let raw = rgba.into_raw();
            let mut out = Vec::with_capacity(header.len() + raw.len());
            out.extend_from_slice(header.as_bytes());
            out.extend_from_slice(&raw);
            out
        }
    }
}

/// Converts a DynamicImage to 8-bit Full-Range YUV444 planar buffers (Y, Cb, Cr) in [0..255].
pub fn rgb_to_yuv444_planar(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let total_pixels = width * height;

    let mut y_plane = Vec::with_capacity(total_pixels);
    let mut u_plane = Vec::with_capacity(total_pixels);
    let mut v_plane = Vec::with_capacity(total_pixels);

    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    match img {
        DynamicImage::ImageRgba16(rgba16) => {
            for pixel in rgba16.pixels() {
                let r = pixel[0] as f32 / 65535.0 * 255.0;
                let g = pixel[1] as f32 / 65535.0 * 255.0;
                let b = pixel[2] as f32 / 65535.0 * 255.0;

                let y = kr * r + kg * g + kb * b;
                let cb = 0.5 * (b - y) / (1.0 - kb) + 128.0;
                let cr = 0.5 * (r - y) / (1.0 - kr) + 128.0;

                y_plane.push((y + 0.5).clamp(0.0, 255.0) as u8);
                u_plane.push((cb + 0.5).clamp(0.0, 255.0) as u8);
                v_plane.push((cr + 0.5).clamp(0.0, 255.0) as u8);
            }
        }
        DynamicImage::ImageRgb16(rgb16) => {
            for pixel in rgb16.pixels() {
                let r = pixel[0] as f32 / 65535.0 * 255.0;
                let g = pixel[1] as f32 / 65535.0 * 255.0;
                let b = pixel[2] as f32 / 65535.0 * 255.0;

                let y = kr * r + kg * g + kb * b;
                let cb = 0.5 * (b - y) / (1.0 - kb) + 128.0;
                let cr = 0.5 * (r - y) / (1.0 - kr) + 128.0;

                y_plane.push((y + 0.5).clamp(0.0, 255.0) as u8);
                u_plane.push((cb + 0.5).clamp(0.0, 255.0) as u8);
                v_plane.push((cr + 0.5).clamp(0.0, 255.0) as u8);
            }
        }
        _ => {
            let rgba = img.to_rgba8();
            for pixel in rgba.pixels() {
                let r = pixel[0] as f32;
                let g = pixel[1] as f32;
                let b = pixel[2] as f32;

                let y = kr * r + kg * g + kb * b;
                let cb = 0.5 * (b - y) / (1.0 - kb) + 128.0;
                let cr = 0.5 * (r - y) / (1.0 - kr) + 128.0;

                y_plane.push((y + 0.5).clamp(0.0, 255.0) as u8);
                u_plane.push((cb + 0.5).clamp(0.0, 255.0) as u8);
                v_plane.push((cr + 0.5).clamp(0.0, 255.0) as u8);
            }
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Converts a DynamicImage to 10-bit Full-Range YUV444 planar buffers (Y, Cb, Cr).
///
/// **Crucial Range Invariant**:
/// Sample values are strictly in `[0..1023]`. They are stored as 16-bit words
/// with the 6 most significant bits set to zero. Writing unscaled 16-bit container
/// values (0..65535) into Y4M p10 would overflow the 10-bit range and corrupt the white point.
pub fn rgb_to_yuv444_planar_10bit(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u16>, Vec<u16>, Vec<u16>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let total_pixels = width * height;

    let mut y_plane = Vec::with_capacity(total_pixels);
    let mut u_plane = Vec::with_capacity(total_pixels);
    let mut v_plane = Vec::with_capacity(total_pixels);

    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    for pixel in img.to_rgba16().pixels() {
        let r = pixel[0] as f32 / 65535.0;
        let g = pixel[1] as f32 / 65535.0;
        let b = pixel[2] as f32 / 65535.0;
        let y = kr * r + kg * g + kb * b;
        let cb = (0.5 * (b - y) / (1.0 - kb) + 0.5) * 1023.0;
        let cr = (0.5 * (r - y) / (1.0 - kr) + 0.5) * 1023.0;
        y_plane.push((y * 1023.0 + 0.5).clamp(0.0, 1023.0) as u16);
        u_plane.push((cb + 0.5).clamp(0.0, 1023.0) as u16);
        v_plane.push((cr + 0.5).clamp(0.0, 1023.0) as u16);
    }

    (y_plane, u_plane, v_plane)
}

/// Serializes Y, U, V 8-bit 4:4:4 planar data into standard YUV4MPEG2 byte stream with Full-Range tag.
pub fn write_y4m_c444(width: u32, height: u32, y: &[u8], u: &[u8], v: &[u8]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C444 XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let mut out = Vec::with_capacity(header.len() + y.len() + u.len() + v.len());
    out.extend_from_slice(header.as_bytes());
    out.extend_from_slice(y);
    out.extend_from_slice(u);
    out.extend_from_slice(v);
    out
}

/// Serializes Y, U, V 10-bit 4:4:4 planar data into standard YUV4MPEG2 `C444p10` stream with Full-Range tag.
///
/// Each 10-bit sample is serialized as a 16-bit little-endian word (0..1023).
pub fn write_y4m_c444_p10(width: u32, height: u32, y: &[u16], u: &[u16], v: &[u16]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C444p10 XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let pixel_bytes = (y.len() + u.len() + v.len()) * 2;
    let mut out = Vec::with_capacity(header.len() + pixel_bytes);
    out.extend_from_slice(header.as_bytes());

    for &sample in y {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in u {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in v {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    out
}

/// Converts a DynamicImage to 8-bit Full-Range YUV420 planar buffers (Y, Cb, Cr).
/// Y has dimensions (width * height).
/// Cb and Cr have dimensions ((width + 1) / 2) * ((height + 1) / 2), downsampled using a 2x2 box average.
pub fn rgb_to_yuv420_planar(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    let chroma_w = (width + 1) / 2;
    let chroma_h = (height + 1) / 2;

    let mut y_plane = vec![0u8; width * height];
    let mut u_plane = vec![0u8; chroma_w * chroma_h];
    let mut v_plane = vec![0u8; chroma_w * chroma_h];

    let rgba = img.to_rgba8();

    for y in 0..height {
        for x in 0..width {
            let p = rgba.get_pixel(x as u32, y as u32);
            let r = p[0] as f32;
            let g = p[1] as f32;
            let b = p[2] as f32;
            let y_val = kr * r + kg * g + kb * b;
            y_plane[y * width + x] = (y_val + 0.5).clamp(0.0, 255.0) as u8;
        }
    }

    for cy in 0..chroma_h {
        for cx in 0..chroma_w {
            let mut sum_cb = 0.0;
            let mut sum_cr = 0.0;
            let mut count = 0.0;

            for dy in 0..2 {
                let py = cy * 2 + dy;
                if py >= height {
                    continue;
                }
                for dx in 0..2 {
                    let px = cx * 2 + dx;
                    if px >= width {
                        continue;
                    }
                    let p = rgba.get_pixel(px as u32, py as u32);
                    let r = p[0] as f32;
                    let g = p[1] as f32;
                    let b = p[2] as f32;
                    let y_val = kr * r + kg * g + kb * b;
                    let cb = 0.5 * (b - y_val) / (1.0 - kb) + 128.0;
                    let cr = 0.5 * (r - y_val) / (1.0 - kr) + 128.0;
                    sum_cb += cb;
                    sum_cr += cr;
                    count += 1.0;
                }
            }

            u_plane[cy * chroma_w + cx] = ((sum_cb / count) + 0.5).clamp(0.0, 255.0) as u8;
            v_plane[cy * chroma_w + cx] = ((sum_cr / count) + 0.5).clamp(0.0, 255.0) as u8;
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Converts a DynamicImage to 10-bit Full-Range YUV420 planar buffers (Y, Cb, Cr).
///
/// **Crucial Range Invariant**:
/// Sample values are strictly in `[0..1023]`.
pub fn rgb_to_yuv420_planar_10bit(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u16>, Vec<u16>, Vec<u16>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    let chroma_w = (width + 1) / 2;
    let chroma_h = (height + 1) / 2;

    let mut y_plane = vec![0u16; width * height];
    let mut u_plane = vec![0u16; chroma_w * chroma_h];
    let mut v_plane = vec![0u16; chroma_w * chroma_h];

    let rgba = img.to_rgba16();
    for y in 0..height {
        for x in 0..width {
            let p = rgba.get_pixel(x as u32, y as u32);
            let r = p[0] as f32 / 65535.0;
            let g = p[1] as f32 / 65535.0;
            let b = p[2] as f32 / 65535.0;
            let y_val = (kr * r + kg * g + kb * b) * 1023.0;
            y_plane[y * width + x] = (y_val + 0.5).clamp(0.0, 1023.0) as u16;
        }
    }
    for cy in 0..chroma_h {
        for cx in 0..chroma_w {
            let mut sum_cb = 0.0;
            let mut sum_cr = 0.0;
            let mut count = 0.0;
            for dy in 0..2 {
                let py = cy * 2 + dy;
                if py >= height {
                    continue;
                }
                for dx in 0..2 {
                    let px = cx * 2 + dx;
                    if px >= width {
                        continue;
                    }
                    let p = rgba.get_pixel(px as u32, py as u32);
                    let r = p[0] as f32 / 65535.0;
                    let g = p[1] as f32 / 65535.0;
                    let b = p[2] as f32 / 65535.0;
                    let y_val = kr * r + kg * g + kb * b;
                    sum_cb += (0.5 * (b - y_val) / (1.0 - kb) + 0.5) * 1023.0;
                    sum_cr += (0.5 * (r - y_val) / (1.0 - kr) + 0.5) * 1023.0;
                    count += 1.0;
                }
            }
            u_plane[cy * chroma_w + cx] = ((sum_cb / count) + 0.5).clamp(0.0, 1023.0) as u16;
            v_plane[cy * chroma_w + cx] = ((sum_cr / count) + 0.5).clamp(0.0, 1023.0) as u16;
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Converts a DynamicImage to 8-bit Full-Range YUV422 planar buffers (Y, Cb, Cr).
/// Y has dimensions (width * height).
/// Cb and Cr have dimensions ((width + 1) / 2) * height, downsampled horizontally with 2-pixel box averaging.
pub fn rgb_to_yuv422_planar(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    let chroma_w = (width + 1) / 2;
    let chroma_h = height;

    let mut y_plane = vec![0u8; width * height];
    let mut u_plane = vec![0u8; chroma_w * chroma_h];
    let mut v_plane = vec![0u8; chroma_w * chroma_h];

    let rgba = img.to_rgba8();

    for y in 0..height {
        for x in 0..width {
            let p = rgba.get_pixel(x as u32, y as u32);
            let r = p[0] as f32;
            let g = p[1] as f32;
            let b = p[2] as f32;
            let y_val = kr * r + kg * g + kb * b;
            y_plane[y * width + x] = (y_val + 0.5).clamp(0.0, 255.0) as u8;
        }

        for cx in 0..chroma_w {
            let px0 = cx * 2;
            let p0 = rgba.get_pixel(px0 as u32, y as u32);
            let r0 = p0[0] as f32;
            let g0 = p0[1] as f32;
            let b0 = p0[2] as f32;
            let y0 = kr * r0 + kg * g0 + kb * b0;
            let cb0 = 0.5 * (b0 - y0) / (1.0 - kb) + 128.0;
            let cr0 = 0.5 * (r0 - y0) / (1.0 - kr) + 128.0;

            if px0 + 1 < width {
                let p1 = rgba.get_pixel((px0 + 1) as u32, y as u32);
                let r1 = p1[0] as f32;
                let g1 = p1[1] as f32;
                let b1 = p1[2] as f32;
                let y1 = kr * r1 + kg * g1 + kb * b1;
                let cb1 = 0.5 * (b1 - y1) / (1.0 - kb) + 128.0;
                let cr1 = 0.5 * (r1 - y1) / (1.0 - kr) + 128.0;
                u_plane[y * chroma_w + cx] = ((cb0 + cb1) * 0.5 + 0.5).clamp(0.0, 255.0) as u8;
                v_plane[y * chroma_w + cx] = ((cr0 + cr1) * 0.5 + 0.5).clamp(0.0, 255.0) as u8;
            } else {
                u_plane[y * chroma_w + cx] = (cb0 + 0.5).clamp(0.0, 255.0) as u8;
                v_plane[y * chroma_w + cx] = (cr0 + 0.5).clamp(0.0, 255.0) as u8;
            }
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Converts a DynamicImage to 10-bit Full-Range YUV422 planar buffers (Y, Cb, Cr).
/// Sample values strictly in [0..1023].
pub fn rgb_to_yuv422_planar_10bit(
    img: &DynamicImage,
    matrix: MatrixCoefficients,
) -> (Vec<u16>, Vec<u16>, Vec<u16>) {
    let width = img.width() as usize;
    let height = img.height() as usize;
    let (kr, kb) = get_matrix_kr_kb(matrix);
    let kg = 1.0 - kr - kb;

    let chroma_w = (width + 1) / 2;
    let chroma_h = height;

    let mut y_plane = vec![0u16; width * height];
    let mut u_plane = vec![0u16; chroma_w * chroma_h];
    let mut v_plane = vec![0u16; chroma_w * chroma_h];

    let rgba = img.to_rgba16();
    let get_rgb_f32 = |x: usize, y: usize| -> (f32, f32, f32) {
        let p = rgba.get_pixel(x as u32, y as u32);
        (
            p[0] as f32 / 65535.0,
            p[1] as f32 / 65535.0,
            p[2] as f32 / 65535.0,
        )
    };

    for y in 0..height {
        for x in 0..width {
            let (r, g, b) = get_rgb_f32(x, y);
            let y_val = (kr * r + kg * g + kb * b) * 1023.0;
            y_plane[y * width + x] = (y_val + 0.5).clamp(0.0, 1023.0) as u16;
        }

        for cx in 0..chroma_w {
            let px0 = cx * 2;
            let (r0, g0, b0) = get_rgb_f32(px0, y);
            let y0 = kr * r0 + kg * g0 + kb * b0;
            let cb0 = (0.5 * (b0 - y0) / (1.0 - kb) + 0.5) * 1023.0;
            let cr0 = (0.5 * (r0 - y0) / (1.0 - kr) + 0.5) * 1023.0;

            if px0 + 1 < width {
                let (r1, g1, b1) = get_rgb_f32(px0 + 1, y);
                let y1 = kr * r1 + kg * g1 + kb * b1;
                let cb1 = (0.5 * (b1 - y1) / (1.0 - kb) + 0.5) * 1023.0;
                let cr1 = (0.5 * (r1 - y1) / (1.0 - kr) + 0.5) * 1023.0;
                u_plane[y * chroma_w + cx] = ((cb0 + cb1) * 0.5 + 0.5).clamp(0.0, 1023.0) as u16;
                v_plane[y * chroma_w + cx] = ((cr0 + cr1) * 0.5 + 0.5).clamp(0.0, 1023.0) as u16;
            } else {
                u_plane[y * chroma_w + cx] = (cb0 + 0.5).clamp(0.0, 1023.0) as u16;
                v_plane[y * chroma_w + cx] = (cr0 + 0.5).clamp(0.0, 1023.0) as u16;
            }
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Serializes Y, U, V 8-bit 4:2:0 planar data into standard YUV4MPEG2 byte stream with Full-Range tag.
pub fn write_y4m_c420(width: u32, height: u32, y: &[u8], u: &[u8], v: &[u8]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C420jpeg XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let mut out = Vec::with_capacity(header.len() + y.len() + u.len() + v.len());
    out.extend_from_slice(header.as_bytes());
    out.extend_from_slice(y);
    out.extend_from_slice(u);
    out.extend_from_slice(v);
    out
}

/// Serializes Y, U, V 10-bit 4:2:0 planar data into standard YUV4MPEG2 `C420p10` stream with Full-Range tag.
pub fn write_y4m_c420_p10(width: u32, height: u32, y: &[u16], u: &[u16], v: &[u16]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C420p10 XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let pixel_bytes = (y.len() + u.len() + v.len()) * 2;
    let mut out = Vec::with_capacity(header.len() + pixel_bytes);
    out.extend_from_slice(header.as_bytes());

    for &sample in y {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in u {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in v {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    out
}

/// Serializes Y, U, V 8-bit 4:2:2 planar data into standard YUV4MPEG2 byte stream with Full-Range tag.
pub fn write_y4m_c422(width: u32, height: u32, y: &[u8], u: &[u8], v: &[u8]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C422 XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let mut out = Vec::with_capacity(header.len() + y.len() + u.len() + v.len());
    out.extend_from_slice(header.as_bytes());
    out.extend_from_slice(y);
    out.extend_from_slice(u);
    out.extend_from_slice(v);
    out
}

/// Serializes Y, U, V 10-bit 4:2:2 planar data into standard YUV4MPEG2 `C422p10` stream with Full-Range tag.
pub fn write_y4m_c422_p10(width: u32, height: u32, y: &[u16], u: &[u16], v: &[u16]) -> Vec<u8> {
    let header = format!(
        "YUV4MPEG2 W{} H{} F25:1 Ip A1:1 C422p10 XCOLORRANGE=FULL\nFRAME\n",
        width, height
    );
    let pixel_bytes = (y.len() + u.len() + v.len()) * 2;
    let mut out = Vec::with_capacity(header.len() + pixel_bytes);
    out.extend_from_slice(header.as_bytes());

    for &sample in y {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in u {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    for &sample in v {
        out.extend_from_slice(&sample.to_le_bytes());
    }
    out
}

/// Header fields parsed from a YUV4MPEG2 stream.
struct Y4mHeader {
    width: u32,
    height: u32,
    subsampling: YuvChromaSubsampling,
    is_10bit: bool,
    range: YuvRange,
    payload_offset: usize,
}

/// Parses the Y4M header up to and including the `FRAME` marker line.
fn parse_y4m_header(data: &[u8]) -> anyhow::Result<Y4mHeader> {
    let header_end = data
        .iter()
        .position(|&b| b == b'\n')
        .ok_or_else(|| anyhow::anyhow!("Invalid Y4M: missing header newline"))?;
    let header_str = std::str::from_utf8(&data[..header_end])?;
    if !header_str.starts_with("YUV4MPEG2") {
        anyhow::bail!("Invalid Y4M: missing YUV4MPEG2 signature");
    }

    let mut width = 0u32;
    let mut height = 0u32;
    let mut chroma_specified = false;
    let mut subsampling = YuvChromaSubsampling::Yuv420;
    let mut is_10bit = false;
    let mut range = YuvRange::Full;

    for token in header_str.split_whitespace() {
        if token.starts_with('W') {
            width = token[1..].parse()?;
        } else if token.starts_with('H') {
            height = token[1..].parse()?;
        } else if token.starts_with('C') {
            chroma_specified = true;
            let cs = &token[1..];
            (subsampling, is_10bit) = match cs {
                "420" | "420jpeg" | "420mpeg2" | "420paldv" => {
                    (YuvChromaSubsampling::Yuv420, false)
                }
                "422" => (YuvChromaSubsampling::Yuv422, false),
                "444" => (YuvChromaSubsampling::Yuv444, false),
                "420p10" => (YuvChromaSubsampling::Yuv420, true),
                "422p10" => (YuvChromaSubsampling::Yuv422, true),
                "444p10" => (YuvChromaSubsampling::Yuv444, true),
                _ => anyhow::bail!("Unsupported Y4M chroma layout: {cs}"),
            };
        } else if token.eq_ignore_ascii_case("XCOLORRANGE=LIMITED") {
            range = YuvRange::Limited;
        }
    }

    if !chroma_specified {
        // Y4M specification default is C420jpeg
        subsampling = YuvChromaSubsampling::Yuv420;
    }

    if width == 0 || height == 0 {
        anyhow::bail!("Invalid Y4M dimensions: {}x{}", width, height);
    }

    let frame_start = data[header_end + 1..]
        .windows(5)
        .position(|w| w == b"FRAME")
        .ok_or_else(|| anyhow::anyhow!("Invalid Y4M: missing FRAME marker"))?;
    let after_frame = &data[header_end + 1 + frame_start..];
    let frame_line_end = after_frame
        .iter()
        .position(|&b| b == b'\n')
        .ok_or_else(|| anyhow::anyhow!("Invalid Y4M: missing newline after FRAME marker"))?;

    Ok(Y4mHeader {
        width,
        height,
        subsampling,
        is_10bit,
        range,
        payload_offset: header_end + 1 + frame_start + frame_line_end + 1,
    })
}

/// Layout of a Y4M stream, expressed in the engine's own enums.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Y4mLayout {
    pub width: u32,
    pub height: u32,
    pub chroma: ChromaSubsampling,
    pub depth: u8,
    pub range: ColorRange,
}

/// Probes a Y4M stream's layout without decoding any samples.
///
/// Callers use this to decide whether the stream's planes can be forwarded to
/// an encoder untouched, so no pixel data is read here.
pub fn probe_y4m(data: &[u8]) -> anyhow::Result<Y4mLayout> {
    let header = parse_y4m_header(data)?;
    Ok(Y4mLayout {
        width: header.width,
        height: header.height,
        chroma: match header.subsampling {
            YuvChromaSubsampling::Yuv420 => ChromaSubsampling::Yuv420,
            YuvChromaSubsampling::Yuv422 => ChromaSubsampling::Yuv422,
            YuvChromaSubsampling::Yuv444 => ChromaSubsampling::Yuv444,
        },
        depth: if header.is_10bit { 10 } else { 8 },
        range: match header.range {
            YuvRange::Full => ColorRange::Full,
            YuvRange::Limited => ColorRange::Limited,
        },
    })
}

/// Reads an uncompressed Y4M byte stream and reconstructs an in-memory DynamicImage.
///
/// Supports 8-bit and 10-bit C444/C422/C420, full and limited range, and the
/// BT.601 / BT.709 / BT.2020-NCL / GBR (identity) matrices.
///
/// Chroma is upsampled with the `yuv` crate's bi-linear filter. The previous
/// nearest-neighbour replication let every 2x2 luma block inherit a single
/// chroma sample, which produced visible colour fringes on saturated edges.
pub fn read_y4m_to_image(data: &[u8], matrix: MatrixCoefficients) -> anyhow::Result<DynamicImage> {
    let header = parse_y4m_header(data)?;
    let payload = &data[header.payload_offset..];
    let w = header.width as usize;
    let h = header.height as usize;

    if header.is_10bit {
        read_y4m_10bit(payload, w, h, header.subsampling, header.range, matrix)
    } else {
        read_y4m_8bit(payload, w, h, header.subsampling, header.range, matrix)
    }
}

fn read_y4m_8bit(
    payload: &[u8],
    w: usize,
    h: usize,
    subsampling: YuvChromaSubsampling,
    range: YuvRange,
    matrix: MatrixCoefficients,
) -> anyhow::Result<DynamicImage> {
    let (chroma_w, chroma_h) = chroma_dims(w, h, subsampling);
    let y_samples = w * h;
    let c_samples = chroma_w * chroma_h;
    if payload.len() < y_samples + c_samples * 2 {
        anyhow::bail!("Truncated Y4M 8-bit payload");
    }

    let src = YuvPlanarImage {
        y_plane: &payload[..y_samples],
        y_stride: w as u32,
        u_plane: &payload[y_samples..y_samples + c_samples],
        u_stride: chroma_w as u32,
        v_plane: &payload[y_samples + c_samples..y_samples + c_samples * 2],
        v_stride: chroma_w as u32,
        width: w as u32,
        height: h as u32,
    };

    let mut rgba = vec![0u8; w * h * 4];
    let stride = (w * 4) as u32;
    let result = if matrix == MatrixCoefficients::Identity {
        gbr_to_rgba(&src, &mut rgba, stride, range)
    } else {
        let m = to_standard_matrix(matrix);
        match subsampling {
            YuvChromaSubsampling::Yuv420 => {
                yuv420_to_rgba_bilinear(&src, &mut rgba, stride, range, m)
            }
            YuvChromaSubsampling::Yuv422 => {
                yuv422_to_rgba_bilinear(&src, &mut rgba, stride, range, m)
            }
            YuvChromaSubsampling::Yuv444 => yuv444_to_rgba(&src, &mut rgba, stride, range, m),
        }
    };
    result.map_err(|e| anyhow::anyhow!("YUV to RGB conversion failed: {}", e))?;

    let buf = image::RgbaImage::from_raw(w as u32, h as u32, rgba)
        .ok_or_else(|| anyhow::anyhow!("Failed to build RGBA image from Y4M"))?;
    Ok(DynamicImage::ImageRgba8(buf))
}

fn read_y4m_10bit(
    payload: &[u8],
    w: usize,
    h: usize,
    subsampling: YuvChromaSubsampling,
    range: YuvRange,
    matrix: MatrixCoefficients,
) -> anyhow::Result<DynamicImage> {
    let (chroma_w, chroma_h) = chroma_dims(w, h, subsampling);
    let y_samples = w * h;
    let c_samples = chroma_w * chroma_h;
    if payload.len() < (y_samples + c_samples * 2) * 2 {
        anyhow::bail!("Truncated Y4M 10-bit payload");
    }

    let read_plane = |offset: usize, count: usize| -> Vec<u16> {
        payload[offset..offset + count * 2]
            .chunks_exact(2)
            .map(|c| u16::from_le_bytes([c[0], c[1]]))
            .collect()
    };
    let y_plane = read_plane(0, y_samples);
    let u_plane = read_plane(y_samples * 2, c_samples);
    let v_plane = read_plane((y_samples + c_samples) * 2, c_samples);

    let src = YuvPlanarImage {
        y_plane: &y_plane,
        y_stride: w as u32,
        u_plane: &u_plane,
        u_stride: chroma_w as u32,
        v_plane: &v_plane,
        v_stride: chroma_w as u32,
        width: w as u32,
        height: h as u32,
    };

    let mut rgba10 = vec![0u16; w * h * 4];
    let stride = (w * 4) as u32;
    let result = if matrix == MatrixCoefficients::Identity {
        gb10_to_rgba10(&src, &mut rgba10, stride, range)
    } else {
        let m = to_standard_matrix(matrix);
        match subsampling {
            YuvChromaSubsampling::Yuv420 => {
                i010_to_rgba10_bilinear(&src, &mut rgba10, stride, range, m)
            }
            YuvChromaSubsampling::Yuv422 => {
                i210_to_rgba10_bilinear(&src, &mut rgba10, stride, range, m)
            }
            YuvChromaSubsampling::Yuv444 => i410_to_rgba10(&src, &mut rgba10, stride, range, m),
        }
    };
    result.map_err(|e| anyhow::anyhow!("YUV to RGB conversion failed: {}", e))?;

    let mut buf = image::ImageBuffer::new(w as u32, h as u32);
    for (dst, px) in buf.pixels_mut().zip(rgba10.chunks_exact(4)) {
        *dst = image::Rgba([
            scale_10bit_to_16bit(px[0]),
            scale_10bit_to_16bit(px[1]),
            scale_10bit_to_16bit(px[2]),
            65535,
        ]);
    }
    Ok(DynamicImage::ImageRgba16(buf))
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::MatrixCoefficients;
    use image::{ImageBuffer, Luma, Rgb, Rgba};

    #[test]
    fn ten_bit_planes_preserve_rgb_and_gray_16bit_precision() {
        let rgb = DynamicImage::ImageRgb16(ImageBuffer::from_fn(2, 2, |x, _| {
            Rgb([10_001 + x as u16 * 29, 20_001, 30_001])
        }));
        let rgba = DynamicImage::ImageRgba16(ImageBuffer::from_fn(2, 2, |x, _| {
            Rgba([10_001 + x as u16 * 29, 20_001, 30_001, 65535])
        }));
        let gray = DynamicImage::ImageLuma16(ImageBuffer::from_fn(2, 2, |x, _| {
            Luma([10_001 + x as u16 * 29])
        }));
        let gray_rgb = DynamicImage::ImageRgb16(ImageBuffer::from_fn(2, 2, |x, _| {
            let v = 10_001 + x as u16 * 29;
            Rgb([v, v, v])
        }));
        for convert in [
            rgb_to_yuv444_planar_10bit,
            rgb_to_yuv422_planar_10bit,
            rgb_to_yuv420_planar_10bit,
        ] {
            assert_eq!(
                convert(&rgb, MatrixCoefficients::Bt709),
                convert(&rgba, MatrixCoefficients::Bt709)
            );
            assert_eq!(
                convert(&gray, MatrixCoefficients::Bt709),
                convert(&gray_rgb, MatrixCoefficients::Bt709)
            );
        }
    }

    #[test]
    fn unknown_y4m_layout_must_not_be_decoded_as_eight_bit() {
        for layout in ["444p12", "420p16", "mono", "420unknown", "411"] {
            let y4m = format!("YUV4MPEG2 W1 H1 C{layout}\nFRAME\n");
            assert!(probe_y4m(y4m.as_bytes()).is_err(), "{layout}");
            assert!(
                read_y4m_to_image(y4m.as_bytes(), MatrixCoefficients::Bt709).is_err(),
                "{layout}"
            );
        }
    }

    /// An 8-bit source and its exact 16-bit upcast must produce the same
    /// 10-bit planes. This pins the pre-converted fallback path in
    /// `rgb_to_yuv422_planar_10bit` (regression: it used to call `to_rgba8()`
    /// per pixel inside the sampler closure).
    #[test]
    fn yuv422_10bit_8bit_input_matches_16bit_upcast() {
        let mut img8 = ImageBuffer::new(5, 4);
        for (x, y, px) in img8.enumerate_pixels_mut() {
            let v = ((x * 47 + y * 91) % 256) as u8;
            *px = Rgb([v, 255 - v, v / 2 + 100]);
        }
        // Exact upcast: 8-bit v == 16-bit v*257, because 257 * 255 == 65535.
        let mut img16 = ImageBuffer::new(5, 4);
        for (x, y, px) in img16.enumerate_pixels_mut() {
            let p = img8.get_pixel(x, y).0;
            *px = Rgb([
                u16::from(p[0]) * 257,
                u16::from(p[1]) * 257,
                u16::from(p[2]) * 257,
            ]);
        }
        let img8 = DynamicImage::ImageRgb8(img8);
        let img16 = DynamicImage::ImageRgb16(img16);

        let (y8, cb8, cr8) = rgb_to_yuv422_planar_10bit(&img8, MatrixCoefficients::Bt709);
        let (y16, cb16, cr16) = rgb_to_yuv422_planar_10bit(&img16, MatrixCoefficients::Bt709);

        assert_eq!(y8.len(), y16.len());
        // f32 rounding of the upcast differs in the last ulp; one sample unit
        // of slack keeps this deterministic without weakening the check.
        let close = |a: &[u16], b: &[u16]| {
            assert_eq!(a.len(), b.len());
            for (i, (x, y)) in a.iter().zip(b.iter()).enumerate() {
                assert!(x.abs_diff(*y) <= 1, "plane sample {i} diverged: {x} vs {y}");
            }
        };
        close(&y8, &y16);
        close(&cb8, &cb16);
        close(&cr8, &cr16);
    }
}
