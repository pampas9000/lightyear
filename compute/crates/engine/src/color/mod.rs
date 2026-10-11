//! Color science for the portable engine.
//!
//! Split by concern:
//! - `transfer`: transfer characteristics (EOTF/OETF) and PQ/HLG curves
//! - `yuv`: RGB to planar YUV conversion and Y4M/PAM serialization
//! - `icc`: ICC profile extraction, classification and injection
//! - `metadata`: EXIF orientation and CICP/`nclx` probing
//! - `chroma`: content-aware chroma subsampling detection
//! - `output`: shared output color pipeline used by host adapters and bindings
//!
//! Primaries conversion is deliberately absent: the engine describes the source
//! color space and carries its ICC/CICP through, but never rewrites pixel values
//! between gamuts.

mod chroma;
mod icc;
mod metadata;
mod output;
mod transfer;
mod yuv;

pub use chroma::*;
pub use icc::*;
pub use metadata::*;
pub use output::*;
pub use transfer::*;
pub use yuv::*;
