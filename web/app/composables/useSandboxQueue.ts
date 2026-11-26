import { useVips } from "~/composables/useVips";
import { sanitize } from "~/lib/image-options";
import { extensionFor, type BatchImageItem } from "~/lib/sandbox";
import type { ImageOptions } from "~/lib/types/image-options";

const defaultSandboxOptions = (): ImageOptions => ({ format: "webp" });

const downloadUrl = (url: string, filename: string) => {
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
};

/**
 * The browser sandbox: a queue of local images processed one at a time with
 * wasm-vips under one set of options. Option changes reprocess every image,
 * at once (debounced) when auto-apply is on, otherwise when the user runs it.
 */
export const useSandboxQueue = () => {
    const { probe, process, error: wasmError } = useVips();
    const { t } = useI18n();

    const items = ref<BatchImageItem[]>([]);
    const selectedItemId = ref<string | null>(null);
    const selectedItem = computed(() => items.value.find((i) => i.id === selectedItemId.value) || null);
    /** True while the selected image is being processed. */
    const isProcessing = ref(false);

    const options = ref<ImageOptions>(defaultSandboxOptions());
    const autoApply = ref(false);
    const hasPendingChanges = ref(false);

    let processDebounceTimeout: ReturnType<typeof setTimeout> | null = null;
    let isQueueProcessing = false;

    const clearDebounce = () => {
        if (processDebounceTimeout) {
            clearTimeout(processDebounceTimeout);
            processDebounceTimeout = null;
        }
    };

    const revokeItemUrls = (item: BatchImageItem) => {
        if (item.originalUrl) URL.revokeObjectURL(item.originalUrl);
        if (item.processedUrl) URL.revokeObjectURL(item.processedUrl);
    };

    const revokeUrls = () => {
        items.value.forEach(revokeItemUrls);
    };

    onBeforeUnmount(() => {
        clearDebounce();
        revokeUrls();
    });

    const processSingleItem = async (item: BatchImageItem) => {
        if (!item.originalBuffer) return;
        const startTime = performance.now();
        const requested = sanitize(options.value);
        const result = await process(item.originalBuffer, requested);
        const duration = performance.now() - startTime;

        if (item.processedUrl) {
            URL.revokeObjectURL(item.processedUrl);
        }
        item.processedBlob = new Blob([result.bytes as BlobPart], { type: result.mime });
        item.processedUrl = URL.createObjectURL(item.processedBlob);
        item.processedFormat = requested.format;

        const origSize = item.metadata ? item.metadata.size : item.file.size;
        item.metrics = {
            duration,
            size: result.bytes.length,
            savings: ((result.bytes.length - origSize) / origSize) * 100,
        };
    };

    /** Processes pending items one by one until none is left. */
    const triggerQueueProcessing = async () => {
        if (isQueueProcessing) return;
        isQueueProcessing = true;

        try {
            while (true) {
                const pendingItem = items.value.find((item) => item.status === "pending");
                if (!pendingItem) break;

                pendingItem.status = "processing";
                if (selectedItemId.value === pendingItem.id) {
                    isProcessing.value = true;
                }

                try {
                    await processSingleItem(pendingItem);
                    pendingItem.status = "done";
                } catch (err: any) {
                    console.error(`Error processing item ${pendingItem.id}:`, err);
                    pendingItem.status = "failed";
                    pendingItem.error = err.message || String(err);
                } finally {
                    if (selectedItemId.value === pendingItem.id) {
                        isProcessing.value = false;
                    }
                }
            }
            hasPendingChanges.value = false;
        } finally {
            isQueueProcessing = false;
        }
    };

    /** Reads and probes an item, then queues it for processing. */
    const loadAndProcessItem = async (item: BatchImageItem) => {
        if (!item.originalBuffer) {
            try {
                const buffer = await item.file.arrayBuffer();
                item.originalBuffer = new Uint8Array(buffer);
            } catch (err: any) {
                console.error("Failed to read file buffer:", err);
                item.error = t("wasm_sandbox.read_file_failed", { error: err.message || String(err) });
                item.status = "failed";
                return;
            }
        }

        if (!item.metadata && item.originalBuffer) {
            try {
                const meta = await probe(item.originalBuffer);
                item.metadata = { width: meta.width, height: meta.height, format: meta.format, size: item.file.size };
            } catch (e: any) {
                console.error("wasm-vips probe failed:", e);
                item.error = t("wasm_sandbox.parse_meta_failed", { error: e.message || String(e) });
                item.status = "failed";
                return;
            }
        }

        triggerQueueProcessing();
    };

    const addFiles = (files: File[]) => {
        for (const file of files) {
            const id = Math.random().toString(36).substring(2, 9);
            const url = URL.createObjectURL(file);
            const baseName = file.name.substring(0, file.name.lastIndexOf("."));

            const newItem: BatchImageItem = {
                id,
                file,
                originalUrl: url,
                originalBuffer: null,
                metadata: null,
                processedUrl: "",
                processedBlob: null,
                metrics: null,
                error: "",
                status: "pending",
                customFilename: `${baseName}-processed`,
            };

            items.value.push(newItem);
            if (!selectedItemId.value) {
                selectedItemId.value = id;
            }

            // Asynchronously load and process this item
            loadAndProcessItem(newItem);
        }
    };

    /** Swaps the selected item's source file in place and reprocesses it. */
    const replaceSelected = (file: File) => {
        const activeItem = selectedItem.value;
        if (!activeItem) return;

        revokeItemUrls(activeItem);

        const baseName = file.name.substring(0, file.name.lastIndexOf("."));

        activeItem.file = file;
        activeItem.originalUrl = URL.createObjectURL(file);
        activeItem.originalBuffer = null;
        activeItem.metadata = null;
        activeItem.processedUrl = "";
        activeItem.processedBlob = null;
        activeItem.metrics = null;
        activeItem.error = "";
        activeItem.status = "pending";
        activeItem.customFilename = `${baseName}-processed`;

        loadAndProcessItem(activeItem);
    };

    const removeItem = (id: string) => {
        const idx = items.value.findIndex((item) => item.id === id);
        if (idx === -1) return;
        const item = items.value[idx];
        if (!item) return;
        revokeItemUrls(item);
        items.value.splice(idx, 1);

        if (selectedItemId.value === id) {
            if (items.value.length > 0) {
                const fallbackItem = items.value[Math.max(0, idx - 1)];
                if (fallbackItem) {
                    selectedItemId.value = fallbackItem.id;
                }
            } else {
                selectedItemId.value = null;
            }
        }
    };

    const selectItem = (id: string) => {
        selectedItemId.value = id;
    };

    /** Empties the queue and restores the default options. */
    const reset = () => {
        revokeUrls();
        items.value = [];
        selectedItemId.value = null;
        options.value = defaultSandboxOptions();
        autoApply.value = false;
        hasPendingChanges.value = false;
    };

    const reprocessAllItems = () => {
        items.value.forEach((item) => {
            item.status = "pending";
        });
        triggerQueueProcessing();
    };

    const debouncedProcess = () => {
        clearDebounce();
        processDebounceTimeout = setTimeout(() => {
            reprocessAllItems();
        }, 250);
    };

    const toggleAutoApply = () => {
        autoApply.value = !autoApply.value;
        if (autoApply.value && hasPendingChanges.value) {
            debouncedProcess();
            hasPendingChanges.value = false;
        }
    };

    const runManualProcess = async () => {
        if (isQueueProcessing) return;
        reprocessAllItems();
        hasPendingChanges.value = false;
    };

    watch(
        options,
        () => {
            hasPendingChanges.value = true;
            if (autoApply.value) {
                debouncedProcess();
            }
        },
        { deep: true },
    );

    /** Saves the selected item's result under the given name (without extension). */
    const downloadSelected = (customFilename: string) => {
        const item = selectedItem.value;
        if (!item?.processedBlob) return;
        const ext = extensionFor(item.processedFormat || options.value.format);
        downloadUrl(item.processedUrl, `${customFilename || "processed-image"}.${ext}`);
    };

    /** Saves every finished result in one zip. */
    const downloadAll = async () => {
        const { zipSync } = await import("fflate");
        const files: Record<string, Uint8Array> = {};

        for (const item of items.value) {
            if (item.processedBlob && item.status === "done") {
                const buffer = new Uint8Array(await item.processedBlob.arrayBuffer());
                const ext = extensionFor(item.processedFormat || options.value.format);
                files[`${item.customFilename || "processed"}.${ext}`] = buffer;
            }
        }

        if (Object.keys(files).length === 0) return;

        const zipped = zipSync(files);
        const zipUrl = URL.createObjectURL(new Blob([zipped], { type: "application/zip" }));
        downloadUrl(zipUrl, "transcoded-images.zip");
        URL.revokeObjectURL(zipUrl);
    };

    return {
        wasmError,
        items,
        selectedItemId,
        selectedItem,
        isProcessing,
        options,
        autoApply,
        hasPendingChanges,
        addFiles,
        replaceSelected,
        removeItem,
        selectItem,
        reset,
        toggleAutoApply,
        runManualProcess,
        downloadSelected,
        downloadAll,
    };
};
