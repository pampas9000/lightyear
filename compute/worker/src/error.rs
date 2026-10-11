//! Failure taxonomy for worker results.
//!
//! The codes are part of the result contract read by the orchestrator and the
//! web console, so they must stay stable.

use compute_imageops::Error as OpsError;

#[derive(Debug)]
pub enum ComputeError {
    /// The dispatch itself is malformed or asks for something invalid.
    InvalidRequest(String),
    /// The source exists but cannot be processed as requested.
    UnsupportedInput(String),
    /// The source object is missing or larger than allowed.
    SourceUnavailable(String),
    /// libvips or cjxl failed on a supported request.
    ProcessingFailed(String),
    /// Object storage failed; usually transient.
    StorageFailed(String),
    Internal(String),
}

impl std::fmt::Display for ComputeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let (kind, msg) = match self {
            Self::InvalidRequest(m) => ("invalid request", m),
            Self::UnsupportedInput(m) => ("unsupported input", m),
            Self::SourceUnavailable(m) => ("source unavailable", m),
            Self::ProcessingFailed(m) => ("processing failed", m),
            Self::StorageFailed(m) => ("storage failed", m),
            Self::Internal(m) => ("internal error", m),
        };
        write!(f, "{kind}: {msg}")
    }
}

impl std::error::Error for ComputeError {}

impl ComputeError {
    pub fn code(&self) -> &'static str {
        match self {
            Self::InvalidRequest(_) => "invalid_request",
            Self::UnsupportedInput(_) => "unsupported_input",
            Self::SourceUnavailable(_) => "source_unavailable",
            Self::ProcessingFailed(_) => "processing_failed",
            Self::StorageFailed(_) => "storage_failed",
            Self::Internal(_) => "internal_error",
        }
    }

    /// Only storage failures are worth another attempt; the rest would fail
    /// the same way again.
    pub fn retryable(&self) -> bool {
        matches!(self, Self::StorageFailed(_))
    }
}

impl From<OpsError> for ComputeError {
    fn from(err: OpsError) -> Self {
        match err {
            OpsError::Invalid(m) => Self::InvalidRequest(m),
            OpsError::Unsupported(m) => Self::UnsupportedInput(m),
            OpsError::Failed(m) => Self::ProcessingFailed(m),
        }
    }
}
