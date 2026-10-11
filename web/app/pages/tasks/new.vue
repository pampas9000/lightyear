<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from "vue";
import { useRouter } from "vue-router";
import { Uppy } from "@uppy/core";
import AwsS3 from "@uppy/aws-s3";
import { useApi, parseApiError } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import type { Task, Workflow } from "~/lib/types/task";
import type { ApiResponse } from "~/lib/types/api";
import type { MultipartUploadResponse, PartSignatureResponse } from "~/lib/types/s3";
import { toast } from "vue-sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import ImageOptionsForm from "@/components/ImageOptionsForm.vue";
import type { SourceProbeSummary } from "@/components/ImageOptionsForm.vue";
import type { ImageOptions } from "~/lib/types/image-options";
import { defaultOptions, describe, sanitize } from "~/lib/image-options";
import {
    Plus,
    X,
    Check,
    Loader2,
    FileImage,
    FileVideo,
    FileCode,
    UploadCloud,
    AlertCircle,
    Settings,
    Layers,
    HardDrive,
    Trash2,
} from "@lucide/vue";

useHead({
    title: $t("new_task.title") + " | Transcoder",
});

const router = useRouter();
const api = useApi();

// Output options for every file in the task.
const options = ref<ImageOptions>(defaultOptions());

// Saved option sets (workflows). While the form still matches the applied
// workflow the task references it; any edit submits explicit options instead.
const workflows = ref<Workflow[]>([]);
const selectedWorkflowId = ref("none");
const workflowDirty = ref(false);
const isApplyingWorkflow = ref(false);
const showPresetSave = ref(false);
const presetName = ref("");
const isSavingPreset = ref(false);

