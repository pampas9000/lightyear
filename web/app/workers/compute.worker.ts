interface WasmInstance {
    default: (wasmBinaryUrl: string) => Promise<unknown>;
    browser_convert: (data: Uint8Array, format: string, quality: number) => Uint8Array;
    browser_convert_advanced: (
        data: Uint8Array,
        format: string,
        quality: number,
        colorJson?: string
    ) => Uint8Array;
    browser_decode_raw_pixels: (data: Uint8Array) => {
        width: number;
        height: number;
        is_16bit: boolean;
        optimal_chroma: string;
        pixels: Uint8Array;
    };
    browser_get_metadata: (data: Uint8Array) => string;
    browser_edit_image: (data: Uint8Array, opsJson: string, format: string, quality: number, colorJson?: string) => Uint8Array;
    browser_edit_to_raw_rgba: (data: Uint8Array, opsJson: string, colorJson?: string) => {
        width: number;
        height: number;
        pixels: Uint8Array;
    };
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
        color?: Record<string, any>;
    };
}

interface ConvertImageMessage {
    type: 'convert';
    id: string;
    payload: {
        data: Uint8Array;
        format: string;
        quality: number;
        color?: Record<string, any>;
    };
}

type WorkerMessage = InitMessage | GetMetadataMessage | EditImageMessage | ConvertImageMessage;

let wasmModule: WasmInstance | null = null

/// Converts a little-endian u16 RGBA buffer (stored as bytes) into 8-bit RGBA.
function u16BufferToRgba8(bytes: Uint8Array): Uint8ClampedArray {
    const u16 = new Uint16Array(bytes.buffer, bytes.byteOffset, bytes.byteLength / 2)
    const out = new Uint8ClampedArray(u16.length)
    for (let i = 0; i < u16.length; i++) {
        out[i] = Math.round(((u16[i] ?? 0) / 65535) * 255)
    }
    return out
}

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
        const { data, opsJson, format, quality, color } = message.payload
        try {
            if (!wasmModule) {
                throw new Error('WASM module not initialized')
            }

            const targetFmt = format.toLowerCase()
            const colorJson = color ? JSON.stringify(color) : undefined

            // If target is JXL, compute-wasm applies edits to pixels, then @jsquash/jxl encodes into JXL container
            if (targetFmt === 'jxl') {
                const edited = wasmModule.browser_edit_to_raw_rgba(data, opsJson, colorJson)
                const jxlModule = await import('@jsquash/jxl')
                const imageData = {
                    data: new Uint8ClampedArray(edited.pixels.buffer, edited.pixels.byteOffset, edited.pixels.byteLength),
                    width: edited.width,
                    height: edited.height,
                }
                const jxlBuffer = await jxlModule.encode(imageData as any, {
                    quality: quality ?? 85,
                } as any)
                const result = new Uint8Array(jxlBuffer)
                self.postMessage(
                    { type: 'edit_image_ok', id, result },
                    [result.buffer] as Transferable[]
                )
                return
            }

            const result = wasmModule.browser_edit_image(data, opsJson, format, quality, colorJson)
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
    } else if (type === 'convert') {
        const { data, format, quality, color } = message.payload
        try {
            const targetFmt = format.toLowerCase()

            // Pixel transcode via compute-wasm; JXL is encoded by @jsquash/jxl, which
            // only accepts pixels (no JPEG reconstruction).
            if (!wasmModule) {
                throw new Error('WASM module not initialized')
            }

            let result: Uint8Array
            if (targetFmt === 'jxl') {
                const decoded = wasmModule.browser_decode_raw_pixels(data)
                const pixels8 = decoded.is_16bit
                    ? u16BufferToRgba8(decoded.pixels)
                    : new Uint8ClampedArray(decoded.pixels.buffer, decoded.pixels.byteOffset, decoded.pixels.byteLength)
                const jxlModule = await import('@jsquash/jxl')
                const imageData = {
                    data: pixels8,
                    width: decoded.width,
                    height: decoded.height,
                }
                const jxlBuffer = await jxlModule.encode(imageData as any, {
                    quality: quality ?? 85,
                } as any)
                result = new Uint8Array(jxlBuffer)
            } else if (color) {
                const colorJson = JSON.stringify(color)
                result = wasmModule.browser_convert_advanced(data, targetFmt, quality, colorJson)
            } else {
                result = wasmModule.browser_convert(data, targetFmt, quality)
            }

            self.postMessage(
                { type: 'convert_ok', id, result },
                [result.buffer] as Transferable[]
            )
        } catch (err) {
            const errorMsg = err instanceof Error ? err.message : String(err)
            self.postMessage({
                type: 'convert_error',
                id,
                error: errorMsg
            })
        }
    }
}
