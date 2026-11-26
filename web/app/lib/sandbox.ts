// Shared pieces of the browser sandbox page (pages/index.vue).

export type SandboxMetadata = { width: number; height: number; format: string; size: number };
export type SandboxMetrics = { duration: number; size: number; savings: number };

/** An image in the sandbox queue and its latest result. */
export type BatchImageItem = {
    id: string;
    file: File;
    originalUrl: string;
    originalBuffer: Uint8Array | null;
    metadata: SandboxMetadata | null;
    processedUrl: string;
    processedBlob: Blob | null;
    metrics: SandboxMetrics | null;
    error: string;
    status: "pending" | "processing" | "done" | "failed";
    customFilename: string;
    processedFormat?: string;
};

export const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
};

/** File extension for an output format. */
export const extensionFor = (format: string) => (format.toLowerCase() === "jpeg" ? "jpg" : format.toLowerCase());

/** Dotted grid behind previews, so transparency shows. */
export const previewCanvasClass =
    "bg-surface-2 bg-[radial-gradient(var(--hairline-strong)_1px,transparent_1px)] bg-size-[16px_16px] bg-center";
