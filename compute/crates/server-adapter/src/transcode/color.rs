use super::profile::SourceProfile;
use anyhow::{Result, bail};
use compute_engine::{Nclx, OutputColorSpec};
use compute_types::{
    Cicp, ColorEncoding, ColorPrimaries, Format, MatrixCoefficients, Params, ToneMappingOperator,
    TransferCharacteristics,
};
use image::DynamicImage;

/// One decision shared by pixel processing and output metadata.
#[derive(Debug, Clone, Copy)]
pub struct OutputColor {
    pub spec: OutputColorSpec,
    pub cicp: Cicp,
    pub tone_mapped: bool,
}

impl OutputColor {
    pub fn resolve(
        img: &DynamicImage,
        target: Format,
        params: &Option<Params>,
        source: &SourceProfile,
    ) -> Result<Self> {
        let color = params.as_ref().and_then(|p| p.color.as_ref());
        let tone_mapped = color.is_some_and(|c| {
            c.tone_mapping
                .is_some_and(|op| op != ToneMappingOperator::None)
        });
        let source_transfer = source_transfer(params, source.nclx);
        let mut cicp = resolve_output_cicp(params, source.icc.is_some(), source.nclx);
        if tone_mapped {
            if source.nclx.is_none()
                && let Some(icc) = source.icc.as_deref()
            {
                cicp.primaries = match compute_engine::classify_icc_color_space(icc) {
                    Some(compute_engine::IccColorSpace::Srgb) => 1,
                    Some(compute_engine::IccColorSpace::DisplayP3) => 12,
                    _ => bail!(
                        "tone mapping this ICC profile requires color management that is not supported"
                    ),
                };
            }
            // These encoders cannot describe a new wide-gamut SDR profile.
            if !matches!(target, Format::Avif | Format::Jxl) && cicp.primaries != 1 {
                bail!(
                    "tone-mapped wide-gamut output requires AVIF or JXL; {:?} needs an output ICC profile or gamut conversion",
                    target
                );
            }
        }

        let requested_depth = color.map(|c| c.nominal_bit_depth);
        let source_depth = if compute_engine::is_16bit_image(img) {
            16
        } else {
            8
        };
        let high_depth = requested_depth.unwrap_or(source_depth) > 8;
        let bit_depth = match target {
            Format::Avif => {
                let legacy = params
                    .as_ref()
                    .map(Params::parse_libavif_params)
                    .transpose()?;
                requested_depth
                    .or_else(|| legacy.and_then(|p| p.depth.map(|d| d as u8)))
                    .unwrap_or(if source_depth > 8 { 10 } else { 8 })
            }
            Format::Jxl | Format::Png if high_depth => 16,
            Format::Heic if high_depth => 10,
            _ => 8,
        };

        Ok(Self {
            spec: OutputColorSpec {
                source_transfer,
                target_transfer: if tone_mapped {
                    TransferCharacteristics::Srgb
                } else {
                    source_transfer
                },
                bit_depth,
            },
            cicp,
            tone_mapped,
        })
    }

    pub fn icc<'a>(&self, source: &'a SourceProfile) -> Option<&'a [u8]> {
        if self.tone_mapped {
            None
        } else {
            source.icc.as_deref()
        }
    }

    pub fn cjxl_hint(&self) -> Option<&'static str> {
        match (self.cicp.primaries, self.cicp.transfer) {
            (1, 13) => Some("sRGB"),
            (12, 13) => Some("DisplayP3"),
            (9, 13) => Some("RGB_D65_202_Rel_SRG"),
            (9, 16) => Some("Rec2100PQ"),
            (9, 18) => Some("Rec2100HLG"),
            _ => None,
        }
    }
}

/// Explicit CLI metadata cannot override the description of the actual pixels.
pub fn validate_cicp(requested: Option<Cicp>, actual: Cicp) -> Result<()> {
    if let Some(requested) = requested
        && requested != actual
    {
        bail!(
            "engine_params.cicp {} conflicts with output pixels {}; use color parameters to select the pixel encoding",
            requested.to_arg_string(),
            actual.to_arg_string()
        );
    }
    Ok(())
}

pub fn source_transfer(
    params: &Option<Params>,
    source_nclx: Option<Nclx>,
) -> TransferCharacteristics {
    source_nclx
        .and_then(source_known_transfer)
        .or_else(|| {
            params
                .as_ref()
                .and_then(|p| p.color.as_ref())
                .map(ColorEncoding::resolved_transfer)
        })
        .unwrap_or(TransferCharacteristics::Srgb)
}

