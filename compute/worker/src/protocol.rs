use serde::{Deserialize, Serialize};
use compute_types::Format;

#[derive(Debug, Deserialize, Clone)]
pub struct ComputePayload {
    /** Schema Version, in case that the fields will change in future.
     * Currently is "1.0"
     */
    pub schema_version: String,
    pub job_id: String,
    pub attempt_id: String,
    pub input_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_format: Option<Format>,
    pub output_path: String,
    pub target_format: String,
    pub params: Option<serde_json::Value>,
}

#[derive(Debug, Serialize, Clone)]
pub struct ResultPayload {
    pub schema_version: String,
    pub job_id: String,
    pub attempt_id: String,
    pub status: String,
    pub error_message: Option<String>,
    pub progress: Option<i32>,
    pub metadata: serde_json::Value,
}

pub const SCHEMA_VERSION: &str = "1.0";
pub const COMPUTE_STREAM_KEY: &str = "transcoder:compute:stream";
pub const COMPUTE_GROUP_NAME: &str = "transcoder:compute:group";
pub const RESULTS_STREAM_KEY: &str = "transcoder:results:stream";
