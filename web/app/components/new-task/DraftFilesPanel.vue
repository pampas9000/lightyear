<script setup lang="ts">
import { FileCode, FileImage, FileVideo, HardDrive, Loader2, Trash2 } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import type { DraftFile } from "~/lib/types/s3";

// Files uploaded earlier but not used in a task yet; each can be added or deleted.
defineProps<{
    files: DraftFile[];
    loading: boolean;
}>();

const emit = defineEmits<{
    refresh: [];
    add: [file: DraftFile];
    remove: [id: string];
}>();
</script>

<template>
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
                @click="emit('refresh')"
                :disabled="loading"
            >
                <Loader2 v-if="loading" class="mr-1 h-3 w-3 animate-spin" />
                刷新
            </Button>
        </div>

        <div
            v-if="files.length > 0"
            class="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-48 overflow-y-auto pr-1"
        >
            <div
                v-for="file in files"
                :key="file.id"
                class="flex items-center justify-between p-2 rounded-lg border border-hairline bg-surface-1 hover:bg-surface-2 transition-colors"
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
                        @click="emit('add', file)"
                    >
                        添加
                    </Button>
                    <Button
                        variant="ghost"
                        size="icon"
                        class="h-6 w-6 text-ink-subtle hover:text-red-500 hover:bg-red-500/5 cursor-pointer"
                        @click="emit('remove', file.id)"
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
</template>
