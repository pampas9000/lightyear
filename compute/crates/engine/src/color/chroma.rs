use compute_types::ChromaSubsampling;
use image::DynamicImage;

/// Analyzes image content to determine whether YUV 4:4:4 or YUV 4:2:0 is optimal.
/// Images with sharp high-contrast saturated edges (UI elements, text, digital art)
/// will suffer from chroma bleeding in 4:2:0 and therefore select Yuv444.
/// Natural continuous gradient photographs select Yuv420.
pub fn detect_optimal_chroma_subsampling(img: &DynamicImage) -> ChromaSubsampling {
    let width = img.width();
    let height = img.height();

    if width < 2 || height < 2 {
        return ChromaSubsampling::Yuv420;
    }

    let rgba = img.to_rgba8();
    let step_x = (width / 64).max(1);
    let step_y = (height / 64).max(1);

    let mut sharp_chroma_edges = 0usize;
    let mut total_samples = 0usize;

    #[inline]
    fn rgb_to_cb_cr(r: f32, g: f32, b: f32) -> (f32, f32) {
        let y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
        let cb = (b - y) * 0.5389; // 0.5 / (1.0 - 0.0722)
        let cr = (r - y) * 0.6350; // 0.5 / (1.0 - 0.2126)
        (cb, cr)
    }

    let max_x = width - 1;
    let max_y = height - 1;

    for y in (0..max_y).step_by(step_y as usize) {
        for x in (0..max_x).step_by(step_x as usize) {
            total_samples += 1;

            let p0 = rgba.get_pixel(x, y);
            let px = rgba.get_pixel(x + 1, y);
            let py = rgba.get_pixel(x, y + 1);

            let (cb0, cr0) = rgb_to_cb_cr(
                p0[0] as f32 / 255.0,
                p0[1] as f32 / 255.0,
                p0[2] as f32 / 255.0,
            );
            let (cbx, crx) = rgb_to_cb_cr(
                px[0] as f32 / 255.0,
                px[1] as f32 / 255.0,
                px[2] as f32 / 255.0,
            );
            let (cby, cry) = rgb_to_cb_cr(
                py[0] as f32 / 255.0,
                py[1] as f32 / 255.0,
                py[2] as f32 / 255.0,
            );

            let d_cb_x = (cbx - cb0).abs();
            let d_cr_x = (crx - cr0).abs();
            let d_cb_y = (cby - cb0).abs();
            let d_cr_y = (cry - cr0).abs();

            let max_chroma_diff = d_cb_x.max(d_cr_x).max(d_cb_y).max(d_cr_y);

            // A chroma difference of > 0.18 represents a sharp, high-contrast color transition
            if max_chroma_diff > 0.18 {
                sharp_chroma_edges += 1;
            }
        }
    }

    if total_samples == 0 {
        return ChromaSubsampling::Yuv420;
    }

    let edge_ratio = sharp_chroma_edges as f32 / total_samples as f32;
    if edge_ratio > 0.008 {
        ChromaSubsampling::Yuv444
    } else {
        ChromaSubsampling::Yuv420
    }
}

// ---------------------------------------------------------------------------
// HLG Dynamic System Gamma (Opto-Optical Transfer Function / OOTF)
// ---------------------------------------------------------------------------
