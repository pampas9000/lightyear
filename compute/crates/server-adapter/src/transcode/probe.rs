use super::process::{PROBE_TIMEOUT, run};
use anyhow::{Result, bail};
use compute_types::{Format, Params};
use tokio::process::Command;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ToolCapabilities {
    pub cjxl_available: bool,
    #[cfg(test)]
    pub djxl_available: bool,
    pub avifenc_available: bool,
    pub avifdec_available: bool,
    pub heif_enc_available: bool,
    pub heif_dec_available: bool,
    pub cwebp_available: bool,
}

impl ToolCapabilities {
    /// Installed tool availability is stable for the lifetime of a worker.
    pub async fn cached() -> Self {
        static TOOLS: tokio::sync::OnceCell<ToolCapabilities> = tokio::sync::OnceCell::const_new();
        *TOOLS.get_or_init(Self::probe).await
    }

    pub async fn probe() -> Self {
        // The probes are independent; run them concurrently so a slow or
        // missing tool does not multiply into the job's startup latency.
        let (cjxl_ok, avifenc_ok, avifdec_ok, heif_enc_ok, heif_dec_ok, cwebp_ok) = tokio::join!(
            version_probe("cjxl", "--version"),
            version_probe("avifenc", "--version"),
            version_probe("avifdec", "--version"),
            version_probe("heif-enc", "-v"),
            version_probe("heif-dec", "-v"),
            version_probe("cwebp", "-version"),
        );

        Self {
            cjxl_available: cjxl_ok,
            #[cfg(test)]
            djxl_available: version_probe("djxl", "--version").await,
            avifenc_available: avifenc_ok,
            avifdec_available: avifdec_ok,
            heif_enc_available: heif_enc_ok,
            heif_dec_available: heif_dec_ok,
            cwebp_available: cwebp_ok,
        }
    }

    /// Ensures the tools required by the target format are installed.
    /// Only relevant in strict mode; missing tools otherwise fall back to the portable engine.
    pub fn require(
        &self,
        target_format: Format,
        is_heic_input: bool,
        params_val: &Option<Params>,
    ) -> Result<()> {
        if target_format == Format::Jxl && !self.cjxl_available {
            bail!("Strict transcode failed: cjxl CLI is required but not found in PATH");
        }
        if target_format == Format::Avif && !self.avifenc_available {
            bail!("Strict transcode failed: avifenc CLI is required but not found in PATH");
        }
        if target_format == Format::Heic && !self.heif_enc_available {
            bail!(
                "Strict transcode failed: heif-enc CLI is required for HEIC encoding but not found in PATH"
            );
        }
        if target_format == Format::Webp && !self.cwebp_available {
            // The portable engine can only emit lossless WebP, which fails the
            // request when lossy output was asked for (the default).
            let wants_lossless = params_val
                .as_ref()
                .and_then(|p| p.engine_params.get("lossless"))
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            if !wants_lossless {
                bail!(
                    "Strict transcode failed: cwebp CLI is required for lossy WebP but not found in PATH"
                );
            }
        }
        if is_heic_input && !self.heif_dec_available {
            bail!("Strict transcode failed: heif-dec CLI is required but not found in PATH");
        }
        Ok(())
    }
}

/// True when `program` runs with `flag` and exits successfully.
///
/// Missing binaries and hung probes both read as "unavailable": each run is
/// bounded by `PROBE_TIMEOUT` and killed past it.
async fn version_probe(program: &str, flag: &str) -> bool {
    let mut cmd = Command::new(program);
    cmd.arg(flag);
    run(&mut cmd, program, PROBE_TIMEOUT)
        .await
        .map(|o| o.status.success())
        .unwrap_or(false)
}
