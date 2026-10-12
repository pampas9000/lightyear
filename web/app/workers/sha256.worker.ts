import { createSHA256 } from 'hash-wasm'

// Hashes a File in slices so a large file never sits in memory whole, and
// the page stays responsive while it runs.
const SLICE = 4 * 1024 * 1024

self.onmessage = async (e: MessageEvent<{ id: string; file: File }>) => {
    const { id, file } = e.data
    try {
        const hasher = await createSHA256()
        hasher.init()
        for (let offset = 0; offset < file.size; offset += SLICE) {
            const chunk = await file.slice(offset, offset + SLICE).arrayBuffer()
            hasher.update(new Uint8Array(chunk))
        }
        self.postMessage({ id, ok: true, hash: hasher.digest('hex') })
    } catch (err) {
        self.postMessage({ id, ok: false, error: (err as Error).message })
    }
}
