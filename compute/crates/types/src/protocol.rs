//! Wire contracts for the asynchronous compute pipeline.
//!
//! These payloads are exchanged over Redis Streams between the job server and
//! the compute worker, so they must stay serialization-compatible with the
//! server-side definitions in `server/internal/task/queue.go`.

use crate::{Format, ImageEditOp};
use serde::{Deserialize, Serialize};

pub const SCHEMA_VERSION: &str = "1.0";
pub const COMPUTE_STREAM_KEY: &str = "transcoder:compute:stream";
pub const COMPUTE_GROUP_NAME: &str = "transcoder:compute:group";
pub const RESULTS_STREAM_KEY: &str = "transcoder:results:stream";

#[derive(Debug, Deserialize, Clone)]
pub struct ComputePayload {
    #[allow(dead_code)]
    pub schema_version: String,
    pub job_id: String,
    pub attempt_id: String,
    pub input_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_format: Option<Format>,
    pub output_path: String,
    pub target_format: String,
    pub params: Option<serde_json::Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub output_file_id: Option<String>,
    #[serde(default)]
    pub attempt_scoped_cancel: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ops: Option<Vec<ImageEditOp>>,
}

#[derive(Debug, Serialize, Clone)]
pub struct ResultPayload {
    pub schema_version: String,
    pub job_id: String,
    pub attempt_id: String,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_code: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub retryable: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub progress: Option<i32>,
    pub metadata: serde_json::Value,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub artifacts: Vec<Artifact>,
}

#[derive(Debug, Serialize, Clone)]
pub struct Artifact {
    pub file_id: String,
    pub path: String,
    pub size: u64,
    pub mime_type: String,
    pub width: u32,
    pub height: u32,
}

#[cfg(test)]
mod tests {
    use super::*;

    const COMPUTE_FIXTURE: &str =
        include_str!("../../../../docs/contracts/fixtures/compute-v1.0.json");
    const RESULT_FIXTURE: &str =
        include_str!("../../../../docs/contracts/fixtures/result-v1.0.json");

    #[test]
    fn reads_shared_compute_fixture_and_legacy_defaults() {
        let payload: ComputePayload = serde_json::from_str(COMPUTE_FIXTURE).unwrap();
        assert_eq!(payload.schema_version, SCHEMA_VERSION);
        assert!(payload.attempt_scoped_cancel);
        assert_eq!(
            payload.output_file_id.as_deref(),
            Some("0198a000-0000-7000-8000-000000000003")
        );
        let mut legacy: serde_json::Value = serde_json::from_str(COMPUTE_FIXTURE).unwrap();
        legacy
            .as_object_mut()
            .unwrap()
            .remove("attempt_scoped_cancel");
        legacy.as_object_mut().unwrap().remove("output_file_id");
        let payload: ComputePayload = serde_json::from_value(legacy).unwrap();
        assert!(!payload.attempt_scoped_cancel);
        assert!(payload.output_file_id.is_none());
    }

    #[test]
    fn writes_shared_result_fixture() {
        let compute: ComputePayload = serde_json::from_str(COMPUTE_FIXTURE).unwrap();
        let result = ResultPayload {
            schema_version: SCHEMA_VERSION.to_string(),
            job_id: compute.job_id,
            attempt_id: compute.attempt_id,
            status: "COMPLETED".to_string(),
            error_message: None,
            error_code: None,
            retryable: None,
            progress: Some(100),
            metadata: serde_json::json!({
                "output_size_bytes": 1234, "mime_type": "image/avif", "width": 512, "height": 256
            }),
            artifacts: vec![Artifact {
                file_id: compute.output_file_id.unwrap(),
                path: compute.output_path,
                size: 1234,
                mime_type: "image/avif".to_string(),
                width: 512,
                height: 256,
            }],
        };
        let expected: serde_json::Value = serde_json::from_str(RESULT_FIXTURE).unwrap();
        assert_eq!(serde_json::to_value(result).unwrap(), expected);
    }
}
