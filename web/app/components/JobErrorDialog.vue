<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Job } from '~/lib/types/task'
import { getErrorDiagnostic } from '~/lib/utils/error-codes'
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
    AlertCircle,
    RotateCcw,
    Sliders,
    Copy,
    Check,
    Terminal,
    Sparkles,
    FileText,
    Cpu,
    Info,
    CheckCircle2,
    Loader2,
} from '@lucide/vue'

const props = defineProps<{
    open: boolean
    job: Job | null
    isRetrying?: boolean
}>()

const emit = defineEmits<{
    (e: 'update:open', val: boolean): void
    (e: 'retry', jobId: string): void
    (e: 'tune', jobId: string): void
}>()

const diagnostic = computed(() => {
    if (!props.job) return null
    return getErrorDiagnostic(props.job.error_code, props.job.error_message)
})

const copied = ref(false)

const copyErrorMessage = async () => {
    if (!props.job?.error_message) return
    try {
        await navigator.clipboard.writeText(props.job.error_message)
        copied.value = true
        setTimeout(() => {
            copied.value = false
        }, 2000)
    } catch (e) {
        console.error('Failed to copy error message', e)
    }
}

const handleDirectRetry = () => {
    if (!props.job) return
    emit('retry', props.job.id)
    emit('update:open', false)
}

const handleTuneRetry = () => {
    if (!props.job) return
    emit('tune', props.job.id)
    emit('update:open', false)
}
</script>

