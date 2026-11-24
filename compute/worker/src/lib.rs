//! The compute worker. The binary in `main.rs` wires these together; the
//! library exists so that integration tests can drive the compute child.

pub mod config;
pub mod error;
pub mod fetch;
pub mod job;
pub mod process;
pub mod queue;
pub mod redis_connection;
pub mod shutdown;
pub mod storage;
pub mod worker;
