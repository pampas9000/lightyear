<script setup lang="ts">
import { computed } from 'vue'
import { AlertCircle, CheckCircle2, Clock, Download, Eye, Info, Loader2, RotateCcw, Sliders, X } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { formatShortBytes } from '~/lib/image-options'
import { formatBytes, formatFilename, isImageFile, isOutputStored, jobSavings, jobStatusStyle } from '~/lib/task-detail'
import type { Job } from '~/lib/types/task'

// One job in the task's job list, with its actions.
const props = defineProps<{
    job: Job
    /** The task has more than one output per source; label each output. */
    hasMultiOutputs: boolean
    /** Any retry of this task is in flight. */
    isRetrying: boolean
    /** A retry of this job is in flight. */
    retrying: boolean
    downloading: boolean
}>()

const emit = defineEmits<{
    preview: [job: Job]
    diagnose: [job: Job]
    retry: [jobId: string]
    tune: [jobId: string]
    download: [job: Job]
}>()

const { t } = useI18n()
const { date } = useTime()

const savings = computed(() => jobSavings(props.job))

const outputGoneLabel = (job: Job) => {
    switch (job.output_file?.status) {
        case 'EXPIRED': return t('task_detail.output_expired')
        case 'DELETED': return t('task_detail.output_deleted')
        default: return ''
    }
}
</script>

<template>
<div
    class="px-5 py-3.5 flex items-center justify-between gap-4 hover:bg-surface-2 transition-colors duration-150"