/// Resolves the CICP triple that must be written into the AVIF container so a
/// decoder reverses the exact RGB→YUV matrix used when building the Y4M.
///
/// `avifenc` defaults to an *unspecified* matrix when `--cicp` is omitted, which
/// decoders resolve as BT.601. The engine always converts with BT.709 unless the
/// caller says otherwise, so the mismatch darkens and desaturates the output
/// (especially visible on wide-gamut sources). The container matrix must
/// therefore always be emitted, even when the caller supplies no `ColorEncoding`.
///
/// When a source ICC profile will be embedded, primaries/transfer are left
/// unspecified so the profile governs instead of contradicting a hard-coded tag.
///
/// Known source primaries and transfer are inherited: converting a matrix does
/// not convert the RGB color space. Untagged sources use the sRGB baseline.
pub fn resolve_output_cicp(
    params: &Option<Params>,
    has_source_icc: bool,
    source_nclx: Option<Nclx>,
) -> Cicp {
    let requested = params.as_ref().and_then(|p| p.color.as_ref());

    // `MatrixCoefficients::Unspecified` still maps to BT.709 coefficients inside
    // the engine, so the container must advertise BT.709 (CICP M=1) as well.
    let matrix = match requested.map(|c| c.matrix) {
        Some(MatrixCoefficients::Bt601) => 6,
        Some(MatrixCoefficients::Bt2020Ncl) => 9,
        Some(MatrixCoefficients::Identity)
        | Some(MatrixCoefficients::Bt709)
        | Some(MatrixCoefficients::Unspecified)
        | None => 1,
    };

    let tone_mapped = requested.is_some_and(|c| {
        c.tone_mapping.is_some() && c.tone_mapping != Some(compute_types::ToneMappingOperator::None)
    });
    let (primaries, transfer) = if tone_mapped {
        // A transfer change is not a gamut conversion. Keep the source
        // primaries, and do not advertise a source ICC with a stale TRC.
        (
            source_nclx
                .map(|_| source_primaries(source_nclx))
                .or_else(|| requested.map(ColorEncoding::resolved_primaries))
                .unwrap_or(ColorPrimaries::Srgb),
            TransferCharacteristics::Srgb,
        )
    } else if has_source_icc {
        (
            ColorPrimaries::Unspecified,
            TransferCharacteristics::Unspecified,
        )
    } else {
        match source_nclx.and_then(source_known_transfer) {
            Some(transfer) => (source_primaries(source_nclx), transfer),
            None => match requested {
                Some(c) => (c.resolved_primaries(), c.resolved_transfer()),
                None => (ColorPrimaries::Srgb, TransferCharacteristics::Srgb),
            },
        }
    };

    let encoded = ColorEncoding {
        primaries,
        transfer,
        matrix: MatrixCoefficients::Bt709,
        ..Default::default()
    }
    .to_cicp();

    Cicp::new(encoded.primaries, encoded.transfer, matrix)
}

/// An unspecified source must not override an explicit fallback.
fn source_known_transfer(source: Nclx) -> Option<TransferCharacteristics> {
    match source.transfer {
        1 => Some(TransferCharacteristics::Bt709),
        8 => Some(TransferCharacteristics::Linear),
        13 => Some(TransferCharacteristics::Srgb),
        16 => Some(TransferCharacteristics::Pq),
        18 => Some(TransferCharacteristics::Hlg),
        _ => None,
    }
}

/// The engine's colour-space enum for a source `nclx` primaries code.
///
/// Known codes map onto their exact counterpart; anything unknown or
/// unspecified stays `Unspecified` so the tag never overstates what is known.
fn source_primaries(source: Option<Nclx>) -> ColorPrimaries {
    match source.map(|nclx| nclx.primaries) {
        Some(12) => ColorPrimaries::DisplayP3,
        Some(9) => ColorPrimaries::Bt2020,
        Some(1) => ColorPrimaries::Srgb,
        _ => ColorPrimaries::Unspecified,
    }
}

/// CICP for the zero-conversion path, where the samples keep the source matrix
/// and range untouched.
///
/// Defaulting the matrix to BT.709 here (as the pixel pipeline does) would
/// mislabel BT.601 or BT.2020-NCL content, and the decoder would then undo the
/// wrong matrix. When the container declares one, that declaration wins.
pub fn resolve_native_cicp(
    source: Option<Nclx>,
    params: &Option<Params>,
    has_source_icc: bool,
) -> Cicp {
    let requested = params.as_ref().and_then(|p| p.color.as_ref());

    // Only codes the engine can actually interpret are carried over; anything
    // unspecified or unknown falls back to the same default the pixel pipeline
    // uses, so the tag always matches how the planes were read.
    let matrix = source
        .and_then(|nclx| nclx.matrix_coefficients())
        .map(|m| match m {
            MatrixCoefficients::Identity => 0,
            MatrixCoefficients::Bt601 => 6,
            MatrixCoefficients::Bt2020Ncl => 9,
            _ => 1,
        })
        .unwrap_or_else(|| match requested.map(|c| c.matrix) {
            Some(MatrixCoefficients::Bt601) => 6,
            Some(MatrixCoefficients::Bt2020Ncl) => 9,
            _ => 1,
        });

    let (primaries, transfer) = if has_source_icc {
        // An embedded profile governs; a hard-coded tag would contradict it.
        (2, 2)
    } else if let Some(nclx) = source {
        (
            u8::try_from(nclx.primaries).unwrap_or(2),
            u8::try_from(nclx.transfer).unwrap_or(2),
        )
    } else if let Some(color) = requested {
        let encoded = color.to_cicp();
        (encoded.primaries, encoded.transfer)
    } else {
        (1, 13)
    };

    Cicp::new(primaries, transfer, matrix)
}

