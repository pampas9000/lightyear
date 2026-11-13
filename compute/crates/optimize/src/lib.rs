//! Searches quality or Butteraugli distance around `compute-imageops` to hit a
//! compression target without modifying single-pass `ImageOptions` semantics.

use compute_imageops::{Error as OpsError, Output, Settings};
use compute_types::ImageOptions;
use compute_types::image_options::{Chroma, Distance, OutputFormat};
use compute_types::protocol::{Achieved, Target};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Instant;

const QUALITY_LEVELS: usize = 100; // Q1..=Q100
const DISTANCE_MIN_HUNDREDTHS: u16 = 10; // 0.10
const DISTANCE_MAX_HUNDREDTHS: u16 = 2500; // 25.00
const DISTANCE_STEP_HUNDREDTHS: u16 = 5; // 0.05
const DISTANCE_LEVELS: usize =
    ((DISTANCE_MAX_HUNDREDTHS - DISTANCE_MIN_HUNDREDTHS) / DISTANCE_STEP_HUNDREDTHS) as usize + 1;

#[derive(Debug)]
pub struct Outcome {
    pub output: Output,
    pub resolved_options: ImageOptions,
    pub target_met: Option<bool>,
    pub achieved: Option<Achieved>,
    pub encode_rounds: u32,
    pub compute_ms: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Error {
    Invalid(String),
    Unsupported(String),
    Failed(String),
    Cancelled,
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Invalid(m) => write!(f, "invalid options: {m}"),
            Self::Unsupported(m) => write!(f, "unsupported input: {m}"),
            Self::Failed(m) => write!(f, "processing failed: {m}"),
            Self::Cancelled => write!(f, "optimization cancelled"),
        }
    }
}

impl std::error::Error for Error {}

impl From<OpsError> for Error {
    fn from(err: OpsError) -> Self {
        match err {
            OpsError::Invalid(m) => Self::Invalid(m),
            OpsError::Unsupported(m) => Self::Unsupported(m),
            OpsError::Failed(m) => Self::Failed(m),
        }
    }
}

struct Candidate {
    level: usize,
    options: ImageOptions,
    output: Output,
}

/// Reports whether `options` + `target` require an iterative parameter search
/// rather than a single deterministic encode.
pub fn needs_search(options: &ImageOptions, target: Option<&Target>) -> bool {
    target.is_some_and(|t| !t.is_empty())
        && options.quality.is_none()
        && options.distance.is_none()
        && !options.lossless
}

/// Runs single-pass encoding or target-guided optimization.
///
/// When `target` is absent, or when `quality`/`distance`/`lossless` are explicitly
/// given, a single encode runs and `target` (if present) is checked without search.
pub fn optimize(
    source: &[u8],
    options: &ImageOptions,
    target: Option<&Target>,
    settings: &Settings,
    cancelled: &AtomicBool,
    mut on_progress: impl FnMut(u8),
) -> Result<Outcome, Error> {
    let started = Instant::now();
    options.validate().map_err(|e| Error::Invalid(e.0))?;
    validate_target(options, target)?;
    check_cancelled(cancelled)?;

    let Some(target) = target else {
        let resolved = options.clone().canonicalize().map_err(|e| Error::Invalid(e.0))?;
        let output = compute_imageops::process(source, &resolved, settings)?;
        return Ok(Outcome {
            output,
            resolved_options: resolved,
            target_met: None,
            achieved: None,
            encode_rounds: 1,
            compute_ms: elapsed_ms(started),
        });
    };

    let max_bytes = target
        .max_bytes
        .ok_or_else(|| Error::Invalid("target cannot be empty".into()))?;

    if !needs_search(options, Some(target)) {
        let mut resolved = options.clone().canonicalize().map_err(|e| Error::Invalid(e.0))?;
        resolve_chroma(&mut resolved);
        let output = compute_imageops::process(source, &resolved, settings)?;
        let size = output.bytes.len() as u64;
        return Ok(Outcome {
            output,
            resolved_options: resolved,
            target_met: Some(size <= max_bytes),
            achieved: Some(Achieved { bytes: Some(size) }),
            encode_rounds: 1,
            compute_ms: elapsed_ms(started),
        });
    }

    search_max_bytes(
        source,
        options,
        max_bytes,
        settings,
        cancelled,
        started,
        &mut on_progress,
    )
}

