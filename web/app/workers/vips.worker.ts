// Runs wasm-vips off the main thread for the local sandbox.
import Vips from 'wasm-vips'
import vipsWasm from 'wasm-vips/vips.wasm?url'
import heifWasm from 'wasm-vips/vips-heif.wasm?url'
import jxlWasm from 'wasm-vips/vips-jxl.wasm?url'
import { errorMessage, probe, process } from '~/lib/vips-ops'
import type { ImageOptions } from '~/lib/types/image-options'

type Request =
    | { id: string; type: 'probe'; input: Uint8Array }
    | { id: string; type: 'process'; input: Uint8Array; options: ImageOptions }

const urls: Record<string, string> = {
    'vips.wasm': vipsWasm,
    'vips-heif.wasm': heifWasm,
    'vips-jxl.wasm': jxlWasm,
}

let ready: ReturnType<typeof Vips> | null = null
const load = () => {
    // AVIF/HEIF and JPEG XL are separate libvips modules loaded with the core.
    ready ??= Vips({
        dynamicLibraries: ['vips-heif.wasm', 'vips-jxl.wasm'],
        locateFile: (file: string) => urls[file] ?? file,
    }).then((vips) => {
        // Untrusted input: refuse loaders libvips marks unsafe (as the server
        // does), re-allowing JPEG XL, and keep no cache between files.
        vips.blockUntrusted(true)
        vips.operationBlock('VipsForeignLoadJxl', false)
        vips.operationBlock('VipsForeignSaveJxl', false)
        vips.Cache.max(0)
        return vips
    })
    return ready
}

self.onmessage = async (e: MessageEvent<Request>) => {
    const req = e.data
    try {
        const vips = await load()
        if (req.type === 'probe') {
            self.postMessage({ id: req.id, ok: true, result: probe(vips, req.input) })
        } else {
            const result = process(vips, req.input, req.options)
            self.postMessage({ id: req.id, ok: true, result }, { transfer: [result.bytes.buffer] })
        }
    } catch (err) {
        self.postMessage({ id: req.id, ok: false, error: errorMessage(err) })
    }
}
