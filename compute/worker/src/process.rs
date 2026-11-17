//! Runs the image computation of one job in a child process, so that a
//! cancelled or timed-out attempt stops using CPU at once.
//!
//! libvips checks its kill flag only between tiles of the pixel pipeline; the
//! encoders behind it (libavif/aom, libheif, libjxl, libwebp) run to the end
//! once started, and an effort-10 AVIF of a large image takes minutes. Killing
//! the process is the only way to stop them.
//!
//! The child is this executable started with [`ARG`]. It reads one JSON line
//! ([`Request`]) and then the source bytes until EOF from stdin, and writes
//! JSON lines to stdout: any number of progress messages, then one result. A
//! successful result is followed by exactly `size` bytes of output. The child
//! runs in its own process group, so killing the group also ends `cjxl`, and
//! with its own `TMPDIR`, removed once it has been reaped.

use crate::error::ComputeError;
use compute_imageops::{AvifEncoder, Settings};
use compute_optimize::Error as OptimizeError;
use compute_types::ImageOptions;
use compute_types::protocol::{Achieved, Target};
use serde::{Deserialize, Serialize};
use std::io::{BufRead, Read, Write};
use std::path::Path;
use std::process::Stdio;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use tokio::process::{Child, ChildStdout};
use tokio::sync::watch;

/// The first argument that turns the worker binary into a compute child.
pub const ARG: &str = "compute";

#[derive(Debug, Serialize, Deserialize)]
pub struct Request {
    pub options: ImageOptions,
    pub target: Option<Target>,
    pub max_pixels: u64,
    pub avif_encoder: String,
}

impl Request {
    pub fn new(options: ImageOptions, target: Option<Target>, settings: &Settings) -> Self {
        Self {
            options,
            target,
            max_pixels: settings.max_pixels,
            avif_encoder: match settings.avif_encoder {
                AvifEncoder::Libavif => "libavif",
                AvifEncoder::Libvips => "libvips",
            }
            .into(),
        }
    }
}

