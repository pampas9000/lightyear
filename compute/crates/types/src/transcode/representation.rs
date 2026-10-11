//! Native pixel representation contracts.
//!
//! A transcode is lossless in the colour dimension exactly when the decoder's
//! output layout and the encoder's accepted input layout intersect. Converting
//! between models always costs a matrix round trip, and changing chroma
//! subsampling costs a resampling round trip on top of it. Making those
//! matches explicit lets the pipeline skip conversions it does not need.
//!
//! The motivating case is `HEIC → AVIF`: both sides are YUV planes read and
//! written by the same tools, so the samples can be forwarded untouched instead
//! of being reconstructed into RGB and projected back.

use serde::{Deserialize, Serialize};

use super::color::{ChromaSubsampling, ColorRange, MatrixCoefficients};

/// The colour model an image's planes are expressed in.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ColorModel {
    /// Colour channels, including GBR identity planes.
    Rgb,
    /// Luma plus chroma, interpreted through a matrix.
    Yuv,
    /// Single-channel luma.
    Gray,
    /// Four ink channels for print.
    Cmyk,
}

/// A concrete plane layout: what a decoder hands back, or what an encoder takes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct NativeRepresentation {
    pub model: ColorModel,
    pub chroma: ChromaSubsampling,
    pub matrix: MatrixCoefficients,
    pub range: ColorRange,
    pub depth: u8,
}

/// How much work a decoder→encoder hand-off costs in the colour dimension.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Handoff {
    /// Identical layout: every sample can be forwarded untouched.
    Identical,
    /// Same planes and subsampling; only the matrix differs, so a single
    /// transform in float is enough — no resampling.
    MatrixOnly,
    /// Model, subsampling or depth differ: a model conversion and/or chroma
    /// resampling is unavoidable.
    RequiresConversion,
}

impl NativeRepresentation {
    /// Classifies the conversion needed to serve `target` from these pixels.
    pub fn handoff_to(&self, target: &Self) -> Handoff {
        if self == target {
            Handoff::Identical
        } else if self.model == target.model
            && self.chroma == target.chroma
            && self.range == target.range
            && self.depth == target.depth
        {
            Handoff::MatrixOnly
        } else {
            Handoff::RequiresConversion
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn yuv420() -> NativeRepresentation {
        NativeRepresentation {
            model: ColorModel::Yuv,
            chroma: ChromaSubsampling::Yuv420,
            matrix: MatrixCoefficients::Bt709,
            range: ColorRange::Full,
            depth: 10,
        }
    }

    #[test]
    fn identical_layout_needs_no_work() {
        assert_eq!(yuv420().handoff_to(&yuv420()), Handoff::Identical);
    }

    #[test]
    fn differing_matrix_stays_on_the_same_planes() {
        let mut target = yuv420();
        target.matrix = MatrixCoefficients::Bt601;
        assert_eq!(yuv420().handoff_to(&target), Handoff::MatrixOnly);
    }

    #[test]
    fn differing_chroma_or_model_needs_resampling() {
        let mut chroma = yuv420();
        chroma.chroma = ChromaSubsampling::Yuv444;
        assert_eq!(yuv420().handoff_to(&chroma), Handoff::RequiresConversion);

        let mut rgb = yuv420();
        rgb.model = ColorModel::Rgb;
        assert_eq!(yuv420().handoff_to(&rgb), Handoff::RequiresConversion);
    }

    #[test]
    fn differing_depth_needs_quantization() {
        let mut shallow = yuv420();
        shallow.depth = 8;
        assert_eq!(yuv420().handoff_to(&shallow), Handoff::RequiresConversion);
    }
}
