//! Ground-truth input detection.
//!
//! Extensions and caller-declared formats are hints, not truth: the worker
//! materialises S3 keys verbatim (which may carry no extension at all), and
//! clients routinely mislabel uploads. Every downstream branch — tool
//! selection, the JPEG→JXL bypass, the native YUV hand-off, strict-mode
//! requirements — keys off the file's actual magic bytes.
//!
//! Resolution order:
//!
//! 1. **magic bytes** (authoritative)
//! 2. the caller-declared [`Format`]
//! 3. the path extension
//!
//! Reading the header never fails a job: unreadable or unrecognised inputs
//! fall through to [`InputKind::Other`], which keeps the old extension-only
//! behaviour for containers this crate does not special-case.

use compute_types::Format;
use std::path::Path;

/// Media kind resolved for an input.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InputKind {
    Jpeg,
    Avif,
    Heic,
    Heif,
    Jxl,
    Webp,
    Png,
    Other,
}

impl InputKind {
    /// Maps a caller-declared format onto a concrete kind.
    fn from_declared(format: Format) -> Option<Self> {
        match format {
            Format::Jpeg => Some(Self::Jpeg),
            Format::Avif => Some(Self::Avif),
            Format::Heic => Some(Self::Heic),
            Format::Heif => Some(Self::Heif),
            Format::Jxl => Some(Self::Jxl),
            Format::Webp => Some(Self::Webp),
            Format::Png => Some(Self::Png),
            _ => None,
        }
    }

    /// Last-resort fallback: the path's extension, case-insensitively.
    fn from_extension(path: &Path) -> Option<Self> {
        match path.extension()?.to_str()?.to_ascii_lowercase().as_str() {
            "jpg" | "jpeg" => Some(Self::Jpeg),
            "avif" => Some(Self::Avif),
            "heic" => Some(Self::Heic),
            "heif" => Some(Self::Heif),
            "jxl" => Some(Self::Jxl),
            "webp" => Some(Self::Webp),
            "png" => Some(Self::Png),
            _ => None,
        }
    }
}

/// What an input actually is, resolved once per job.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct InputProbe {
    pub kind: InputKind,
}

impl InputProbe {
    /// Bytes of the header inspected by [`Self::from_file`].
    ///
    /// 8 KiB comfortably covers the ISOBMFF `ftyp` box including its
    /// compatible-brand list.
    const HEAD_LEN: usize = 8192;

    pub fn is_jpeg(self) -> bool {
        self.kind == InputKind::Jpeg
    }

    pub fn is_avif(self) -> bool {
        self.kind == InputKind::Avif
    }

    /// HEIC and generic HEIF both decode through `heif-dec`.
    pub fn is_heic(self) -> bool {
        matches!(self.kind, InputKind::Heic | InputKind::Heif)
    }

    /// Probes a file on disk, falling back to the declared format and then the
    /// path extension when the header is unreadable or unrecognised.
    pub async fn from_file(path: &Path, declared: Option<Format>) -> Self {
        use tokio::io::AsyncReadExt;

        let mut head = [0u8; Self::HEAD_LEN];
        let read = match tokio::fs::File::open(path).await {
            Ok(mut file) => file.read(&mut head).await.unwrap_or(0),
            Err(_) => 0,
        };

        let probe = Self::from_bytes(&head[..read], declared);
        if probe.kind != InputKind::Other {
            return probe;
        }

        Self {
            kind: InputKind::from_extension(path).unwrap_or(InputKind::Other),
        }
    }

    /// Pure constructor for in-memory callers and tests.
    pub fn from_bytes(head: &[u8], declared: Option<Format>) -> Self {
        let kind = classify(head)
            .or_else(|| declared.and_then(InputKind::from_declared))
            .unwrap_or(InputKind::Other);
        Self { kind }
    }
}

