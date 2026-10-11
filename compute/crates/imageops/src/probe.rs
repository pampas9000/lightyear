//! Detects HDR transfer functions that libvips does not carry through.
//!
//! libvips reads HEIF/AVIF and PNG pixels without their CICP description, so a
//! PQ or HLG source would be written back tagged as sRGB. These containers
//! carry the transfer in a fixed byte layout, which is all that is checked.

/// CICP transfer characteristics: SMPTE ST 2084 (PQ) and ARIB STD-B67 (HLG).
const HDR_TRANSFERS: [u16; 2] = [16, 18];

pub fn has_hdr_transfer(input: &[u8], loader: &str) -> bool {
    let transfer = if loader.starts_with("heifload") {
        // ISOBMFF `colr` box of type `nclx`: primaries u16, transfer u16, …
        find_after(input, b"colrnclx").and_then(|rest| read_u16(rest, 2))
    } else if loader.starts_with("pngload") {
        // PNG `cICP` chunk: primaries u8, transfer u8, matrix u8, range u8.
        find_after(input, b"cICP").and_then(|rest| rest.get(1).map(|&t| t as u16))
    } else {
        None
    };
    transfer.is_some_and(|t| HDR_TRANSFERS.contains(&t))
}

fn find_after<'a>(haystack: &'a [u8], needle: &[u8]) -> Option<&'a [u8]> {
    haystack
        .windows(needle.len())
        .position(|w| w == needle)
        .map(|i| &haystack[i + needle.len()..])
}

fn read_u16(bytes: &[u8], offset: usize) -> Option<u16> {
    let b = bytes.get(offset..offset + 2)?;
    Some(u16::from_be_bytes([b[0], b[1]]))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_nclx_and_cicp_transfers() {
        let nclx = |t: u16| {
            [
                b"....colrnclx".as_slice(),
                &[0, 9],
                &t.to_be_bytes(),
                &[0, 9, 0x80],
            ]
            .concat()
        };
        assert!(has_hdr_transfer(&nclx(16), "heifload_buffer"));
        assert!(has_hdr_transfer(&nclx(18), "heifload_buffer"));
        assert!(!has_hdr_transfer(&nclx(13), "heifload_buffer"));
        // The same bytes mean nothing to other loaders.
        assert!(!has_hdr_transfer(&nclx(16), "jpegload_buffer"));

        let cicp = |t: u8| [b"\0\0\0\x04cICP".as_slice(), &[9, t, 0, 1]].concat();
        assert!(has_hdr_transfer(&cicp(16), "pngload_buffer"));
        assert!(!has_hdr_transfer(&cicp(13), "pngload_buffer"));
        assert!(!has_hdr_transfer(b"cICP", "pngload_buffer"));
    }
}
