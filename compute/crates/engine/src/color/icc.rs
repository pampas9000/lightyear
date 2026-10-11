//! ICC profile handling for the portable engine.
//!
//! Container parsing and rewriting (JPEG `APP2`, PNG `iCCP`, WebP `ICCP`) is
//! delegated to `img-parts`. Only the ISOBMFF `colr` reader used by AVIF/HEIC
//! and the profile classification helper below are hand-written.

use img_parts::jpeg::{Jpeg, JpegSegment, markers};
use img_parts::png::Png;
use img_parts::{Bytes, DynImage, ImageICC};

// ---------------------------------------------------------------------------
// ICC Profile Extraction
// ---------------------------------------------------------------------------

fn extract_icc_avif(data: &[u8]) -> Option<Vec<u8>> {
    fn find_colr_icc(box_data: &[u8]) -> Option<Vec<u8>> {
        let mut offset = 0;
        while offset + 8 <= box_data.len() {
            let mut box_size = u32::from_be_bytes([
                box_data[offset],
                box_data[offset + 1],
                box_data[offset + 2],
                box_data[offset + 3],
            ]) as usize;
            let box_type = &box_data[offset + 4..offset + 8];
            let header_size = if box_size == 1 {
                if offset + 16 > box_data.len() {
                    break;
                }
                box_size = u64::from_be_bytes([
                    box_data[offset + 8],
                    box_data[offset + 9],
                    box_data[offset + 10],
                    box_data[offset + 11],
                    box_data[offset + 12],
                    box_data[offset + 13],
                    box_data[offset + 14],
                    box_data[offset + 15],
                ]) as usize;
                16
            } else if box_size == 0 {
                box_data.len() - offset
            } else {
                8
            };

            if box_size < header_size || offset + box_size > box_data.len() {
                break;
            }

            let payload = &box_data[offset + header_size..offset + box_size];

            if box_type == b"colr" && payload.len() >= 4 {
                let colour_type = &payload[..4];
                if colour_type == b"prof" || colour_type == b"rICC" {
                    return Some(payload[4..].to_vec());
                }
            }

            if matches!(
                box_type,
                b"meta" | b"iprp" | b"ipco" | b"moov" | b"trak" | b"mdia" | b"minf" | b"stbl"
            ) {
                let inner_payload = if box_type == b"meta" && payload.len() >= 4 {
                    &payload[4..]
                } else {
                    payload
                };
                if let Some(icc) = find_colr_icc(inner_payload) {
                    return Some(icc);
                }
            }

            offset += box_size;
        }
        None
    }

    if data.len() >= 8 {
        find_colr_icc(data)
    } else {
        None
    }
}

/// Extracts raw binary ICC profile from standard image file containers
/// (JPEG `APP2`, PNG `iCCP`, WebP `ICCP`, AVIF/HEIC `colr` box).
pub fn extract_icc_from_memory(data: &[u8]) -> Option<Vec<u8>> {
    // `img-parts` covers JPEG/PNG/WebP but not ISOBMFF, so AVIF/HEIC keep the
    // local `colr` walker.
    if data.len() >= 8 && (&data[4..8] == b"ftyp" || &data[4..8] == b"meta") {
        return extract_icc_avif(data);
    }

    DynImage::from_bytes(Bytes::copy_from_slice(data))
        .ok()
        .flatten()?
        .icc_profile()
        .map(|profile| profile.to_vec())
}

// ---------------------------------------------------------------------------
// ICC Profile Injection
// ---------------------------------------------------------------------------
//
// The engine never rewrites pixels between primaries, so preserving a
// wide-gamut source on formats that can carry a profile means writing the
// profile into the container. AVIF (`--icc`) and JXL (`color_space` /
// `icc_pathname`) are handled by their CLIs; JPEG and PNG are written here.

/// Maximum ICC payload per JPEG `APP2` segment: 65535 minus the 2 length bytes,
/// the 12-byte `ICC_PROFILE\0` identifier and the 2 sequence bytes.
const JPEG_ICC_CHUNK_SIZE: usize = 65_519;

/// Identifier prefix of every ICC `APP2` segment.
const ICC_SEGMENT_PREFIX: &[u8] = b"ICC_PROFILE\0";