>
    <!-- Left: Status Icon & File Info -->
    <div class="flex items-center gap-3.5 min-w-0 flex-1">
        <!-- Far Left Status Badge Icon -->
        <div class="shrink-0">
            <!-- Processing -->
            <div v-if="job.status === 'PROCESSING' || job.status === 'PENDING'" class="w-6 h-6 rounded-full bg-primary/10 text-primary flex items-center justify-center">
                <Loader2 class="w-3.5 h-3.5 animate-spin" />
            </div>
            <!-- Completed -->
            <div v-else-if="job.status === 'COMPLETED'" class="w-6 h-6 rounded-full bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 flex items-center justify-center">
                <CheckCircle2 class="w-3.5 h-3.5" />
            </div>
            <!-- Cancelled -->
            <div v-else-if="job.status === 'CANCELLED'" class="w-6 h-6 rounded-full bg-zinc-500/10 text-zinc-500 flex items-center justify-center" title="已取消">
                <X class="w-3.5 h-3.5" />
            </div>
            <!-- Failed -->
            <div v-else class="w-6 h-6 rounded-full bg-rose-500/10 text-rose-600 dark:text-rose-400 flex items-center justify-center" title="失败">
                <AlertCircle class="w-3.5 h-3.5" />
            </div>
        </div>

        <!-- File Details & Inline Metadata -->
        <div class="min-w-0 flex-1 space-y-0.5">
            <!-- Line 1: Filename, Format Badge & Status Badge -->
            <div class="flex items-center gap-2 flex-wrap">
                <span class="text-sm font-semibold text-ink truncate max-w-[280px] sm:max-w-[440px]" :title="job.source_file?.name">
                    {{ formatFilename(job.source_file?.name) }}
                </span>
                <span
                    v-if="hasMultiOutputs || job.output_name"
                    class="text-[10px] font-mono font-semibold px-1.5 py-0.2 rounded bg-surface-2 text-ink border border-hairline"
                >
                    {{ job.output_name || `#${(job.output_index ?? 0) + 1}` }}
                </span>
                <span class="text-[10px] font-mono font-semibold uppercase px-1.5 py-0.2 rounded bg-surface-2 text-ink-subtle border border-hairline">
                    {{ job.options?.format }}
                </span>
                <span
                    v-if="job.target?.max_bytes"
                    class="text-[10px] font-mono font-medium px-1.5 py-0.2 rounded bg-surface-2 text-ink-subtle border border-hairline"
                >
                    ≤{{ formatShortBytes(job.target.max_bytes) }}
                </span>
                <Badge variant="secondary" class="rounded font-medium text-[10px] px-1.5 py-0.2 border capitalize tracking-normal" :class="jobStatusStyle(job.status)">
                    {{ $t('status.' + job.status) }}
                </Badge>
            </div>

            <!-- Line 2: Size, Retries & Error Detail (Inline) -->
            <div class="flex items-center gap-2 text-xs text-ink-subtle font-medium flex-wrap">
                <span class="font-mono">{{ formatBytes(job.source_file?.size || 0) }}</span>

                <!-- Completed Output Size -->
                <template v-if="job.status === 'COMPLETED' && job.output_file?.size">
                    <span>·</span>
                    <span
                        class="font-mono font-semibold"
                        :class="savings?.larger ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'"
                    >
                        {{ formatBytes(job.output_file.size) }}
                        <template v-if="savings">({{ savings?.label }})</template>
                    </span>
                    <span
                        v-if="savings?.larger"
                        class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20"
                    >
                        {{ $t('task_detail.larger_than_source') }}
                    </span>
                    <span
                        v-if="job.resolved_options?.quality != null && job.options?.quality == null"
                        class="text-[10px] font-mono px-1.5 py-0.2 rounded bg-surface-2 text-ink-subtle border border-hairline"
                    >
                        {{ $t('task_detail.resolved_label') }} Q{{ job.resolved_options.quality }}
                    </span>
                    <span
                        v-else-if="job.resolved_options?.distance != null && job.options?.distance == null"
                        class="text-[10px] font-mono px-1.5 py-0.2 rounded bg-surface-2 text-ink-subtle border border-hairline"
                    >
                        {{ $t('task_detail.resolved_label') }} d{{ job.resolved_options.distance }}
                    </span>
                    <span
                        v-if="job.target_met === true"
                        class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20"
                    >
                        {{ $t('task_detail.target_met') }}
                    </span>
                    <span
                        v-else-if="job.target_met === false"
                        class="text-[10px] font-medium px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20"
                    >
                        {{ $t('task_detail.target_missed') }}
                    </span>
                </template>

                <!-- Processing State -->
                <template v-else-if="job.status === 'PROCESSING' || job.status === 'PENDING'">
                    <span>·</span>
                    <span class="text-primary font-medium animate-pulse">{{ $t('task_detail.job_processing') }}</span>
                </template>

                <!-- Attempts badge once a job has run more than once -->
                <template v-if="job.attempts > 1">
                    <span>·</span>
                    <span class="text-[10.5px] font-mono text-amber-600 dark:text-amber-400 bg-amber-500/10 px-1.5 py-0.2 rounded border border-amber-500/20">
                        执行 {{ job.attempts }}/{{ job.max_attempts || 3 }} 次
                    </span>
                </template>

                <!-- Interactive Error Diagnosis Badge for Failed Jobs -->
                <template v-if="job.status === 'FAILED'">
                    <span>·</span>
                    <button
                        type="button"
                        @click.stop="emit('diagnose', job)"
                        class="inline-flex items-center gap-1 text-[11px] font-mono font-semibold text-rose-600 dark:text-rose-400 bg-rose-500/10 hover:bg-rose-500/20 px-2 py-0.5 rounded border border-rose-500/20 transition-all cursor-pointer group"
                        title="点击查看异常诊断与堆栈日志"
                    >
                        <AlertCircle class="w-3 h-3 text-rose-500" />
                        <span>{{ job.error_code || 'error' }}</span>
                        <span class="text-[10px] opacity-75 group-hover:opacity-100 group-hover:underline ml-0.5">诊断</span>
                    </button>

                    <!-- Inline Error Message Snippet -->
                    <span
                        v-if="job.error_message"
                        @click.stop="emit('diagnose', job)"
                        class="font-mono text-rose-600/80 dark:text-rose-400/80 text-[11px] truncate max-w-[200px] sm:max-w-[340px] hover:text-rose-600 cursor-pointer hover:underline"
                        :title="job.error_message"
                    >
                        {{ job.error_message }}
                    </span>
                </template>
            </div>
        </div>
    </div>

    <!-- Far Right: Clean Action Button Group -->
    <div class="flex items-center gap-2 shrink-0">
        <!-- Image Preview Trigger -->
        <button
            v-if="isOutputStored(job) && isImageFile(job.source_file?.name)"
            @click="emit('preview', job)"
            class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
            :title="$t('task_detail.preview')"
        >
            <Eye class="w-3.5 h-3.5 text-ink-subtle" />
            <span class="hidden sm:inline">{{ $t('task_detail.preview') }}</span>
        </button>

        <!-- Diagnosis Inspector Trigger for Failed Jobs -->
        <button
            v-if="job.status === 'FAILED'"
            @click="emit('diagnose', job)"
            class="h-8 px-2.5 text-xs font-medium border border-rose-500/30 bg-rose-500/5 hover:bg-rose-500/15 text-rose-600 dark:text-rose-400 rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
            title="查看异常原因与修复建议"
        >
            <Info class="w-3.5 h-3.5" />
            <span class="hidden sm:inline">诊断</span>
        </button>

        <!-- Per-Job Direct Retry Button -->
        <button
            v-if="job.status === 'FAILED' || job.status === 'CANCELLED'"
            @click="emit('retry', job.id)"
            :disabled="isRetrying || retrying"
            class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors disabled:opacity-50"
            :title="$t('task_detail.retry_task')"
        >
            <Loader2 v-if="retrying" class="w-3.5 h-3.5 animate-spin text-primary" />
            <RotateCcw v-else class="w-3.5 h-3.5 text-ink-subtle" />
            <span>{{ retrying ? $t('task_detail.retrying') : '重试' }}</span>
        </button>

        <!-- Adjust Parameters & Retry Drawer Trigger (Targeted to this single job) -->
        <button
            v-if="job.status === 'FAILED' || job.status === 'CANCELLED' || (job.status === 'COMPLETED' && job.target_met === false)"
            @click="emit('tune', job.id)"
            class="h-8 px-2.5 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink-subtle hover:text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
            title="针对此作业单独调参重试"
        >
            <Sliders class="w-3.5 h-3.5" />
            <span class="hidden sm:inline">调参</span>
        </button>

        <!-- Output removed by retention or deletion -->
        <span
            v-if="job.status === 'COMPLETED' && outputGoneLabel(job)"
            class="h-8 px-2.5 text-xs font-medium border border-dashed border-hairline text-ink-subtle rounded-lg flex items-center gap-1.5"
            :title="$t('task_detail.output_gone_hint')"
        >
            <Clock class="w-3.5 h-3.5" />
            <span>{{ outputGoneLabel(job) }}</span>
        </span>

        <!-- Download Button -->
        <button
            v-else-if="job.status === 'COMPLETED'"
            :disabled="downloading || !job.output_file_id"
            :title="job.output_file?.expires_at ? $t('task_detail.kept_until', { date: date(job.output_file.expires_at) }) : undefined"
            @click="emit('download', job)"
            class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
        >
            <Loader2 v-if="downloading" class="w-3.5 h-3.5 animate-spin" />
            <Download v-else class="w-3.5 h-3.5 text-ink-subtle" />
            <span>{{ downloading ? $t('task_detail.downloading') : $t('task_detail.download') }}</span>
        </button>
    </div>
</div>
</template>
