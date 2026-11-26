<script setup lang="ts">
import { useRouter } from "vue-router";
import { toast } from "vue-sonner";
import { parseApiError } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { useCreateTask } from "~/composables/queries/tasks";
import { useDeleteDraft, useDraftFiles } from "~/composables/queries/drafts";
import { useUploadQueue } from "~/composables/useUploadQueue";
import { useTaskDraft } from "~/composables/useTaskDraft";
import { useWorkflowPresets } from "~/composables/useWorkflowPresets";
import type { CompressionTarget } from "~/lib/types/task";
import type { DraftFile } from "~/lib/types/s3";
import type { ImageOptions } from "~/lib/types/image-options";
import { defaultOptions, sanitize } from "~/lib/image-options";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import ImageOptionsForm from "@/components/ImageOptionsForm.vue";
import WorkflowPresetPanel from "@/components/new-task/WorkflowPresetPanel.vue";
import FileDropOverlay from "@/components/new-task/FileDropOverlay.vue";
import UploadQueueList from "@/components/new-task/UploadQueueList.vue";
import TaskSubmitBar from "@/components/new-task/TaskSubmitBar.vue";
import DraftFilesPanel from "@/components/new-task/DraftFilesPanel.vue";
import { Plus, Settings } from "@lucide/vue";

useHead({
    title: $t("new_task.title") + " | Transcoder",
});

const router = useRouter();
const { isAuthenticated, showAuthModal, user } = useAuth();

// Output options and optional target for every file in the task.
const options = ref<ImageOptions>(defaultOptions());
const target = ref<CompressionTarget | undefined>(undefined);

// Authentication guard
const checkAuthBeforeAction = (): boolean => {
    if (!isAuthenticated.value) {
        showAuthModal.value = true;
        return false;
    }
    return true;
};

const presets = useWorkflowPresets(options, target, checkAuthBeforeAction);
const {
    workflows,
    selectedWorkflowId,
    selectedWorkflow,
    isMultiOutputWorkflow,
    workflowDirty,
    usesWorkflow,
    showPresetSave,
    presetName,
    isSavingPreset,
} = presets;

// Recent uploads (Draft files)
const draftsQuery = useDraftFiles();
const deleteDraft = useDeleteDraft();

const queue = useUploadQueue({
    onChange: () => draft.save(),
    deleteServerFile: (fileId) => deleteDraft.mutateAsync(fileId),
});
const { files, uploadedFileIds, globalProgress, isUploading, firstFileProbe } = queue;

const draft = useTaskDraft({
    files,
    uploadedFileIds,
    options,
    target,
    fetchServerFiles: () => draftsQuery.refetch(),
});
const { pendingSubmission, idempotencyKey } = draft;

const isCreatingTask = ref(false);
const taskCreated = ref(false);

const isFormDisabled = computed(() => isCreatingTask.value || taskCreated.value);

const isSubmitDisabled = computed(() => {
    if (isUploading.value) return true;
    if (isCreatingTask.value) return true;
    if (taskCreated.value) return true;
    if (files.value.length === 0) return true;
    return !files.value.every((f) => f.status === "uploaded" && f.serverFileId);
});

watch(
    () => user.value?.id,
    (newUid, oldUid) => {
        if (newUid !== oldUid) {
            queue.reset();
            pendingSubmission.value = null;
            idempotencyKey.value = "";
            // The drafts and workflow queries refetch for the new user.
            if (newUid) {
                draft.loadAndReconcile();
            }
        }
    },
);

const recentFiles = computed<DraftFile[]>(() => draftsQuery.data.value ?? []);
const filteredRecentFiles = computed(() => {
    return recentFiles.value.filter(
        (rf) => !files.value.some((f) => f.serverFileId === rf.id)
    );
});
const isLoadingRecent = computed(() => draftsQuery.isFetching.value);

const fetchRecentFiles = () => {
    if (!isAuthenticated.value) return;
    void draftsQuery.refetch();
};

const addRecentFile = (file: DraftFile) => {
    if (!checkAuthBeforeAction()) return;
    if (!queue.addServerFile(file)) {
        toast.info("文件已在处理队列中");
    }
};

const deleteRecentFile = async (fileId: string) => {
    if (!checkAuthBeforeAction()) return;
    try {
        await deleteDraft.mutateAsync(fileId);
        queue.forgetServerFile(fileId);
        toast.success("文件物理删除成功");
        draft.save();
    } catch (err) {
        console.error("Failed to delete file:", err);
        toast.error("删除文件失败");
    }
};

