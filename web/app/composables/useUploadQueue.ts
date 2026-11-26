import { Uppy } from "@uppy/core";
import AwsS3 from "@uppy/aws-s3";
import { toast } from "vue-sonner";
import { useApi, parseApiError } from "~/composables/useApi";
import { createLimiter, probeImage, sha256File } from "~/lib/upload-prep";
import type { ApiResponse } from "~/lib/types/api";
import type { CompleteUploadData, DraftFile, InitUploadData, ListPartsData, SignPartData } from "~/lib/types/s3";
import type { SourceProbeSummary } from "@/components/ImageOptionsForm.vue";

/** A file in the new-task queue: picked locally (and uploading) or added from the drafts. */
export type QueueItem = {
    id: string; // Client ID
    source: "local" | "recent";
    uppyId?: string;
    serverFileId?: string;
    name: string;
    size: number;
    progress: number;
    status: "queued" | "uploading" | "uploaded" | "error" | "deleting" | "aborting";
    type?: string;
    error?: string;
    hash?: string;
    probe?: SourceProbeSummary;
};

// Presigned URLs live an hour; re-sign a part a little before that.
const PART_URL_TTL_MS = 50 * 60 * 1000;

interface UploadQueueHooks {
    /** Called after the queue changes outside a reactive update (uploads finishing, files removed). */
    onChange: () => void;
    /** Deletes a stored file the user removed from the queue. */
    deleteServerFile: (fileId: string) => Promise<unknown>;
}

/**
 * The files of a new task and their uploads. Each file is hashed and probed,
 * then /upload/init either finds it already stored (fast upload) or signs a
 * single PUT or a multipart upload that Uppy runs. Uppy lives while the
 * calling component is mounted.
 */