// QueueItem type
type QueueItem = {
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

// useAuth composable hook
const { isAuthenticated, showAuthModal, user } = useAuth();

// Uppy and Task Creation state
const files = ref<QueueItem[]>([]);
const globalProgress = ref(0);
const isCreatingTask = ref(false);
const taskCreated = ref(false);
const uploadedFileIds = ref<Record<string, string>>({});
const idempotencyKey = ref("");

// Probe summary from the first uploaded file in current queue
const firstFileProbe = computed<SourceProbeSummary | null>(() => {
    const item = files.value.find((f) => f.probe);
    return item?.probe || null;
});

const isUploading = computed(() => {
    return files.value.some((f) => f.status === "uploading" || f.status === "queued");
});

const isFormDisabled = computed(() => isCreatingTask.value || taskCreated.value);
const isParamsDisabled = computed(() => isCreatingTask.value || taskCreated.value);

const isSubmitDisabled = computed(() => {
    if (isUploading.value) return true;
    if (isCreatingTask.value) return true;
    if (taskCreated.value) return true;
    if (files.value.length === 0) return true;
    return !files.value.every((f) => f.status === "uploaded" && f.serverFileId);
});

// Authentication guard
const checkAuthBeforeAction = (): boolean => {
    if (!isAuthenticated.value) {
        showAuthModal.value = true;
        return false;
    }
    return true;
};

// Draft Box handling
const getDraftKey = () => {
    const uId = user.value?.id || "anonymous";
    return `task_new_draft:${uId}:v2`;
};

const pendingSubmission = ref<{ idempotencyKey: string; requestBody: string } | null>(null);

const saveDraft = () => {
    if (!isAuthenticated.value) return;
    const selectedServerFileIds = files.value
        .filter((f) => f.status === "uploaded" && f.serverFileId)
        .map((f) => f.serverFileId as string);

    const draft = {
        selectedServerFileIds,
        options: options.value,
        updatedAt: Date.now(),
        pendingSubmission: pendingSubmission.value,
    };
    localStorage.setItem(getDraftKey(), JSON.stringify(draft));
};

const clearDraft = () => {
    localStorage.removeItem(getDraftKey());
};

const isReconciling = ref(false);

const loadAndReconcileDraft = async () => {
    if (!isAuthenticated.value) return;
    const stored = localStorage.getItem(getDraftKey());
    if (!stored) return;
    try {
        const draft = JSON.parse(stored);
        if (!draft) return;
        if (draft.options?.format) options.value = sanitize(draft.options);
        if (draft.pendingSubmission) {
            pendingSubmission.value = draft.pendingSubmission;
            idempotencyKey.value = draft.pendingSubmission.idempotencyKey;
        }

        isReconciling.value = true;
        const res = await api<ApiResponse<{ files: any[] }>>("/drafts/list");
        const serverFiles = res?.data?.files || [];
        const serverFileMap = new Map(serverFiles.map((f) => [f.id, f]));

        if (draft.selectedServerFileIds && draft.selectedServerFileIds.length > 0) {
            const reconciledFiles: QueueItem[] = [];
            for (const serverId of draft.selectedServerFileIds) {
                const sf = serverFileMap.get(serverId);
                if (sf) {
                    reconciledFiles.push({
                        id: `recent-${sf.id}`,
                        source: "recent",
                        serverFileId: sf.id,
                        name: sf.name,
                        size: sf.size,
                        progress: 100,
                        status: "uploaded",
                        type: sf.mime_type || "",
                        hash: sf.hash || "",
                    });
                    uploadedFileIds.value[`recent-${sf.id}`] = sf.id;
                } else if (draft.pendingSubmission) {
                    // Reconstruct from draft for pending submissions
                    reconciledFiles.push({
                        id: `recent-${serverId}`,
                        source: "recent",
                        serverFileId: serverId,
                        name: "Pending file",
                        size: 0,
                        progress: 100,
                        status: "uploaded",
                        type: "",
                    });
                    uploadedFileIds.value[`recent-${serverId}`] = serverId;
                }
            }
            files.value = reconciledFiles;
        }
    } catch (e) {
        console.error("Failed to load draft:", e);
    } finally {
        isReconciling.value = false;
    }
};

// Watchers for draft box
watch(
    [files, options],
    () => {
        saveDraft();
    },
    { deep: true },
);

watch(
    () => user.value?.id,
    (newUid, oldUid) => {
        if (newUid !== oldUid) {
            if (uppy) {
                uppy.cancelAll();
            }
            files.value = [];
            uploadedFileIds.value = {};
            pendingSubmission.value = null;
            idempotencyKey.value = "";
            if (newUid) {
                loadAndReconcileDraft();
                fetchRecentFiles();
                fetchWorkflows();
            }
        }
    },
);

// Recent uploads (Draft files)
const recentFiles = ref<any[]>([]);
const filteredRecentFiles = computed(() => {
    return recentFiles.value.filter(
        (rf) => !files.value.some((f) => f.serverFileId === rf.id)
    );
});
const isLoadingRecent = ref(false);

const fetchRecentFiles = async () => {
    if (!isAuthenticated.value) return;
    isLoadingRecent.value = true;
    try {
        const res = await api<ApiResponse<{ files: any[] }>>("/drafts/list");
        if (res && res.data && res.data.files) {
            recentFiles.value = res.data.files;
        }
    } catch (err) {
        console.error("Failed to fetch recent files:", err);
    } finally {
        isLoadingRecent.value = false;
    }
};

const addRecentFile = (file: any) => {
    if (!checkAuthBeforeAction()) return;
    if (files.value.some((f) => f.serverFileId === file.id)) {
        toast.info("文件已在处理队列中");
        return;
    }
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
    saveDraft();
};

const deleteRecentFile = async (fileId: string) => {
    if (!checkAuthBeforeAction()) return;
    try {
        await api(`/drafts/${fileId}/delete`, { method: "POST" });
        recentFiles.value = recentFiles.value.filter((f) => f.id !== fileId);
        const idx = files.value.findIndex((f) => f.serverFileId === fileId);
        if (idx !== -1) {
            const item = files.value[idx];
            if (item) {
                const clientId = item.id;
                files.value.splice(idx, 1);
                delete uploadedFileIds.value[clientId];
            }
        }
        toast.success("文件物理删除成功");
        saveDraft();
    } catch (err) {
        console.error("Failed to delete file:", err);
        toast.error("删除文件失败");
    }
};

let uppy: Uppy;

onMounted(() => {
    uppy = new Uppy({
        restrictions: {
            maxNumberOfFiles: 50,
            allowedFileTypes: ["image/*", "video/*"],
        },
        autoProceed: true,
    });

    uppy.use(AwsS3, {
        shouldUseMultipart: true,
        limit: 4,
        createMultipartUpload: async (file) => {
            if (!file) {
                throw new Error("No file provided for upload");
            }
            if (file.meta.uploadId && file.meta.key) {
                return {
                    uploadId: file.meta.uploadId as string,
                    key: file.meta.key as string,
                };
            }
            const hash = (file.meta.hash as string) || "";
            const res = await api<MultipartUploadResponse>("/upload/multipart/init", {
                method: "POST",
                body: { filename: file.name, type: file.type, hash },
            });
            return { uploadId: res.uploadId, key: res.key };
        },
        listParts: async (file, { uploadId, key }) => {
            const res = await api<any>(
                `/upload/multipart/${uploadId}/list-parts?key=${encodeURIComponent(key)}`,
            );
            return res;
        },
        signPart: async (file, { uploadId, key, partNumber }) => {
            const res = await api<PartSignatureResponse>(
                `/upload/multipart/${uploadId}/sign-part/${partNumber}?key=${encodeURIComponent(key)}`,
            );
            return res; // contains { url }
        },
        abortMultipartUpload: async (file, { uploadId, key }) => {
            await api(`/upload/multipart/${uploadId}/abort`, {
                method: "POST",
                body: { key },
            });
        },
        completeMultipartUpload: async (file, { uploadId, key, parts }) => {
            const res = await api<any>(`/upload/multipart/${uploadId}/complete`, {
                method: "POST",
                body: { key, parts },
            });
            return res;
        },
    });

    uppy.on("file-added", (file) => {
        if (!checkAuthBeforeAction()) {
            uppy.removeFile(file.id);
            return;
        }
        const hash = (file.meta.hash as string) || "";
        const probe = file.meta.probe as SourceProbeSummary | undefined;
        files.value.push({
            id: file.id,
            source: "local",
            uppyId: file.id,
            name: file.name,
            size: file.size || 0,
            progress: 0,
            status: "uploading",
            type: file.type,
            hash: hash,
            probe,
        });
    });

    uppy.on("file-removed", (file) => {
        files.value = files.value.filter((f) => f.id !== file.id);
    });

    uppy.on("upload-progress", (file, progress) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "uploading";
            const p = progress as any;
            f.progress = Math.round((p.bytesUploaded / p.bytesTotal) * 100);
        }
    });

    uppy.on("progress", (progress) => {
        globalProgress.value = progress;
    });

    uppy.on("upload-success", (file, response) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "uploaded";
            f.progress = 100;
        }
        const fileId = response.body?.fileId || (response.body as any)?.file_id;
        if (fileId) {
            uploadedFileIds.value[file.id] = fileId;
            if (f) {
                f.serverFileId = fileId;
            }
        }
        saveDraft();
    });

    uppy.on("upload-error", (file, error) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "error";
            f.error = parseApiError(error).userMessage;
        }
    });

    if (isAuthenticated.value) {
        loadAndReconcileDraft();
        fetchRecentFiles();
        fetchWorkflows();
    }
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

