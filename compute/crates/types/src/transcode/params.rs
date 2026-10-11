use crate::format::Format;
use crate::transcode::color::{ChromaSubsampling, ColorEncoding, ColorRange, ProfilePolicy};
use crate::transcode::libavif::LibavifParams;
use crate::transcode::libjxl::LibjxlParams;
use serde::{Deserialize, Serialize};
use validator::Validate;

// Params represents the unified dynamic configuration format for the transcoder.
#[derive(Debug, Clone, Serialize, Deserialize, Validate, PartialEq, Default)]
pub struct Params {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub engine: Option<String>,

    #[serde(default)]
    pub engine_params: serde_json::Value,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color: Option<ColorEncoding>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub profile_policy: Option<ProfilePolicy>,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub strict: Option<bool>,
}

// Define custom error enum for validate_for_format
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TranscodeParamError {
    UnsupportedEngine { expected: &'static str, got: String },
    ValidationError(String),
    ParseError(String),
    StrictBypassUnavailable(String),
}

impl std::fmt::Display for TranscodeParamError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::UnsupportedEngine { expected, got } => {
                write!(
                    f,
                    "unsupported engine for the target format: expected '{}', got '{}'",
                    expected, got
                )
            }
            Self::ValidationError(msg) => write!(f, "validation error: {}", msg),
            Self::ParseError(msg) => write!(f, "parse error: {}", msg),
            Self::StrictBypassUnavailable(msg) => write!(f, "strict bypass unavailable: {}", msg),
        }
    }
}

impl std::error::Error for TranscodeParamError {}

impl Params {
    pub fn parse_libavif_params(&self) -> Result<LibavifParams, serde_json::Error> {
        if self.engine_params.is_null() {
            return Ok(LibavifParams::default());
        }
        serde_json::from_value(self.engine_params.clone())
    }

    pub fn parse_libjxl_params(&self) -> Result<LibjxlParams, serde_json::Error> {
        if self.engine_params.is_null() {
            return Ok(LibjxlParams::default());
        }
        serde_json::from_value(self.engine_params.clone())
    }

    pub fn validate_for_format(&self, target_format: Format) -> Result<(), TranscodeParamError> {
        if let Some(color) = &self.color {
            if !matches!(color.nominal_bit_depth, 8 | 10) {
                return Err(TranscodeParamError::ValidationError(
                    "color.nominal_bit_depth supports only 8 or 10".into(),
                ));
            }
            if target_format == Format::Avif {
                if color.chroma == ChromaSubsampling::Yuv400 {
                    return Err(TranscodeParamError::ValidationError(
                        "AVIF yuv400 output is not supported".into(),
                    ));
                }
                if color.range != ColorRange::Full {
                    return Err(TranscodeParamError::ValidationError(
                        "AVIF limited range output is not supported".into(),
                    ));
                }
            }
        }
        match target_format {
            Format::Avif => {
                if let Some(ref engine) = self.engine {
                    if engine != "libavif:avif" && engine != "ravif" && engine != "auto" {
                        return Err(TranscodeParamError::UnsupportedEngine {
                            expected: "libavif:avif",
                            got: engine.clone(),
                        });
                    }
                }
                if !self.engine_params.is_null() {
                    let avif_opts = self
                        .parse_libavif_params()
                        .map_err(|e| TranscodeParamError::ParseError(e.to_string()))?;
                    avif_opts
                        .validate()
                        .map_err(|e| TranscodeParamError::ValidationError(e.to_string()))?;
                    if avif_opts
                        .depth
                        .is_some_and(|depth| depth != 8 && depth != 10)
                    {
                        return Err(TranscodeParamError::ValidationError(
                            "AVIF encoder depth supports only 8 or 10".into(),
                        ));
                    }
                    if avif_opts.color_range == Some(ColorRange::Limited) {
                        return Err(TranscodeParamError::ValidationError(
                            "AVIF limited range output is not supported".into(),
                        ));
                    }
                }
            }
            Format::Jxl => {
                if let Some(ref engine) = self.engine {
                    if engine != "libjxl:jxl" && engine != "jxl-oxide" && engine != "auto" {
                        return Err(TranscodeParamError::UnsupportedEngine {
                            expected: "libjxl:jxl",
                            got: engine.clone(),
                        });
                    }
                }
                if !self.engine_params.is_null() {
                    let jxl_opts = self
                        .parse_libjxl_params()
                        .map_err(|e| TranscodeParamError::ParseError(e.to_string()))?;
                    jxl_opts
                        .validate()
                        .map_err(|e| TranscodeParamError::ValidationError(e.to_string()))?;
                }
            }
            _ => {}
        }
        Ok(())
    }
}