/// Classifies a header.
///
/// Explicitly ordered so the result never depends on the matcher registry
/// order inside `infer` (AVIF and HEIC are both ISOBMFF).
fn classify(head: &[u8]) -> Option<InputKind> {
    use infer::image as matcher;

    if matcher::is_jpeg(head) {
        Some(InputKind::Jpeg)
    } else if matcher::is_avif(head) {
        Some(InputKind::Avif)
    } else if matcher::is_heif(head) {
        // `infer` reports HEIC and generic HEIF through one predicate; both are
        // decoded by `heif-dec`, so they share one kind here too.
        Some(InputKind::Heic)
    } else if matcher::is_jxl(head) {
        Some(InputKind::Jxl)
    } else if matcher::is_webp(head) {
        Some(InputKind::Webp)
    } else if matcher::is_png(head) {
        Some(InputKind::Png)
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const JPEG: &[u8] = &[0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10];
    const PNG: &[u8] = &[0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00];
    const JXL_RAW: &[u8] = &[0xFF, 0x0A, 0x00];
    const JXL_CONTAINER: &[u8] = &[
        0x00, 0x00, 0x00, 0x0C, 0x4A, 0x58, 0x4C, 0x20, 0x0D, 0x0A, 0x87, 0x0A, 0x00,
    ];

    /// Minimal ISOBMFF `ftyp` box: size, "ftyp", major brand, minor version,
    /// then the compatible brands.
    fn ftyp(major: &[u8; 4], compatible: &[&[u8; 4]]) -> Vec<u8> {
        let size = 16 + compatible.len() as u32 * 4;
        let mut box_bytes = Vec::with_capacity(size as usize);
        box_bytes.extend_from_slice(&size.to_be_bytes());
        box_bytes.extend_from_slice(b"ftyp");
        box_bytes.extend_from_slice(major);
        box_bytes.extend_from_slice(&[0, 0, 0, 0]);
        for brand in compatible {
            box_bytes.extend_from_slice(*brand);
        }
        box_bytes
    }

    fn webp() -> Vec<u8> {
        let mut bytes = b"RIFF".to_vec();
        bytes.extend_from_slice(&[0, 0, 0, 0]);
        bytes.extend_from_slice(b"WEBP");
        bytes
    }

    #[test]
    fn classifies_known_headers() {
        let cases: Vec<(&str, Vec<u8>, InputKind)> = vec![
            ("jpeg", JPEG.to_vec(), InputKind::Jpeg),
            ("png", PNG.to_vec(), InputKind::Png),
            ("webp", webp(), InputKind::Webp),
            ("jxl raw codestream", JXL_RAW.to_vec(), InputKind::Jxl),
            ("jxl container", JXL_CONTAINER.to_vec(), InputKind::Jxl),
            ("avif", ftyp(b"avif", &[]), InputKind::Avif),
            ("heic", ftyp(b"heic", &[]), InputKind::Heic),
            (
                "heif with heic compatible brand",
                ftyp(b"mif1", &[b"heic"]),
                InputKind::Heic,
            ),
        ];

        for (name, bytes, expected) in cases {
            assert_eq!(
                classify(&bytes),
                Some(expected),
                "{} header should classify as {:?}",
                name,
                expected
            );
        }
    }

    #[test]
    fn magic_bytes_win_over_a_lying_declaration() {
        // Declared as JPEG, actually a PNG: bytes are authoritative.
        assert_eq!(
            InputProbe::from_bytes(PNG, Some(Format::Jpeg)).kind,
            InputKind::Png
        );
    }

    #[test]
    fn unknown_header_falls_back_to_declared_format() {
        let probe = InputProbe::from_bytes(&[0x10, 0x11, 0x12], Some(Format::Jpeg));
        assert_eq!(probe.kind, InputKind::Jpeg);
        assert!(probe.is_jpeg());
    }

    #[test]
    fn unknown_header_without_declaration_is_other() {
        let probe = InputProbe::from_bytes(&[], None);
        assert_eq!(probe.kind, InputKind::Other);
        assert!(!probe.is_jpeg() && !probe.is_avif() && !probe.is_heic());
    }

    #[tokio::test]
    async fn file_probe_prefers_bytes_then_declared_then_extension() {
        let dir = tempfile::tempdir().unwrap();

        // Extension-less path with JPEG bytes: bytes must win.
        let no_ext = dir.path().join("input.tmp");
        std::fs::write(&no_ext, JPEG).unwrap();
        let probe = InputProbe::from_file(&no_ext, None).await;
        assert!(probe.is_jpeg(), "jpeg bytes must win over a .tmp extension");

        // PNG bytes under a .jpg name: bytes must still win.
        let mislabeled = dir.path().join("photo.jpg");
        std::fs::write(&mislabeled, PNG).unwrap();
        let probe = InputProbe::from_file(&mislabeled, Some(Format::Jpeg)).await;
        assert_eq!(probe.kind, InputKind::Png);

        // Unrecognised bytes: declared format, then extension.
        let garbage = dir.path().join("mystery");
        std::fs::write(&garbage, [0x10, 0x11, 0x12]).unwrap();
        let probe = InputProbe::from_file(&garbage, Some(Format::Heic)).await;
        assert_eq!(probe.kind, InputKind::Heic);

        let garbage_jpg = dir.path().join("mystery.jpg");
        std::fs::write(&garbage_jpg, [0x10, 0x11, 0x12]).unwrap();
        let probe = InputProbe::from_file(&garbage_jpg, None).await;
        assert!(probe.is_jpeg(), "extension is the last-resort fallback");

        // Missing file: no error, just the fallback chain.
        let missing = dir.path().join("missing.png");
        let probe = InputProbe::from_file(&missing, None).await;
        assert_eq!(probe.kind, InputKind::Png, "extension still applies");

        let missing_no_ext = dir.path().join("missing");
        let probe = InputProbe::from_file(&missing_no_ext, None).await;
        assert_eq!(probe.kind, InputKind::Other);
    }
}
