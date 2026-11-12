// Helpers for editing ImageOptions in the UI. The server validates and
// canonicalizes again; these only keep the form from offering settings that do
// not apply (see docs/contracts/image-options.md).
import type { ImageOptions, OutputFormat } from '~/lib/types/image-options'

export const FORMATS: OutputFormat[] = ['avif', 'jxl', 'webp', 'jpeg', 'png', 'heic']

/** Quality the server uses when none is given; null where there is none. */
export const DEFAULT_QUALITY: Record<OutputFormat, number | null> = {
    avif: 60,
    heic: 60,
    jxl: 80,
    webp: 80,
    jpeg: 82,
    png: null,
}

export const hasQuality = (o: ImageOptions) => !o.lossless && o.format !== 'png'
/** JXL can be tuned by Butteraugli distance instead of quality. */
export const hasDistance = (o: ImageOptions) => o.format === 'jxl' && !o.lossless

export const MIN_DISTANCE = 0.1
export const MAX_DISTANCE = 25

/** Rounds to the two decimals the contract allows, within 0.1–25. */
export const clampDistance = (d: number) =>
    Math.min(MAX_DISTANCE, Math.max(MIN_DISTANCE, Math.round(d * 100) / 100))

/**
 * libvips' quality → distance mapping for jxlsave (libjxl's), so switching
 * modes keeps the same encode. Q100 is distance 0, i.e. lossless, which a
 * distance cannot express; it lands on the 0.1 floor instead.
 */
export const qualityToDistance = (q: number) =>
    clampDistance(q >= 30 ? 0.1 + (100 - q) * 0.09 : (53 / 3000) * q * q - (23 / 20) * q + 25)
/** Inverse of qualityToDistance, rounded to the nearest quality. */
export function distanceToQuality(d: number): number {
    const a = 53 / 3000
    const q = d <= 6.4 ? 100 - (d - 0.1) / 0.09 : (23 / 20 - Math.sqrt((23 / 20) ** 2 - 4 * a * (25 - d))) / (2 * a)
    return Math.min(100, Math.max(1, Math.round(Number.isNaN(q) ? 1 : q)))
}
export const hasLossless = (format: OutputFormat) => format !== 'jpeg'
export const hasEffort = (format: OutputFormat) => format !== 'jpeg'
export const hasChroma = (o: ImageOptions) => ['avif', 'heic', 'jpeg'].includes(o.format) && !o.lossless
/** Depths that can be asked for; empty where the output follows the source. */
export const bitDepths = (format: OutputFormat): (8 | 10 | 12 | 16)[] =>
    format === 'avif' || format === 'heic' ? [8, 10, 12] : format === 'jxl' ? [8, 16] : []
export const hasBitDepth = (format: OutputFormat) => bitDepths(format).length > 0

export const defaultOptions = (): ImageOptions => ({ format: 'avif' })

/** Returns a copy without fields that do not apply to the current choices. */
export function sanitize(o: ImageOptions): ImageOptions {
    const out: ImageOptions = { format: o.format }
    if (o.lossless && hasLossless(o.format)) out.lossless = true
    if (o.distance != null && hasDistance(out)) out.distance = clampDistance(o.distance)
    else if (o.quality != null && hasQuality(out)) out.quality = o.quality
    if (o.effort != null && hasEffort(o.format)) out.effort = o.effort
    if (o.resize && (o.resize.width || o.resize.height)) {
        const both = !!(o.resize.width && o.resize.height)
        out.resize = {
            ...(o.resize.width ? { width: o.resize.width } : {}),
            ...(o.resize.height ? { height: o.resize.height } : {}),
            fit: both ? (o.resize.fit ?? 'inside') : 'inside',
            enlarge: !!o.resize.enlarge,
        }
    }
    if (o.color) out.color = o.color
    if (o.dynamic_range) out.dynamic_range = o.dynamic_range
    if (o.metadata) out.metadata = o.metadata
    if (o.chroma && hasChroma(out)) out.chroma = o.chroma
    if (o.bit_depth && bitDepths(o.format).includes(o.bit_depth)) out.bit_depth = o.bit_depth
    return out
}

/** Short human-readable summary, e.g. "AVIF · Q60 · ≤1600px". */
export function describe(o: ImageOptions | undefined | null): string {
    if (!o) return ''
    const parts = [o.format.toUpperCase()]
    if (o.lossless) parts.push('lossless')
    else if (o.distance != null && hasDistance(o)) parts.push(`d${o.distance}`)
    else if (hasQuality(o)) parts.push(`Q${o.quality ?? DEFAULT_QUALITY[o.format]}`)
    const r = o.resize
    if (r && (r.width || r.height)) {
        if (r.width && r.height) parts.push(`${r.width}×${r.height} ${r.fit ?? 'inside'}`)
        else parts.push(`≤${r.width ?? r.height}px`)
    }
    if (o.color === 'srgb') parts.push('sRGB')
    if (o.dynamic_range === 'sdr') parts.push('SDR')
    return parts.join(' · ')
}