fn validate_target(options: &ImageOptions, target: Option<&Target>) -> Result<(), Error> {
    let Some(target) = target else {
        return Ok(());
    };
    if target.is_empty() {
        return Err(Error::Invalid("target cannot be empty".into()));
    }
    if target.max_bytes == Some(0) {
        return Err(Error::Invalid("target.max_bytes must be greater than 0".into()));
    }
    if options.format == OutputFormat::Png {
        return Err(Error::Invalid("png has no quality setting for target".into()));
    }
    Ok(())
}

fn search_max_bytes(
    source: &[u8],
    base: &ImageOptions,
    max_bytes: u64,
    settings: &Settings,
    cancelled: &AtomicBool,
    started: Instant,
    on_progress: &mut impl FnMut(u8),
) -> Result<Outcome, Error> {
    let mut rounds: u32 = 0;
    let mut best_fitting: Option<Candidate> = None;
    let mut smallest: Option<Candidate> = None;

    if base.format == OutputFormat::Jxl {
        let mut bypass_opts = base.clone();
        bypass_opts.lossless = true;
        bypass_opts.quality = None;
        bypass_opts.distance = None;
        bypass_opts.chroma = None;
        if let Some(output) = compute_imageops::try_jpeg_to_jxl_bypass(source, &bypass_opts, settings)?
        {
            rounds += 1;
            on_progress(10);
            let size = output.bytes.len() as u64;
            if size <= max_bytes {
                return Ok(Outcome {
                    output,
                    resolved_options: bypass_opts,
                    target_met: Some(true),
                    achieved: Some(Achieved { bytes: Some(size) }),
                    encode_rounds: rounds,
                    compute_ms: elapsed_ms(started),
                });
            }
            smallest = Some(Candidate {
                level: usize::MAX,
                options: bypass_opts,
                output,
            });
        }
    }

    check_cancelled(cancelled)?;
    let prepared = compute_imageops::prepare(source, base, settings)?;
    on_progress(15);

    let (levels, est_rounds) = match base.format {
        OutputFormat::Jxl => (DISTANCE_LEVELS, 9u32),
        _ => (QUALITY_LEVELS, 7u32),
    };
    let mut lo = 0usize;
    let mut hi = levels - 1;

    while lo <= hi {
        check_cancelled(cancelled)?;
        let mid = lo + (hi - lo) / 2;
        let round_opts = options_for_level(base, mid);
        let output = compute_imageops::encode(&prepared, &round_opts, settings)?;
        rounds += 1;
        let pct = 15 + ((rounds.min(est_rounds) * 75) / est_rounds) as u8;
        on_progress(pct);

        let fits = output.bytes.len() as u64 <= max_bytes;
        let candidate = Candidate {
            level: mid,
            options: round_opts,
            output,
        };
        record_candidate(&mut best_fitting, &mut smallest, candidate, fits);

        if fits {
            lo = mid + 1;
        } else {
            if mid == 0 {
                break;
            }
            hi = mid - 1;
        }
    }

    let (winner, target_met) = match best_fitting {
        Some(c) => (c, true),
        None => (
            smallest.expect("binary search always produces at least one candidate"),
            false,
        ),
    };
    let size = winner.output.bytes.len() as u64;
    Ok(Outcome {
        output: winner.output,
        resolved_options: winner.options,
        target_met: Some(target_met),
        achieved: Some(Achieved { bytes: Some(size) }),
        encode_rounds: rounds,
        compute_ms: elapsed_ms(started),
    })
}

fn record_candidate(
    best_fitting: &mut Option<Candidate>,
    smallest: &mut Option<Candidate>,
    candidate: Candidate,
    fits: bool,
) {
    if fits {
        let replace = best_fitting.as_ref().is_none_or(|cur| {
            candidate.level > cur.level
                || (candidate.level == cur.level
                    && candidate.output.bytes.len() < cur.output.bytes.len())
        });
        if replace {
            *best_fitting = Some(candidate);
        }
        return;
    }
    if best_fitting.is_some() {
        return;
    }
    let replace_smallest = smallest.as_ref().is_none_or(|cur| {
        candidate.output.bytes.len() < cur.output.bytes.len()
            || (candidate.output.bytes.len() == cur.output.bytes.len()
                && candidate.level > cur.level)
    });
    if replace_smallest {
        *smallest = Some(candidate);
    }
}

fn options_for_level(base: &ImageOptions, level: usize) -> ImageOptions {
    let mut opts = base.clone();
    opts.lossless = false;
    match base.format {
        OutputFormat::Jxl => {
            opts.quality = None;
            let hundredths =
                DISTANCE_MAX_HUNDREDTHS - (level as u16) * DISTANCE_STEP_HUNDREDTHS;
            opts.distance = Some(Distance::from_hundredths(hundredths));
        }
        _ => {
            opts.distance = None;
            opts.quality = Some((level + 1) as u8);
            resolve_chroma(&mut opts);
        }
    }
    opts
}

