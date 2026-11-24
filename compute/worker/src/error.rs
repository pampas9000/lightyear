//! Failure taxonomy for worker results.
//!
//! The codes are part of the result contract read by the orchestrator and the
//! web console, so they must stay stable.

use compute_optimize::Error as OptimizeError;

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
    /// A source URL may not be fetched: not http(s), user info, an address
    /// that is not allowed, too many redirects, or larger than the limit.
    UrlRejected(String),
    /// Fetching a source URL failed: timeouts, connection failures and
    /// server errors may pass next time; most client errors would not.
    FetchFailed { message: String, retryable: bool },
    Cancelled,
    /// The worker was shut down and the job did not finish within the
    /// shutdown grace period; it was stopped. Another attempt can run it.
    WorkerShutdown,
    Internal(String),
}

impl std::fmt::Display for ComputeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let (kind, msg): (&str, &str) = match self {
            Self::InvalidRequest(m) => ("invalid request", m),
            Self::UnsupportedInput(m) => ("unsupported input", m),
            Self::SourceUnavailable(m) => ("source unavailable", m),
            Self::ProcessingFailed(m) => ("processing failed", m),
            Self::StorageFailed(m) => ("storage failed", m),
            Self::UrlRejected(m) => ("url rejected", m),
            Self::FetchFailed { message, .. } => ("fetch failed", message),
            Self::Cancelled => ("cancelled", ""),
            Self::WorkerShutdown => (
                "worker shutdown",
                "the worker stopped before the job finished",
            ),
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
            Self::UrlRejected(_) => "url_rejected",
            Self::FetchFailed { .. } => "fetch_failed",
            Self::Cancelled => "cancelled",
            Self::WorkerShutdown => "worker_shutdown",
            Self::Internal(_) => "internal_error",
        }
    }

    /// Only storage and transient fetch failures, and jobs stopped by a
    /// worker shutdown, are worth another attempt; the rest would fail the
    /// same way again.
    pub fn retryable(&self) -> bool {
        match self {
            Self::StorageFailed(_) | Self::WorkerShutdown => true,
            Self::FetchFailed { retryable, .. } => *retryable,
            _ => false,
        }
    }
}

impl From<OptimizeError> for ComputeError {
    fn from(err: OptimizeError) -> Self {
        match err {
            OptimizeError::Invalid(m) => Self::InvalidRequest(m),
            OptimizeError::Unsupported(m) => Self::UnsupportedInput(m),
            OptimizeError::Failed(m) => Self::ProcessingFailed(m),
            OptimizeError::Cancelled => Self::Cancelled,
        }
    }
}
