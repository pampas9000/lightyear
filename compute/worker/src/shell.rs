use anyhow::Result;
use compute_types::Job;
// use std::process::Command;

pub async fn ffmpeg_cli_execute(_job: &Job) -> Result<Vec<u8>> {
    println!("Executing external shell command...");
    // let output = Command::new("ffmpeg").args(...).output()?;
    unimplemented!("Shell fallback execution is not implemented yet.");
}
