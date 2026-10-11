//! Queue contract between the orchestrator and workers (version 2).
//!
//! Mirrors `server/internal/task/queue.go`; both sides run the fixtures in
//! `docs/contracts/fixtures/{dispatch,results}-v2.json`.

use crate::ImageOptions;
use serde::{Deserialize, Serialize};

pub const VERSION: u32 = 2;
/// Dispatches for image workers. Workers read with `NOACK`: delivery is the
/// acknowledgement, and the orchestrator's lease decides who owns an attempt.
pub const QUEUE_IMAGE: &str = "q:image";
pub const WORKER_GROUP: &str = "workers";
/// Heartbeats and outcomes for the orchestrator.
pub const QUEUE_RESULTS: &str = "q:results";
pub const RESULTS_MAX_LEN: usize = 100_000;
/// `CANCEL_KEY_PREFIX` + attempt ID exists when the attempt should be abandoned.
pub const CANCEL_KEY_PREFIX: &str = "cancel:attempt:";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Dispatch {
    pub v: u32,
    pub job_id: String,
    pub attempt_id: String,
    pub op: String,
    pub options: ImageOptions,
    pub source_key: String,
    pub output_key: String,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Status {
    Running,
    Completed,
    Failed,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Artifact {
    pub key: String,
    pub size: u64,
    pub mime: String,
    pub width: u32,
    pub height: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ResultError {
    pub code: String,
    pub message: String,
    pub retryable: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct JobResult {
    pub v: u32,
    pub job_id: String,
    pub attempt_id: String,
    pub status: Status,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub progress: Option<u8>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub artifact: Option<Artifact>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ResultError>,
}

impl JobResult {
    pub fn for_dispatch(dispatch: &Dispatch, status: Status) -> Self {
        Self {
            v: VERSION,
            job_id: dispatch.job_id.clone(),
            attempt_id: dispatch.attempt_id.clone(),
            status,
            progress: None,
            artifact: None,
            error: None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    const DISPATCH: &str = include_str!("../../../../docs/contracts/fixtures/dispatch-v2.json");
    const RESULTS: &str = include_str!("../../../../docs/contracts/fixtures/results-v2.json");

    #[test]
    fn dispatch_fixture_round_trips() {
        let dispatch: Dispatch = serde_json::from_str(DISPATCH).unwrap();
        assert_eq!(dispatch.v, VERSION);
        assert_eq!(dispatch.op, "image.convert");
        let expected: Value = serde_json::from_str(DISPATCH).unwrap();
        assert_eq!(serde_json::to_value(dispatch).unwrap(), expected);
    }

    #[test]
    fn result_fixtures_round_trip() {
        let expected: Vec<Value> = serde_json::from_str(RESULTS).unwrap();
        for value in expected {
            let result: JobResult = serde_json::from_value(value.clone()).unwrap();
            assert_eq!(serde_json::to_value(result).unwrap(), value);
        }
    }
}
