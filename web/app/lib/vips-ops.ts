// Browser twin of compute/crates/imageops: applies ImageOptions with
// wasm-vips. Keep the two in step; both follow docs/contracts/image-options.md.
// Differences (checked by scripts/consistency.ts):
// - there is no cjxl here, so lossless JPEG -> JXL re-encodes pixels instead of
//   repacking the JPEG;
// - wasm-vips has no HEVC codec: HEIC can be neither read nor written;
// - its JPEG XL decoder rejects files carrying an Exif or XMP box.
import type VipsModule from 'wasm-vips'
import type { ImageOptions, OutputFormat } from '~/lib/types/image-options'
import { DEFAULT_QUALITY, FORMATS } from '~/lib/image-options'

type Vips = Awaited<ReturnType<typeof VipsModule>>
type Image = InstanceType<Vips['Image']>

export interface ProcessResult {
    bytes: Uint8Array
    mime: string
    width: number
    height: number
}

export interface Probe {
    width: number
    height: number
    format: string
}

export const MAX_PIXELS = 100_000_000
export const BROWSER_FORMATS = FORMATS.filter((f) => f !== 'heic')
const UNBOUNDED = 10_000_000
const KEEP = { exif: 1, xmp: 2, iptc: 4, icc: 8, gainmap: 32 }

const MIME: Record<OutputFormat, string> = {
    avif: 'image/avif',
    heic: 'image/heic',
    jxl: 'image/jxl',
    webp: 'image/webp',
    jpeg: 'image/jpeg',
    png: 'image/png',
}

/** libvips errors arrive as `WebAssembly.Exception` with `message: [type, text]`. */
export function errorMessage(err: unknown): string {
    const message = (err as { message?: unknown } | null)?.message
    if (Array.isArray(message)) return String(message[1] ?? message[0]).trim()
    return typeof message === 'string' ? message : String(err)
}

/** CICP transfer 16 (PQ) / 18 (HLG): libvips would write these as sRGB. */
export function hasHdrTransfer(input: Uint8Array, loader: string): boolean {
    const find = (needle: string) => {
        const n = new TextEncoder().encode(needle)
        outer: for (let i = 0; i + n.length <= input.length; i++) {
            for (let j = 0; j < n.length; j++) if (input[i + j] !== n[j]) continue outer
            return i + n.length
        }
        return -1
    }
    let transfer = -1
    if (loader.startsWith('heifload')) {
        const at = find('colrnclx')
        if (at >= 0 && at + 4 <= input.length) transfer = (input[at + 2]! << 8) | input[at + 3]!
    } else if (loader.startsWith('pngload')) {
        const at = find('cICP')
        if (at >= 0 && at + 1 < input.length) transfer = input[at + 1]!
    }
    return transfer === 16 || transfer === 18
}

export function probe(vips: Vips, input: Uint8Array): Probe {
    using image = vips.Image.newFromBuffer(input)
    const loader = image.getString('vips-loader')
    return { width: image.width, height: image.height, format: loader.replace(/load.*$/, '') }
}

/**
 * Spreads the shared 1-10 effort over an encoder's own range, as the option
 * `name`; an unset effort passes nothing, leaving libvips' own default.
 */
function effort(name: string, o: ImageOptions, low: number, high: number) {
    if (o.effort == null) return {}
    const e = Math.min(10, Math.max(1, o.effort))
    return { [name]: low + Math.floor(((e - 1) * (high - low) + 4) / 9) }
}

export function process(vips: Vips, input: Uint8Array, o: ImageOptions): ProcessResult {
    using source = vips.Image.newFromBuffer(input)
    if (source.width * source.height > MAX_PIXELS) {
        throw new Error(`${source.width * source.height} pixels exceeds the limit of ${MAX_PIXELS}`)
    }
    const loader = source.getString('vips-loader')
    if (hasHdrTransfer(input, loader)) throw new Error('HDR (PQ/HLG) sources are not supported yet')
    const hasGainMap = source.getTypeof('gainmap-data') !== 0
    // thumbnail always produces 8-bit, so deeper sources keep their precision
    // through the explicit path.
    using image = source.format === 'uchar' ? thumbnail(vips, input, o) : fullDepth(vips, input, o)
    const bytes = save(image, o, hasGainMap)
    return { bytes, mime: MIME[o.format], width: image.width, height: image.height }
}

