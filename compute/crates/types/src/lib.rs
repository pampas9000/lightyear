pub mod edit;
pub mod format;
pub mod image_options;
pub mod protocol;
pub mod transcode;

pub use edit::ImageEditOp;
pub use format::Format;
pub use image_options::ImageOptions;
pub use protocol::{
    COMPUTE_GROUP_NAME, COMPUTE_STREAM_KEY, ComputePayload, RESULTS_STREAM_KEY, ResultPayload,
    SCHEMA_VERSION,
};
pub use transcode::color::*;
pub use transcode::libavif::{LibavifAdvancedParams, LibavifParams, YuvFormat};
pub use transcode::libjxl::LibjxlParams;
pub use transcode::params::{Params, TranscodeParamError};

#[cfg(test)]
mod tests {
    use super::*;
    use validator::Validate;

    #[test]
    fn test_validate_libavif_params() {
        let valid = LibavifParams {
            quality: Some(80),
            alpha_quality: Some(85),
            speed: Some(6),
            jobs: Some(4),
            sharp_yuv: Some(true),
            yuv: Some(YuvFormat::Yuv420),
            depth: Some(10),
            color_range: Some(ColorRange::Full),
            cicp: Some(Cicp::new(1, 13, 6)),
            icc_path: None,
            target_size: None,
            advanced: Some(LibavifAdvancedParams {
                sharpness: Some(3),
                color_sharpness: Some(2),
                alpha_sharpness: Some(0),
            }),
        };
        assert!(valid.validate().is_ok());

        let invalid_quality = LibavifParams {
            quality: Some(101),
            ..Default::default()
        };
        assert!(invalid_quality.validate().is_err());
    }

    #[test]
    fn test_validate_libjxl_params() {
        let valid_distance = LibjxlParams {
            distance: Some(1.0),
            effort: Some(7),
            progressive: Some(true),
            color_space: Some("DisplayP3".to_string()),
            ..Default::default()
        };
        assert!(valid_distance.validate().is_ok());

        let valid_quality = LibjxlParams {
            quality: Some(85),
            effort: Some(7),
            ..Default::default()
        };
        assert!(valid_quality.validate().is_ok());

        let invalid_both = LibjxlParams {
            distance: Some(1.0),
            quality: Some(85),
            ..Default::default()
        };
        assert!(invalid_both.validate().is_err());

        let invalid_effort = LibjxlParams {
            effort: Some(11),
            ..Default::default()
        };
        assert!(invalid_effort.validate().is_err());
    }

    #[test]
    fn request_cannot_choose_worker_icc_files() {
        let params = Params {
            engine_params: serde_json::json!({
                "quality": 80,
                "icc_path": "/proc/self/environ",
                "icc_pathname": "/etc/passwd"
            }),
            ..Default::default()
        };
        let avif = params.parse_libavif_params().unwrap();
        assert_eq!(avif.quality, Some(80));
        assert!(avif.icc_path.is_none());
        assert!(params.parse_libjxl_params().unwrap().icc_pathname.is_none());
    }

    #[test]
    fn test_params_validation() {
        let avif_json = serde_json::json!({
            "quality": 90,
            "speed": 6
        });
        let params = Params {
            engine: Some("libavif:avif".to_string()),
            engine_params: avif_json,
            ..Default::default()
        };
        assert!(params.validate_for_format(Format::Avif).is_ok());

        // Wrong engine
        assert!(params.validate_for_format(Format::Jxl).is_err());

        let invalid_params = Params {
            engine: Some("libavif:avif".to_string()),
            engine_params: serde_json::json!({
                "quality": 120
            }),
            ..Default::default()
        };
        assert!(invalid_params.validate_for_format(Format::Avif).is_err());
    }

    #[test]
    fn test_format_serde() {
        let format = Format::Avif;
        let serialized = serde_json::to_string(&format).unwrap();
        assert_eq!(serialized, "\"avif\"");

        let deserialized: Format = serde_json::from_str("\"jxl\"").unwrap();
        assert_eq!(deserialized, Format::Jxl);
    }

