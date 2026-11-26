<script setup lang="ts">
import { computed } from 'vue'
import { AlertCircle, CheckCircle, Download, Loader2, RotateCcw, Sliders, X } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { formatBytes, type TotalSavings } from '~/lib/task-detail'
import { getErrorDiagnostic } from '~/lib/utils/error-codes'
import type { Task } from '~/lib/types/task'

// The banner under the task header: progress while the task runs, savings
// once it completes, retry actions after a failure or cancel.
const props = defineProps<{
    task: Task
    savings: TotalSavings | null
    completedCount: number
    failedCount: number
    downloadableCount: number
    isRetrying: boolean
    isDownloadingAll: boolean
}>()

const emit = defineEmits<{
    /** Retry every failed (or cancelled) job with its options. */
    retry: []
    /** Open the retry drawer to change options first. */
    tune: []
    'download-all': []
}>()

const progress = computed(() => {
    const jobs = props.task.jobs
    if (!jobs || jobs.length === 0) return 0
    const sum = jobs.reduce((acc, job) => acc + (job.progress || 0), 0)
    return Math.round(sum / jobs.length)
})

// Failed jobs grouped by diagnosed error code.
const failedSummary = computed(() => {
    const counts: Record<string, { code: string; title: string; count: number; badgeClass: string; retryable: boolean }> = {}
    for (const job of props.task.jobs ?? []) {
        if (job.status === 'FAILED') {
            const diag = getErrorDiagnostic(job.error_code, job.error_message)
            if (!counts[diag.code]) {
                counts[diag.code] = {
                    code: diag.code,
                    title: diag.title,
                    count: 1,
                    badgeClass: diag.badgeClass,
                    retryable: diag.retryable,
                }
            } else {
                const item = counts[diag.code]
                if (item) item.count++
            }
        }
    }
    return Object.values(counts)
})
</script>

