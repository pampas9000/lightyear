//! Source metadata probing for containers the engine reads directly.
//!
//! Three things are needed before pixels can be interpreted correctly:
//!
//! 1. **EXIF orientation** — phone photos are frequently stored sideways with
//!    the rotation recorded only in EXIF. Dropping that tag without applying the
//!    rotation (which is what happens when a decoder does not understand it)
//!    ships visibly rotated images.
//! 2. **CICP / `nclx` matrix coefficients** — AVIF and HEIC store YUV planes
//!    whose matrix is declared in a `colr` box. `avifdec`/`heif-dec` hand the
//!    planes back unchanged, so the matrix has to come from the file rather than
//!    from a hard-coded default.
//! 3. **Alpha presence** — the planar (Y4M) decode path has no alpha channel, so
//!    an alpha-bearing source has to be recognised *before* choosing that path.
//!
//! All readers work on raw container bytes and never touch pixel data.

use compute_types::MatrixCoefficients;

/// MIAF auxiliary type declaring a full-resolution alpha plane.
const MIAF_ALPHA_AUX_URN: &[u8] = b"urn:mpeg:mpegB:cicp:systems:auxiliary:alpha";

/// Legacy HEVC auxiliary type used by older Apple HEIC writers (`auxid 1` is alpha).
const HEVC_ALPHA_AUX_URN: &[u8] = b"urn:mpeg:hevc:2015:auxid:1";

/// CICP colour description as carried by an ISOBMFF `colr`/`nclx` box.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Nclx {
    pub primaries: u16,
    pub transfer: u16,
    pub matrix: u16,
    pub full_range: bool,
}

impl Nclx {
    /// Maps the CICP matrix code onto the engine's coefficients.
    ///
    /// Returns `None` for unspecified/unknown codes. Identity is GBR and is
    /// handled explicitly by the Y4M decoder.
    pub fn matrix_coefficients(self) -> Option<MatrixCoefficients> {
        match self.matrix {
            0 => Some(MatrixCoefficients::Identity),
            1 => Some(MatrixCoefficients::Bt709),
            6 => Some(MatrixCoefficients::Bt601),
            9 => Some(MatrixCoefficients::Bt2020Ncl),
            _ => None,
        }
    }
}

/// Reads the EXIF orientation (1..=8) from a JPEG, PNG, WebP or HEIF/AVIF buffer.
///
/// Container parsing (including the ISOBMFF `iinf`/`iloc` item location walk
/// that phone HEIC/AVIF files need) is delegated to `kamadak-exif`; the tag
/// itself is returned as-is so callers can normalize it themselves.
pub fn extract_exif_orientation(data: &[u8]) -> Option<u8> {
    let exif = exif::Reader::new()
        .read_from_container(&mut std::io::Cursor::new(data))
        .ok()?;
    let orientation = exif.get_field(exif::Tag::Orientation, exif::In::PRIMARY)?;
    u8::try_from(orientation.value.get_uint(0)?)
        .ok()
        .filter(|value| (1..=8).contains(value))
}

/// Reads the `colr`/`nclx` box from an AVIF or HEIC buffer.
pub fn probe_nclx(data: &[u8]) -> Option<Nclx> {
    if data.len() < 8 || (&data[4..8] != b"ftyp" && &data[4..8] != b"meta") {
        return None;
    }
    find_nclx(data, 0)
}

/// True when an AVIF/HEIC buffer declares a full-resolution alpha auxiliary plane.
///
/// The planar decode path hands planes to `read_y4m_to_image`, and Y4M has no
/// alpha channel, so an alpha-bearing source decoded that way loses its
/// transparency silently. Callers use this to pick a container that can carry it.
pub fn probe_alpha_channel(data: &[u8]) -> bool {
    if data.len() < 8 || (&data[4..8] != b"ftyp" && &data[4..8] != b"meta") {
        return false;
    }
    let Some(payload) = find_box_payload(data, b"auxC", 0) else {
        return false;
    };
    // `auxC` is a full box: one version byte and three flag bytes precede the
    // NUL-terminated `aux_type` URN.
    let Some(payload) = payload.get(4..) else {
        return false;
    };
    let end = payload
        .iter()
        .position(|&b| b == 0)
        .unwrap_or(payload.len());
    let aux_type = &payload[..end];
    aux_type == MIAF_ALPHA_AUX_URN || aux_type == HEVC_ALPHA_AUX_URN
}