    #[test]
    fn test_cicp_and_color_encoding() {
        let p3_encoding = ColorEncoding {
            primaries: ColorPrimaries::DisplayP3,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt601,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: ChromaSubsampling::Yuv420,
            ..Default::default()
        };
        let cicp = p3_encoding.to_cicp();
        assert_eq!(cicp.primaries, 12);
        assert_eq!(cicp.transfer, 13);
        assert_eq!(cicp.matrix, 6);
        assert_eq!(cicp.to_arg_string(), "12/13/6");
        assert_eq!(p3_encoding.to_cjxl_color_space_hint(), Some("DisplayP3"));

        let pq_encoding = ColorEncoding {
            primaries: ColorPrimaries::Bt2020,
            transfer: TransferCharacteristics::Pq,
            matrix: MatrixCoefficients::Bt2020Ncl,
            range: ColorRange::Full,
            nominal_bit_depth: 10,
            chroma: ChromaSubsampling::Yuv420,
            ..Default::default()
        };
        let pq_cicp = pq_encoding.to_cicp();
        assert_eq!(pq_cicp.primaries, 9);
        assert_eq!(pq_cicp.transfer, 16);
        assert_eq!(pq_cicp.matrix, 9);
        assert_eq!(pq_cicp.to_arg_string(), "9/16/9");
        assert_eq!(pq_encoding.to_cjxl_color_space_hint(), Some("Rec2100PQ"));
    }

    #[test]
    fn test_color_space_resolution() {
        let legacy = ColorEncoding {
            primaries: ColorPrimaries::Bt2020,
            transfer: TransferCharacteristics::Pq,
            ..Default::default()
        };
        assert_eq!(legacy.resolved_primaries(), ColorPrimaries::Bt2020);
        assert_eq!(legacy.resolved_transfer(), TransferCharacteristics::Pq);

        // The coupled `color_space` always wins over the legacy fields.
        let coupled = ColorEncoding {
            color_space: Some(ColorSpace::DisplayP3),
            primaries: ColorPrimaries::Bt2020,
            transfer: TransferCharacteristics::Pq,
            ..Default::default()
        };
        assert_eq!(coupled.resolved_primaries(), ColorPrimaries::DisplayP3);
        assert_eq!(coupled.resolved_transfer(), TransferCharacteristics::Srgb);
        assert_eq!(coupled.to_cicp().to_arg_string(), "12/13/1");
        assert_eq!(coupled.to_cjxl_color_space_hint(), Some("DisplayP3"));

        let hdr = ColorEncoding {
            color_space: Some(ColorSpace::Bt2020Hlg),
            ..Default::default()
        };
        assert_eq!(hdr.to_cicp().to_arg_string(), "9/18/1");
        assert_eq!(hdr.to_cjxl_color_space_hint(), Some("Rec2100HLG"));
    }

    #[test]
    fn test_color_space_partial_payload() {
        let decoded: ColorEncoding =
            serde_json::from_str(r#"{"color_space":"bt2020_pq","chroma":"auto"}"#).unwrap();
        assert_eq!(decoded.color_space, Some(ColorSpace::Bt2020Pq));
        assert_eq!(decoded.resolved_primaries(), ColorPrimaries::Bt2020);
        assert_eq!(decoded.resolved_transfer(), TransferCharacteristics::Pq);
        assert_eq!(decoded.nominal_bit_depth, 8);
        assert_eq!(decoded.matrix, MatrixCoefficients::Bt709);
        assert_eq!(decoded.range, ColorRange::Full);
    }

    #[test]
    fn test_narrowed_enums_accept_legacy_payloads() {
        // Boolean dither toggle.
        let on: ColorEncoding = serde_json::from_str(r#"{"dither":true}"#).unwrap();
        assert!(on.dither);
        let off: ColorEncoding = serde_json::from_str(r#"{"dither":false}"#).unwrap();
        assert!(!off.dither);

        // Legacy algorithm names collapse onto the toggle.
        let legacy_blue: ColorEncoding =
            serde_json::from_str(r#"{"dither":"blue_noise"}"#).unwrap();
        assert!(legacy_blue.dither);
        let legacy_bayer: ColorEncoding = serde_json::from_str(r#"{"dither":"bayer"}"#).unwrap();
        assert!(legacy_bayer.dither);
        let legacy_none: ColorEncoding = serde_json::from_str(r#"{"dither":"none"}"#).unwrap();
        assert!(!legacy_none.dither);

        // Removed tone mapping curves fall back to the surviving ACES filmic curve.
        let none: ColorEncoding = serde_json::from_str(r#"{"tone_mapping":"none"}"#).unwrap();
        assert_eq!(none.tone_mapping, Some(ToneMappingOperator::None));
        let aces: ColorEncoding =
            serde_json::from_str(r#"{"tone_mapping":"aces_filmic"}"#).unwrap();
        assert_eq!(aces.tone_mapping, Some(ToneMappingOperator::AcesFilmic));
        let hable: ColorEncoding = serde_json::from_str(r#"{"tone_mapping":"hable"}"#).unwrap();
        assert_eq!(hable.tone_mapping, Some(ToneMappingOperator::AcesFilmic));
        assert!(
            serde_json::from_str::<ColorEncoding>(r#"{"tone_mapping":"not_a_curve"}"#).is_err()
        );
    }
}
