interface WasmInstance {
    default: (wasmBinaryUrl: string) => Promise<unknown>;
    browser_convert: (data: Uint8Array, format: string, quality: number) => Uint8Array;
    browser_get_metadata: (data: Uint8Array) => string;
    browser_edit_image: (data: Uint8Array, opsJson: string, format: string, quality: number) => Uint8Array;
}

interface InitMessage {
    type: 'init';
    id: string;
    payload: {
        wasmJsUrl: string;
        wasmBinaryUrl: string;
    };
}

interface GetMetadataMessage {
    type: 'get_metadata';
    id: string;
    payload: {
        data: Uint8Array;
    };
}

interface EditImageMessage {
    type: 'edit_image';
    id: string;
    payload: {
        data: Uint8Array;
        opsJson: string;
        format: string;
        quality: number;
    };
}

type WorkerMessage = InitMessage | GetMetadataMessage | EditImageMessage;

let wasmModule: WasmInstance | null = null

self.onmessage = async (e: MessageEvent<WorkerMessage>) => {
    const message = e.data
    const { type, id } = message

    if (type === 'init') {
        const { wasmJsUrl, wasmBinaryUrl } = message.payload
        try {
            // Import the ES module WASM bindings
            const wasm = (await import(/* @vite-ignore */ wasmJsUrl)) as WasmInstance
            
            // Initialize the WebAssembly module with its static binary URL
            await wasm.default(wasmBinaryUrl)
            
            wasmModule = wasm
            self.postMessage({ type: 'init_ok', id })
        } catch (err) {
            const errorMsg = err instanceof Error ? err.message : String(err)
            self.postMessage({
                type: 'init_error',
                id,
                error: errorMsg
            })
        }
    } else if (type === 'get_metadata') {
        const { data } = message.payload
        try {
            if (!wasmModule) {
                throw new Error('WASM module not initialized')
            }
            const result = wasmModule.browser_get_metadata(data)
            self.postMessage({ type: 'get_metadata_ok', id, result })
        } catch (err) {
            const errorMsg = err instanceof Error ? err.message : String(err)
            self.postMessage({
                type: 'get_metadata_error',
                id,
                error: errorMsg
            })
        }
    } else if (type === 'edit_image') {
        const { data, opsJson, format, quality } = message.payload
        try {
            if (!wasmModule) {
                throw new Error('WASM module not initialized')
            }
            const result = wasmModule.browser_edit_image(data, opsJson, format, quality)
            // Transfer result back to main thread to avoid copying
            self.postMessage(
                { type: 'edit_image_ok', id, result },
                [result.buffer] as Transferable[]
            )
        } catch (err) {
            const errorMsg = err instanceof Error ? err.message : String(err)
            self.postMessage({
                type: 'edit_image_error',
                id,
                error: errorMsg
            })
        }
    }
}