// ---------------------------------------------------------------------------
// ISOBMFF box walking
// ---------------------------------------------------------------------------

/// Boxes that only contain other boxes; their payload needs no version/flags
/// prefix skipped before descending.
fn is_plain_container(box_type: &[u8]) -> bool {
    matches!(
        box_type,
        b"iprp" | b"ipco" | b"moov" | b"trak" | b"mdia" | b"minf" | b"stbl"
    )
}

/// Boxes that carry a full-box (version + flags) header before their children.
fn is_full_container(box_type: &[u8]) -> bool {
    matches!(box_type, b"meta")
}

/// Recursively searches for the first `colr` box carrying an `nclx` description.
fn find_nclx(data: &[u8], depth: u8) -> Option<Nclx> {
    if depth > 6 {
        return None;
    }
    let mut offset = 0;
    while offset + 8 <= data.len() {
        let size = u32::from_be_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]) as usize;
        let box_type = &data[offset + 4..offset + 8];

        // A zero size means "extends to end of file"; 1 means a 64-bit size.
        let (header, size) = match size {
            0 => (8, data.len() - offset),
            1 if offset + 16 <= data.len() => {
                let large = u64::from_be_bytes([
                    data[offset + 8],
                    data[offset + 9],
                    data[offset + 10],
                    data[offset + 11],
                    data[offset + 12],
                    data[offset + 13],
                    data[offset + 14],
                    data[offset + 15],
                ]) as usize;
                (16, large)
            }
            _ => (8, size),
        };
        if size < header || offset + size > data.len() {
            return None;
        }

        let payload = &data[offset + header..offset + size];

        if box_type == b"colr" {
            if let Some(nclx) = parse_colr(payload) {
                return Some(nclx);
            }
        } else if is_plain_container(box_type) {
            if let Some(found) = find_nclx(payload, depth + 1) {
                return Some(found);
            }
        } else if is_full_container(box_type) && payload.len() >= 4 {
            if let Some(found) = find_nclx(&payload[4..], depth + 1) {
                return Some(found);
            }
        }

        offset += size;
    }
    None
}

/// Parses a `colr` payload, returning the `nclx` fields when present.
fn parse_colr(payload: &[u8]) -> Option<Nclx> {
    if payload.len() < 4 || &payload[..4] != b"nclx" {
        return None;
    }
    let field = |at: usize| -> Option<u16> {
        let raw = payload.get(at..at + 2)?;
        Some(u16::from_be_bytes([raw[0], raw[1]]))
    };
    Some(Nclx {
        primaries: field(4)?,
        transfer: field(6)?,
        matrix: field(8)?,
        full_range: *payload.get(10)? & 0x80 != 0,
    })
}

