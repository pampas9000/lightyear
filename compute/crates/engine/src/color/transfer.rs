use compute_types::TransferCharacteristics;

/// Converts an sRGB non-linear component in [0.0, 1.0] to linear optical intensity.
#[inline]
pub fn srgb_to_linear_f32(c: f32) -> f32 {
    let c = c.clamp(0.0, 1.0);
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

/// Converts a linear optical intensity in [0.0, 1.0] to non-linear sRGB.
#[inline]
pub fn linear_to_srgb_f32(l: f32) -> f32 {
    let l = l.clamp(0.0, 1.0);
    if l <= 0.0031308 {
        l * 12.92
    } else {
        1.055 * l.powf(1.0 / 2.4) - 0.055
    }
}

/// Converts a BT.709 non-linear component in [0.0, 1.0] to linear optical intensity.
#[inline]
pub fn bt709_to_linear_f32(v: f32) -> f32 {
    let v = v.clamp(0.0, 1.0);
    if v < 0.081 {
        v / 4.5
    } else {
        ((v + 0.099) / 1.099).powf(1.0 / 0.45)
    }
}

/// Converts a linear optical intensity in [0.0, 1.0] to non-linear BT.709.
#[inline]
pub fn linear_to_bt709_f32(l: f32) -> f32 {
    let l = l.clamp(0.0, 1.0);
    if l < 0.018 {
        4.5 * l
    } else {
        1.099 * l.powf(0.45) - 0.099
    }
}

/// Converts an HLG (ITU-R BT.2100) non-linear component in [0.0, 1.0] to normalized linear scene light.
#[inline]
pub fn hlg_to_linear_f32(v: f32) -> f32 {
    let v = v.clamp(0.0, 1.0);
    const A: f32 = 0.17883277;
    const B: f32 = 0.28466892;
    const C: f32 = 0.55991073;
    if v <= 0.5 {
        (v * v) / 3.0
    } else {
        (((v - C) / A).exp() + B) / 12.0
    }
}

/// Converts normalized linear scene light [0.0, 1.0] to HLG non-linear component [0.0, 1.0].
#[inline]
pub fn linear_to_hlg_f32(l: f32) -> f32 {
    let l = l.clamp(0.0, 1.0);
    const A: f32 = 0.17883277;
    const B: f32 = 0.28466892;
    const C: f32 = 0.55991073;
    if l <= 1.0 / 12.0 {
        (3.0 * l).sqrt()
    } else {
        A * (12.0 * l - B).ln() + C
    }
}

/// Fast conversion from u8 sRGB [0..255] to linear f32 [0.0..1.0].
#[inline]
pub fn srgb_u8_to_linear(v: u8) -> f32 {
    srgb_to_linear_f32(v as f32 / 255.0)
}

/// Fast conversion from linear f32 [0.0..1.0] to u8 sRGB [0..255].
#[inline]
pub fn linear_to_srgb_u8(l: f32) -> u8 {
    (linear_to_srgb_f32(l) * 255.0 + 0.5).clamp(0.0, 255.0) as u8
}

// SMPTE ST 2084 (PQ) constants
const PQ_M1: f32 = 2610.0 / 16384.0;

const PQ_M2: f32 = 2523.0 / 32.0;

const PQ_C1: f32 = 3424.0 / 4096.0;

const PQ_C2: f32 = 2413.0 / 128.0;

const PQ_C3: f32 = 2392.0 / 128.0;

/// Converts PQ encoded value [0.0, 1.0] to absolute linear luminance in Nits [0, 10000].
pub fn pq_to_linear_nits(v: f32) -> f32 {
    let v = v.clamp(0.0, 1.0);
    let vm2 = v.powf(1.0 / PQ_M2);
    let num = (vm2 - PQ_C1).max(0.0);
    let den = PQ_C2 - PQ_C3 * vm2;
    if den <= 0.0 {
        return 10000.0;
    }
    10000.0 * (num / den).powf(1.0 / PQ_M1)
}

/// Converts absolute linear luminance in Nits [0, 10000] to PQ encoded value [0.0, 1.0].
pub fn linear_nits_to_pq(nits: f32) -> f32 {
    let y = (nits.clamp(0.0, 10000.0) / 10000.0).powf(PQ_M1);
    let num = PQ_C1 + PQ_C2 * y;
    let den = 1.0 + PQ_C3 * y;
    (num / den).powf(PQ_M2).clamp(0.0, 1.0)
}

/// Converts an encoded non-linear component in [0.0, 1.0] to normalized linear optical intensity [0.0, 1.0].
#[inline]
pub fn eotf_to_linear(v: f32, transfer: TransferCharacteristics) -> f32 {
    match transfer {
        TransferCharacteristics::Srgb | TransferCharacteristics::Unspecified => {
            srgb_to_linear_f32(v)
        }
        TransferCharacteristics::Linear => v.clamp(0.0, 1.0),
        TransferCharacteristics::Pq => (pq_to_linear_nits(v) / 10000.0).clamp(0.0, 1.0),
        TransferCharacteristics::Hlg => hlg_to_linear_f32(v),
        TransferCharacteristics::Bt709 => bt709_to_linear_f32(v),
    }
}

/// Converts normalized linear optical intensity [0.0, 1.0] to encoded non-linear component [0.0, 1.0].
#[inline]
pub fn oetf_from_linear(l: f32, transfer: TransferCharacteristics) -> f32 {
    match transfer {
        TransferCharacteristics::Srgb | TransferCharacteristics::Unspecified => {
            linear_to_srgb_f32(l)
        }
        TransferCharacteristics::Linear => l.clamp(0.0, 1.0),
        TransferCharacteristics::Pq => linear_nits_to_pq(l * 10000.0),
        TransferCharacteristics::Hlg => linear_to_hlg_f32(l),
        TransferCharacteristics::Bt709 => linear_to_bt709_f32(l),
    }
}

/// BT.2408 diffuse/reference white. HDR signals encode absolute luminance, so
/// tone mapping has to express them relative to this level; SDR signals are
/// already white-relative and need no scaling.
pub const SDR_REFERENCE_WHITE_NITS: f32 = 203.0;

/// HLG peak luminance on the reference 1000-nit display, used to place HLG scene
/// light on the same relative scale as PQ.
pub const HLG_REFERENCE_PEAK_NITS: f32 = 1000.0;

/// Converts an encoded component to linear light **relative to SDR diffuse
/// white**: SDR white is 1.0, and HDR highlights go above 1.0.
///
/// This is the single definition of the "linear" workspace that tone mapping
/// consumes. `eotf_to_linear` instead normalizes each transfer to its own code
/// range (kept in `[0, 1]`), which is what the edit pipeline wants.
#[inline]
pub fn eotf_to_sdr_relative(v: f32, transfer: TransferCharacteristics) -> f32 {
    match transfer {
        TransferCharacteristics::Pq => pq_to_linear_nits(v) / SDR_REFERENCE_WHITE_NITS,
        TransferCharacteristics::Hlg => {
            hlg_to_linear_f32(v) * (HLG_REFERENCE_PEAK_NITS / SDR_REFERENCE_WHITE_NITS)
        }
        _ => eotf_to_linear(v, transfer),
    }
}

/// Applies ITU-R BT.2100 HLG Opto-Optical Transfer Function (OOTF)
/// based on target display peak luminance in Nits.
/// gamma = 1.2 * 1.11^(log2(target_peak_nits / 1000.0))
#[inline]
pub fn hlg_ootf(r: f32, g: f32, b: f32, target_peak_nits: f32) -> (f32, f32, f32) {
    let peak = target_peak_nits.clamp(100.0, 10000.0);
    let log2_ratio = (peak / 1000.0).log2();
    let gamma = 1.2 * 1.11f32.powf(log2_ratio);

    // BT.2020 / BT.2100 luminance weights
    let y_scene = 0.2627 * r.max(0.0) + 0.6780 * g.max(0.0) + 0.0593 * b.max(0.0);
    if y_scene <= 1e-6 {
        return (0.0, 0.0, 0.0);
    }

    let scale = y_scene.powf(gamma - 1.0);
    (r * scale, g * scale, b * scale)
}
