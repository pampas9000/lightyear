//! Output intent for an image job, shared by the API, the worker and the
//! browser sandbox.
//!
//! Only the result is described here; everything about the source (color
//! space, depth, orientation, HDR) is probed from the file itself. The JSON
//! Schema in `docs/contracts/schemas/image-options.schema.json` is generated
//! from these types, and `docs/contracts/fixtures/image-options.json` pins the
//! validation and canonicalization rules for every implementation.

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

pub const MAX_DIMENSION: u32 = 16_384;

#[derive(Debug, Clone, Copy, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum OutputFormat {
    Avif,
    Jxl,
    Webp,
    Jpeg,
    Png,
    Heic,
}

impl OutputFormat {
    /// Quality used when the request leaves it out; `None` for formats
    /// without a quality setting.
    fn default_quality(self) -> Option<u8> {
        match self {
            Self::Avif | Self::Heic => Some(60),
            Self::Jxl | Self::Webp => Some(80),
            Self::Jpeg => Some(82),
            Self::Png => None,
        }
    }
}

/// How the image is fitted into `width` × `height`.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Fit {
    /// Scale down to fit inside the box, keeping the aspect ratio.
    #[default]
    Inside,
    /// Fill the box, keeping the aspect ratio and cropping the overflow.
    Cover,
    /// Stretch to exactly the box.
    Fill,
}

#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(deny_unknown_fields)]
pub struct Resize {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(range(min = 1, max = 16384))]
    pub width: Option<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(range(min = 1, max = 16384))]
    pub height: Option<u32>,
    #[serde(default)]
    pub fit: Fit,
    /// Allow upscaling when the source is smaller than the box.
    #[serde(default)]
    pub enlarge: bool,
}

/// What happens to the source's color profile.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum ColorIntent {
    /// Keep the source profile; pixels are not converted.
    #[default]
    Keep,
    /// Convert to sRGB and tag the result as sRGB.
    Srgb,
}

#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum DynamicRange {
    /// Keep HDR content as HDR; rejected where it cannot be preserved.
    #[default]
    Keep,
    /// Deliver standard dynamic range.
    Sdr,
}

/// EXIF and XMP handling; the ICC profile is governed by `color`.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Metadata {
    Keep,
    #[default]
    Strip,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
pub enum Chroma {
    #[serde(rename = "420")]
    Yuv420,
    #[serde(rename = "444")]
    Yuv444,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema, PartialEq, Eq, Hash)]
#[serde(deny_unknown_fields)]
pub struct ImageOptions {
    pub format: OutputFormat,
    /// 1–100. Not used with `lossless` or for PNG.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(range(min = 1, max = 100))]
    pub quality: Option<u8>,
    /// Not available for JPEG.
    #[serde(default)]
    pub lossless: bool,
    /// 1 (fastest) – 10 (smallest). Not available for JPEG.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(range(min = 1, max = 10))]
    pub effort: Option<u8>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub resize: Option<Resize>,
    #[serde(default)]
    pub color: ColorIntent,
    #[serde(default)]
    pub dynamic_range: DynamicRange,
    #[serde(default)]
    pub metadata: Metadata,
    /// AVIF, HEIC and JPEG only; absent means chosen automatically.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub chroma: Option<Chroma>,
    /// 8 or 10, AVIF and HEIC only; absent means follow the source.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schemars(range(min = 8, max = 10))]
    pub bit_depth: Option<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OptionsError(pub String);

impl std::fmt::Display for OptionsError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "invalid image options: {}", self.0)
    }
}

impl std::error::Error for OptionsError {}

fn reject<T>(message: &str) -> Result<T, OptionsError> {
    Err(OptionsError(message.to_string()))
}

impl ImageOptions {
    /// Checks the cross-field rules that the JSON Schema cannot express.
    pub fn validate(&self) -> Result<(), OptionsError> {
        use OutputFormat::*;
        if let Some(quality) = self.quality {
            if !(1..=100).contains(&quality) {
                return reject("quality must be 1–100");
            }
            if self.lossless {
                return reject("quality cannot be combined with lossless");
            }
            if self.format == Png {
                return reject("png has no quality setting");
            }
        }
        if self.lossless && self.format == Jpeg {
            return reject("jpeg cannot be lossless");
        }
        if let Some(effort) = self.effort {
            if !(1..=10).contains(&effort) {
                return reject("effort must be 1–10");
            }
            if self.format == Jpeg {
                return reject("jpeg has no effort setting");
            }
        }
        if let Some(resize) = &self.resize {
            let in_range = |v: Option<u32>| v.is_none_or(|v| (1..=MAX_DIMENSION).contains(&v));
            if resize.width.is_none() && resize.height.is_none() {
                return reject("resize needs width or height");
            }
            if !in_range(resize.width) || !in_range(resize.height) {
                return reject("resize width and height must be 1–16384");
            }
            if resize.fit != Fit::Inside && (resize.width.is_none() || resize.height.is_none()) {
                return reject("cover and fill need both width and height");
            }
        }
        if self.chroma.is_some() {
            if !matches!(self.format, Avif | Heic | Jpeg) {
                return reject("chroma applies to avif, heic and jpeg only");
            }
            if self.lossless {
                return reject("chroma cannot be combined with lossless");
            }
        }
        if let Some(depth) = self.bit_depth {
            if depth != 8 && depth != 10 {
                return reject("bit_depth must be 8 or 10");
            }
            if !matches!(self.format, Avif | Heic) {
                return reject("bit_depth applies to avif and heic only");
            }
        }
        Ok(())
    }

    /// Validates and fills every default, so equal intents serialize equally.
    pub fn canonicalize(mut self) -> Result<Self, OptionsError> {
        self.validate()?;
        if self.quality.is_none() && !self.lossless {
            self.quality = self.format.default_quality();
        }
        if self.effort.is_none() && self.format != OutputFormat::Jpeg {
            self.effort = Some(5);
        }
        Ok(self)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    const SCHEMA_PATH: &str = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../docs/contracts/schemas/image-options.schema.json"
    );
    const CASES: &str = include_str!("../../../../docs/contracts/fixtures/image-options.json");

    /// Regenerate with `UPDATE_SCHEMAS=1 cargo test -p compute-types`.
    #[test]
    fn json_schema_is_up_to_date() {
        let schema = schemars::schema_for!(ImageOptions);
        let generated = serde_json::to_string_pretty(&schema).unwrap() + "\n";
        if std::env::var_os("UPDATE_SCHEMAS").is_some() {
            std::fs::write(SCHEMA_PATH, &generated).unwrap();
        }
        let committed = std::fs::read_to_string(SCHEMA_PATH).unwrap_or_default();
        assert_eq!(
            committed, generated,
            "schema is stale; run UPDATE_SCHEMAS=1 cargo test -p compute-types"
        );
    }

    #[test]
    fn shared_fixture_cases() {
        let cases: Vec<Value> = serde_json::from_str(CASES).unwrap();
        for case in cases {
            let name = case["name"].as_str().unwrap();
            let result = serde_json::from_value::<ImageOptions>(case["input"].clone())
                .map_err(|e| OptionsError(e.to_string()))
                .and_then(ImageOptions::canonicalize);
            match case.get("canonical") {
                Some(expected) => {
                    let canonical = result.unwrap_or_else(|e| panic!("{name}: {e}"));
                    assert_eq!(
                        &serde_json::to_value(canonical).unwrap(),
                        expected,
                        "{name}"
                    );
                }
                None => assert!(result.is_err(), "{name}: expected rejection"),
            }
        }
    }
}
