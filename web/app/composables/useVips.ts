import { ref } from 'vue'
import type { ImageOptions } from '~/lib/types/image-options'
import type { Probe, ProcessResult } from '~/lib/vips-ops'

// One worker for the whole app; wasm-vips is ~4 MB and loads on first use.
let worker: Worker | null = null
const pending = new Map<string, { resolve: (v: any) => void; reject: (e: Error) => void }>()
const error = ref<Error | null>(null)

function getWorker(): Worker {
    if (worker) return worker
    if (!globalThis.crossOriginIsolated) {
        // wasm-vips needs SharedArrayBuffer, which requires COOP/COEP headers.
        throw new Error('This browser page is not cross-origin isolated, so the local sandbox cannot run.')
    }
    worker = new Worker(new URL('../workers/vips.worker.ts', import.meta.url), { type: 'module' })
    worker.onmessage = (e: MessageEvent) => {
        const { id, ok, result, error: message } = e.data
        const request = pending.get(id)
        if (!request) return
        pending.delete(id)
        if (ok) request.resolve(result)
        else request.reject(new Error(message))
    }
    worker.onerror = (e) => {
        error.value = new Error(e.message || 'wasm-vips worker failed')
        for (const request of pending.values()) request.reject(error.value)
        pending.clear()
    }
    return worker
}

function call<T>(message: Record<string, unknown>, transfer: Transferable[] = []): Promise<T> {
    return new Promise<T>((resolve, reject) => {
        try {
            const id = crypto.randomUUID()
            pending.set(id, { resolve, reject })
            getWorker().postMessage({ id, ...message }, transfer)
        } catch (err) {
            error.value = err as Error
            reject(err)
        }
    })
}

export function useVips() {
    return {
        error,
        probe: (input: Uint8Array) => call<Probe>({ type: 'probe', input: input.slice() }),
        // The input is copied so the caller keeps its buffer for re-processing.
        process: (input: Uint8Array, options: ImageOptions) => {
            const copy = input.slice()
            return call<ProcessResult>({ type: 'process', input: copy, options: JSON.parse(JSON.stringify(options)) }, [copy.buffer])
        },
    }
}
