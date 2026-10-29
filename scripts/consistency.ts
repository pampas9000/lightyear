// Server/browser consistency: runs the same fixture + ImageOptions through
// compute-imageops (Rust, system libvips) and web/app/lib/vips-ops.ts
// (wasm-vips), then compares the artifacts. Any difference, error or missing
// tool fails the run.
//
//   task test:consistency     (node scripts/consistency.ts)
//
// Needs Node >= 23.6 (TypeScript type stripping, `using`) and cargo.
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))

// vips-ops.ts uses Nuxt's `~/` alias.
registerHooks({
    resolve(specifier, context, next) {
        if (specifier.startsWith('~/')) specifier = join(root, 'web/app', `${specifier.slice(2)}.ts`)
        return next(specifier, context)
    },
})

const { default: Vips } = await import(join(root, 'web/node_modules/wasm-vips/lib/vips-node.mjs'))
const ops = await import(join(root, 'web/app/lib/vips-ops.ts'))

type Options = Record<string, unknown> & { format: string }
interface Case {
    fixture: string
    options: Options
    /** Both sides must reject the input instead of producing an artifact. */
    rejected?: boolean
    /** A known wasm-vips gap: the server must succeed and the browser fail. */
    browserGap?: string
    /** Mean absolute pixel difference allowed, on a 0-255 scale. */
    tolerance?: number
}

// The server encodes AVIF with libavif, the browser with libvips heifsave: two
// independent lossy AV1 encodes, each about 2.8 from the source at Q60, land
// about 2 apart without any shift in color (measured on webkit_iceland_p3.jpg).
const AVIF_ENCODERS = 3

const CASES: Case[] = [
    { fixture: 'quality/jpeg_420_90.jpg', options: { format: 'avif' } },
    { fixture: 'quality/jpeg_420_90.jpg', options: { format: 'jpeg', chroma: '444', quality: 90 } },
    { fixture: 'quality/checker_rb_444.png', options: { format: 'webp', lossless: true } },
    { fixture: 'quality/checker_rb_444.png', options: { format: 'jxl', quality: 90 } },
    { fixture: 'quality/gradient_sunset_8bit.png', options: { format: 'png' } },
    { fixture: 'quality/gradient_sunset_8bit.png', options: { format: 'jpeg' } },
    { fixture: 'quality/avif_8bit_420_alpha.avif', options: { format: 'webp', lossless: true } },
    { fixture: 'quality/gradient_gray16bit.png', options: { format: 'avif', resize: { width: 512 } } },
    { fixture: 'quality/gradient_gray16bit.png', options: { format: 'png', resize: { width: 300, height: 300, fit: 'cover' } } },
    { fixture: 'quality/p3_gradient.png', options: { format: 'png', color: 'srgb' } },
    { fixture: 'quality/jxl_lossy_16bit.jxl', options: { format: 'png' } },
    { fixture: 'quality/heic_yuv_85.heic', options: { format: 'webp' }, browserGap: 'no HEVC decoder' },
    { fixture: 'quality/checker_rb_444.png', options: { format: 'heic' }, browserGap: 'no HEVC encoder' },
    { fixture: 'external/webkit_iceland_p3.jpg', options: { format: 'webp', color: 'srgb', resize: { width: 800 } } },
    { fixture: 'external/webkit_iceland_p3.jpg', options: { format: 'avif', metadata: 'keep' }, tolerance: AVIF_ENCODERS },
    { fixture: 'hd_benchmark/landscape_mountains_4k.jpg', options: { format: 'avif', resize: { width: 300, height: 300, fit: 'cover' } }, tolerance: AVIF_ENCODERS },
    { fixture: 'quality/checker_rb_444.png', options: { format: 'avif', lossless: true }, tolerance: 0 },
    { fixture: 'quality/avif_8bit_420_alpha.avif', options: { format: 'avif' }, tolerance: AVIF_ENCODERS },
    { fixture: 'hd_benchmark/landscape_mountains_4k.jpg', options: { format: 'jpeg', resize: { width: 120, height: 300, fit: 'fill' } } },
    { fixture: 'external/aom_apple_gainmap_new.jpg', options: { format: 'jpeg', resize: { width: 400 } } },
    { fixture: 'external/aom_apple_gainmap_new.jpg', options: { format: 'jpeg', dynamic_range: 'sdr' } },
    // With metadata "keep" the server repacks the JPEG with cjxl instead; that
    // output carries Exif, which wasm-vips cannot decode (see vips-ops.ts).
    { fixture: 'external/webkit_rose_srgb.jpg', options: { format: 'jxl', lossless: true } },
    { fixture: 'quality/hdr_pq_10bit.avif', options: { format: 'avif' }, rejected: true },
    { fixture: 'quality/hdr_hlg_10bit.avif', options: { format: 'png' }, rejected: true },
]

