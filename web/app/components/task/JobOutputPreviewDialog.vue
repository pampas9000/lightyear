<script setup lang="ts">
import { Download, Eye, Loader2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { formatBytes } from '~/lib/task-detail'
import type { Job } from '~/lib/types/task'

// A job's output image; open while `job` is set.
defineProps<{
    job: Job | null
    url: string | null
    loading: boolean
}>()

const emit = defineEmits<{
    close: []
    download: [job: Job]
}>()
</script>

<template>
    <Dialog :open="!!job" @update:open="emit('close')">
        <DialogContent class="max-w-2xl bg-surface-1 border-hairline rounded-xl p-6 shadow-xl">
            <DialogHeader class="pb-4 border-b border-hairline flex flex-row items-center justify-between">
                <DialogTitle class="text-sm font-semibold text-ink flex items-center gap-2">
                    <Eye class="w-4 h-4 text-primary" />
                    {{ $t('task_detail.preview_title') }}
                </DialogTitle>
            </DialogHeader>

            <div class="py-6 flex flex-col items-center justify-center min-h-[260px] space-y-4">
                <Loader2 v-if="loading" class="w-8 h-8 text-primary animate-spin" />
                <template v-else-if="url">
                    <div class="relative max-h-[420px] overflow-hidden rounded-lg border border-hairline bg-surface-2 p-2 shadow-xs flex items-center justify-center w-full">
                        <img :src="url" :alt="job?.output_file?.name || 'Preview'" class="max-h-[380px] w-auto max-w-full object-contain rounded" />
                    </div>
                    <div class="flex items-center justify-between w-full text-xs font-medium text-ink-subtle px-1">
                        <span>Format: {{ job?.options?.format }}</span>
                        <span>Output Size: {{ formatBytes(job?.output_file?.size) }}</span>
                    </div>
                </template>
            </div>

            <div class="pt-4 border-t border-hairline flex items-center justify-end gap-3">
                <Button variant="outline" size="sm" @click="emit('close')" class="h-8.5 px-4 text-xs font-medium border-hairline">
                    {{ $t('task_detail.preview_close') }}
                </Button>
                <Button
                    v-if="job"
                    size="sm"
                    @click="emit('download', job)"
                    class="h-8.5 px-4 text-xs font-semibold gap-1.5 bg-primary text-primary-foreground shadow-xs"
                >
                    <Download class="w-3.5 h-3.5" />
                    <span>{{ $t('task_detail.download') }}</span>
                </Button>
            </div>
        </DialogContent>
    </Dialog>
</template>
