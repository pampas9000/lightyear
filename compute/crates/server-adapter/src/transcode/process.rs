//! External CLI lifecycle: spawn → wait under a hard timeout → diagnose.
//!
//! Every invocation of an external tool is bounded: a hung or runaway
//! `avifenc`/`cjxl`/… must not wedge a worker slot forever. Two mechanisms:
//!
//! - `kill_on_drop(true)`: if the awaiting future is dropped — e.g. the worker
//!   abandons a cancelled job — the child is killed instead of outliving the
//!   task and silently burning CPU.
//! - a timeout: a tool that never terminates is killed and reported with its
//!   elapsed budget, so callers see *why* nothing was produced.

use super::truncate_str;
use anyhow::{Result, bail};
use std::process::Output;
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::process::{Child, Command};

/// Upper bound for a decode/encode CLI run. Single-image encodes finish in
/// minutes; anything beyond this is a hung or pathological run.
pub(super) const CLI_TIMEOUT: Duration = Duration::from_secs(600);

/// Shorter bound for `--version` capability probes.
pub(super) const PROBE_TIMEOUT: Duration = Duration::from_secs(10);

/// Runs `cmd` to completion under `timeout` and returns its output.
///
/// The child is killed when the timeout elapses; a spawn failure is reported
/// with the tool's name so missing binaries read as diagnostics, not IO noise.
pub(super) async fn run(cmd: &mut Command, label: &str, timeout: Duration) -> Result<Output> {
    cmd.kill_on_drop(true);
    // Capture the tool's streams, matching `Command::output`'s behaviour: the
    // output is returned to the caller, and stderr is available for diagnostics.
    cmd.stdout(std::process::Stdio::piped());
    cmd.stderr(std::process::Stdio::piped());
    let mut child = cmd
        .spawn()
        .map_err(|e| anyhow::anyhow!("failed to spawn '{label}': {e}"))?;
    wait_output(&mut child, label, timeout).await
}

/// Runs `cmd` to completion, bailing with the tool's stderr on non-zero exit.
pub(super) async fn run_checked(cmd: &mut Command, label: &str) -> Result<()> {
    let output = run(cmd, label, CLI_TIMEOUT).await?;
    ensure_success(label, output)
}

/// Runs `cmd`, streams `stdin_data` into its stdin, then waits under
/// [`CLI_TIMEOUT`]. The write is bounded by the same timeout as the run.
pub(super) async fn run_with_stdin(
    cmd: &mut Command,
    stdin_data: &[u8],
    label: &str,
) -> Result<()> {
    run_with_stdin_timeout(cmd, stdin_data, label, CLI_TIMEOUT).await
}

async fn run_with_stdin_timeout(
    cmd: &mut Command,
    stdin_data: &[u8],
    label: &str,
    timeout: Duration,
) -> Result<()> {
    cmd.kill_on_drop(true);
    // Capture the streams so the failure path can quote the tool's stderr.
    cmd.stdout(std::process::Stdio::piped());
    cmd.stderr(std::process::Stdio::piped());
    let mut child = cmd
        .spawn()
        .map_err(|e| anyhow::anyhow!("failed to spawn '{label}': {e}"))?;

    let mut stdin = child.stdin.take();
    let writer = async {
        let result = if let Some(ref mut pipe) = stdin {
            pipe.write_all(stdin_data).await
        } else {
            Ok(())
        };
        drop(stdin);
        result
    };
    let outcome = tokio::time::timeout(timeout, async {
        tokio::join!(writer, collect_output(&mut child))
    })
    .await;
    let (write_result, output) = match outcome {
        Ok(result) => result,
        Err(_) => {
            let _ = child.kill().await;
            let _ = child.wait().await;
            bail!(
                "{label} did not finish within {}s and was killed",
                timeout.as_secs()
            );
        }
    };
    let output = output?;
    ensure_success(label, output).and_then(|()| {
        write_result.map_err(|e| anyhow::anyhow!("{label} failed while receiving input: {e}"))
    })
}

/// Waits for `child` under `timeout`, killing it when it elapses.
///
/// Reimplements `Child::wait_with_output` so the timeout can kill the child:
/// the built-in version consumes `self`, which would leave a timed-out child
/// running with no handle to stop it.
async fn wait_output(child: &mut Child, label: &str, timeout: Duration) -> Result<Output> {
    match tokio::time::timeout(timeout, collect_output(child)).await {
        Ok(output) => Ok(output?),
        Err(_) => {
            let _ = child.kill().await;
            let _ = child.wait().await;
            bail!(
                "{label} did not finish within {}s and was killed",
                timeout.as_secs()
            );
        }
    }
}

async fn collect_output(child: &mut Child) -> std::io::Result<Output> {
    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    let (status, stdout, stderr) =
        tokio::try_join!(child.wait(), read_bounded(stdout), read_bounded(stderr),)?;
    Ok(Output {
        status,
        stdout,
        stderr,
    })
}

/// Drain the entire pipe to avoid blocking the child, retaining only diagnostics.
async fn read_bounded<R>(mut reader: Option<R>) -> std::io::Result<Vec<u8>>
where
    R: tokio::io::AsyncRead + Unpin,
{
    let mut buffer = Vec::new();
    if let Some(reader) = reader.as_mut() {
        let mut chunk = [0u8; 1024];
        loop {
            let len = reader.read(&mut chunk).await?;
            if len == 0 {
                break;
            }
            let keep = len.min(64 * 1024 - buffer.len());
            buffer.extend_from_slice(&chunk[..keep]);
        }
    }
    Ok(buffer)
}

/// Bails with the tool's truncated stderr on a non-zero exit.
pub(super) fn ensure_success(label: &str, output: Output) -> Result<()> {
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        bail!("{} failed: {}", label, truncate_str(&stderr, 4096).trim());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn kills_a_hung_process_on_timeout() {
        let mut cmd = Command::new("sleep");
        cmd.arg("5");
        let err = run(&mut cmd, "sleep", Duration::from_millis(150))
            .await
            .expect_err("sleep 5 must hit the watchdog");
        assert!(err.to_string().contains("was killed"), "{err}");
    }

    #[tokio::test]
    async fn surfaces_stderr_on_non_zero_exit() {
        let mut cmd = Command::new("sh");
        cmd.args(["-c", "echo boom >&2; exit 3"]);
        let output = run(&mut cmd, "sh-fail", CLI_TIMEOUT)
            .await
            .expect("the run itself must not fail; the exit status is a caller concern");
        let err = ensure_success("sh-fail", output).expect_err("must surface the failure");
        assert!(err.to_string().contains("boom"), "{err}");
    }

    #[tokio::test]
    async fn stdin_runs_to_completion() {
        let mut cmd = Command::new("sh");
        cmd.args(["-c", "cat > /dev/null; echo done"]);
        cmd.stdin(std::process::Stdio::piped());
        run_with_stdin(&mut cmd, b"payload", "sh-cat")
            .await
            .expect("stdin consumer must exit cleanly");
    }

    #[tokio::test]
    async fn drains_output_while_writing_large_stdin() {
        let mut cmd = Command::new("sh");
        cmd.args(["-c", "head -c 1048576 /dev/zero >&2; cat > /dev/null"]);
        cmd.stdin(std::process::Stdio::piped());
        tokio::time::timeout(
            Duration::from_secs(3),
            run_with_stdin_timeout(
                &mut cmd,
                &vec![1; 1024 * 1024],
                "sh-duplex",
                Duration::from_secs(2),
            ),
        )
        .await
        .expect("the pipes must make progress")
        .expect("the child must exit successfully");
    }
}