const DEFAULT_TOLERANCE = 1.5

const vips = await Vips({ dynamicLibraries: ['vips-heif.wasm', 'vips-jxl.wasm'] })
vips.blockUntrusted(true)
vips.operationBlock('VipsForeignLoadJxl', false)
vips.operationBlock('VipsForeignSaveJxl', false)
vips.Cache.max(0)

const build = spawnSync('cargo', ['build', '--quiet', '--release', '-p', 'compute-imageops', '--example', 'process'], {
    cwd: root,
    stdio: 'inherit',
})
if (build.status !== 0) {
    console.error('cargo build failed')
    process.exit(1)
}
const serverBin = join(root, 'target/release/examples/process')
const work = mkdtempSync(join(tmpdir(), 'lightyear-consistency-'))

type Outcome = { bytes: Uint8Array; mime: string; width: number; height: number } | { error: string }

function server(input: string, options: Options): Outcome {
    const out = join(work, 'server.bin')
    const run = spawnSync(serverBin, [input, JSON.stringify(options), out], { encoding: 'utf8' })
    if (run.error) throw run.error
    const summary = JSON.parse(run.stdout)
    if (summary.error) return { error: summary.message }
    return { ...summary, bytes: new Uint8Array(readFileSync(out)) }
}

function browser(input: string, options: Options): Outcome {
    try {
        return ops.process(vips, new Uint8Array(readFileSync(input)), options)
    } catch (e) {
        return { error: ops.errorMessage(e) }
    }
}

/** Properties an artifact must share across the two implementations. */
function describe(bytes: Uint8Array) {
    using image = vips.Image.newFromBuffer(bytes)
    const field = (name: string) => image.getTypeof(name) !== 0
    return {
        loader: image.getString('vips-loader'),
        width: image.width,
        height: image.height,
        bands: image.bands,
        format: image.format,
        bits: field('bits-per-sample') ? image.getInt('bits-per-sample') : null,
        icc: field('icc-profile-data'),
        exif: field('exif-data'),
        gainmap: field('gainmap-data'),
        chroma: field('jpeg-chroma-subsample') ? image.getString('jpeg-chroma-subsample') : null,
    }
}

/** Mean absolute difference of the decoded pixels, on a 0-255 scale. */
function pixelDifference(a: Uint8Array, b: Uint8Array): number {
    using x = vips.Image.newFromBuffer(a)
    using y = vips.Image.newFromBuffer(b)
    const scale = x.format === 'ushort' ? 1 / 257 : 1
    using diff = x.subtract(y).abs()
    return diff.avg() * scale
}

let failures = 0
for (const c of CASES) {
    const label = `${c.fixture} ${JSON.stringify(c.options)}`
    const input = join(root, 'scripts/fixtures', c.fixture)
    const problems: string[] = []
    const s = server(input, c.options)
    const b = browser(input, c.options)
    if (c.browserGap) {
        if ('error' in s) problems.push(`server: ${s.error}`)
        if (!('error' in b)) problems.push(`browser now succeeds (${c.browserGap}): update vips-ops.ts and this case`)
    } else if (c.rejected) {
        if (!('error' in s)) problems.push('server produced an artifact')
        if (!('error' in b)) problems.push('browser produced an artifact')
    } else if ('error' in s || 'error' in b) {
        if ('error' in s) problems.push(`server: ${s.error}`)
        if ('error' in b) problems.push(`browser: ${b.error}`)
    } else {
        if (s.mime !== b.mime) problems.push(`mime ${s.mime} vs ${b.mime}`)
        try {
            const ds = describe(s.bytes)
            const db = describe(b.bytes)
            for (const key of Object.keys(ds) as (keyof typeof ds)[]) {
                if (ds[key] !== db[key]) problems.push(`${key} ${ds[key]} vs ${db[key]}`)
            }
            if (problems.length === 0) {
                const diff = pixelDifference(s.bytes, b.bytes)
                if (diff > (c.tolerance ?? DEFAULT_TOLERANCE)) problems.push(`pixels differ by ${diff.toFixed(2)} on average`)
            }
        } catch (e) {
            problems.push(`comparing artifacts: ${ops.errorMessage(e)}`)
        }
    }
    if (problems.length) failures++
    console.log(`${problems.length ? 'FAIL' : 'ok  '}  ${label}${problems.length ? `\n        ${problems.join('\n        ')}` : ''}`)
}

rmSync(work, { recursive: true, force: true })
console.log(`\n${CASES.length - failures}/${CASES.length} consistent`)
process.exit(failures ? 1 : 0)