function thumbnail(vips: Vips, input: Uint8Array, o: ImageOptions): Image {
    const r = o.resize
    const size = !r ? 'down' : r.fit === 'fill' ? 'force' : r.enlarge ? 'both' : 'down'
    return vips.Image.thumbnailBuffer(input, r?.width ?? UNBOUNDED, {
        height: r?.height ?? UNBOUNDED,
        size,
        fail_on: 'error',
        ...(r && r.fit === 'cover' ? { crop: 'centre' } : {}),
        ...(o.color === 'srgb' ? { input_profile: 'srgb', output_profile: 'srgb' } : {}),
    })
}

function fullDepth(vips: Vips, input: Uint8Array, o: ImageOptions): Image {
    let image = vips.Image.newFromBuffer(input, '', { fail_on: 'error' }).autorot()
    const r = o.resize
    if (r) {
        const tw = r.width ?? Infinity
        const th = r.height ?? Infinity
        if (r.fit === 'fill') {
            image = image.resize(tw / image.width, { vscale: th / image.height })
        } else {
            let scale = r.fit === 'cover'
                ? Math.max(tw / image.width, th / image.height)
                : Math.min(tw / image.width, th / image.height)
            if (!r.enlarge) scale = Math.min(scale, 1)
            if (scale !== 1) image = image.resize(scale)
            if (r.fit === 'cover') {
                const cw = Math.min(tw, image.width)
                const ch = Math.min(th, image.height)
                image = image.extractArea(Math.floor((image.width - cw) / 2), Math.floor((image.height - ch) / 2), cw, ch)
            }
        }
    }
    if (o.color === 'srgb') {
        image = image.iccTransform('srgb', { input_profile: 'srgb', embedded: true, depth: 16 })
    }
    return image
}

function save(image: Image, o: ImageOptions, hasGainMap: boolean): Uint8Array {
    const keepGainMap = hasGainMap && (o.dynamic_range ?? 'keep') === 'keep'
    let keep = KEEP.icc
    if (o.metadata === 'keep') keep |= KEEP.exif | KEEP.xmp | KEEP.iptc
    if (keepGainMap) keep |= KEEP.gainmap
    const quality = o.lossless || o.format === 'png' ? undefined : o.quality ?? DEFAULT_QUALITY[o.format] ?? undefined
    const common = { keep, ...(quality != null ? { Q: quality } : {}) }
    const deep = image.format === 'ushort' || image.format === 'float'
    const subsample = o.chroma === '420' ? 'on' : o.chroma === '444' ? 'off' : 'auto'
    switch (o.format) {
        case 'avif':
        case 'heic':
            return image.heifsaveBuffer({
                ...common,
                compression: o.format === 'avif' ? 'av1' : 'hevc',
                lossless: !!o.lossless,
                ...effort('effort', o, 0, 9),
                bitdepth: o.bit_depth ?? (deep ? 10 : 8),
                subsample_mode: subsample,
            })
        case 'jxl':
            return image.jxlsaveBuffer({ ...common, lossless: !!o.lossless, ...effort('effort', o, 1, 9) })
        case 'webp':
            return image.webpsaveBuffer({ ...common, lossless: !!o.lossless, ...effort('effort', o, 0, 6) })
        case 'png':
            return image.pngsaveBuffer({ keep, ...effort('compression', o, 0, 9), bitdepth: deep ? 16 : 8 })
        case 'jpeg':
            if (keepGainMap) return image.uhdrsaveBuffer(common)
            return image.jpegsaveBuffer({ ...common, subsample_mode: subsample, optimize_coding: true, background: [255] })
    }
}