/// Finds the payload of the first box of the given type, walking containers.
fn find_box_payload<'a>(data: &'a [u8], wanted: &[u8], depth: u8) -> Option<&'a [u8]> {
    if depth > 6 {
        return None;
    }
    let mut offset = 0;
    while offset + 8 <= data.len() {
        let size = u32::from_be_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]) as usize;
        let box_type = &data[offset + 4..offset + 8];

        let (header, size) = match size {
            0 => (8, data.len() - offset),
            1 if offset + 16 <= data.len() => {
                let large = u64::from_be_bytes([
                    data[offset + 8],
                    data[offset + 9],
                    data[offset + 10],
                    data[offset + 11],
                    data[offset + 12],
                    data[offset + 13],
                    data[offset + 14],
                    data[offset + 15],
                ]) as usize;
                (16, large)
            }
            _ => (8, size),
        };
        if size < header || offset + size > data.len() {
            return None;
        }

        let payload = &data[offset + header..offset + size];

        if box_type == wanted {
            return Some(payload);
        }
        if is_plain_container(box_type) {
            if let Some(found) = find_box_payload(payload, wanted, depth + 1) {
                return Some(found);
            }
        } else if is_full_container(box_type) && payload.len() >= 4 {
            if let Some(found) = find_box_payload(&payload[4..], wanted, depth + 1) {
                return Some(found);
            }
        }

        offset += size;
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Minimal little-endian TIFF header with a single orientation entry.
    fn tiff_with_orientation(value: u16) -> Vec<u8> {
        let mut tiff = Vec::new();
        tiff.extend_from_slice(b"II");
        tiff.extend_from_slice(&42u16.to_le_bytes());
        tiff.extend_from_slice(&8u32.to_le_bytes()); // IFD0 at offset 8
        tiff.extend_from_slice(&1u16.to_le_bytes()); // one entry
        tiff.extend_from_slice(&0x0112u16.to_le_bytes()); // orientation tag
        tiff.extend_from_slice(&3u16.to_le_bytes()); // SHORT
        tiff.extend_from_slice(&1u32.to_le_bytes()); // count
        tiff.extend_from_slice(&value.to_le_bytes());
        tiff.extend_from_slice(&0u16.to_le_bytes()); // inline padding
        tiff.extend_from_slice(&0u32.to_le_bytes()); // next IFD
        tiff
    }

    fn jpeg_with_exif(tiff: &[u8]) -> Vec<u8> {
        let mut payload = b"Exif\0\0".to_vec();
        payload.extend_from_slice(tiff);

        let mut jpeg = vec![0xFF, 0xD8];
        jpeg.extend_from_slice(&[0xFF, 0xE1]);
        jpeg.extend_from_slice(&((payload.len() + 2) as u16).to_be_bytes());
        jpeg.extend_from_slice(&payload);
        jpeg.extend_from_slice(&[0xFF, 0xDA, 0x00, 0x02]); // SOS
        jpeg
    }

    fn png_with_exif(tiff: &[u8]) -> Vec<u8> {
        let mut png = b"\x89PNG\r\n\x1a\n".to_vec();
        png.extend_from_slice(&(tiff.len() as u32).to_be_bytes());
        png.extend_from_slice(b"eXIf");
        png.extend_from_slice(tiff);
        png.extend_from_slice(&0u32.to_be_bytes()); // CRC placeholder
        png
    }

    fn webp_with_exif(tiff: &[u8]) -> Vec<u8> {
        let pad = tiff.len() % 2;
        let mut webp = b"RIFF".to_vec();
        // The RIFF size field counts everything after itself.
        webp.extend_from_slice(&((4 + 8 + tiff.len() + pad) as u32).to_le_bytes());
        webp.extend_from_slice(b"WEBP");
        webp.extend_from_slice(b"EXIF");
        webp.extend_from_slice(&(tiff.len() as u32).to_le_bytes());
        webp.extend_from_slice(tiff);
        if pad == 1 {
            webp.push(0);
        }
        webp
    }

    /// Wraps a box body (`type` + payload) with its 4-byte length prefix.
    fn push_box(out: &mut Vec<u8>, body: &[u8]) {
        out.extend_from_slice(&((body.len() + 4) as u32).to_be_bytes());
        out.extend_from_slice(body);
    }

    /// Top-level `ftyp` + `colr` boxes, enough for the nclx/alpha probes.
    fn isobmff_with_colr(primaries: u16, transfer: u16, matrix: u16) -> Vec<u8> {
        let mut out = Vec::new();

        let mut ftyp = b"ftyp".to_vec();
        ftyp.extend_from_slice(b"avif");
        ftyp.extend_from_slice(&0u32.to_be_bytes());
        ftyp.extend_from_slice(b"mif1"); // compatible brand
        push_box(&mut out, &ftyp);

        let mut colr = b"colr".to_vec();
        colr.extend_from_slice(b"nclx");
        colr.extend_from_slice(&primaries.to_be_bytes());
        colr.extend_from_slice(&transfer.to_be_bytes());
        colr.extend_from_slice(&matrix.to_be_bytes());
        colr.push(0x80); // full range
        push_box(&mut out, &colr);

        out
    }

    /// A spec-compliant minimal HEIF file: `ftyp` with a `mif1` compatible
    /// brand, a `meta` box declaring an `Exif` item (`iinf`) and pointing at it
    /// (`iloc`, absolute file offset), then the item data itself.
    ///
    /// This mirrors what phone HEIC/AVIF files do; a bare top-level `Exif` box
    /// is not something real muxers emit.
    fn heif_with_exif(tiff: &[u8]) -> Vec<u8> {
        let mut infe = b"infe".to_vec();
        infe.extend_from_slice(&[2, 0, 0, 0]); // version 2 + flags
        infe.extend_from_slice(&1u16.to_be_bytes()); // item id
        infe.extend_from_slice(&0u16.to_be_bytes()); // protection index
        infe.extend_from_slice(b"Exif");
        infe.extend_from_slice(b"Exif\0"); // item name
        let mut iinf = b"iinf".to_vec();
        iinf.extend_from_slice(&[0, 0, 0, 0]); // version 0 + flags
        iinf.extend_from_slice(&1u16.to_be_bytes()); // entry count
        push_box(&mut iinf, &infe);
        let mut iinf_box = Vec::new();
        push_box(&mut iinf_box, &iinf);

        let mut iloc = b"iloc".to_vec();
        iloc.extend_from_slice(&[0, 0, 0, 0]); // version 0 + flags
        iloc.extend_from_slice(&0x4400u16.to_be_bytes()); // 4-byte offsets/lengths
        iloc.extend_from_slice(&1u16.to_be_bytes()); // item count
        iloc.extend_from_slice(&1u16.to_be_bytes()); // item id
        iloc.extend_from_slice(&0u16.to_be_bytes()); // data reference index
        iloc.extend_from_slice(&1u16.to_be_bytes()); // extent count
        let offset_field = 4 + iloc.len(); // patched below, size field included
        iloc.extend_from_slice(&0u32.to_be_bytes()); // extent offset (patched)
        iloc.extend_from_slice(&((4 + tiff.len()) as u32).to_be_bytes()); // extent length
        let mut iloc_box = Vec::new();
        push_box(&mut iloc_box, &iloc);

        let mut meta = b"meta".to_vec();
        meta.extend_from_slice(&[0, 0, 0, 0]); // version 0 + flags
        meta.extend_from_slice(&iinf_box);
        meta.extend_from_slice(&iloc_box);

        let mut ftyp = b"ftyp".to_vec();
        ftyp.extend_from_slice(b"heic");
        ftyp.extend_from_slice(&0u32.to_be_bytes());
        ftyp.extend_from_slice(b"mif1"); // compatible brand

        let mut out = Vec::new();
        push_box(&mut out, &ftyp);
        let meta_box_offset = out.len();
        push_box(&mut out, &meta);

        // The item data follows the header, at an absolute file offset.
        let item_offset = out.len() as u32;
        let patch_at = meta_box_offset + 12 + iinf_box.len() + offset_field;
        out[patch_at..patch_at + 4].copy_from_slice(&item_offset.to_be_bytes());

        out.extend_from_slice(&0u32.to_be_bytes()); // TIFF header offset
        out.extend_from_slice(tiff);
        out
    }

    #[test]
    fn reads_jpeg_orientation() {
        assert_eq!(
            extract_exif_orientation(&jpeg_with_exif(&tiff_with_orientation(6))),
            Some(6)
        );
    }

    #[test]
    fn reads_png_orientation() {
        assert_eq!(
            extract_exif_orientation(&png_with_exif(&tiff_with_orientation(3))),
            Some(3)
        );
    }

    #[test]
    fn reads_webp_orientation() {
        assert_eq!(
            extract_exif_orientation(&webp_with_exif(&tiff_with_orientation(8))),
            Some(8)
        );
    }

    #[test]
    fn reads_heif_orientation() {
        let data = heif_with_exif(&tiff_with_orientation(6));
        assert_eq!(extract_exif_orientation(&data), Some(6));
    }

    #[test]
    fn reads_real_jpeg_fixture_orientation() {
        let fixture =
            std::path::Path::new("../../../scripts/fixtures/external/webkit_iceland_p3.jpg");
        if fixture.exists() {
            let bytes = std::fs::read(fixture).unwrap();
            assert_eq!(extract_exif_orientation(&bytes), Some(1));
        }
    }

    #[test]
    fn ignores_upright_and_invalid_orientation() {
        assert_eq!(
            extract_exif_orientation(&jpeg_with_exif(&tiff_with_orientation(1))),
            Some(1)
        );
        assert_eq!(
            extract_exif_orientation(&jpeg_with_exif(&tiff_with_orientation(99))),
            None
        );
        assert_eq!(extract_exif_orientation(b"not an image"), None);
        assert_eq!(extract_exif_orientation(&[0xFF, 0xD8]), None);
    }

    #[test]
    fn reads_nclx_matrix() {
        let data = isobmff_with_colr(9, 16, 9);
        let nclx = probe_nclx(&data).expect("nclx should be found");
        assert_eq!(nclx.primaries, 9);
        assert_eq!(nclx.transfer, 16);
        assert_eq!(nclx.matrix, 9);
        assert!(nclx.full_range);
        assert_eq!(
            nclx.matrix_coefficients(),
            Some(MatrixCoefficients::Bt2020Ncl)
        );
    }

    #[test]
    fn ignores_unknown_matrix_and_non_isobmff() {
        let data = isobmff_with_colr(1, 13, 2);
        assert_eq!(probe_nclx(&data).unwrap().matrix_coefficients(), None);
        assert_eq!(probe_nclx(b"not isobmff"), None);
        assert_eq!(
            probe_nclx(&[0xFF, 0xD8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]),
            None
        );
    }

    fn isobmff_with_auxc(aux_type: &[u8]) -> Vec<u8> {
        let mut out = Vec::new();

        let mut ftyp = b"ftyp".to_vec();
        ftyp.extend_from_slice(b"avif");
        ftyp.extend_from_slice(&0u32.to_be_bytes());
        push_box(&mut out, &ftyp);

        let mut auxc = b"auxC".to_vec();
        auxc.extend_from_slice(&[0, 0, 0, 0]); // version + flags
        auxc.extend_from_slice(aux_type);
        auxc.push(0);
        push_box(&mut out, &auxc);

        out
    }

    #[test]
    fn detects_alpha_auxiliary_plane() {
        assert!(probe_alpha_channel(&isobmff_with_auxc(MIAF_ALPHA_AUX_URN)));
        assert!(probe_alpha_channel(&isobmff_with_auxc(HEVC_ALPHA_AUX_URN)));
        // Depth maps are auxiliary too, but they are not alpha.
        assert!(!probe_alpha_channel(&isobmff_with_auxc(
            b"urn:mpeg:mpegB:cicp:systems:auxiliary:depth"
        )));
        // A plain colour image declares no auxiliary plane at all.
        assert!(!probe_alpha_channel(&isobmff_with_colr(1, 13, 1)));
        assert!(!probe_alpha_channel(b"not isobmff"));
    }

    #[test]
    fn detects_alpha_in_real_avif_fixtures() {
        let alpha =
            std::path::Path::new("../../../scripts/fixtures/quality/avif_8bit_420_alpha.avif");
        if alpha.exists() {
            let bytes = std::fs::read(alpha).unwrap();
            assert!(
                probe_alpha_channel(&bytes),
                "avifenc writes an auxC box for the alpha plane"
            );
        }

        let opaque = std::path::Path::new("../../../scripts/fixtures/quality/avif_8bit_420.avif");
        if opaque.exists() {
            let bytes = std::fs::read(opaque).unwrap();
            assert!(!probe_alpha_channel(&bytes));
        }
    }
}
