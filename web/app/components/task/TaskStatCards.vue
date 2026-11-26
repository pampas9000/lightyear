<script setup lang="ts">
import { computed } from 'vue'
import { Clock, FileVideo, Layers, TrendingDown } from '@lucide/vue'
import { formatBytes, hasMultiOutputs as taskHasMultiOutputs, type TotalSavings } from '~/lib/task-detail'
import type { Task } from '~/lib/types/task'

// Format, file count, size change and duration of a task.
const props = defineProps<{
    task: Task
    savings: TotalSavings | null
    completedCount: number
    duration: string
}>()

const hasMultiOutputs = computed(() => taskHasMultiOutputs(props.task))

const uniqueSourceFilesCount = computed(() =>
    new Set((props.task.jobs || []).map(j => j.source_file_id)).size,
)

const uniqueFormatsLabel = computed(() => {
    const fmts = Array.from(
        new Set((props.task.jobs || []).map(j => j.options?.format?.toUpperCase()).filter(Boolean)),
    )
    return fmts.length > 0 ? fmts.join(' · ') : '—'
})
</script>

<template>
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
        <!-- Card 1: Target Format & Engine -->
        <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
            <div class="flex items-center justify-between">
                <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.target_format') }}</span>
                <Layers class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
            </div>
            <div class="flex items-baseline gap-2 mt-2">
                <span class="text-xl font-bold tracking-tight text-ink uppercase">{{ uniqueFormatsLabel }}</span>
            </div>
        </div>

        <!-- Card 2: Total Files & Jobs -->
        <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
            <div class="flex items-center justify-between">
                <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.total_files') }}</span>
                <FileVideo class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
            </div>
            <div class="flex items-baseline gap-2 mt-2">
                <span class="text-xl font-bold tracking-tight text-ink">{{ hasMultiOutputs ? uniqueSourceFilesCount : (task.jobs?.length || 0) }}</span>
                <span class="text-xs font-medium text-ink-subtle">{{ $t('task_detail.files_unit') }}</span>
                <span v-if="hasMultiOutputs" class="text-xs font-medium text-ink-subtle">
                    · {{ $t('task_detail.outputs_count', { count: task.jobs?.length || 0 }) }}
                </span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded border border-emerald-500/20 ml-auto" v-if="completedCount > 0">
                    {{ $t('task_detail.completed_badge', { count: completedCount }) }}
                </span>
            </div>
        </div>

        <!-- Card 3: Storage Reduction -->
        <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
            <div class="flex items-center justify-between">
                <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.total_savings') }}</span>
                <TrendingDown class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
            </div>
            <div class="flex items-baseline gap-1.5 mt-2">
                <template v-if="savings">
                    <span
                        class="text-xl font-bold tracking-tight tabular-nums"
                        :class="savings.larger ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'"
                    >
                        {{ savings.larger ? '+' : '-' }}{{ savings.ratio }}%
                    </span>
                    <span class="text-xs font-medium text-ink-subtle truncate">
                        <template v-if="savings.multi">
                            ({{ $t('task_detail.avg_per_output_total', { total: formatBytes(savings.output) }) }})
                        </template>
                        <template v-else>
                            ({{ savings.larger ? `+${formatBytes(savings.absDelta)}` : formatBytes(savings.saved) }})
                        </template>
                    </span>
                </template>
                <template v-else-if="task.status === 'PROCESSING' || task.status === 'PENDING'">
                    <span class="text-xs font-medium text-ink-subtle animate-pulse">{{ $t('task_detail.calculating') }}</span>
                </template>
                <template v-else>
                    <span class="text-xl font-bold tracking-tight text-ink">—</span>
                </template>
            </div>
        </div>

        <!-- Card 4: Elapsed & Duration -->
        <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
            <div class="flex items-center justify-between">
                <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.duration') }}</span>
                <Clock class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
            </div>
            <div class="flex items-baseline gap-2 mt-2">
                <span class="text-xl font-bold tracking-tight text-ink">{{ duration }}</span>
                <span class="text-xs font-medium text-ink-subtle" v-if="task.status === 'PROCESSING'">Live</span>
            </div>
        </div>
    </div>
</template>
