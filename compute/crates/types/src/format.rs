use serde::{Deserialize, Serialize};

/** File Format */
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Format {
    /** Can either be still image or animated image */
    Jxl,
    /** Can either be still image or animated image */
    Avif,
    Webp,
    Jpeg,
    Heic,
    /** HEIF is a container format for images, similar to MP4
     *
     */
    Heif,
    Png,
    /** MP4 is a container format for video, under the container can be either AV1 or HEVC (H.265) or AVC (H.264) or VVC (H.266), etc. */
    Mp4,
    /** MOV is a container format for video, similar to MP4 */
    Mov,
    /** MKV is a container format for video, similar to MP4 */
    Mkv,
    /** FLV is a container format for video, similar to MP4 */
    Flv,
}