const getSHA256 = async (file: File): Promise<string> => {
    const buffer = await file.arrayBuffer();
    const hashBuffer = await crypto.subtle.digest("SHA-256", buffer);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
};

const probeImageFile = (file: File): Promise<SourceProbeSummary> => {
    return new Promise((resolve) => {
        const ext = file.name.split(".").pop()?.toLowerCase() || "";
        const summary: SourceProbeSummary = {
            format: ext || file.type.split("/")[1] || "unknown",
        };

        if (typeof window === "undefined" || !file.type.startsWith("image/")) {
            return resolve(summary);
        }

        const img = new Image();
        const url = URL.createObjectURL(file);
        img.onload = () => {
            summary.width = img.naturalWidth;
            summary.height = img.naturalHeight;
            URL.revokeObjectURL(url);
            resolve(summary);
        };
        img.onerror = () => {
            URL.revokeObjectURL(url);
            resolve(summary);
        };
        img.src = url;
    });
};

const processSingleFile = async (file: File) => {
    try {
        const hash = await getSHA256(file);
        const probe = await probeImageFile(file);

        // Local duplicate prevention
        if (files.value.some((f) => f.hash === hash)) {
            toast.warning(`文件 '${file.name}' 已经存在于当前列表中`);
            return;
        }

        const res = await api<MultipartUploadResponse>("/upload/multipart/init", {
            method: "POST",
            body: { filename: file.name, type: file.type, hash },
        });

        if (res.fastUpload) {
            const serverId = res.fileId || "";
            if (!serverId) {
                throw new Error("Fast upload response is missing fileId");
            }
            const clientFileId = `fast-${Math.random().toString(36).substring(2, 9)}`;
            files.value.push({
                id: clientFileId,
                source: "local",
                serverFileId: serverId,
                name: file.name,
                size: file.size,
                progress: 100,
                status: "uploaded",
                type: file.type || "",
                hash: hash,
                probe,
            });
            uploadedFileIds.value[clientFileId] = serverId;
            toast.success(`文件 '${file.name}' 秒传成功！`);
            saveDraft();
            return;
        }

        if (!res.uploadId || !res.key) {
            throw new Error("Multipart upload response is missing uploadId or key");
        }

        uppy.addFile({
            name: file.name,
            type: file.type,
            data: file,
            meta: {
                hash,
                uploadId: res.uploadId,
                key: res.key,
                probe,
            },
        });
    } catch (err: any) {
        console.error("Failed to process file:", err);
        const errMsg = parseApiError(err).userMessage || err.message || String(err);
        toast.error(`初始化文件 '${file.name}' 上传失败: ${errMsg}`);
    }
};