<template>
    <Dialog :open="open" @update:open="val => emit('update:open', val)">
        <DialogContent class="max-w-2xl bg-surface-1 border-hairline rounded-2xl p-0 shadow-2xl overflow-hidden font-sans text-ink">
            <!-- Header Bar -->
            <DialogHeader class="px-6 py-5 border-b border-hairline bg-surface-1/80 backdrop-blur-md space-y-1.5">
                <div class="flex items-center justify-between">
                    <div class="flex items-center gap-2.5">
                        <div class="w-8 h-8 rounded-lg bg-rose-500/10 text-rose-600 dark:text-rose-400 flex items-center justify-center shrink-0 border border-rose-500/20">
                            <AlertCircle class="w-4.5 h-4.5 stroke-[2.2]" />
                        </div>
                        <div>
                            <DialogTitle class="text-sm font-bold text-ink tracking-tight font-satoshi flex items-center gap-2">
                                <span>作业失败异常诊断</span>
                                <Badge variant="outline" class="font-mono text-[10px] font-semibold px-1.5 py-0 uppercase border-hairline">
                                    {{ job?.target_format }}
                                </Badge>
                            </DialogTitle>
                            <DialogDescription class="text-xs text-ink-subtle truncate max-w-[420px]">
                                {{ job?.input_file?.name || job?.input_path }}
                            </DialogDescription>
                        </div>
                    </div>

                    <!-- Error Code Badge -->
                    <div v-if="diagnostic" class="shrink-0">
                        <Badge
                            variant="secondary"
                            class="rounded-md font-mono text-[11px] font-bold px-2 py-0.5 border"
                            :class="diagnostic.badgeClass"
                        >
                            {{ diagnostic.code }}
                        </Badge>
                    </div>
                </div>
            </DialogHeader>

            <!-- Modal Body (Scrollable) -->
            <div class="px-6 py-5 space-y-5 max-h-[70vh] overflow-y-auto">
                <!-- 1. Diagnostic Card (Humanized Summary) -->
                <div v-if="diagnostic" class="p-4 rounded-xl bg-surface-2/60 border border-hairline space-y-2.5">
                    <div class="flex items-center gap-2 text-xs font-bold text-ink">
                        <Sparkles class="w-4 h-4 text-primary shrink-0" />
                        <span>{{ diagnostic.title }}</span>
                    </div>

                    <p class="text-xs text-ink-subtle leading-relaxed">
                        {{ diagnostic.description }}
                    </p>

                    <!-- Suggestion Box -->
                    <div class="pt-2 border-t border-hairline flex items-start gap-2 text-xs text-primary dark:text-primary-hover font-medium">
                        <Info class="w-3.5 h-3.5 mt-0.5 shrink-0" />
                        <span>{{ diagnostic.suggestion }}</span>
                    </div>
                </div>

                <!-- 2. Technical Traceback / Raw Error Log -->
                <div class="space-y-2">
                    <div class="flex items-center justify-between text-xs font-semibold text-ink">
                        <span class="flex items-center gap-1.5 font-satoshi">
                            <Terminal class="w-3.5 h-3.5 text-ink-subtle" />
                            底层错误日志 / Error Traceback
                        </span>

                        <button
                            v-if="job?.error_message"
                            type="button"
                            @click="copyErrorMessage"
                            class="text-[11px] font-medium text-ink-subtle hover:text-ink flex items-center gap-1 transition-colors cursor-pointer"
                        >
                            <Check v-if="copied" class="w-3 h-3 text-emerald-500" />
                            <Copy v-else class="w-3 h-3" />
                            <span>{{ copied ? '已复制到剪贴板' : '复制错误' }}</span>
                        </button>
                    </div>

                    <div class="relative group">
                        <pre class="p-3.5 rounded-xl bg-zinc-950 text-zinc-200 border border-zinc-800 text-[11.5px] font-mono overflow-x-auto leading-relaxed max-h-[160px] whitespace-pre-wrap break-all selection:bg-rose-500/30">{{ job?.error_message || '暂无详细底层错误堆栈。' }}</pre>
                    </div>
                </div>

                <!-- 3. Job Execution Metadata -->
                <div class="grid grid-cols-2 sm:grid-cols-4 gap-2 pt-1">
                    <div class="p-2.5 rounded-lg bg-surface-2/40 border border-hairline space-y-0.5">
                        <span class="text-[10px] text-ink-subtle uppercase font-semibold font-mono">转码引擎</span>
                        <p class="text-xs font-bold text-ink font-mono">{{ job?.params?.engine || 'Auto' }}</p>
                    </div>

                    <div class="p-2.5 rounded-lg bg-surface-2/40 border border-hairline space-y-0.5">
                        <span class="text-[10px] text-ink-subtle uppercase font-semibold font-mono">自动重试记录</span>
                        <p class="text-xs font-bold text-ink font-mono">
                            {{ job?.retry_count || 0 }} / {{ job?.max_retries || 3 }} 次
                        </p>
                    </div>

                    <div class="p-2.5 rounded-lg bg-surface-2/40 border border-hairline space-y-0.5">
                        <span class="text-[10px] text-ink-subtle uppercase font-semibold font-mono">可否直接重试</span>
                        <p class="text-xs font-bold font-mono" :class="diagnostic?.retryable ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
                            {{ diagnostic?.retryable ? '建议重试' : '建议调参' }}
                        </p>
                    </div>

                    <div class="p-2.5 rounded-lg bg-surface-2/40 border border-hairline space-y-0.5">
                        <span class="text-[10px] text-ink-subtle uppercase font-semibold font-mono">作业 ID</span>
                        <p class="text-xs font-bold text-ink font-mono truncate" :title="job?.id">
                            {{ job?.id ? job.id.substring(0, 8) : '-' }}
                        </p>
                    </div>
                </div>
            </div>

            <!-- Footer Actions -->
            <div class="px-6 py-4 border-t border-hairline bg-surface-1 flex items-center justify-between gap-3">
                <Button
                    variant="outline"
                    size="sm"
                    @click="emit('update:open', false)"
                    class="h-8.5 px-4 text-xs font-medium border-hairline"
                >
                    关闭
                </Button>

                <div class="flex items-center gap-2">
                    <!-- Tune Parameters Button -->
                    <Button
                        variant="outline"
                        size="sm"
                        @click="handleTuneRetry"
                        class="h-8.5 px-3.5 text-xs font-medium border-hairline gap-1.5 hover:bg-surface-2 text-ink"
                    >
                        <Sliders class="w-3.5 h-3.5 text-ink-subtle" />
                        <span>调整参数重试</span>
                    </Button>

                    <!-- Direct Retry Button -->
                    <Button
                        size="sm"
                        @click="handleDirectRetry"
                        :disabled="isRetrying"
                        class="h-8.5 px-4 text-xs font-semibold gap-1.5 bg-primary hover:bg-primary-hover text-primary-foreground shadow-xs"
                    >
                        <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                        <RotateCcw v-else class="w-3.5 h-3.5" />
                        <span>直接重试</span>
                    </Button>
                </div>
            </div>
        </DialogContent>
    </Dialog>
</template>
