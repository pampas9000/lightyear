import { imageSize } from 'image-size'
import type { SourceProbeSummary } from '@/components/ImageOptionsForm.vue'

let worker: Worker | null = null
const pending = new Map<string, { resolve: (hash: string) => void; reject: (e: Error) => void }>()

function getWorker(): Worker {
    if (worker) return worker
    worker = new Worker(new URL('../workers/sha256.worker.ts', import.meta.url), { type: 'module' })
    worker.onmessage = (e: MessageEvent) => {
        const { id, ok, hash, error } = e.data
        const request = pending.get(id)
        if (!request) return
        pending.delete(id)
        if (ok) request.resolve(hash)
        else request.reject(new Error(error))
    }
    worker.onerror = (e) => {
        const err = new Error(e.message || 'SHA-256 worker failed')
        for (const request of pending.values()) request.reject(err)
        pending.clear()
        worker = null
    }
    return worker
}

/** Hex SHA-256 of a file, computed off the main thread. */
export function sha256File(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
        const id = crypto.randomUUID()
        pending.set(id, { resolve, reject })
        getWorker().postMessage({ id, file })
    })
}

// Headers usually fit in the first 64 KB; a JPEG with a large Exif block or
// embedded thumbnail can push its SOF marker further out.
const PROBE_READS = [64 * 1024, 1024 * 1024]

function formatOf(type: string | undefined, file: File): string {
    if (type === 'jxl-stream') return 'jxl'
    if (type === 'jpg') return 'jpeg'
    // image-size reports the HEIF major brand (avif, avis, heic, heix, mif1, ...).
    if (type?.startsWith('avi')) return 'avif'
    if (type && /^(hei|hev|mif|msf)/.test(type)) return 'heic'
    if (type) return type
    return file.name.split('.').pop()?.toLowerCase() || file.type.split('/')[1] || 'unknown'
}

/**
 * Format and dimensions read from the file header, without decoding pixels.
 * Dimensions are as displayed: JPEG Exif orientations 5–8 swap them.
 */
export async function probeImage(file: File): Promise<SourceProbeSummary> {
    if (!file.type.startsWith('image/') && file.type !== '') {
        return { format: formatOf(undefined, file) }
    }
    for (const length of PROBE_READS) {
        const head = new Uint8Array(await file.slice(0, length).arrayBuffer())
        try {
            const size = imageSize(head)
            const rotated = (size.orientation ?? 1) >= 5
            return {
                format: formatOf(size.type, file),
                width: rotated ? size.height : size.width,
                height: rotated ? size.width : size.height,
            }
        } catch {
            if (length >= file.size) break
        }
    }
    return { format: formatOf(undefined, file) }
}

/** Runs at most `limit` tasks at once; the rest wait in arrival order. */
export function createLimiter(limit: number) {
    let running = 0
    const queue: (() => void)[] = []
    const acquire = () =>
        running < limit ? (running++, Promise.resolve()) : new Promise<void>((r) => queue.push(r))
    // Hand the slot straight to the next waiter so nothing can jump the queue.
    const release = () => {
        const next = queue.shift()
        if (next) next()
        else running--
    }
    return async <T>(task: () => Promise<T>): Promise<T> => {
        await acquire()
        try {
            return await task()
        } finally {
            release()
        }
    }
}