const handleFileSelect = (event: Event) => {
    if (!checkAuthBeforeAction()) return;
    const input = event.target as HTMLInputElement;
    if (input.files) {
        Array.from(input.files).forEach((file) => {
            processSingleFile(file);
        });
    }
    input.value = "";
};

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
            await api(`/drafts/${f.serverFileId}/delete`, { method: "POST" });
        } catch (err) {
            console.error("Failed to delete file on server:", err);
        }
    }
    saveDraft();
};

const generateRequestSnapshot = () => {
    const sortedIds = files.value
        .filter((f) => f.status === "uploaded" && f.serverFileId)
        .map((f) => f.serverFileId as string);

    const items = sortedIds.map((id) => ({ file_id: id }));

    if (selectedWorkflowId.value !== "none" && !workflowDirty.value) {
        return JSON.stringify({
            items,
            workflow_id: selectedWorkflowId.value,
        });
    }

    return JSON.stringify({
        items,
        options: sanitize(options.value),
    });
};

const fetchWorkflows = async () => {
    if (!isAuthenticated.value) return;
    try {
        const res = await api<ApiResponse<Workflow[]>>("/workflows");
        if (res?.data) workflows.value = res.data;
    } catch (err) {
        console.error("Failed to fetch workflows:", err);
    }
};

const applyWorkflow = async (id: string) => {
    const wf = workflows.value.find((w) => w.id === id);
    if (!wf) {
        selectedWorkflowId.value = "none";
        workflowDirty.value = false;
        return;
    }
    isApplyingWorkflow.value = true;
    try {
        options.value = sanitize(wf.options);
        selectedWorkflowId.value = wf.id;
        workflowDirty.value = false;
        toast.success(`已应用预设「${wf.name}」`);
    } finally {
        await nextTick();
        isApplyingWorkflow.value = false;
    }
};

