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
export const hasLossless = (format: OutputFormat) => format !== 'jpeg'
export const hasEffort = (format: OutputFormat) => format !== 'jpeg'
export const hasChroma = (o: ImageOptions) => ['avif', 'heic', 'jpeg'].includes(o.format) && !o.lossless
export const hasBitDepth = (format: OutputFormat) => format === 'avif' || format === 'heic'

export const defaultOptions = (): ImageOptions => ({ format: 'avif' })

/** Returns a copy without fields that do not apply to the current choices. */
export function sanitize(o: ImageOptions): ImageOptions {
    const out: ImageOptions = { format: o.format }
    if (o.lossless && hasLossless(o.format)) out.lossless = true
    if (o.quality != null && hasQuality(out)) out.quality = o.quality
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
    if (o.bit_depth && hasBitDepth(o.format)) out.bit_depth = o.bit_depth
    return out
}

/** Short human-readable summary, e.g. "AVIF · Q60 · ≤1600px". */
export function describe(o: ImageOptions | undefined | null): string {
    if (!o) return ''
    const parts = [o.format.toUpperCase()]
    if (o.lossless) parts.push('lossless')
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
