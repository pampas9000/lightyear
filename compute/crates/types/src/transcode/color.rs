use serde::{Deserialize, Deserializer, Serialize, de::Error};

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum ColorPrimaries {
    #[default]
    Srgb, // ITU-R BT.709 / sRGB (CICP P=1)
    DisplayP3,   // SMPTE RP 431-2 / DCI-P3 D65 (CICP P=12)
    Bt2020,      // ITU-R BT.2020 / BT.2100 (CICP P=9)
    Unspecified, // CICP P=2
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum TransferCharacteristics {
    #[default]
    Srgb, // IEC 61966-2-1 (CICP T=13)
    Linear,      // Linear optical luminance (CICP T=8)
    Pq,          // SMPTE ST 2084 / HDR10 (CICP T=16)
    Hlg,         // ARIB STD-B67 / BT.2100 HLG (CICP T=18)
    Bt709,       // ITU-R BT.709 OETF (CICP T=1)
    Unspecified, // CICP T=2
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum MatrixCoefficients {
    Identity, // GBR / Direct RGB (CICP M=0)
    #[default]
    Bt709, // ITU-R BT.709 (CICP M=1)
    Bt601,    // SMPTE 170M / BT.601 (CICP M=6)
    Bt2020Ncl, // ITU-R BT.2020 non-constant luminance (CICP M=9)
    Unspecified, // CICP M=2
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum ColorRange {
    #[default]
    Full, // PC Full range: Y: 0..255/1023, Cb/Cr: 0..255/1023
    Limited, // Studio/TV range: Y: 16..235, Cb/Cr: 16..240
}

/// Combined authoring color space: primaries paired with the transfer function
/// they are conventionally encoded with.
///
/// This is the single source of truth for the color pipeline. The standalone
/// `primaries`/`transfer` fields remain only so payloads written before this
/// enum existed keep working; when `color_space` is present it always wins.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum ColorSpace {
    #[default]
    Srgb, // BT.709 primaries + sRGB transfer (CICP 1/13)
    DisplayP3, // DCI-P3 D65 primaries + sRGB transfer (CICP 12/13)
    Bt2020Pq,  // BT.2020 primaries + PQ (CICP 9/16)
    Bt2020Hlg, // BT.2020 primaries + HLG (CICP 9/18)
    Linear,    // sRGB primaries + linear light (CICP 1/8)
}

impl ColorSpace {
    pub fn primaries(self) -> ColorPrimaries {
        match self {
            Self::Srgb | Self::Linear => ColorPrimaries::Srgb,
            Self::DisplayP3 => ColorPrimaries::DisplayP3,
            Self::Bt2020Pq | Self::Bt2020Hlg => ColorPrimaries::Bt2020,
        }
    }

    pub fn transfer(self) -> TransferCharacteristics {
        match self {
            Self::Srgb | Self::DisplayP3 => TransferCharacteristics::Srgb,
            Self::Bt2020Pq => TransferCharacteristics::Pq,
            Self::Bt2020Hlg => TransferCharacteristics::Hlg,
            Self::Linear => TransferCharacteristics::Linear,
        }
    }

    /// The matching `cjxl -x color_space=` shorthand, when one exists.
    pub fn cjxl_hint(self) -> Option<&'static str> {
        match self {
            Self::Srgb => Some("sRGB"),
            Self::DisplayP3 => Some("DisplayP3"),
            Self::Bt2020Pq => Some("Rec2100PQ"),
            Self::Bt2020Hlg => Some("Rec2100HLG"),
            Self::Linear => None,
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum ChromaSubsampling {
    Yuv420,
    Yuv422,
    #[default]
    Yuv444,
    Yuv400, // Monochrome
    Auto,
}

/// Dynamic-range tone mapping curve used when an HDR source is delivered to an
/// SDR target. Only ACES filmic survived the surface narrowing; the other curves
/// were never reachable from a shipped preset.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum ToneMappingOperator {
    #[default]
    None,
    AcesFilmic,
}

impl<'de> Deserialize<'de> for ToneMappingOperator {
    /// Preserve known historical curves without accepting misspelled values.
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let raw = String::deserialize(deserializer)?;
        Ok(match raw.as_str() {
            "none" => Self::None,
            "aces_filmic" | "reinhard" | "hable" | "mobius" | "bt2390" => Self::AcesFilmic,
            _ => return Err(D::Error::custom(format!("unknown tone mapping: {raw}"))),
        })
    }
}

/// Accepts both the boolean dither toggle and the legacy algorithm names
/// (`none`/`blue_noise`/`floyd_steinberg`/`bayer`) that used to be sent.
fn deserialize_dither_toggle<'de, D: Deserializer<'de>>(deserializer: D) -> Result<bool, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Toggle {
        Bool(bool),
        Name(String),
    }
    Ok(match Toggle::deserialize(deserializer)? {
        Toggle::Bool(on) => on,
        Toggle::Name(name) => name != "none",
    })
}

/// CICP (Code Points for Identification of Coding) three-tuple
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash)]
pub struct Cicp {
    pub primaries: u8,
    pub transfer: u8,
    pub matrix: u8,
}