/// What the child computed; the same fields as `compute_optimize::Outcome`.
#[derive(Debug)]
pub struct Computed {
    pub bytes: Vec<u8>,
    pub mime: String,
    pub width: u32,
    pub height: u32,
    pub resolved_options: ImageOptions,
    pub target_met: Option<bool>,
    pub achieved: Option<Achieved>,
    pub encode_rounds: u32,
    pub compute_ms: u64,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum Message {
    Progress(u8),
    Done(Done),
    Failed { kind: FailureKind, message: String },
}

#[derive(Serialize, Deserialize)]
struct Done {
    size: u64,
    mime: String,
    width: u32,
    height: u32,
    resolved_options: ImageOptions,
    target_met: Option<bool>,
    achieved: Option<Achieved>,
    encode_rounds: u32,
    compute_ms: u64,
}

/// `compute_optimize::Error` across the process boundary, so that the worker
/// maps it to the same result code as before.
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum FailureKind {
    Invalid,
    Unsupported,
    Failed,
    Cancelled,
}

fn split(err: OptimizeError) -> (FailureKind, String) {
    match err {
        OptimizeError::Invalid(m) => (FailureKind::Invalid, m),
        OptimizeError::Unsupported(m) => (FailureKind::Unsupported, m),
        OptimizeError::Failed(m) => (FailureKind::Failed, m),
        OptimizeError::Cancelled => (FailureKind::Cancelled, String::new()),
    }
}

fn join(kind: FailureKind, message: String) -> ComputeError {
    match kind {
        FailureKind::Invalid => OptimizeError::Invalid(message),
        FailureKind::Unsupported => OptimizeError::Unsupported(message),
        FailureKind::Failed => OptimizeError::Failed(message),
        FailureKind::Cancelled => OptimizeError::Cancelled,
    }
    .into()
}

/// Resolves once `cancel` turns true; never if its sender is gone.
pub async fn cancelled(cancel: &mut watch::Receiver<bool>) {
    if cancel.wait_for(|c| *c).await.is_err() {
        std::future::pending::<()>().await;
    }
}

/// A running compute child. Dropping it without [`ComputeProcess::finish`]
/// still kills its process group.
pub struct ComputeProcess {
    child: Child,
    pid: u32,
    stdout: BufReader<ChildStdout>,
    stdin: Option<tokio::task::JoinHandle<()>>,
    tmp: Option<tempfile::TempDir>,
    reaped: bool,
}

impl ComputeProcess {
    pub fn spawn(program: &Path, request: &Request, source: Vec<u8>) -> Result<Self, ComputeError> {
        let internal =
            |what: &str, e: std::io::Error| ComputeError::Internal(format!("{what}: {e}"));
        let tmp = tempfile::Builder::new()
            .prefix("lightyear-compute-")
            .tempdir()
            .map_err(|e| internal("compute temp dir", e))?;
        let mut header = serde_json::to_vec(request)
            .map_err(|e| ComputeError::Internal(format!("compute request: {e}")))?;
        header.push(b'\n');
        let mut child = tokio::process::Command::new(program)
            .arg(ARG)
            .env("TMPDIR", tmp.path())
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .process_group(0)
            .kill_on_drop(true)
            .spawn()
            .map_err(|e| internal("start compute process", e))?;
        let pid = child.id().expect("a just-spawned child has a pid");
        let mut stdin = child.stdin.take().expect("stdin is piped");
        let stdout = child.stdout.take().expect("stdout is piped");
        // Written concurrently with reading, so neither pipe can fill up and
        // block both sides. A child that dies early makes this fail; its exit
        // is reported by `finish`.
        let writer = tokio::spawn(async move {
            if stdin.write_all(&header).await.is_ok() && stdin.write_all(&source).await.is_ok() {
                let _ = stdin.shutdown().await;
            }
        });
        Ok(Self {
            child,
            pid,
            stdout: BufReader::new(stdout),
            stdin: Some(writer),
            tmp: Some(tmp),
            reaped: false,
        })
    }

    pub fn pid(&self) -> u32 {
        self.pid
    }