// Runs after the upload queue has set up Uppy.
onMounted(() => {
    if (isAuthenticated.value) {
        draft.loadAndReconcile();
    }
});

const handleFileSelect = (event: Event) => {
    if (!checkAuthBeforeAction()) return;
    const input = event.target as HTMLInputElement;
    if (input.files) {
        queue.addLocalFiles(input.files);
    }
    input.value = "";
};

const generateRequestSnapshot = () => {
    const sortedIds = files.value
        .filter((f) => f.status === "uploaded" && f.serverFileId)
        .map((f) => f.serverFileId as string);

    const items = sortedIds.map((id) => ({ file_id: id }));

    if (usesWorkflow.value) {
        return JSON.stringify({
            items,
            workflow_id: selectedWorkflowId.value,
        });
    }

    return JSON.stringify({
        items,
        options: sanitize(options.value),
        ...(target.value?.max_bytes ? { target: target.value } : {}),
    });
};

const createTask = useCreateTask();

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
        draft.save();
    }

    const body = JSON.parse(currentSnapshot);
    body.idempotency_key = key;

    isCreatingTask.value = true;
    try {
        const created = await createTask.mutateAsync(body);

        draft.clear();
        pendingSubmission.value = null;
        idempotencyKey.value = "";

        taskCreated.value = true;
        if (created?.id) {
            router.push(`/tasks/${created.id}`);
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

const handleDragEnter = () => {
    if (isFormDisabled.value) return;
    dragCounter.value++;
};

const handleDragLeave = () => {
    if (isFormDisabled.value) return;
    dragCounter.value = Math.max(0, dragCounter.value - 1);
};

const handleDrop = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value = 0;
    if (e.dataTransfer?.files) {
        queue.addLocalFiles(e.dataTransfer.files);
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
                    <WorkflowPresetPanel
                        v-if="isAuthenticated"
                        v-model:show-preset-save="showPresetSave"
                        v-model:preset-name="presetName"
                        :workflows="workflows"
                        :selected-workflow-id="selectedWorkflowId"
                        :selected-workflow="selectedWorkflow"
                        :is-multi-output-workflow="isMultiOutputWorkflow"
                        :workflow-dirty="workflowDirty"
                        :is-saving-preset="isSavingPreset"
                        :disabled="isFormDisabled"
                        @apply="presets.applyWorkflow"
                        @update="presets.updatePreset"
                        @remove="presets.deletePreset"
                        @save="presets.savePreset"
                    />

                    <ImageOptionsForm
                        v-model="options"
                        v-model:target="target"
                        target-support
                        :source-probe="firstFileProbe"
                        :disabled="isFormDisabled"
                        jpeg-repack
                    />
                </CardContent>
            </Card>

            <!-- Files Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden relative bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
                :class="{
                    'border-primary ring-1 ring-primary/20 bg-primary-soft': isDragActive,
                }"
                @dragenter.prevent="handleDragEnter"
                @dragover.prevent
                @drop.stop.prevent="handleDrop"
            >
                <!-- Drag overlay -->
                <FileDropOverlay :active="isDragActive" @leave="handleDragLeave" @drop="handleDrop" />

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
                            variant="outline"
                            size="sm"
                            class="rounded-md px-3 h-8 gap-1.5 text-xs cursor-pointer"
                            :disabled="isFormDisabled"
                        >
                            <Plus class="w-3.5 h-3.5" />
                            {{ $t("new_task.add_files") }}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent class="p-5">
                    <UploadQueueList
                        :files="files"
                        :is-creating-task="isCreatingTask"
                        @remove="queue.removeFile"
                    />

                    <TaskSubmitBar
                        v-if="files.length > 0"
                        :file-count="files.length"
                        :is-uploading="isUploading"
                        :is-creating-task="isCreatingTask"
                        :global-progress="globalProgress"
                        :is-submit-disabled="isSubmitDisabled"
                        :submit-button-text="submitButtonText"
                        @submit="submitTask"
                    />

                    <!-- Recent Uploads Section -->
                    <DraftFilesPanel
                        :files="filteredRecentFiles"
                        :loading="isLoadingRecent"
                        @refresh="fetchRecentFiles"
                        @add="addRecentFile"
                        @remove="deleteRecentFile"
                    />
                </CardContent>
            </Card>
        </div>
    </div>
</template>
