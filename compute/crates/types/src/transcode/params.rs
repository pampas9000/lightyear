use serde::{Deserialize, Serialize};
use validator::Validate;
use crate::format::Format;
use crate::transcode::libavif::LibavifParams;
use crate::transcode::libjxl::LibjxlParams;

// Params represents the unified dynamic configuration format for the transcoder.
#[derive(Debug, Clone, Serialize, Deserialize, Validate, PartialEq)]
pub struct Params {
    #[validate(length(min = 1))]
    pub engine: String,
    pub engine_params: serde_json::Value,
}

// Define custom error enum for validate_for_format
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TranscodeParamError {
    UnsupportedEngine { expected: &'static str, got: String },
    ValidationError(String),
    ParseError(String),
}

impl std::fmt::Display for TranscodeParamError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::UnsupportedEngine { expected, got } => {
                write!(f, "unsupported engine for the target format: expected '{}', got '{}'", expected, got)
            }
            Self::ValidationError(msg) => write!(f, "validation error: {}", msg),
            Self::ParseError(msg) => write!(f, "parse error: {}", msg),
        }
    }
}

impl std::error::Error for TranscodeParamError {}

impl Params {
    pub fn parse_libavif_params(&self) -> Result<LibavifParams, serde_json::Error> {
        serde_json::from_value(self.engine_params.clone())
    }

    pub fn parse_libjxl_params(&self) -> Result<LibjxlParams, serde_json::Error> {
        serde_json::from_value(self.engine_params.clone())
    }

    pub fn validate_for_format(&self, target_format: Format) -> Result<(), TranscodeParamError> {
        match target_format {
            Format::Avif => {
                if self.engine != "libavif:avif" {
                    return Err(TranscodeParamError::UnsupportedEngine {
                        expected: "libavif:avif",
                        got: self.engine.clone(),
                    });
                }
                if self.engine_params.is_null() {
                    return Ok(());
                }
                let avif_opts = self.parse_libavif_params()
                    .map_err(|e| TranscodeParamError::ParseError(e.to_string()))?;
                avif_opts.validate()
                    .map_err(|e| TranscodeParamError::ValidationError(e.to_string()))?;
            }
            Format::Jxl => {
                if self.engine != "libjxl:jxl" {
                    return Err(TranscodeParamError::UnsupportedEngine {
                        expected: "libjxl:jxl",
                        got: self.engine.clone(),
                    });
                }
                if self.engine_params.is_null() {
                    return Ok(());
                }
                let jxl_opts = self.parse_libjxl_params()
                    .map_err(|e| TranscodeParamError::ParseError(e.to_string()))?;
                jxl_opts.validate()
                    .map_err(|e| TranscodeParamError::ValidationError(e.to_string()))?;
            }
            _ => {}
        }
        Ok(())
    }
}