<template>
    <div v-if="task.status === 'PROCESSING' || task.status === 'PENDING'" class="border border-primary/20 bg-primary/5 rounded-xl p-4.5 space-y-2.5">
        <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
                <Loader2 class="w-4 h-4 text-primary animate-spin" />
                <h3 class="text-xs font-semibold text-ink uppercase tracking-wider">{{ $t('task_detail.processing_banner_title') }}</h3>
            </div>
            <span class="text-xs font-mono font-semibold text-primary">{{ progress }}%</span>
        </div>
        <p class="text-xs text-ink-subtle font-medium">{{ $t('task_detail.processing_banner_desc') }}</p>
        <!-- Glowing Progress Bar -->
        <div class="w-full bg-surface-2 rounded-full h-1.5 overflow-hidden border border-hairline">
            <div class="bg-primary h-full rounded-full transition-all duration-500" :style="{ width: `${progress}%` }"></div>
        </div>
    </div>

    <!-- 2) Completion Banner -->
    <div v-else-if="task.status === 'COMPLETED' && savings" class="border border-emerald-500/20 bg-emerald-500/5 rounded-xl p-4.5 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div class="space-y-1">
            <div class="flex items-center gap-2">
                <CheckCircle class="w-4 h-4 text-emerald-600 dark:text-emerald-400" />
                <h3 class="text-xs font-semibold text-ink uppercase tracking-wider">{{ $t('task_detail.completed_banner_title') }}</h3>
            </div>
            <p class="text-xs text-ink-subtle font-medium">
                <template v-if="savings.multi">
                    {{
                        savings.larger
                            ? $t('task_detail.completed_banner_multi_grew_desc', { count: savings.completedCount, input: formatBytes(savings.input), output: formatBytes(savings.output), ratio: savings.ratio })
                            : $t('task_detail.completed_banner_multi_desc', { count: savings.completedCount, input: formatBytes(savings.input), output: formatBytes(savings.output), ratio: savings.ratio })
                    }}
                </template>
                <template v-else>
                    {{
                        savings.larger
                            ? $t('task_detail.completed_banner_grew_desc', { count: completedCount, delta: formatBytes(savings.absDelta), ratio: savings.ratio })
                            : $t('task_detail.completed_banner_desc', { count: completedCount, saved: formatBytes(savings.saved), ratio: savings.ratio })
                    }}
                </template>
            </p>
        </div>
        <button
            v-if="downloadableCount > 0"
            @click="emit('download-all')"
            :disabled="isDownloadingAll"
            class="shrink-0 h-8.5 px-4 rounded-lg bg-emerald-600 hover:bg-emerald-700 text-white text-xs font-semibold flex items-center justify-center gap-2 transition-colors cursor-pointer disabled:opacity-50"
        >
            <Loader2 v-if="isDownloadingAll" class="w-3.5 h-3.5 animate-spin" />
            <Download v-else class="w-3.5 h-3.5" />
            <span>{{ $t('task_detail.download_all') }}</span>
        </button>
    </div>

    <!-- 3) Clean Failed Banner (Sleek Linear/Vercel Style) -->
    <div v-else-if="task.status === 'FAILED' || task.status === 'PARTIALLY_FAILED'"
        class="border border-rose-500/20 bg-rose-500/5 dark:bg-rose-950/20 rounded-xl p-4 sm:p-5 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div class="space-y-2 min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
                <AlertCircle class="w-4 h-4 text-rose-600 dark:text-rose-400 shrink-0" />
                <h3 class="text-xs font-semibold text-rose-600 dark:text-rose-400 uppercase tracking-wider flex items-center gap-2">
                    {{ $t('task_detail.failed_banner_title') }}
                </h3>
                <Badge variant="outline" class="text-[10px] font-mono px-2 py-0 border-rose-500/30 text-rose-600 dark:text-rose-400 bg-rose-500/10">
                    {{ failedCount }} 个失败作业
                </Badge>
            </div>
            <p class="text-xs text-ink-subtle font-medium leading-relaxed">
                {{ $t('task_detail.failed_banner_desc', { count: failedCount }) }}
                点击任意作业的异常标签可查看具体诊断分析与错误堆栈。
            </p>

            <!-- Error Code Breakdown Badges in Banner -->
            <div v-if="failedSummary.length > 0" class="flex items-center gap-1.5 flex-wrap pt-0.5">
                <span
                    v-for="item in failedSummary"
                    :key="item.code"
                    class="inline-flex items-center gap-1 text-[10.5px] font-mono font-semibold px-2 py-0.5 rounded-md border"
                    :class="item.badgeClass"
                >
                    <span>{{ item.title }}</span>
                    <span class="opacity-80">({{ item.count }})</span>
                </span>
            </div>
        </div>

        <div class="flex items-center gap-2 shrink-0">
            <button
                @click="emit('retry')"
                :disabled="isRetrying"
                class="h-8.5 px-3.5 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-medium flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
            >
                <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                <RotateCcw v-else class="w-3.5 h-3.5" />
                <span>一键重试失败项</span>
            </button>

            <button
                @click="emit('tune')"
                :disabled="isRetrying"
                class="h-8.5 px-3.5 rounded-lg border border-hairline bg-surface-1 hover:bg-surface-2 text-ink text-xs font-medium flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
            >
                <Sliders class="w-3.5 h-3.5 text-ink-subtle" />
                <span>调整参数重试</span>
            </button>
        </div>
    </div>

    <!-- 4) Cancelled Banner -->
    <div v-else-if="task.status === 'CANCELLED'"
        class="border border-zinc-500/20 bg-zinc-500/5 dark:bg-zinc-900/20 rounded-xl p-4 sm:p-5 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div class="space-y-1 min-w-0">
            <div class="flex items-center gap-2">
                <X class="w-4 h-4 text-zinc-500 shrink-0" />
                <h3 class="text-xs font-semibold text-zinc-600 dark:text-zinc-400 uppercase tracking-wider">
                    {{ $t('task_detail.cancelled_banner_title') }}
                </h3>
            </div>
            <p class="text-xs text-ink-subtle font-medium leading-relaxed">
                {{ $t('task_detail.cancelled_banner_desc') }}
            </p>
        </div>

        <div class="flex items-center gap-2 shrink-0">
            <button
                @click="emit('retry')"
                :disabled="isRetrying"
                class="h-8.5 px-3.5 rounded-lg bg-primary hover:bg-primary-hover text-primary-foreground text-xs font-medium flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
            >
                <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                <RotateCcw v-else class="w-3.5 h-3.5" />
                <span>重新调度全部作业</span>
            </button>
        </div>
    </div>
</template>
