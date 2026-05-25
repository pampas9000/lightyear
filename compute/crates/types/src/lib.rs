pub mod format;
pub mod job;
pub mod transcode;

pub use format::Format;
pub use job::{EncodeOptions, Job};
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
    fn test_params_validation() {
        let avif_json = serde_json::json!({
            "quality": 90,
            "speed": 6
        });
        let params = Params {
            engine: "libavif:avif".to_string(),
            engine_params: avif_json,
        };
        assert!(params.validate_for_format(Format::Avif).is_ok());

        // Wrong engine
        assert!(params.validate_for_format(Format::Jxl).is_err());

        let invalid_params = Params {
            engine: "libavif:avif".to_string(),
            engine_params: serde_json::json!({
                "quality": 120
            }),
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
}