/// Injects an ICC profile into a JPEG byte stream as chunked `APP2` segments.
///
/// The segments are written just before `SOS` and replace any profile already
/// in the file. Returns the input unchanged when it is not a JPEG, the profile
/// is empty, or the stream has no `SOS` (neither decodable nor safe to
/// rebuild). `img-parts`' own `set_icc_profile` inserts at a fixed segment
/// index, which would place the profile after `SOS` for streams with fewer than
/// three header segments, so the segments are built explicitly here.
pub fn inject_icc_into_jpeg(jpeg: &[u8], icc: &[u8]) -> Vec<u8> {
    if icc.is_empty() || icc.len() > JPEG_ICC_CHUNK_SIZE * u8::MAX as usize {
        return jpeg.to_vec();
    }

    let Ok(mut parsed) = Jpeg::from_bytes(Bytes::copy_from_slice(jpeg)) else {
        return jpeg.to_vec();
    };

    parsed.segments_mut().retain(|segment| {
        !(segment.marker() == markers::APP2 && segment.contents().starts_with(ICC_SEGMENT_PREFIX))
    });

    // The parser parks the entropy data (including the trailing `EOI`) inside
    // the `SOS` segment, so a stream without one cannot be rebuilt faithfully.
    let Some(insert_at) = parsed
        .segments()
        .iter()
        .position(|segment| segment.marker() == markers::SOS)
    else {
        return jpeg.to_vec();
    };

    let total = icc.len().div_ceil(JPEG_ICC_CHUNK_SIZE) as u8;
    for (index, chunk) in icc.chunks(JPEG_ICC_CHUNK_SIZE).enumerate() {
        let mut contents = Vec::with_capacity(ICC_SEGMENT_PREFIX.len() + 2 + chunk.len());
        contents.extend_from_slice(ICC_SEGMENT_PREFIX);
        contents.push(index as u8 + 1);
        contents.push(total);
        contents.extend_from_slice(chunk);
        parsed.segments_mut().insert(
            insert_at + index,
            JpegSegment::new_with_contents(markers::APP2, Bytes::from(contents)),
        );
    }

    let mut out = Vec::with_capacity(jpeg.len());
    if parsed.encoder().write_to(&mut out).is_err() {
        return jpeg.to_vec();
    }
    out
}

/// Injects an ICC profile into a PNG byte stream as an `iCCP` chunk.
///
/// The chunk is inserted immediately after `IHDR`, which is where the PNG
/// specification requires it, and replaces any profile already in the file.
/// Returns the input unchanged when it is not a complete PNG (IHDR first,
/// IEND present) or the profile is empty.
pub fn inject_icc_into_png(png: &[u8], icc: &[u8]) -> Vec<u8> {
    if icc.is_empty() {
        return png.to_vec();
    }

    let Ok(mut parsed) = Png::from_bytes(Bytes::copy_from_slice(png)) else {
        return png.to_vec();
    };

    let has_ihdr_first = parsed
        .chunks()
        .first()
        .is_some_and(|chunk| chunk.kind() == *b"IHDR");
    if !has_ihdr_first || parsed.chunk_by_type(*b"IEND").is_none() {
        return png.to_vec();
    }

    parsed.set_icc_profile(Some(Bytes::copy_from_slice(icc)));

    let mut out = Vec::with_capacity(png.len());
    if parsed.encoder().write_to(&mut out).is_err() {
        return png.to_vec();
    }
    out
}

// ---------------------------------------------------------------------------
// ICC Profile Color Space Classification
// ---------------------------------------------------------------------------

/// Well-known RGB color spaces that can be identified from the colorant tags of
/// a matrix/TRC ICC profile and expressed with an encoder-native encoding.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IccColorSpace {
    Srgb,
    DisplayP3,
    AdobeRgb,
    ProPhoto,
}

impl IccColorSpace {
    /// Corresponding `cjxl -x color_space=` shorthand.
    pub fn cjxl_shorthand(self) -> &'static str {
        match self {
            IccColorSpace::Srgb => "sRGB",
            IccColorSpace::DisplayP3 => "DisplayP3",
            IccColorSpace::AdobeRgb => "Adobe98",
            IccColorSpace::ProPhoto => "ProPhoto",
        }
    }
}