fn resolve_chroma(options: &mut ImageOptions) {
    if options.chroma.is_some()
        || options.lossless
        || !matches!(
            options.format,
            OutputFormat::Avif | OutputFormat::Heic | OutputFormat::Jpeg
        )
    {
        return;
    }
    let q = options
        .quality
        .or_else(|| options.format.default_quality())
        .unwrap_or(80);
    options.chroma = Some(if q >= 90 {
        Chroma::Yuv444
    } else {
        Chroma::Yuv420
    });
}

fn check_cancelled(cancelled: &AtomicBool) -> Result<(), Error> {
    if cancelled.load(Ordering::Relaxed) {
        Err(Error::Cancelled)
    } else {
        Ok(())
    }
}

fn elapsed_ms(started: Instant) -> u64 {
    started.elapsed().as_millis().min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(path: &str) -> Vec<u8> {
        let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../../../scripts/fixtures/");
        std::fs::read(format!("{root}{path}")).unwrap_or_else(|e| panic!("{path}: {e}"))
    }

    fn opts(json: &str) -> ImageOptions {
        serde_json::from_str(json).unwrap()
    }

    #[test]
    fn non_target_and_check_only_run_single_round() {
        let input = fixture("hd_benchmark/landscape_mountains_4k.jpg");
        let settings = Settings::default();
        let cancel = AtomicBool::new(false);

        let no_target = optimize(
            &input,
            &opts(r#"{"format":"webp","resize":{"width":400}}"#),
            None,
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(no_target.encode_rounds, 1);
        assert_eq!(no_target.target_met, None);
        assert_eq!(no_target.resolved_options.quality, Some(80));

        let exact = compute_imageops::process(
            &input,
            &opts(r#"{"format":"webp","quality":75,"resize":{"width":400}}"#),
            &settings,
        )
        .unwrap();
        let check_hit = optimize(
            &input,
            &opts(r#"{"format":"webp","quality":75,"resize":{"width":400}}"#),
            Some(&Target {
                max_bytes: Some(exact.bytes.len() as u64 + 1024),
            }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(check_hit.encode_rounds, 1);
        assert_eq!(check_hit.target_met, Some(true));
        assert_eq!(check_hit.output.bytes, exact.bytes);

        let check_miss = optimize(
            &input,
            &opts(r#"{"format":"webp","quality":75,"resize":{"width":400}}"#),
            Some(&Target { max_bytes: Some(100) }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(check_miss.encode_rounds, 1);
        assert_eq!(check_miss.target_met, Some(false));
    }

    #[test]
    fn rejects_png_with_target_and_invalid_targets() {
        let input = fixture("quality/checker_rb_444.png");
        let cancel = AtomicBool::new(false);
        assert!(matches!(
            optimize(
                &input,
                &opts(r#"{"format":"png"}"#),
                Some(&Target {
                    max_bytes: Some(10_000)
                }),
                &Settings::default(),
                &cancel,
                |_| {},
            ),
            Err(Error::Invalid(_))
        ));
        assert!(matches!(
            optimize(
                &input,
                &opts(r#"{"format":"avif"}"#),
                Some(&Target { max_bytes: None }),
                &Settings::default(),
                &cancel,
                |_| {},
            ),
            Err(Error::Invalid(_))
        ));
        assert!(matches!(
            optimize(
                &input,
                &opts(r#"{"format":"avif"}"#),
                Some(&Target { max_bytes: Some(0) }),
                &Settings::default(),
                &cancel,
                |_| {},
            ),
            Err(Error::Invalid(_))
        ));
    }

    #[test]
    fn binary_search_meets_target_and_matches_process_byte_for_byte() {
        let photo = fixture("hd_benchmark/landscape_mountains_4k.jpg");
        let settings = Settings::default();
        let cancel = AtomicBool::new(false);

        for (spec, max_bytes) in [
            (r#"{"format":"avif","resize":{"width":640}}"#, 18_000u64),
            (r#"{"format":"webp","resize":{"width":640}}"#, 22_000u64),
            (r#"{"format":"jpeg","resize":{"width":640}}"#, 35_000u64),
            (r#"{"format":"jxl","resize":{"width":640}}"#, 24_000u64),
        ] {
            let mut progress = Vec::new();
            let outcome = optimize(
                &photo,
                &opts(spec),
                Some(&Target {
                    max_bytes: Some(max_bytes),
                }),
                &settings,
                &cancel,
                |p| progress.push(p),
            )
            .unwrap();
            assert_eq!(outcome.target_met, Some(true), "{spec}");
            assert!(outcome.output.bytes.len() as u64 <= max_bytes, "{spec}");
            assert_eq!(
                outcome.achieved,
                Some(Achieved {
                    bytes: Some(outcome.output.bytes.len() as u64)
                })
            );
            assert!(outcome.encode_rounds <= 10, "{spec}: {} rounds", outcome.encode_rounds);
            assert!(progress.windows(2).all(|w| w[0] <= w[1]), "{progress:?}");

            let reproduced =
                compute_imageops::process(&photo, &outcome.resolved_options, &settings).unwrap();
            assert_eq!(
                outcome.output.bytes, reproduced.bytes,
                "{spec}: resolved={:?}",
                outcome.resolved_options
            );
        }
    }

    #[test]
    fn resolves_chroma_to_444_when_budget_allows_high_quality() {
        let checkerboard = fixture("quality/checker_rb_444.png");
        let settings = Settings::default();
        let cancel = AtomicBool::new(false);
        let outcome = optimize(
            &checkerboard,
            &opts(r#"{"format":"jpeg"}"#),
            Some(&Target {
                max_bytes: Some(500_000),
            }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(outcome.target_met, Some(true));
        assert!(outcome.resolved_options.quality.unwrap() >= 90);
        assert_eq!(outcome.resolved_options.chroma, Some(Chroma::Yuv444));
        let reproduced =
            compute_imageops::process(&checkerboard, &outcome.resolved_options, &settings).unwrap();
        assert_eq!(outcome.output.bytes, reproduced.bytes);
    }

    #[test]
    fn jpeg_to_jxl_tries_bypass_first_and_falls_back_to_distance_search() {
        let jpeg = fixture("external/webkit_iceland_p3.jpg");
        let settings = Settings::default();
        let cancel = AtomicBool::new(false);

        let big_budget = optimize(
            &jpeg,
            &opts(r#"{"format":"jxl"}"#),
            Some(&Target {
                max_bytes: Some(jpeg.len() as u64),
            }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(big_budget.target_met, Some(true));
        assert_eq!(big_budget.encode_rounds, 1);
        assert!(big_budget.resolved_options.lossless);
        let reproduced_bypass =
            compute_imageops::process(&jpeg, &big_budget.resolved_options, &settings).unwrap();
        assert_eq!(big_budget.output.bytes, reproduced_bypass.bytes);

        let tight_budget = optimize(
            &jpeg,
            &opts(r#"{"format":"jxl"}"#),
            Some(&Target {
                max_bytes: Some(30_000),
            }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(tight_budget.target_met, Some(true));
        assert!(tight_budget.output.bytes.len() <= 30_000);
        assert!(!tight_budget.resolved_options.lossless);
        assert!(tight_budget.resolved_options.distance.is_some());
        let reproduced_lossy =
            compute_imageops::process(&jpeg, &tight_budget.resolved_options, &settings).unwrap();
        assert_eq!(tight_budget.output.bytes, reproduced_lossy.bytes);
    }

    #[test]
    fn unreachable_target_delivers_smallest_with_target_met_false() {
        let checkerboard = fixture("quality/checker_rb_444.png");
        let settings = Settings::default();
        let cancel = AtomicBool::new(false);
        let outcome = optimize(
            &checkerboard,
            &opts(r#"{"format":"jpeg"}"#),
            Some(&Target { max_bytes: Some(50) }),
            &settings,
            &cancel,
            |_| {},
        )
        .unwrap();
        assert_eq!(outcome.target_met, Some(false));
        assert!(outcome.output.bytes.len() > 50);
        let reproduced =
            compute_imageops::process(&checkerboard, &outcome.resolved_options, &settings).unwrap();
        assert_eq!(outcome.output.bytes, reproduced.bytes);
    }

    #[test]
    fn cancellation_aborts_search() {
        let photo = fixture("hd_benchmark/landscape_mountains_4k.jpg");
        let cancel = AtomicBool::new(false);
        let mut rounds = 0;
        let result = optimize(
            &photo,
            &opts(r#"{"format":"avif","resize":{"width":640}}"#),
            Some(&Target {
                max_bytes: Some(15_000),
            }),
            &Settings::default(),
            &cancel,
            |_| {
                rounds += 1;
                if rounds >= 2 {
                    cancel.store(true, Ordering::Relaxed);
                }
            },
        );
        assert!(matches!(result, Err(Error::Cancelled)));
    }
}