impl Cicp {
    pub fn new(primaries: u8, transfer: u8, matrix: u8) -> Self {
        Self {
            primaries,
            transfer,
            matrix,
        }
    }

    /// Returns argument in format "P/T/M" for avifenc --cicp
    pub fn to_arg_string(&self) -> String {
        format!("{}/{}/{}", self.primaries, self.transfer, self.matrix)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct ColorEncoding {
    /// Coupled primaries + transfer authoring choice. When present it overrides
    /// the legacy standalone fields below.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color_space: Option<ColorSpace>,
    #[serde(default)]
    pub primaries: ColorPrimaries,
    #[serde(default)]
    pub transfer: TransferCharacteristics,
    #[serde(default)]
    pub matrix: MatrixCoefficients,
    #[serde(default)]
    pub range: ColorRange,
    #[serde(default = "default_nominal_bit_depth")]
    pub nominal_bit_depth: u8,
    #[serde(default)]
    pub chroma: ChromaSubsampling,
    #[serde(default)]
    pub tone_mapping: Option<ToneMappingOperator>,
    #[serde(default, deserialize_with = "deserialize_dither_toggle")]
    pub dither: bool,
}

fn default_nominal_bit_depth() -> u8 {
    8
}

impl Default for ColorEncoding {
    fn default() -> Self {
        Self {
            color_space: None,
            primaries: ColorPrimaries::Srgb,
            transfer: TransferCharacteristics::Srgb,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            nominal_bit_depth: 8,
            chroma: ChromaSubsampling::Yuv444,
            tone_mapping: None,
            dither: false,
        }
    }
}

impl ColorEncoding {
    /// Effective primaries, preferring the coupled `color_space` when present.
    pub fn resolved_primaries(&self) -> ColorPrimaries {
        self.color_space
            .map(ColorSpace::primaries)
            .unwrap_or(self.primaries)
    }

    /// Effective transfer, preferring the coupled `color_space` when present.
    pub fn resolved_transfer(&self) -> TransferCharacteristics {
        self.color_space
            .map(ColorSpace::transfer)
            .unwrap_or(self.transfer)
    }

    pub fn to_cicp(&self) -> Cicp {
        let p = match self.resolved_primaries() {
            ColorPrimaries::Srgb => 1,
            ColorPrimaries::DisplayP3 => 12,
            ColorPrimaries::Bt2020 => 9,
            ColorPrimaries::Unspecified => 2,
        };
        let t = match self.resolved_transfer() {
            TransferCharacteristics::Bt709 => 1,
            TransferCharacteristics::Linear => 8,
            TransferCharacteristics::Srgb => 13,
            TransferCharacteristics::Pq => 16,
            TransferCharacteristics::Hlg => 18,
            TransferCharacteristics::Unspecified => 2,
        };
        let m = match self.matrix {
            MatrixCoefficients::Identity => 0,
            MatrixCoefficients::Bt709 => 1,
            MatrixCoefficients::Bt601 => 6,
            MatrixCoefficients::Bt2020Ncl => 9,
            MatrixCoefficients::Unspecified => 2,
        };
        Cicp {
            primaries: p,
            transfer: t,
            matrix: m,
        }
    }

    pub fn to_cjxl_color_space_hint(&self) -> Option<&'static str> {
        if let Some(space) = self.color_space {
            return space.cjxl_hint();
        }
        match (self.primaries, self.transfer) {
            (ColorPrimaries::DisplayP3, TransferCharacteristics::Srgb) => Some("DisplayP3"),
            (ColorPrimaries::Srgb, TransferCharacteristics::Srgb) => Some("sRGB"),
            (ColorPrimaries::Bt2020, TransferCharacteristics::Pq) => Some("Rec2100PQ"),
            (ColorPrimaries::Bt2020, TransferCharacteristics::Hlg) => Some("Rec2100HLG"),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash, Default)]
#[serde(rename_all = "snake_case")]
pub enum ProfilePolicy {
    #[default]
    PreserveSource, // Carry the source ICC profile through to the output
    Strip, // Drop the ICC profile, rely on container CICP
}