/// Reads a single `XYZType` tag entry (type + reserved + 3× s15Fixed16 values).
fn read_icc_xyz(data: &[u8], tag_offset: usize) -> Option<[f32; 3]> {
    if tag_offset + 20 > data.len() || &data[tag_offset..tag_offset + 4] != b"XYZ " {
        return None;
    }
    let mut out = [0.0f32; 3];
    for (i, chunk) in data[tag_offset + 8..tag_offset + 20]
        .chunks_exact(4)
        .enumerate()
    {
        let raw = i32::from_be_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]);
        out[i] = raw as f32 / 65536.0;
    }
    Some(out)
}

/// Identifies a well-known RGB color space from the `rXYZ`/`gXYZ`/`bXYZ`
/// colorant tags of a matrix/TRC ICC profile. Returns `None` for profiles
/// without matrix colorants or whose primaries match no recognized space.
pub fn classify_icc_color_space(icc: &[u8]) -> Option<IccColorSpace> {
    if icc.len() < 132 {
        return None;
    }
    let tag_count = u32::from_be_bytes([icc[128], icc[129], icc[130], icc[131]]) as usize;

    let mut r = None;
    let mut g = None;
    let mut b = None;
    for i in 0..tag_count {
        let entry = 132 + i * 12;
        if entry + 12 > icc.len() {
            break;
        }
        let offset = u32::from_be_bytes([
            icc[entry + 4],
            icc[entry + 5],
            icc[entry + 6],
            icc[entry + 7],
        ]) as usize;
        match &icc[entry..entry + 4] {
            b"rXYZ" => r = read_icc_xyz(icc, offset),
            b"gXYZ" => g = read_icc_xyz(icc, offset),
            b"bXYZ" => b = read_icc_xyz(icc, offset),
            _ => {}
        }
    }
    let (r, g, b) = (r?, g?, b?);

    // D50-adapted colorants (PCS XYZ) as found in the ICC spec profiles.
    const SRGB: [[f32; 3]; 3] = [
        [0.436066, 0.222488, 0.013916],
        [0.385147, 0.716873, 0.097076],
        [0.143066, 0.060608, 0.714096],
    ];
    const DISPLAY_P3: [[f32; 3]; 3] = [
        [0.515121, 0.241196, -0.001053],
        [0.291977, 0.692245, 0.041885],
        [0.157104, 0.066574, 0.784073],
    ];
    const ADOBE_RGB: [[f32; 3]; 3] = [
        [0.609741, 0.311111, 0.019470],
        [0.205276, 0.625671, 0.060867],
        [0.149185, 0.063217, 0.744568],
    ];
    const PRO_PHOTO: [[f32; 3]; 3] = [
        [0.797668, 0.288040, 0.0],
        [0.135193, 0.711884, 0.0],
        [0.031357, 0.000092, 0.825195],
    ];

    const TOLERANCE: f32 = 0.0015;
    let close = |a: [f32; 3], b: [f32; 3]| {
        (a[0] - b[0]).abs() <= TOLERANCE
            && (a[1] - b[1]).abs() <= TOLERANCE
            && (a[2] - b[2]).abs() <= TOLERANCE
    };
    let matches =
        |space: [[f32; 3]; 3]| close(r, space[0]) && close(g, space[1]) && close(b, space[2]);

    if matches(SRGB) {
        Some(IccColorSpace::Srgb)
    } else if matches(DISPLAY_P3) {
        Some(IccColorSpace::DisplayP3)
    } else if matches(ADOBE_RGB) {
        Some(IccColorSpace::AdobeRgb)
    } else if matches(PRO_PHOTO) {
        Some(IccColorSpace::ProPhoto)
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::{DynamicImage, ImageBuffer, Rgb};
    use std::io::Cursor;

    /// A real, complete JPEG (entropy + EOI) as the encoder produces it.
    fn real_jpeg() -> Vec<u8> {
        let img = DynamicImage::ImageRgb8(ImageBuffer::from_pixel(8, 8, Rgb([200, 100, 50])));
        let mut out = Cursor::new(Vec::new());
        img.write_to(&mut out, image::ImageFormat::Jpeg).unwrap();
        out.into_inner()
    }

    /// A real, complete PNG (IHDR first, IEND last).
    fn real_png() -> Vec<u8> {
        let img = DynamicImage::ImageRgb8(ImageBuffer::from_pixel(8, 8, Rgb([10, 20, 30])));
        let mut out = Cursor::new(Vec::new());
        img.write_to(&mut out, image::ImageFormat::Png).unwrap();
        out.into_inner()
    }

    fn fake_icc(len: usize) -> Vec<u8> {
        (0..len).map(|i| (i % 251) as u8).collect()
    }

    /// Byte offset of the first marker with the given second byte.
    fn marker_offset(jpeg: &[u8], marker: u8) -> Option<usize> {
        jpeg.windows(2)
            .position(|pair| pair[0] == 0xFF && pair[1] == marker)
    }

    #[test]
    fn jpeg_icc_roundtrip_single_segment() {
        let icc = fake_icc(512);
        let tagged = inject_icc_into_jpeg(&real_jpeg(), &icc);
        assert_eq!(extract_icc_from_memory(&tagged).as_deref(), Some(&icc[..]));
    }

    #[test]
    fn jpeg_icc_roundtrip_multi_segment() {
        // Larger than one APP2 payload, forcing the chunked path.
        let icc = fake_icc(JPEG_ICC_CHUNK_SIZE * 2 + 977);
        let tagged = inject_icc_into_jpeg(&real_jpeg(), &icc);
        assert_eq!(extract_icc_from_memory(&tagged).as_deref(), Some(&icc[..]));
    }

    #[test]
    fn jpeg_profile_is_written_before_sos() {
        let tagged = inject_icc_into_jpeg(&real_jpeg(), &fake_icc(64));
        let icc_at = marker_offset(&tagged, 0xE2).expect("an APP2 segment");
        let sos_at = marker_offset(&tagged, 0xDA).expect("an SOS segment");
        assert!(icc_at < sos_at, "ICC segments must precede SOS");
    }

    #[test]
    fn png_icc_roundtrip() {
        let icc = fake_icc(1024);
        let tagged = inject_icc_into_png(&real_png(), &icc);
        assert_eq!(extract_icc_from_memory(&tagged).as_deref(), Some(&icc[..]));
    }

    #[test]
    fn injection_replaces_a_pre_existing_profile() {
        let first = fake_icc(256);
        let second: Vec<u8> = (0..256).map(|i| 255 - (i % 251) as u8).collect();

        let jpeg = inject_icc_into_jpeg(&inject_icc_into_jpeg(&real_jpeg(), &first), &second);
        assert_eq!(extract_icc_from_memory(&jpeg).as_deref(), Some(&second[..]));

        let png = inject_icc_into_png(&inject_icc_into_png(&real_png(), &first), &second);
        assert_eq!(extract_icc_from_memory(&png).as_deref(), Some(&second[..]));
    }

    #[test]
    fn injection_is_a_no_op_for_foreign_or_empty_input() {
        let icc = fake_icc(64);
        assert_eq!(inject_icc_into_jpeg(b"not a jpeg", &icc), b"not a jpeg");
        assert_eq!(inject_icc_into_png(b"not a png", &icc), b"not a png");
        assert_eq!(inject_icc_into_jpeg(&real_jpeg(), &[]), real_jpeg());
        assert_eq!(inject_icc_into_png(&real_png(), &[]), real_png());
    }

    #[test]
    fn degenerate_streams_are_returned_unchanged() {
        // SOI + EOI only: no SOS segment, so the stream must not be rebuilt.
        let degenerate_jpeg = vec![0xFF, 0xD8, 0xFF, 0xD9];
        assert_eq!(
            inject_icc_into_jpeg(&degenerate_jpeg, &fake_icc(16)),
            degenerate_jpeg
        );

        // PNG signature only: no IHDR/IEND to anchor an iCCP chunk.
        let degenerate_png = b"\x89PNG\r\n\x1a\n".to_vec();
        assert_eq!(
            inject_icc_into_png(&degenerate_png, &fake_icc(16)),
            degenerate_png
        );
    }
}
