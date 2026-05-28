import { ref } from 'vue'
import { useRuntimeConfig } from '#app'

export interface ComputeWasmModule {
    browser_convert: (data: Uint8Array, format: string, quality: number) => Uint8Array;
    browser_get_metadata: (data: Uint8Array) => string;
    browser_edit_image: (data: Uint8Array, opsJson: string, format: string, quality: number) => Uint8Array;
}

// Module-level globals (singleton worker across component mounts)
let worker: Worker | null = null
let initPromise: Promise<void> | null = null
const isReady = ref(false)
const isLoading = ref(false)
const error = ref<Error | null>(null)

// Callback tracking mapping
type WorkerRequest = {
    resolve: (val: any) => void
    reject: (err: Error) => void
}
const activeRequests = new Map<string, WorkerRequest>()

function getWorker(): Worker {
    if (!worker && import.meta.client) {
        worker = new Worker(
            new URL('../workers/compute.worker.ts', import.meta.url),
            { type: 'module' }
        )

        worker.onmessage = (e: MessageEvent) => {
            const { type, id, result, error: workerErr } = e.data
            const request = activeRequests.get(id)
            if (!request) return

            activeRequests.delete(id)
            if (type.endsWith('_ok')) {
                request.resolve(result)
            } else {
                request.reject(new Error(workerErr || 'Worker operation failed'))
            }
        }

        worker.onerror = (e) => {
            console.error('Web Worker general error:', e)
            error.value = new Error('Web Worker general error')
        }
    }
    if (!worker) {
        throw new Error('Web Worker cannot be initialized on server-side')
    }
    return worker
}

export function useComputeWasm() {
    const initWasm = async (): Promise<void> => {
        if (!import.meta.client) return

        if (isReady.value) return

        if (!initPromise) {
            isLoading.value = true
            error.value = null

            initPromise = (async () => {
                try {
                    const config = useRuntimeConfig()
                    const baseURL = config.app?.baseURL || '/'
                    const cleanBaseURL = baseURL.endsWith('/') ? baseURL : `${baseURL}/`

                    const wasmJsUrl = `${cleanBaseURL}wasm/compute_wasm.js`
                    const wasmBinaryUrl = `${cleanBaseURL}wasm/compute_wasm_bg.wasm`

                    const w = getWorker()
                    const id = Math.random().toString(36).substring(2, 9)

                    await new Promise<void>((resolve, reject) => {
                        activeRequests.set(id, {
                            resolve: () => resolve(),
                            reject: (err) => reject(err)
                        })
                        w.postMessage({
                            type: 'init',
                            id,
                            payload: { wasmJsUrl, wasmBinaryUrl }
                        })
                    })

                    isReady.value = true
                } catch (e: any) {
                    error.value = e
                    initPromise = null // Allow retry on failure
                    throw e
                } finally {
                    isLoading.value = false
                }
            })()
        }

        return initPromise
    }

    const getMetadata = async (data: Uint8Array): Promise<string> => {
        if (!import.meta.client) throw new Error('Cannot run in SSR mode')
        await initWasm()
        const w = getWorker()
        const id = Math.random().toString(36).substring(2, 9)

        return new Promise<string>((resolve, reject) => {
            activeRequests.set(id, { resolve, reject })
            w.postMessage({
                type: 'get_metadata',
                id,
                payload: { data }
            })
        })
    }

    const editImage = async (
        data: Uint8Array,
        opsJson: string,
        format: string,
        quality: number
    ): Promise<Uint8Array> => {
        if (!import.meta.client) throw new Error('Cannot run in SSR mode')
        await initWasm()
        const w = getWorker()
        const id = Math.random().toString(36).substring(2, 9)

        return new Promise<Uint8Array>((resolve, reject) => {
            activeRequests.set(id, { resolve, reject })
            w.postMessage({
                type: 'edit_image',
                id,
                payload: { data, opsJson, format, quality }
            })
        })
    }

    return {
        isReady,
        isLoading,
        error,
        initWasm,
        getMetadata,
        editImage
    }
}
