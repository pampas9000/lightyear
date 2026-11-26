// Pure helpers behind the task detail page and its components.
import type { CompressionTarget, Job, Task, TaskStatus } from "~/lib/types/task";
import type { ImageOptions } from "~/lib/types/image-options";

export const isActiveStatus = (status: string | undefined) => status === "PROCESSING" || status === "PENDING";

export const taskStatusStyle = (status: TaskStatus | string | undefined) => {
    if (!status) return "bg-surface-2 text-ink-subtle border-hairline";
    switch (status) {
        case "COMPLETED": return "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400";
        case "FAILED": return "bg-rose-500/10 text-rose-600 border-rose-500/20 dark:text-rose-400";
        case "CANCELLED": return "bg-zinc-500/10 text-zinc-600 border-zinc-500/20 dark:text-zinc-400";
        case "PARTIALLY_FAILED": return "bg-orange-500/10 text-orange-600 border-orange-500/20 dark:text-orange-400";
        case "PROCESSING": return "bg-primary/10 text-primary border-primary/20 dark:text-primary-hover";
        case "PENDING": return "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400";
        default: return "bg-surface-2 text-ink-subtle border-hairline";
    }
};

export const jobStatusStyle = (status: string) => {
    switch (status) {
        case "COMPLETED": return "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400";
        case "FAILED": return "bg-rose-500/10 text-rose-600 border-rose-500/20 dark:text-rose-400";
        case "CANCELLED": return "bg-zinc-500/10 text-zinc-600 border-zinc-500/20 dark:text-zinc-400";
        case "PROCESSING": return "bg-primary/10 text-primary border-primary/20 dark:text-primary-hover";
        case "PENDING": return "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400";
        default: return "bg-surface-2 text-ink-subtle border-hairline";
    }
};

export const formatBytes = (bytes: number | undefined) => {
    if (bytes === undefined || bytes === null || bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
};

/** A completed job's size change against its source, or null when a size is missing. */
export const jobSavings = (job: Job) => {
    const src = job.compression?.source_bytes ?? job.source_file?.size ?? 0;
    const out = job.compression?.output_bytes ?? job.output_file?.size ?? 0;
    if (!src || !out) return null;
    const larger = job.compression?.larger_than_source ?? out > src;
    const pct = Math.round((Math.abs(src - out) / src) * 100);
    return {
        larger,
        pct,
        label: `${larger ? "+" : "-"}${pct}%`,
    };
};

export interface TotalSavings {
    input: number;
    output: number;
    /** More than one output per source: sizes compare each output with its own source. */
    multi: boolean;
    completedCount: number;
    ratio: number;
    saved: number;
    absDelta: number;
    larger: boolean;
}

/** The size change across a task's completed jobs, or null before any job completes. */
export const totalSavings = (jobs: Job[] | undefined): TotalSavings | null => {
    if (!jobs || jobs.length === 0) return null;
    const sourceSizes = new Map<string, number>();
    let pairedInput = 0;
    let totalOutput = 0;
    let completedCount = 0;

    for (const job of jobs) {
        if (job.status === "COMPLETED") {
            const src = job.compression?.source_bytes ?? job.source_file?.size ?? 0;
            const out = job.compression?.output_bytes ?? job.output_file?.size ?? 0;
            if (src > 0 && out > 0) {
                if (!sourceSizes.has(job.source_file_id)) {
                    sourceSizes.set(job.source_file_id, src);
                }
                pairedInput += src;
                totalOutput += out;
                completedCount += 1;
            }
        }
    }

    let totalInput = 0;
    for (const sz of sourceSizes.values()) {
        totalInput += sz;
    }

    if (completedCount === 0 || totalInput === 0 || pairedInput === 0) return null;
    const multi = completedCount > sourceSizes.size;
    const base = multi ? pairedInput : totalInput;
    const saved = base - totalOutput;
    const larger = totalOutput > base;
    const ratio = Math.round((Math.abs(saved) / base) * 100);
    return {
        input: totalInput,
        output: totalOutput,
        multi,
        completedCount,
        ratio,
        saved,
        absDelta: Math.abs(saved),
        larger,
    };
};

/** True when a task has more than one output per source. */
export const hasMultiOutputs = (task: Task) =>
    Boolean(task.jobs?.some((j) => (j.output_index ?? 0) > 0 || !!j.output_name));

export interface OutputSpec {
    index: number;
    name?: string;
    options: ImageOptions;
    target?: CompressionTarget;
    resolved?: ImageOptions;
}

/** The distinct outputs a task asked for, in output order. */
export const outputSpecs = (jobs: Job[]): OutputSpec[] => {
    const byKey = new Map<string, OutputSpec>();
    for (const job of jobs) {
        const key = `${job.output_index ?? 0}:${job.output_name ?? ""}`;
        if (!byKey.has(key)) {
            byKey.set(key, {
                index: job.output_index ?? 0,
                name: job.output_name,
                options: job.options,
                target: job.target,
                resolved: job.resolved_options,
            });
        }
    }
    return Array.from(byKey.values()).sort((a, b) => a.index - b.index);
};

// An output is downloadable while it is stored; expired or deleted outputs
// keep their record (and the job stays COMPLETED) but have no content.
export const isOutputStored = (job: Job) =>
    job.status === "COMPLETED" && !!job.output_file_id && (!job.output_file || job.output_file.status === "UPLOADED");

export const isImageFile = (filename: string | undefined) => {
    if (!filename) return false;
    const ext = filename.split(".").pop()?.toLowerCase() || "";
    return ["png", "jpg", "jpeg", "webp", "avif", "jxl", "gif", "apng", "heic", "heif"].includes(ext);
};

/** Shortens a long file name in the middle, keeping its extension. */
export const formatFilename = (name: string | undefined) => {
    if (!name) return "";
    const parts = name.split(".");
    const ext = parts.pop() || "";
    const base = parts.join(".");
    if (base.length > 22) {
        return `${base.substring(0, 10)}...${base.substring(base.length - 8)}.${ext}`;
    }
    return name;
};