    /// Waits for the result, or kills the child as soon as `cancel` turns
    /// true. Returns only once the child's whole process group is gone, so no
    /// computation of this job outlives the call.
    pub async fn finish(
        mut self,
        cancel: &mut watch::Receiver<bool>,
        progress: &AtomicU8,
    ) -> Result<Computed, ComputeError> {
        let outcome = tokio::select! {
            outcome = read_result(&mut self.stdout, progress) => outcome,
            () = cancelled(cancel) => Err(ComputeError::Cancelled),
        };
        // Also after a result: the child may still be exiting, and a crashed
        // child may have left `cjxl` running.
        kill_group(self.pid);
        let status = self.child.wait().await;
        self.reaped = true;
        if let Some(writer) = self.stdin.take() {
            writer.abort();
        }
        drop(self.tmp.take());
        match outcome {
            Ok(Some(done)) => Ok(done),
            Ok(None) => Err(ComputeError::ProcessingFailed(match status {
                Ok(status) => format!("compute process ended without a result ({status})"),
                Err(e) => format!("compute process ended without a result ({e})"),
            })),
            Err(e) => Err(e),
        }
    }
}

impl Drop for ComputeProcess {
    fn drop(&mut self) {
        if !self.reaped {
            // `kill_on_drop` reaps the leader in the background.
            kill_group(self.pid);
        }
    }
}

/// Sends SIGKILL to the child's process group. The leader is not reaped yet
/// (it is alive or a zombie), so its pid still names this group.
fn kill_group(pid: u32) {
    unsafe {
        libc::kill(-(pid as libc::pid_t), libc::SIGKILL);
    }
}

/// `Ok(None)` when the child closed stdout without a result.
async fn read_result(
    stdout: &mut BufReader<ChildStdout>,
    progress: &AtomicU8,
) -> Result<Option<Computed>, ComputeError> {
    let broken = |e: String| ComputeError::Internal(format!("compute process output: {e}"));
    let mut line = String::new();
    loop {
        line.clear();
        if stdout
            .read_line(&mut line)
            .await
            .map_err(|e| broken(e.to_string()))?
            == 0
        {
            return Ok(None);
        }
        match serde_json::from_str(&line).map_err(|e| broken(e.to_string()))? {
            Message::Progress(pct) => progress.store(pct, Ordering::Relaxed),
            Message::Failed { kind, message } => return Err(join(kind, message)),
            Message::Done(done) => {
                let mut bytes = vec![0; done.size as usize];
                match stdout.read_exact(&mut bytes).await {
                    Ok(_) => {}
                    Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => return Ok(None),
                    Err(e) => return Err(broken(e.to_string())),
                }
                return Ok(Some(Computed {
                    bytes,
                    mime: done.mime,
                    width: done.width,
                    height: done.height,
                    resolved_options: done.resolved_options,
                    target_met: done.target_met,
                    achieved: done.achieved,
                    encode_rounds: done.encode_rounds,
                    compute_ms: done.compute_ms,
                }));
            }
        }
    }
}

/// The child side: compute one request and exit. Returns the exit code.
pub fn serve() -> i32 {
    // fd 1 is the channel to the parent. Point it at stderr so that nothing a
    // library prints can corrupt the messages, and write them to a copy.
    let channel = unsafe {
        let fd = libc::dup(1);
        if fd < 0 || libc::dup2(2, 1) < 0 {
            eprintln!("compute: cannot set up the output channel");
            return 2;
        }
        <std::fs::File as std::os::fd::FromRawFd>::from_raw_fd(fd)
    };
    let mut out = std::io::BufWriter::new(channel);

    let mut stdin = std::io::stdin().lock();
    let mut header = String::new();
    let mut source = Vec::new();
    if let Err(e) = stdin
        .read_line(&mut header)
        .and_then(|_| stdin.read_to_end(&mut source))
    {
        eprintln!("compute: reading the request: {e}");
        return 2;
    }
    let request: Request = match serde_json::from_str(&header) {
        Ok(r) => r,
        Err(e) => {
            eprintln!("compute: bad request: {e}");
            return 2;
        }
    };
    let avif_encoder = match request.avif_encoder.parse() {
        Ok(a) => a,
        Err(e) => {
            eprintln!("compute: {e}");
            return 2;
        }
    };
    let settings = Settings {
        max_pixels: request.max_pixels,
        avif_encoder,
    };

    // Cancellation kills this process; nothing sets the flag.
    let never = AtomicBool::new(false);
    let result = compute_optimize::optimize(
        &source,
        &request.options,
        request.target.as_ref(),
        &settings,
        &never,
        |pct| {
            // A parent that stopped reading is about to kill us.
            let _ = send(&mut out, &Message::Progress(pct)).and_then(|()| out.flush());
        },
    );
    drop(source);
    let written = match result {
        Ok(outcome) => {
            let output = outcome.output;
            let done = Done {
                size: output.bytes.len() as u64,
                mime: output.mime.into(),
                width: output.width,
                height: output.height,
                resolved_options: outcome.resolved_options,
                target_met: outcome.target_met,
                achieved: outcome.achieved,
                encode_rounds: outcome.encode_rounds,
                compute_ms: outcome.compute_ms,
            };
            send(&mut out, &Message::Done(done)).and_then(|()| out.write_all(&output.bytes))
        }
        Err(err) => {
            let (kind, message) = split(err);
            send(&mut out, &Message::Failed { kind, message })
        }
    };
    match written.and_then(|()| out.flush()) {
        Ok(()) => 0,
        Err(e) => {
            eprintln!("compute: writing the result: {e}");
            2
        }
    }
}

fn send(out: &mut impl Write, message: &Message) -> std::io::Result<()> {
    serde_json::to_writer(&mut *out, message)?;
    out.write_all(b"\n")
}
