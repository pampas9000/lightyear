use anyhow::Result;
use compute_engine::Nclx;
use compute_types::{Params, ProfilePolicy};
use std::path::Path;

/// Source colour metadata extracted from the raw input file.
#[derive(Default)]
pub struct SourceProfile {
    pub icc: Option<Vec<u8>>,
    /// The container's `colr`/`nclx` description, used to label outputs with
    /// the source's own colour identity when the caller specifies none.
    pub nclx: Option<Nclx>,
}

/// Extracts the source ICC profile, unless the caller asked to strip it.
///
/// The engine never converts pixels between primaries, so carrying the profile
/// through to the output is what preserves wide-gamut colour identity.
pub async fn load_source_profile(
    input_path: &Path,
    params: &Option<Params>,
) -> Result<SourceProfile> {
    let strip = params.as_ref().and_then(|p| p.profile_policy) == Some(ProfilePolicy::Strip);

    let source_bytes = tokio::fs::read(input_path).await?;
    let nclx = compute_engine::probe_nclx(&source_bytes);
    let icc = if strip {
        None
    } else {
        compute_engine::extract_icc_from_memory(&source_bytes)
    };

    Ok(SourceProfile { icc, nclx })
}

/// The returned guard keeps a CLI-only profile file alive until encoding ends.
pub async fn write_icc_file(icc: &[u8]) -> Result<tempfile::NamedTempFile> {
    let file = tempfile::NamedTempFile::new()?;
    tokio::fs::write(file.path(), icc).await?;
    Ok(file)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn cli_profile_file_lives_only_as_long_as_its_guard() {
        let bytes = b"test profile bytes";
        let file = write_icc_file(bytes).await.unwrap();
        let path = file.path().to_owned();
        assert_eq!(tokio::fs::read(&path).await.unwrap(), bytes);
        drop(file);
        assert!(!path.exists());
    }
}