export const useUploadQueue = ({ onChange, deleteServerFile }: UploadQueueHooks) => {
    const api = useApi();

    const files = ref<QueueItem[]>([]);
    const globalProgress = ref(0);
    /** Server file id by client id, for every uploaded file. */
    const uploadedFileIds = ref<Record<string, string>>({});

    const isUploading = computed(() => files.value.some((f) => f.status === "uploading" || f.status === "queued"));

    // Probe summary from the first uploaded file in current queue
    const firstFileProbe = computed<SourceProbeSummary | null>(() => {
        const item = files.value.find((f) => f.probe);
        return item?.probe || null;
    });

    let uppy: Uppy;

    // Hashing reads the whole file, so cap how many run at once.
    const prepareSlot = createLimiter(3);

    onMounted(() => {
        uppy = new Uppy({
            restrictions: {
                maxNumberOfFiles: 50,
                allowedFileTypes: ["image/*", "video/*"],
            },
            autoProceed: true,
        });

        // /upload/init has already run for every file Uppy sees: it chose single
        // PUT or multipart by size and signed the URLs, which arrive in file.meta.
        uppy.use(AwsS3, {
            shouldUseMultipart: (file) => Boolean(file.meta.uploadId),
            limit: 4,
            // Same rule as partSize in server/internal/api/handlers/upload.go.
            getChunkSize: (file) => Math.max(5 * 1024 * 1024, Math.ceil(file.size / 10000)),
            getUploadParameters: (file) => ({ method: "PUT", url: file.meta.url as string }),
            createMultipartUpload: (file) => ({
                uploadId: file.meta.uploadId as string,
                key: file.meta.key as string,
            }),
            listParts: async (file, { uploadId, key }) => {
                const res = await api<ApiResponse<ListPartsData>>(
                    `/upload/multipart/${uploadId}/list-parts?key=${encodeURIComponent(key)}`,
                );
                return res.data.parts.map((p) => ({ PartNumber: p.part_number, Size: p.size, ETag: p.etag }));
            },
            signPart: async (file, { uploadId, key, partNumber }) => {
                const cached = (file.meta.partUrls as string[] | undefined)?.[partNumber - 1];
                const signedAt = (file.meta.signedAt as number | undefined) ?? 0;
                if (cached && Temporal.Now.instant().epochMilliseconds - signedAt < PART_URL_TTL_MS) {
                    return { url: cached };
                }
                const res = await api<ApiResponse<SignPartData>>(
                    `/upload/multipart/${uploadId}/sign-part/${partNumber}?key=${encodeURIComponent(key)}`,
                );
                return { url: res.data.url };
            },
            abortMultipartUpload: async (file, { uploadId, key }) => {
                await api(`/upload/multipart/${uploadId}/abort`, {
                    method: "POST",
                    body: { key },
                });
            },
            // What this returns becomes the upload-success response body.
            completeMultipartUpload: async (file, { key, parts }) => {
                const res = await api<ApiResponse<CompleteUploadData>>("/upload/complete", {
                    method: "POST",
                    body: { key, parts: parts.map((p) => ({ part_number: p.PartNumber, etag: p.ETag })) },
                });
                return res.data;
            },
        });

        uppy.on("file-added", (file) => {
            const f = files.value.find((f) => f.id === file.meta.clientId);
            if (f) {
                f.uppyId = file.id;
                f.status = "uploading";
            }
        });

        uppy.on("file-removed", (file) => {
            files.value = files.value.filter((f) => f.uppyId !== file.id);
        });

        uppy.on("upload-progress", (file, progress) => {
            if (!file) return;
            const f = files.value.find((f) => f.uppyId === file.id);
            if (f) {
                f.status = "uploading";
                const p = progress as any;
                f.progress = Math.round((p.bytesUploaded / p.bytesTotal) * 100);
            }
        });

        uppy.on("progress", (progress) => {
            globalProgress.value = progress;
        });

        uppy.on("upload-success", async (file, response) => {
            if (!file) return;
            const f = files.value.find((f) => f.uppyId === file.id);
            try {
                // Multipart was completed inside Uppy; a single PUT still needs
                // the server to record the object.
                let fileId = (response.body as CompleteUploadData | undefined)?.file_id;
                if (!file.meta.uploadId) {
                    const res = await api<ApiResponse<CompleteUploadData>>("/upload/complete", {
                        method: "POST",
                        body: { key: file.meta.key },
                    });
                    fileId = res.data.file_id;
                }
                if (!f) return;
                f.status = "uploaded";
                f.progress = 100;
                if (fileId) {
                    uploadedFileIds.value[f.id] = fileId;
                    f.serverFileId = fileId;
                }
                onChange();
            } catch (err) {
                if (f) {
                    f.status = "error";
                    f.error = parseApiError(err).userMessage;
                }
            }
        });

        uppy.on("upload-error", (file, error) => {
            if (!file) return;
            const f = files.value.find((f) => f.uppyId === file.id);
            if (f) {
                f.status = "error";
                f.error = parseApiError(error).userMessage;
            }
        });
    });

    onUnmounted(() => {
        if (uppy) {
            const u = uppy as any;
            if (typeof u.close === "function") {
                u.close();
            } else if (typeof u.destroy === "function") {
                u.destroy();
            }
        }
    });

    const processSingleFile = async (file: File) => {
        // Show the file at once; hashing, probing and /upload/init follow.
        files.value.push({
            id: `local-${crypto.randomUUID()}`,
            source: "local",
            name: file.name,
            size: file.size,
            progress: 0,
            status: "queued",
            type: file.type,
        });
        const item = files.value[files.value.length - 1]!;
        const isQueued = () => files.value.includes(item);

        try {
            await prepareSlot(async () => {
                if (!isQueued()) return;
                const [hash, probe] = await Promise.all([sha256File(file), probeImage(file)]);
                item.hash = hash;
                item.probe = probe;

                // Local duplicate prevention
                if (files.value.some((f) => f !== item && f.hash === hash)) {
                    files.value = files.value.filter((f) => f !== item);
                    toast.warning(`文件 '${file.name}' 已经存在于当前列表中`);
                    return;
                }

                const { data: init } = await api<ApiResponse<InitUploadData>>("/upload/init", {
                    method: "POST",
                    body: { filename: file.name, type: file.type, size: file.size, hash },
                });
                if (!isQueued()) return;

                if (init.fast_upload) {
                    item.serverFileId = init.file_id;
                    item.status = "uploaded";
                    item.progress = 100;
                    uploadedFileIds.value[item.id] = init.file_id;
                    toast.success(`文件 '${file.name}' 秒传成功！`);
                    onChange();
                    return;
                }

                uppy.addFile({
                    name: file.name,
                    type: file.type,
                    data: file,
                    meta: {
                        clientId: item.id,
                        key: init.key,
                        url: init.url,
                        uploadId: init.upload_id,
                        partUrls: init.part_urls,
                        signedAt: Temporal.Now.instant().epochMilliseconds,
                    },
                });
            });
        } catch (err: any) {
            console.error("Failed to process file:", err);
            const errMsg = parseApiError(err).userMessage || err.message || String(err);
            item.status = "error";
            item.error = errMsg;
            toast.error(`初始化文件 '${file.name}' 上传失败: ${errMsg}`);
        }
    };

    /** Queues and uploads files the user picked or dropped. */
    const addLocalFiles = (list: FileList | File[]) => {
        Array.from(list).forEach((file) => {
            processSingleFile(file);
        });
    };

    /** Queues a stored draft file; returns false when it is already queued. */
    const addServerFile = (file: DraftFile) => {
        if (files.value.some((f) => f.serverFileId === file.id)) return false;
        files.value.push({
            id: `recent-${file.id}`,
            source: "recent",
            serverFileId: file.id,
            name: file.name,
            size: file.size,
            progress: 100,
            status: "uploaded",
            type: file.mime_type || "",
        });
        uploadedFileIds.value[`recent-${file.id}`] = file.id;
        onChange();
        return true;
    };

    /** Drops a stored file from the queue after it was deleted elsewhere. */
    const forgetServerFile = (fileId: string) => {
        const idx = files.value.findIndex((f) => f.serverFileId === fileId);
        if (idx !== -1) {
            const item = files.value[idx];
            if (item) {
                const clientId = item.id;
                files.value.splice(idx, 1);
                delete uploadedFileIds.value[clientId];
            }
        }
    };

    /** Removes a file from the queue, stopping its upload and deleting what was stored. */
    const removeFile = async (id: string) => {
        const idx = files.value.findIndex((f) => f.id === id);
        if (idx === -1) return;
        const f = files.value[idx];
        if (!f) return;

        files.value.splice(idx, 1);
        if (f.uppyId) {
            try {
                uppy.removeFile(f.uppyId);
            } catch (e) {
                console.error(e);
            }
        }
        if (f.serverFileId) {
            delete uploadedFileIds.value[f.id];
            f.status = "deleting";
            try {
                await deleteServerFile(f.serverFileId);
            } catch (err) {
                console.error("Failed to delete file on server:", err);
            }
        }
        onChange();
    };

    /** Stops every upload and empties the queue. */
    const reset = () => {
        if (uppy) {
            uppy.cancelAll();
        }
        files.value = [];
        uploadedFileIds.value = {};
    };

    return {
        files,
        uploadedFileIds,
        globalProgress,
        isUploading,
        firstFileProbe,
        addLocalFiles,
        addServerFile,
        forgetServerFile,
        removeFile,
        reset,
    };
};