const savePreset = async () => {
    if (!checkAuthBeforeAction()) return;
    const name = presetName.value.trim();
    if (!name) {
        toast.error("请输入预设名称");
        return;
    }
    isSavingPreset.value = true;
    try {
        await api("/workflows", {
            method: "POST",
            body: { name, options: sanitize(options.value) },
        });
        toast.success(`预设「${name}」已保存`);
        presetName.value = "";
        showPresetSave.value = false;
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to save workflow:", err);
        toast.error("保存预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

const updatePreset = async () => {
    if (selectedWorkflowId.value === "none") return;
    const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
    if (!wf) return;
    isSavingPreset.value = true;
    try {
        await api(`/workflows/${wf.id}`, {
            method: "PUT",
            body: { name: wf.name, options: sanitize(options.value) },
        });
        workflowDirty.value = false;
        toast.success(`预设「${wf.name}」已更新为当前参数`);
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to update workflow:", err);
        toast.error("更新预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

const deletePreset = async () => {
    if (selectedWorkflowId.value === "none") return;
    const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
    if (!wf) return;
    if (!window.confirm(`确认删除预设「${wf.name}」？已创建的任务不受影响。`)) return;
    isSavingPreset.value = true;
    try {
        await api(`/workflows/${wf.id}`, { method: "DELETE" });
        selectedWorkflowId.value = "none";
        workflowDirty.value = false;
        toast.success(`预设「${wf.name}」已删除`);
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to delete workflow:", err);
        toast.error("删除预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

// Mark the applied workflow as modified as soon as any parameter is tweaked.
// The task still submits explicit params in that case; the link only survives
// while the form matches the stored preset.
watch(
    options,
    () => {
        if (isApplyingWorkflow.value) return;
        if (selectedWorkflowId.value !== "none") {
            workflowDirty.value = true;
        }
    },
    { deep: true },
);

const submitTask = async () => {
    if (!checkAuthBeforeAction()) return;

    const currentSnapshot = generateRequestSnapshot();
    let key = idempotencyKey.value;
    if (pendingSubmission.value && pendingSubmission.value.requestBody === currentSnapshot) {
        key = pendingSubmission.value.idempotencyKey;
    } else {
        key = crypto.randomUUID();
        idempotencyKey.value = key;
        pendingSubmission.value = {
            idempotencyKey: key,
            requestBody: currentSnapshot,
        };
        saveDraft();
    }

    const body = JSON.parse(currentSnapshot);
    body.idempotency_key = key;

    isCreatingTask.value = true;
    try {
        const response = await api<ApiResponse<Task>>("/tasks", {
            method: "POST",
            body,
        });

        clearDraft();
        pendingSubmission.value = null;
        idempotencyKey.value = "";

        taskCreated.value = true;
        if (response && response.data && response.data.id) {
            router.push(`/tasks/${response.data.id}`);
        } else {
            router.push("/tasks");
        }
    } catch (err) {
        console.error("Failed to create task:", err);
        toast.error($t("new_task.task_creation_failed"), {
            description: parseApiError(err).userMessage,
        });
    } finally {
        isCreatingTask.value = false;
    }
};

const handleAction = () => {
    submitTask();
};

const submitButtonText = computed(() => {
    if (isCreatingTask.value) return $t("new_task.creating_task");
    if (taskCreated.value) return $t("new_task.task_created");

    const totalCount = files.value.length;
    const uploadingCount = files.value.filter((f) => f.status === "uploading").length;

    if (uploadingCount > 0) {
        const finishedCount = files.value.filter((f) => f.status === "uploaded").length;
        return `正在上传 ${finishedCount}/${totalCount}`;
    }

    const successfulCount = files.value.filter((f) => f.status === "uploaded").length;
    if (successfulCount > 0) {
        return `开始处理 ${successfulCount} 个文件`;
    }

    return $t("new_task.start_processing");
});

// Drag and drop state
const dragCounter = ref(0);
const isDragActive = computed(() => dragCounter.value > 0);

const handleDragEnter = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value++;
};

const handleDragLeave = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value = Math.max(0, dragCounter.value - 1);
};

const handleDragOver = (e: DragEvent) => {
    e.preventDefault();
};

const handleDrop = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value = 0;
    if (e.dataTransfer?.files) {
        Array.from(e.dataTransfer.files).forEach((file) => {
            processSingleFile(file);
        });
    }
};
</script>

<template>
    <div class="max-w-3xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-8 py-8 pb-24">
        <!-- Header -->
        <div class="flex flex-col gap-1">
            <h1 class="text-xl font-semibold tracking-tight text-ink font-sans">
                {{ $t("new_task.title") }}
            </h1>
            <p class="text-xs text-ink-subtle font-medium">
                {{ $t("new_task.subtitle") }}
            </p>
        </div>

        <div class="grid gap-6">
            <!-- Parameters Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
            >
                <CardHeader
                    class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between"
                >
                    <div class="space-y-0.5">
                        <CardTitle
                            class="text-sm font-semibold flex items-center gap-1.5 text-ink tracking-tight"
                        >
                            <Settings class="w-4 h-4 text-primary" />
                            {{ $t("new_task.params") }}
                        </CardTitle>
                        <p class="text-xs text-ink-subtle font-medium">
                            {{ $t("new_task.params_subtitle") }}
                        </p>
                    </div>
</CardHeader>
                <CardContent class="p-5 space-y-6">
                    <!-- Workflow Presets -->
                    <div
                        v-if="isAuthenticated"
                        class="p-3.5 rounded-xl bg-surface-2/40 border border-hairline space-y-2.5"
                    >
                        <div class="flex items-center justify-between">
                            <Label class="text-xs font-semibold text-ink flex items-center gap-1.5">
                                <Layers class="w-3.5 h-3.5 text-primary" />
                                我的转码预设 (Workflows)
                            </Label>
                            <button
                                type="button"
                                :disabled="isParamsDisabled"
                                @click="showPresetSave = !showPresetSave"
                                class="text-xs font-semibold text-primary hover:text-primary-hover cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                {{ showPresetSave ? "取消保存" : "保存当前参数为预设" }}
                            </button>
                        </div>
                        <div class="flex gap-2">
                            <Select
                                :model-value="selectedWorkflowId"
                                @update:model-value="val => applyWorkflow(String(val ?? 'none'))"
                                :disabled="isParamsDisabled || workflows.length === 0"
                            >
                                <SelectTrigger
                                    class="h-9 flex-1 rounded-lg bg-surface-2 border-hairline text-ink text-xs"
                                >
                                    <SelectValue
                                        :placeholder="
                                            workflows.length === 0
                                                ? '暂无预设，可先保存当前参数'
                                                : '选择预设以一键套用'
                                        "
                                    />
                                </SelectTrigger>
                                <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                    <SelectItem value="none">不使用预设（手动参数）</SelectItem>
                                    <SelectItem v-for="wf in workflows" :key="wf.id" :value="wf.id">
                                        {{ wf.name }} · {{ describe(wf.options) }}
                                    </SelectItem>
                                </SelectContent>
                            </Select>
                            <Button
                                v-if="selectedWorkflowId !== 'none'"
                                type="button"
                                size="sm"
                                variant="outline"
                                :disabled="isSavingPreset || isParamsDisabled"
                                @click="updatePreset"
                                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold cursor-pointer"
                            >
                                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                                <span v-else>更新预设</span>
                            </Button>
                            <Button
                                v-if="selectedWorkflowId !== 'none'"
                                type="button"
                                size="sm"
                                variant="outline"
                                :disabled="isSavingPreset || isParamsDisabled"
                                @click="deletePreset"
                                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold text-red-500 hover:text-red-600 hover:bg-red-500/5 cursor-pointer"
                            >
                                删除
                            </Button>
                        </div>
                        <p
                            v-if="selectedWorkflowId !== 'none' && workflowDirty"
                            class="text-[11px] font-medium text-amber-600 dark:text-amber-400"
                        >
                            参数已修改：提交将使用当前参数；可点击「更新预设」覆盖原预设。
                        </p>
                        <div v-if="showPresetSave" class="flex gap-2 animate-in fade-in duration-200">
                            <Input
                                v-model="presetName"
                                :disabled="isSavingPreset"
                                placeholder="预设名称，例如：AVIF 画质优先"
                                class="h-9 flex-1 rounded-lg bg-surface-2 border-hairline text-xs"
                                @keyup.enter="savePreset"
                            />
                            <Button
                                type="button"
                                size="sm"
                                :disabled="isSavingPreset || !presetName.trim()"
                                @click="savePreset"
                                class="h-9 px-4 rounded-lg bg-primary text-primary-foreground hover:bg-primary-hover text-xs font-semibold cursor-pointer gap-1.5"
                            >
                                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                                <span>保存</span>
                            </Button>
                        </div>
                    </div>

                    <ImageOptionsForm
                        v-model="options"
                        :source-probe="firstFileProbe"
                        :disabled="isParamsDisabled"
                    />
                </CardContent>
            </Card>

            <!-- Files Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden relative bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
                :class="{
                    'border-primary ring-1 ring-primary/20 bg-surface-2/10': isDragActive,
                }"
                @dragenter.prevent="handleDragEnter"
                @dragover.prevent="handleDragOver"
                @drop.stop.prevent="handleDrop"
            >
                <!-- Drag overlay -->
                <Transition
                    enter-active-class="transition duration-200 ease-out"
                    enter-from-class="opacity-0 scale-95"
                    enter-to-class="opacity-100 scale-100"
                    leave-active-class="transition duration-150 ease-in"
                    leave-from-class="opacity-100 scale-100"
                    leave-to-class="opacity-0 scale-95"
                >
                    <div
                        v-if="isDragActive"
                        class="absolute inset-0 bg-canvas/90 backdrop-blur-[1px] z-50 flex flex-col items-center justify-center gap-4 transition-all duration-300 border border-dashed border-primary/50 rounded-xl pointer-events-auto"
                        @dragleave.prevent="handleDragLeave"
                        @dragover.prevent="handleDragOver"
                        @drop.stop.prevent="handleDrop"
                    >
                        <div
                            class="h-12 w-12 rounded-lg bg-surface-2 flex items-center justify-center text-primary shadow-sm border border-hairline animate-bounce"
                        >
                            <UploadCloud class="w-6 h-6" />
                        </div>
                        <p class="text-xs font-semibold text-primary tracking-wide">
                            {{ $t("new_task.drop_files") }}
                        </p>
                    </div>
                </Transition>

                <CardHeader
                    class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between space-y-0"
                >
                    <CardTitle class="text-sm font-semibold text-ink tracking-tight">
                        {{ $t("new_task.media_files") }}
                    </CardTitle>
                    <div class="relative">
                        <input
                            type="file"
                            multiple
                            class="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed z-10"
                            @change="handleFileSelect"
                            :disabled="isFormDisabled"
                        />
                        <Button
                            variant="secondary"
                            size="sm"
                            class="rounded-lg px-3 h-8 gap-1.5 font-semibold bg-surface-2 text-ink border border-hairline hover:bg-surface-3 cursor-pointer"
                            :disabled="isFormDisabled"
                        >
                            <Plus class="w-3.5 h-3.5" />
                            {{ $t("new_task.add_files") }}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent class="p-5">
                    <div v-if="files.length > 0" class="space-y-2">
                        <div
                            v-for="file in files"
                            :key="file.id"
                            class="group flex items-center justify-between p-3 rounded-lg border border-hairline bg-surface-2/40 hover:bg-surface-2 transition-all duration-200"
                        >
                            <div class="flex items-center space-x-3 truncate flex-1 pr-4">
                                <div
                                    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface-1 border border-hairline text-ink-subtle shadow-xs group-hover:text-primary group-hover:border-primary/20 transition-all"
                                >
                                    <FileImage
                                        v-if="file.type?.startsWith('image/')"
                                        class="w-4 h-4"
                                    />
                                    <FileVideo
                                        v-else-if="file.type?.startsWith('video/')"
                                        class="w-4 h-4"
                                    />
                                    <FileCode v-else class="w-4 h-4" />
                                </div>
                                <div class="flex-1 truncate">
                                    <p class="text-xs font-semibold text-ink truncate">
                                        {{ file.name }}
                                    </p>
                                    <p
                                        v-if="file.status === 'error' && file.error"
                                        class="text-xs font-medium text-red-500 mt-1 truncate"
                                        :title="file.error"
                                    >
                                        {{ file.error }}
                                    </p>
                                    <p v-else class="text-xs font-medium text-ink-subtle mt-0.5">
                                        {{ (file.size / 1024 / 1024).toFixed(2) }} MB
                                    </p>
                                </div>
                            </div>

                            <div class="flex items-center space-x-4">
                                <div
                                    v-if="file.status === 'uploading'"
                                    class="w-24 flex flex-col items-end gap-1 shrink-0"
                                >
                                    <span class="text-xs font-semibold text-primary"
                                        >{{ file.progress }}%</span
                                    >
                                    <div
                                        class="h-1 w-full rounded-full bg-surface-2 overflow-hidden"
                                    >
                                        <div
                                            class="h-full bg-primary transition-all duration-500 ease-out"
                                            :style="{ width: `${file.progress}%` }"
                                        />
                                    </div>
                                </div>

                                <Button
                                    v-if="
                                        !isCreatingTask &&
                                        file.status !== 'deleting' &&
                                        file.status !== 'aborting'
                                    "
                                    variant="ghost"
                                    size="icon"
                                    @click="removeFile(file.id)"
                                    class="h-7 w-7 rounded-lg text-ink-subtle hover:text-red-500 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-all cursor-pointer"
                                >
                                    <X class="w-3.5 h-3.5" />
                                </Button>
                                <div
                                    v-if="file.status === 'uploaded'"
                                    class="w-7 h-7 rounded-full bg-emerald-500/10 flex items-center justify-center text-emerald-600 dark:text-emerald-400"
                                >
                                    <Check
                                        class="w-3.5 h-3.5 text-emerald-600 dark:text-emerald-400"
                                        stroke-width="2.5"
                                    />
                                </div>
                                <div
                                    v-else-if="file.status === 'error'"
                                    class="w-7 h-7 rounded-full bg-red-500/10 flex items-center justify-center text-red-500"
                                >
                                    <AlertCircle class="w-3.5 h-3.5" />
                                </div>
                            </div>
                        </div>
                    </div>

                    <div
                        v-else
                        class="flex flex-col items-center justify-center py-16 px-6 text-center border-2 border-dashed border-hairline rounded-xl bg-surface-2/20"
                    >
                        <div
                            class="h-10 w-10 rounded-lg bg-surface-2 flex items-center justify-center mb-4 text-ink-subtle shadow-xs border border-hairline"
                        >
                            <UploadCloud class="w-5 h-5 opacity-70" />
                        </div>
                        <p class="text-sm font-semibold text-ink mb-1">
                            {{ $t("new_task.no_files") }}
                        </p>
                        <p class="text-sm text-ink-subtle font-medium max-w-xs">
                            {{ $t("new_task.no_files_desc") }}
                        </p>
                    </div>

                    <div v-if="files.length > 0" class="pt-6 mt-6 border-t border-hairline">
                        <div
                            v-if="isUploading"
                            class="space-y-3 mb-6 max-w-md ml-auto animate-in fade-in duration-300"
                        >
                            <div class="flex justify-between items-end">
                                <span class="text-xs font-semibold text-ink-muted">
                                    {{ $t("new_task.uploading", { count: files.length }) }}
                                </span>
                                <span
                                    class="text-xl font-semibold text-primary dark:text-primary-hover"
                                    >{{ globalProgress }}%</span
                                >
                            </div>
                            <div class="h-1.5 w-full rounded-full bg-surface-2 overflow-hidden">
                                <div
                                    class="h-full bg-primary rounded-full transition-all duration-500 ease-out shadow-xs"
                                    :style="{ width: `${globalProgress}%` }"
                                />
                            </div>
                        </div>

                        <div class="flex justify-end">
                            <Button
                                @click="handleAction"
                                :disabled="isSubmitDisabled"
                                class="rounded-lg px-6 h-9 text-xs font-semibold bg-primary hover:bg-primary-hover text-primary-foreground shadow-sm transition-all duration-200 cursor-pointer"
                            >
                                <Loader2
                                    v-if="isUploading || isCreatingTask"
                                    class="mr-2 h-3.5 w-3.5 animate-spin"
                                />
                                {{ submitButtonText }}
                            </Button>
                        </div>
                    </div>

                    <!-- Recent Uploads Section -->
                    <div class="mt-8 pt-6 border-t border-hairline">
                        <div class="flex items-center justify-between mb-4">
                            <h3
                                class="text-xs font-semibold uppercase tracking-wider text-ink-muted flex items-center gap-1.5"
                            >
                                <HardDrive class="w-3.5 h-3.5" />
                                未提交的历史文件草稿
                            </h3>
                            <Button
                                variant="ghost"
                                size="sm"
                                class="h-7 px-2 text-xs font-semibold text-primary hover:bg-primary/5 cursor-pointer"
                                @click="fetchRecentFiles"
                                :disabled="isLoadingRecent"
                            >
                                <Loader2 v-if="isLoadingRecent" class="mr-1 h-3 w-3 animate-spin" />
                                刷新
                            </Button>
                        </div>

                        <div
                            v-if="filteredRecentFiles.length > 0"
                            class="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-48 overflow-y-auto pr-1"
                        >
                            <div
                                v-for="file in filteredRecentFiles"
                                :key="file.id"
                                class="flex items-center justify-between p-2 rounded-lg border border-hairline bg-surface-2/20 hover:bg-surface-2 transition-all"
                            >
                                <div
                                    class="flex items-center space-x-2 truncate flex-1 min-w-0 mr-2"
                                >
                                    <FileImage
                                        v-if="file.mime_type?.startsWith('image/')"
                                        class="w-3.5 h-3.5 shrink-0 text-ink-subtle"
                                    />
                                    <FileVideo
                                        v-else-if="file.mime_type?.startsWith('video/')"
                                        class="w-3.5 h-3.5 shrink-0 text-ink-subtle"
                                    />
                                    <FileCode v-else class="w-3.5 h-3.5 shrink-0 text-ink-subtle" />
                                    <span
                                        class="text-xs font-medium text-ink truncate"
                                        :title="file.name"
                                        >{{ file.name }}</span
                                    >
                                </div>
                                <div class="flex items-center space-x-1 shrink-0">
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        class="h-6 px-1.5 text-xs text-primary hover:bg-primary/5 font-semibold cursor-pointer"
                                        @click="addRecentFile(file)"
                                    >
                                        添加
                                    </Button>
                                    <Button
                                        variant="ghost"
                                        size="icon"
                                        class="h-6 w-6 text-ink-subtle hover:text-red-500 hover:bg-red-500/5 cursor-pointer"
                                        @click="deleteRecentFile(file.id)"
                                    >
                                        <Trash2 class="w-3.5 h-3.5" />
                                    </Button>
                                </div>
                            </div>
                        </div>
                        <div
                            v-else
                            class="text-center py-6 text-xs text-ink-subtle border border-dashed border-hairline rounded-lg"
                        >
                            无未提交的历史文件草稿
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    </div>
</template>
