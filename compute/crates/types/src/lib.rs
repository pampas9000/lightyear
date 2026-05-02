use serde::{Deserialize, Serialize};

/** File Format */
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum Format {
    /** Can either be still image or animated image */
    JXL,
    /** Can either be still image or animated image */
    AVIF,
    WEBP,
    JPEG,
    HEIC,
    /** HEIF is a container format for images, similar to MP4
     *
     */
    HEIF,
    PNG,
    /** MP4 is a container format for video, under the container can be either AV1 or HEVC (H.265) or AVC (H.264) or VVC (H.266), etc. */
    MP4,
    /** MOV is a container format for video, similar to MP4 */
    MOV,
    /** MKV is a container format for video, similar to MP4 */
    MKV,
    /** FLV is a container format for video, similar to MP4 */
    FLV,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EncodeOptions {
    pub quality: u8,
    // Add additional encoding / rescaling parameters as needed
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Job {
    pub id: String,
    pub data: Vec<u8>,
    pub target_format: Format,
    pub options: Option<EncodeOptions>,
}
