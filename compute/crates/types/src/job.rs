use serde::{Deserialize, Serialize};
use crate::format::Format;

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
