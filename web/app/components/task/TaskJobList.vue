<script setup lang="ts">
import { computed, ref } from 'vue'
import { Sparkles } from '@lucide/vue'
import TaskJobRow from '~/components/task/TaskJobRow.vue'
import type { Job } from '~/lib/types/task'

// A task's jobs, filtered by the All / Completed / Failed tabs.
const props = defineProps<{
    jobs: Job[]
    completedCount: number
    failedCount: number
    hasMultiOutputs: boolean
    isRetrying: boolean
    retryingJobs: Record<string, boolean>
    downloadingJobs: Record<string, boolean>
}>()

const emit = defineEmits<{
    preview: [job: Job]
    diagnose: [job: Job]
    retry: [jobId: string]
    tune: [jobId: string]
    download: [job: Job]
}>()

const activeTab = ref<'all' | 'completed' | 'failed'>('all')

const filteredJobs = computed(() => {
    if (activeTab.value === 'completed') {
        return props.jobs.filter(j => j.status === 'COMPLETED')
    }
    if (activeTab.value === 'failed') {
        return props.jobs.filter(j => j.status === 'FAILED')
    }
    return props.jobs
})
</script>

<template>
    <div class="border border-hairline rounded-xl bg-surface-1 overflow-hidden flex flex-col">
        <!-- List Header & Filter Tabs -->
        <div class="flex flex-col sm:flex-row sm:items-center justify-between border-b border-hairline px-5 py-3 bg-surface-1 gap-3">
            <div class="flex items-center gap-2">
                <Sparkles class="w-4 h-4 text-ink-subtle" stroke-width="1.5" />
                <h3 class="font-semibold text-xs text-ink uppercase tracking-wider flex items-center gap-2">
                    {{ $t('task_detail.transcoding_jobs') }}
                    <span class="text-ink-subtle font-mono text-xs font-normal">({{ jobs.length }})</span>
                </h3>
            </div>

            <!-- Filter Tabs (Segment Control Style) -->
            <div class="flex items-center gap-0.5 bg-surface-2 p-0.5 rounded-lg border border-hairline self-start sm:self-auto">
                <button
                    @click="activeTab = 'all'"
                    class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                    :class="activeTab === 'all' ? 'bg-surface-1 text-ink font-semibold' : 'text-ink-subtle hover:text-ink'"
                >
                    {{ $t('task_detail.all_jobs', { count: jobs.length }) }}
                </button>
                <button
                    @click="activeTab = 'completed'"
                    class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                    :class="activeTab === 'completed' ? 'bg-surface-1 text-ink font-semibold' : 'text-ink-subtle hover:text-ink'"
                >
                    {{ $t('task_detail.completed_jobs', { count: completedCount }) }}
                </button>
                <button
                    v-if="failedCount > 0"
                    @click="activeTab = 'failed'"
                    class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                    :class="activeTab === 'failed' ? 'bg-surface-1 text-rose-600 font-semibold' : 'text-ink-subtle hover:text-ink'"
                >
                    {{ $t('task_detail.failed_jobs', { count: failedCount }) }}
                </button>
            </div>
        </div>

        <!-- Job Items List (Clean Flat Layout) -->
        <div class="divide-y divide-hairline">
            <TaskJobRow
                v-for="job in filteredJobs"
                :key="job.id"
                :job="job"
                :has-multi-outputs="hasMultiOutputs"
                :is-retrying="isRetrying"
                :retrying="!!retryingJobs[job.id]"
                :downloading="!!downloadingJobs[job.id]"
                @preview="emit('preview', $event)"
                @diagnose="emit('diagnose', $event)"
                @retry="emit('retry', $event)"
                @tune="emit('tune', $event)"
                @download="emit('download', $event)"
            />

            <div v-if="filteredJobs.length === 0" class="p-8 text-center text-xs text-ink-subtle">
                该分类下暂无作业。
            </div>
        </div>
    </div>
</template>