#[cfg(test)]
mod tests {
    use super::*;
    use compute_types::ColorSpace;
    use image::{ImageBuffer, Rgba};

    fn hdr_params() -> Option<Params> {
        Some(Params {
            color: Some(ColorEncoding {
                color_space: Some(ColorSpace::Bt2020Pq),
                nominal_bit_depth: 10,
                tone_mapping: Some(ToneMappingOperator::AcesFilmic),
                ..Default::default()
            }),
            ..Default::default()
        })
    }

    fn img16() -> DynamicImage {
        DynamicImage::ImageRgba16(ImageBuffer::from_pixel(4, 4, Rgba([40_000u16; 4])))
    }

    #[test]
    fn tone_mapping_and_jxl_label_share_the_output_transfer() {
        let source = SourceProfile {
            icc: None,
            nclx: Some(Nclx {
                primaries: 9,
                transfer: 16,
                matrix: 9,
                full_range: true,
            }),
        };
        for target in [Format::Jxl, Format::Avif] {
            let output = OutputColor::resolve(&img16(), target, &hdr_params(), &source).unwrap();
            assert_eq!(output.spec.source_transfer, TransferCharacteristics::Pq);
            assert_eq!(output.spec.target_transfer, TransferCharacteristics::Srgb);
            assert_eq!(output.cicp, Cicp::new(9, 13, 1));
            assert_eq!(output.cjxl_hint(), Some("RGB_D65_202_Rel_SRG"));
        }
    }

    #[test]
    fn p3_sdr_source_is_not_relabelled_srgb() {
        let source = SourceProfile {
            icc: None,
            nclx: Some(Nclx {
                primaries: 12,
                transfer: 13,
                matrix: 1,
                full_range: true,
            }),
        };
        let output = OutputColor::resolve(&img16(), Format::Jxl, &None, &source).unwrap();
        assert_eq!(output.cjxl_hint(), Some("DisplayP3"));
        assert_eq!(output.cicp, Cicp::new(12, 13, 1));
    }

    #[test]
    fn conflicting_cicp_is_rejected_instead_of_relabelling_pixels() {
        let actual = Cicp::new(1, 13, 1);
        assert!(validate_cicp(None, actual).is_ok());
        assert!(validate_cicp(Some(actual), actual).is_ok());
        for conflict in [
            Cicp::new(1, 13, 6),
            Cicp::new(9, 16, 1),
            Cicp::new(12, 13, 1),
        ] {
            assert!(validate_cicp(Some(conflict), actual).is_err());
        }
    }

    #[test]
    fn cannot_export_untagged_wide_gamut_after_tone_mapping() {
        for target in [Format::Jpeg, Format::Png, Format::Webp, Format::Heic] {
            let err =
                OutputColor::resolve(&img16(), target, &hdr_params(), &SourceProfile::default())
                    .unwrap_err();
            assert!(err.to_string().contains("wide-gamut"));
        }
    }

    #[test]
    fn processed_pixels_never_reuse_the_original_icc() {
        let source = SourceProfile {
            icc: Some(vec![1, 2, 3]),
            nclx: Some(Nclx {
                primaries: 9,
                transfer: 16,
                matrix: 9,
                full_range: true,
            }),
        };
        let output = OutputColor::resolve(&img16(), Format::Avif, &hdr_params(), &source).unwrap();
        assert!(output.icc(&source).is_none());
        let output = OutputColor::resolve(&img16(), Format::Avif, &None, &source).unwrap();
        assert_eq!(output.icc(&source), source.icc.as_deref());
    }

    #[test]
    fn png_and_heic_do_not_quantize_deep_requests_to_eight_bits() {
        let params = Some(Params {
            color: Some(ColorEncoding {
                nominal_bit_depth: 10,
                ..Default::default()
            }),
            ..Default::default()
        });
        for (target, depth) in [(Format::Png, 16), (Format::Heic, 10), (Format::Jxl, 16)] {
            let output =
                OutputColor::resolve(&img16(), target, &params, &SourceProfile::default()).unwrap();
            assert_eq!(output.spec.bit_depth, depth);
        }
    }
}
