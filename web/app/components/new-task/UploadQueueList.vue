<script setup lang="ts">
import { AlertCircle, Check, FileCode, FileImage, FileVideo, Loader2, UploadCloud, X } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import type { QueueItem } from "~/composables/useUploadQueue";

// The files queued for the task, with each one's upload state.
defineProps<{
    files: QueueItem[];
    /** While the task is being created, files can no longer be removed. */
    isCreatingTask: boolean;
}>();

const emit = defineEmits<{
    remove: [id: string];
}>();
</script>

<template>
    <div v-if="files.length > 0" class="space-y-2">
        <div
            v-for="file in files"
            :key="file.id"
            class="group flex items-center justify-between p-3 rounded-lg border border-hairline bg-surface-1 hover:bg-surface-2 transition-colors"
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
                    v-if="file.status === 'queued'"
                    class="flex items-center gap-1.5 shrink-0 text-xs font-medium text-ink-subtle"
                >
                    <Loader2 class="w-3.5 h-3.5 animate-spin" />
                    {{ $t("new_task.preparing") }}
                </div>
                <div
                    v-else-if="file.status === 'uploading'"
                    class="w-24 flex flex-col items-end gap-1 shrink-0"
                >
                    <span class="text-xs font-semibold text-primary"
                        >{{ file.progress }}%</span
                    >
                    <div
                        class="h-1 w-full rounded-full bg-surface-3 overflow-hidden"
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
                    @click="emit('remove', file.id)"
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
        class="flex flex-col items-center justify-center py-16 px-6 text-center border-[1.5px] border-dashed border-hairline-tertiary rounded-xl bg-surface-1"
    >
        <div
            class="h-10 w-10 rounded-full bg-primary-soft flex items-center justify-center mb-4 text-primary"
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
</template>
