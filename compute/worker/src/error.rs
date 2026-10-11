//! Failure taxonomy for compute jobs.
//!
//! The `error_code` strings are part of the result contract consumed by the
//! job server, so they must stay stable.

#[derive(Debug)]
pub enum ComputeError {
    InvalidRequest(String),
    SourceUnavailable(String),
    EncodeFailed(String),
    StorageFailed(String),
    Internal(String),
}

impl std::fmt::Display for ComputeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ComputeError::InvalidRequest(msg) => write!(f, "Invalid request: {}", msg),
            ComputeError::SourceUnavailable(msg) => write!(f, "Source unavailable: {}", msg),
            ComputeError::EncodeFailed(msg) => write!(f, "Encode failed: {}", msg),
            ComputeError::StorageFailed(msg) => write!(f, "Storage failed: {}", msg),
            ComputeError::Internal(msg) => write!(f, "Internal error: {}", msg),
        }
    }
}

impl std::error::Error for ComputeError {}

impl ComputeError {
    pub fn error_code(&self) -> &'static str {
        match self {
            ComputeError::InvalidRequest(_) => "invalid_request",
            ComputeError::SourceUnavailable(_) => "source_unavailable",
            ComputeError::EncodeFailed(_) => "encode_failed",
            ComputeError::StorageFailed(_) => "storage_failed",
            ComputeError::Internal(_) => "internal_error",
        }
    }

    pub fn is_retryable(&self) -> bool {
        match self {
            ComputeError::InvalidRequest(_) => false,
            ComputeError::SourceUnavailable(_) => false,
            ComputeError::EncodeFailed(_) => false,
            ComputeError::StorageFailed(_) => true,
            ComputeError::Internal(_) => false,
        }
    }
}
