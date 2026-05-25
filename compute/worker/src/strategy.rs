use crate::shell;
use anyhow::Result;
use compute_types::{Format, Job};

pub async fn process_job(job: Job) -> Result<Vec<u8>> {
    match job.target_format.clone() {
        // 1. 优先尝试 compute-engine (Rust 原生，速度快，无进程开销)
        Format::Jxl | Format::Avif | Format::Webp | Format::Jpeg | Format::Png | Format::Heic => {
            println!("[Strategy] Use compute-engine natively inside the worker memory...");
            compute_engine::process(&job.data, job.target_format.clone(), job.options.clone())
        }

        // 2. 视频或复杂格式，尝试 compute-server-adapter (如有)
        Format::Mp4 => {
            println!("[Strategy] Use compute-server-adapter native bindings...");
            compute_server_adapter::ffmpeg_encode(
                &job.data,
                job.target_format.clone(),
                job.options.clone(),
            )
        }
        _ => {
            // 3. 兜底方案 / 硬件加速：调用 Shell (FFmpeg CLI / NVENC)
            println!("[Strategy] Fallback to shell execution (e.g. ffmpeg cli)...");
            shell::ffmpeg_cli_execute(&job).await
        }
    }
}
