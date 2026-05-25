use serde::{Deserialize, Serialize};
use validator::Validate;

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash)]
pub enum YuvFormat {
    #[serde(rename = "444")]
    Yuv444,
    #[serde(rename = "420")]
    Yuv420,
    #[serde(rename = "422")]
    Yuv422,
    #[serde(rename = "auto")]
    Auto,
}

// LibavifParams represents the configuration options for the libavif encoder (avifenc).
#[derive(Debug, Clone, Serialize, Deserialize, Validate, PartialEq, Default)]
pub struct LibavifParams {
    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 100))]
    pub quality: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 100))]
    pub alpha_quality: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 10))]
    pub speed: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 1))]
    pub jobs: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub sharp_yuv: Option<bool>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub yuv: Option<YuvFormat>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(nested)]
    pub advanced: Option<LibavifAdvancedParams>,
}

// LibavifAdvancedParams represents advanced/tweak configurations for the libavif encoder.
#[derive(Debug, Clone, Serialize, Deserialize, Validate, PartialEq, Default)]
pub struct LibavifAdvancedParams {
    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 7))]
    pub sharpness: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 7))]
    pub color_sharpness: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 7))]
    pub alpha_sharpness: Option<i32>,
}
