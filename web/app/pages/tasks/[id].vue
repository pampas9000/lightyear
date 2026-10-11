<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useApi } from '~/composables/useApi'
import type { Task, Job, File, TaskStatus } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import { toast } from 'vue-sonner'
import {
    ChevronLeft, Loader2, Calendar, Clock,
    FileImage, FileVideo, HardDrive, Download,
    CheckCircle2, AlertCircle, Info, Settings,
    ArrowRight, ShieldCheck, Layers, TrendingDown,
    Sparkles, Cpu, Copy, Check, RotateCw, RotateCcw, Plus,
    Eye, X, Code2, CheckCircle, Sliders
} from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogClose
} from '@/components/ui/dialog'
import TaskRetryDrawer from '~/components/TaskRetryDrawer.vue'
import type { ImageOptions } from '~/lib/types/image-options'
import JobErrorDialog from '~/components/JobErrorDialog.vue'
import { getErrorDiagnostic } from '~/lib/utils/error-codes'

useHead({
    title: $t('task_detail.details_title') + ' | Transcoder',
})

const route = useRoute()
const router = useRouter()
const api = useApi()
const taskId = route.params.id as string

const task = ref<Task | null>(null)
const loading = ref(true)
const polling = ref<any>(null)
const downloadingJobs = ref<Record<string, boolean>>({})
const isDownloadingAll = ref(false)
const isRefreshing = ref(false)
const showRawJson = ref(false)
const activeTab = ref<'all' | 'completed' | 'failed'>('all')

// Preview modal state
const selectedJobForPreview = ref<Job | null>(null)
const previewImageUrl = ref<string | null>(null)
const isPreviewLoading = ref(false)

const fetchTask = async () => {
    try {
        const res = await api<ApiResponse<Task>>(`/tasks/${taskId}`)
        if (res.success) {
            task.value = res.data
            // If completed, failed, or partially failed, stop polling
            if (task.value.status !== 'PROCESSING' && task.value.status !== 'PENDING') {
                stopPolling()
            }
        }
    } catch (err: any) {
        console.error("Failed to fetch task:", err)
        toast.error($t('task_detail.toast_fetch_failed'))
        if (err.statusCode === 404) {
            router.push('/tasks')
        }
    } finally {
        loading.value = false
    }
}

const refreshTask = async () => {
    isRefreshing.value = true
    await fetchTask()
    setTimeout(() => {
        isRefreshing.value = false
    }, 400)
}

const startPolling = () => {
    if (polling.value) return
    polling.value = setInterval(fetchTask, 2000)
}

const stopPolling = () => {
    if (polling.value) {
        clearInterval(polling.value)
        polling.value = null
    }
}

watch(() => task.value?.status, (newStatus) => {
    if (newStatus === 'PROCESSING' || newStatus === 'PENDING') {
        startPolling()
    } else {
        stopPolling()
    }
})

onMounted(async () => {
    await fetchTask()
    if (task.value?.status === 'PROCESSING' || task.value?.status === 'PENDING') {
        startPolling()
    }
})

onUnmounted(() => {
    stopPolling()
})

const getStatusStyle = (status: TaskStatus | string | undefined) => {
    if (!status) return 'bg-surface-2 text-ink-subtle border-hairline'
    switch (status) {
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400'
        case 'FAILED': return 'bg-rose-500/10 text-rose-600 border-rose-500/20 dark:text-rose-400'
        case 'CANCELLED': return 'bg-zinc-500/10 text-zinc-600 border-zinc-500/20 dark:text-zinc-400'
        case 'PARTIALLY_FAILED': return 'bg-orange-500/10 text-orange-600 border-orange-500/20 dark:text-orange-400'
        case 'PROCESSING': return 'bg-primary/10 text-primary border-primary/20 dark:text-primary-hover'
        case 'PENDING': return 'bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400'
        default: return 'bg-surface-2 text-ink-subtle border-hairline'
    }
}

const getJobStatusStyle = (status: string) => {
    switch (status) {
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400'
        case 'FAILED': return 'bg-rose-500/10 text-rose-600 border-rose-500/20 dark:text-rose-400'
        case 'CANCELLED': return 'bg-zinc-500/10 text-zinc-600 border-zinc-500/20 dark:text-zinc-400'
        case 'PROCESSING': return 'bg-primary/10 text-primary border-primary/20 dark:text-primary-hover'
        case 'PENDING': return 'bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400'
        default: return 'bg-surface-2 text-ink-subtle border-hairline'
    }
}

const formatBytes = (bytes: number | undefined) => {
    if (bytes === undefined || bytes === null || bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

const getSavingsRatio = (inputSize: number | undefined, outputSize: number | undefined) => {
    if (!inputSize || !outputSize) return 0
    const savings = inputSize - outputSize
    return Math.max(0, Math.round((savings / inputSize) * 100))
}

const getTotalSavings = computed(() => {
    if (!task.value?.jobs || task.value.jobs.length === 0) return null
    let totalInput = 0
    let totalOutput = 0
    let hasCompleted = false
    
    task.value.jobs.forEach(job => {
        if (job.status === 'COMPLETED') {
            totalInput += job.source_file?.size || 0
            totalOutput += job.output_file?.size || 0
            hasCompleted = true
        }
    })
    
    if (!hasCompleted || totalInput === 0) return null
    const ratio = getSavingsRatio(totalInput, totalOutput)
    return {
        input: totalInput,
        output: totalOutput,
        ratio,
        saved: totalInput - totalOutput
    }
})

const getTaskProgress = computed(() => {
    if (!task.value?.jobs || task.value.jobs.length === 0) return 0
    const sum = task.value.jobs.reduce((acc, job) => acc + (job.progress || 0), 0)
    return Math.round(sum / task.value.jobs.length)
})

const getTaskTimeDetails = computed(() => {
    if (!task.value) return { started: '', duration: '' }
    const started = new Date(task.value.created_at).toLocaleString()
    
    const created = new Date(task.value.created_at).getTime()
    let end = new Date(task.value.updated_at).getTime()
    
    if (task.value.status === 'PROCESSING' || task.value.status === 'PENDING') {
        end = new Date().getTime()
    }
    
    const diff = Math.max(0, Math.floor((end - created) / 1000))
    let duration = ''
    if (diff < 60) {
        duration = `${diff}s`
    } else {
        const mins = Math.floor(diff / 60)
        const secs = diff % 60
        duration = `${mins}m ${secs}s`
    }
    
    return { started, duration }
})

const completedJobsCount = computed(() => {
    return task.value?.jobs?.filter(j => j.status === 'COMPLETED').length || 0
})

const failedJobsCount = computed(() => {
    return task.value?.jobs?.filter(j => j.status === 'FAILED').length || 0
})

const filteredJobs = computed(() => {
    if (!task.value?.jobs) return []
    if (activeTab.value === 'completed') {
        return task.value.jobs.filter(j => j.status === 'COMPLETED')
    }
    if (activeTab.value === 'failed') {
        return task.value.jobs.filter(j => j.status === 'FAILED')
    }
    return task.value.jobs
})

const isImageFile = (filename: string | undefined) => {
    if (!filename) return false
    const ext = filename.split('.').pop()?.toLowerCase() || ''
    return ['png', 'jpg', 'jpeg', 'webp', 'avif', 'jxl', 'gif', 'apng', 'heic', 'heif'].includes(ext)
}

const formatFilename = (name: string | undefined) => {
    if (!name) return ''
    const parts = name.split('.')
    const ext = parts.pop() || ''
    const base = parts.join('.')
    if (base.length > 22) {
        return `${base.substring(0, 10)}...${base.substring(base.length - 8)}.${ext}`
    }
    return name
}

// Label/value pairs for the parameters card.
const optionEntries = (o?: ImageOptions) => {
    if (!o) return []
    const r = o.resize
    const resize = r ? (r.width && r.height ? `${r.width}×${r.height} ${r.fit ?? 'inside'}` : `≤${r.width ?? r.height}px`) + (r.enlarge ? ' ↑' : '') : '—'
    return [
        ['format', o.format.toUpperCase()],
        ['quality', o.lossless ? $t('image_options.lossless') : String(o.quality ?? '—')],
        ['effort', String(o.effort ?? '—')],
        ['resize', resize],
        ['color', $t(`image_options.color_${o.color ?? 'keep'}`)],
        ['dynamic_range', $t(`image_options.dynamic_range_${o.dynamic_range ?? 'keep'}`)],
        ['metadata', $t(`image_options.metadata_${o.metadata ?? 'strip'}`)],
        ['chroma', o.chroma ? `4:${o.chroma.slice(1, 2)}:${o.chroma.slice(2)}` : $t('image_options.auto')],
        ['bit_depth', o.bit_depth ? `${o.bit_depth}-bit` : $t('image_options.follow_source')],
    ] as const
}

const copyUuid = () => {
    if (!task.value?.id) return
    navigator.clipboard.writeText(task.value.id)
    toast.success($t('task_detail.toast_uuid_copied'))
}

const downloadJobOutput = async (job: Job) => {
    if (!job.output_file_id || job.status !== 'COMPLETED') return
    
    downloadingJobs.value[job.id] = true
    try {
        const res = await api<ApiResponse<{ url: string }>>(`/files/${job.output_file_id}/download-url`)
        if (res.success && res.data.url) {
            const a = document.createElement('a')
            a.href = res.data.url
            a.download = job.output_file?.name || 'transcoded-file'
            a.target = '_blank'
            document.body.appendChild(a)
            a.click()
            document.body.removeChild(a)
            toast.success($t('task_detail.toast_download_success'))
        } else {
            toast.error($t('task_detail.toast_download_link_failed'))
        }
    } catch (err: any) {
        console.error('Failed to download file:', err)
        toast.error(err.message || 'Download request failed')
    } finally {
        downloadingJobs.value[job.id] = false
    }
}

const downloadAllOutputs = async () => {
    if (!task.value?.jobs) return
    const completedJobs = task.value.jobs.filter(j => j.status === 'COMPLETED' && j.output_file_id)
    if (completedJobs.length === 0) return

    isDownloadingAll.value = true
    try {
        for (const job of completedJobs) {
            await downloadJobOutput(job)
            await new Promise(resolve => setTimeout(resolve, 300))
        }
    } finally {
        isDownloadingAll.value = false
    }
}

const openPreview = async (job: Job) => {
    if (!job.output_file_id || job.status !== 'COMPLETED') return
    selectedJobForPreview.value = job
    previewImageUrl.value = null
    isPreviewLoading.value = true
    try {
        const res = await api<ApiResponse<{ url: string }>>(`/files/${job.output_file_id}/download-url`)
        if (res.success && res.data.url) {
            previewImageUrl.value = res.data.url
        } else {
            toast.error($t('task_detail.toast_download_link_failed'))
            selectedJobForPreview.value = null
        }
    } catch (err) {
        console.error('Failed to get preview URL:', err)
        toast.error('Failed to load preview image')
        selectedJobForPreview.value = null
    } finally {
        isPreviewLoading.value = false
    }
}

const closePreview = () => {
    selectedJobForPreview.value = null
    previewImageUrl.value = null
}

// Retry state & TaskRetryDrawer logic
const isRetrying = ref(false)
const retryingJobs = ref<Record<string, boolean>>({})
const showRetryDrawer = ref(false)
const selectedJobForRetry = ref<string | null>(null)
const isCancelling = ref(false)
const showCancelDialog = ref(false)

// Error Diagnostic Dialog logic
const selectedJobForErrorModal = ref<Job | null>(null)
const showErrorDialog = ref(false)

const openErrorModal = (job: Job) => {
    selectedJobForErrorModal.value = job
    showErrorDialog.value = true
}

const failedJobsDiagnosticSummary = computed(() => {
    if (!task.value?.jobs) return []
    const counts: Record<string, { code: string; title: string; count: number; badgeClass: string; retryable: boolean }> = {}
    for (const job of task.value.jobs) {
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

const openRetryDrawer = (jobId?: string) => {
    selectedJobForRetry.value = jobId || null
    showRetryDrawer.value = true
}

const handleCancelTask = async () => {
    isCancelling.value = true
    try {
        const res = await api<ApiResponse<Task>>(`/tasks/${taskId}/cancel`, {
            method: 'POST',
        })
        if (res.success) {
            toast.success($t('task_detail.toast_cancel_success'))
            task.value = res.data
            showCancelDialog.value = false
            stopPolling()
        }
    } catch (err: any) {
        console.error('Failed to cancel task:', err)
        toast.error(err.data?.message || $t('task_detail.toast_cancel_failed'))
    } finally {
        isCancelling.value = false
    }
}

const handleDirectRetry = async (jobIds?: string[]) => {
    isRetrying.value = true
    if (jobIds && jobIds.length > 0) {
        jobIds.forEach(id => {
            retryingJobs.value[id] = true
        })
    }
    try {
        const body: any = {}
        if (jobIds && jobIds.length > 0) {
            body.job_ids = jobIds
        }
        const res = await api<ApiResponse<Task>>(`/tasks/${taskId}/retry`, {
            method: 'POST',
            body,
        })
        if (res.success) {
            if (jobIds && jobIds.length === 1) {
                toast.success($t('task_detail.toast_retry_single_success'))
            } else {
                toast.success($t('task_detail.toast_retry_all_success'))
            }
            task.value = res.data
            startPolling()
        }
    } catch (err: any) {
        console.error('Failed to retry task:', err)
        toast.error(err.data?.message || $t('task_detail.toast_retry_failed'))
    } finally {
        isRetrying.value = false
        if (jobIds && jobIds.length > 0) {
            jobIds.forEach(id => {
                retryingJobs.value[id] = false
            })
        }
    }
}

const handleRetryFromDrawer = async (payload: { options?: ImageOptions; retryAll: boolean; jobIds?: string[] }) => {
    isRetrying.value = true
    try {
        const body: Record<string, unknown> = { retry_all: payload.retryAll }
        if (payload.options) body.options = payload.options
        if (payload.jobIds && payload.jobIds.length > 0) {
            body.job_ids = payload.jobIds
            body.retry_all = false
        }
        const res = await api<ApiResponse<Task>>(`/tasks/${taskId}/retry`, {
            method: 'POST',
            body,
        })
        if (res.success) {
            toast.success($t('task_detail.toast_retry_success'))
            task.value = res.data
            showRetryDrawer.value = false
            selectedJobForRetry.value = null
            startPolling()
        }
    } catch (err: any) {
        console.error('Failed to retry task:', err)
        toast.error(err.data?.message || $t('task_detail.toast_retry_failed'))
    } finally {
        isRetrying.value = false
    }
}
</script>

<template>
    <div class="max-w-5xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-6 py-8 pb-24">
        <!-- Navigation & Header Quick Actions -->
        <div class="flex items-center justify-between gap-4">
            <NuxtLink to="/tasks" class="flex items-center gap-1.5 text-xs font-semibold text-ink-subtle hover:text-ink transition-colors cursor-pointer group">
                <ChevronLeft class="w-4 h-4 transition-transform group-hover:-translate-x-0.5" />
                {{ $t('task_detail.back_to_tasks') }}
            </NuxtLink>
            
            <div class="flex items-center gap-2">
                <!-- Refresh Button -->
                <button
                    @click="refreshTask"
                    :disabled="isRefreshing"
                    class="h-8 px-2.5 rounded-lg border border-hairline bg-surface-1 hover:bg-surface-2 text-ink-subtle hover:text-ink text-xs font-medium flex items-center gap-1.5 transition-all cursor-pointer disabled:opacity-50"
                    :title="$t('task_detail.refresh')"
                >
                    <RotateCw class="w-3.5 h-3.5" :class="{ 'animate-spin': isRefreshing }" />
                    <span class="hidden sm:inline">{{ $t('task_detail.refresh') }}</span>
                </button>

                <!-- Download All Action Button (if any jobs completed) -->
                <button
                    v-if="completedJobsCount > 0"
                    @click="downloadAllOutputs"
                    :disabled="isDownloadingAll"
                    class="h-8 px-3 rounded-lg bg-emerald-500/10 hover:bg-emerald-500/20 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20 text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer disabled:opacity-50"
                >
                    <Loader2 v-if="isDownloadingAll" class="w-3.5 h-3.5 animate-spin" />
                    <Download v-else class="w-3.5 h-3.5" />
                    <span>{{ isDownloadingAll ? $t('task_detail.downloading_all') : $t('task_detail.download_all') }}</span>
                </button>

                <!-- Cancel Task Button (if processing or pending) -->
                <button
                    v-if="task?.status === 'PROCESSING' || task?.status === 'PENDING'"
                    @click="showCancelDialog = true"
                    :disabled="isCancelling"
                    class="h-8 px-2.5 rounded-lg border border-rose-500/30 bg-rose-500/10 hover:bg-rose-500/20 text-rose-600 dark:text-rose-400 text-xs font-medium flex items-center gap-1.5 transition-all cursor-pointer disabled:opacity-50"
                    :title="$t('task_detail.cancel_task')"
                >
                    <Loader2 v-if="isCancelling" class="w-3.5 h-3.5 animate-spin" />
                    <X v-else class="w-3.5 h-3.5" />
                    <span class="hidden sm:inline">{{ $t('task_detail.cancel_task') }}</span>
                </button>

                <!-- New Task Shortcut -->
                <NuxtLink
                    to="/tasks/new"
                    class="h-8 px-3 rounded-lg bg-primary hover:bg-primary-hover text-primary-foreground text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer"
                >
                    <Plus class="w-3.5 h-3.5" stroke-width="2.5" />
                    <span class="hidden sm:inline">{{ $t('task_detail.new_task') }}</span>
                </NuxtLink>
            </div>
        </div>

        <!-- Loading State -->
        <div v-if="loading" class="p-20 flex flex-col items-center justify-center space-y-3 bg-surface-1 border border-hairline rounded-xl">
            <Loader2 class="h-7 w-7 text-primary animate-spin" />
            <p class="text-xs font-medium text-ink-subtle uppercase tracking-wider">{{ $t('common.loading') }}</p>
        </div>

        <!-- Task Content -->
        <div v-else-if="task" class="space-y-6">
            <!-- Header Block -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-hairline pb-5">
                <div class="space-y-2">
                    <div class="flex items-center gap-3">
                        <h1 class="text-xl font-semibold tracking-tight text-ink font-mono">
                            Task-{{ task.id.substring(0, 8) }}
                        </h1>
                        <Badge variant="secondary" class="rounded-full font-medium text-xs px-2.5 py-0.5 border capitalize tracking-normal flex items-center gap-1.5" :class="getStatusStyle(task.status)">
                            <span v-if="task.status === 'PROCESSING' || task.status === 'PENDING'" class="w-1.5 h-1.5 rounded-full bg-primary animate-ping"></span>
                            {{ $t('status.' + task.status) }}
                        </Badge>
                    </div>

                    <!-- Interactive Copyable UUID Capsule -->
                    <div class="flex items-center gap-2">
                        <button
                            @click="copyUuid"
                            class="group flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-surface-2 hover:bg-surface-3 border border-hairline text-xs font-mono text-ink-subtle hover:text-ink transition-colors cursor-pointer"
                            title="Click to copy full UUID"
                        >
                            <span>UUID: {{ task.id }}</span>
                            <Copy class="w-3 h-3 text-ink-subtle opacity-70 group-hover:opacity-100 transition-opacity" />
                        </button>
                    </div>
                </div>

                <!-- Timing Details -->
                <div class="flex flex-wrap gap-4 text-xs font-medium text-ink-subtle">
                    <div class="flex items-center gap-1.5">
                        <Calendar class="w-3.5 h-3.5 text-ink-subtle" />
                        <span>{{ $t('task_detail.started_at') }} {{ getTaskTimeDetails.started }}</span>
                    </div>
                    <div class="w-px h-3 bg-hairline-strong hidden sm:block"></div>
                    <div class="flex items-center gap-1.5">
                        <Clock class="w-3.5 h-3.5 text-ink-subtle" />
                        <span>
                            {{ task.status === 'PROCESSING' || task.status === 'PENDING' ? $t('task_detail.elapsed') : $t('task_detail.processed') }}: {{ getTaskTimeDetails.duration }}
                        </span>
                    </div>
                </div>
            </div>

            <!-- Live Status Banners -->
            <!-- 1) Processing Banner -->
            <div v-if="task.status === 'PROCESSING' || task.status === 'PENDING'" class="border border-primary/20 bg-primary/5 rounded-xl p-4.5 space-y-2.5">
                <div class="flex items-center justify-between">
                    <div class="flex items-center gap-2">
                        <Loader2 class="w-4 h-4 text-primary animate-spin" />
                        <h3 class="text-xs font-semibold text-ink uppercase tracking-wider">{{ $t('task_detail.processing_banner_title') }}</h3>
                    </div>
                    <span class="text-xs font-mono font-semibold text-primary">{{ getTaskProgress }}%</span>
                </div>
                <p class="text-xs text-ink-subtle font-medium">{{ $t('task_detail.processing_banner_desc') }}</p>
                <!-- Glowing Progress Bar -->
                <div class="w-full bg-surface-2 rounded-full h-1.5 overflow-hidden border border-hairline">
                    <div class="bg-primary h-full rounded-full transition-all duration-500" :style="{ width: `${getTaskProgress}%` }"></div>
                </div>
            </div>

            <!-- 2) Completion Banner -->
            <div v-else-if="task.status === 'COMPLETED' && getTotalSavings" class="border border-emerald-500/20 bg-emerald-500/5 rounded-xl p-4.5 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
                <div class="space-y-1">
                    <div class="flex items-center gap-2">
                        <CheckCircle class="w-4 h-4 text-emerald-600 dark:text-emerald-400" />
                        <h3 class="text-xs font-semibold text-ink uppercase tracking-wider">{{ $t('task_detail.completed_banner_title') }}</h3>
                    </div>
                    <p class="text-xs text-ink-subtle font-medium">
                        {{ $t('task_detail.completed_banner_desc', { count: completedJobsCount, saved: formatBytes(getTotalSavings.saved), ratio: getTotalSavings.ratio }) }}
                    </p>
                </div>
                <button
                    @click="downloadAllOutputs"
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
                            {{ failedJobsCount }} 个失败作业
                        </Badge>
                    </div>
                    <p class="text-xs text-ink-subtle font-medium leading-relaxed">
                        {{ $t('task_detail.failed_banner_desc', { count: failedJobsCount }) }}
                        点击任意作业的异常标签可查看具体诊断分析与错误堆栈。
                    </p>

                    <!-- Error Code Breakdown Badges in Banner -->
                    <div v-if="failedJobsDiagnosticSummary.length > 0" class="flex items-center gap-1.5 flex-wrap pt-0.5">
                        <span
                            v-for="item in failedJobsDiagnosticSummary"
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
                        @click="handleDirectRetry()"
                        :disabled="isRetrying"
                        class="h-8.5 px-3.5 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-medium flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
                    >
                        <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                        <RotateCcw v-else class="w-3.5 h-3.5" />
                        <span>一键重试失败项</span>
                    </button>

                    <button
                        @click="openRetryDrawer()"
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
                        @click="handleDirectRetry()"
                        :disabled="isRetrying"
                        class="h-8.5 px-3.5 rounded-lg bg-primary hover:bg-primary-hover text-primary-foreground text-xs font-medium flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
                    >
                        <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                        <RotateCcw v-else class="w-3.5 h-3.5" />
                        <span>重新调度全部作业</span>
                    </button>
                </div>
            </div>

            <!-- Overview Stat Cards (Minimalist Flat Cards) -->
            <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
                <!-- Card 1: Target Format & Engine -->
                <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
                    <div class="flex items-center justify-between">
                        <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.target_format') }}</span>
                        <Layers class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
                    </div>
                    <div class="flex items-baseline gap-2 mt-2">
                        <span class="text-xl font-bold tracking-tight text-ink uppercase">{{ task.jobs?.[0]?.options?.format || '—' }}</span>
                    </div>
                </div>

                <!-- Card 2: Total Files & Jobs -->
                <div class="border border-hairline bg-surface-1 rounded-xl p-4 flex flex-col justify-between min-h-[96px]">
                    <div class="flex items-center justify-between">
                        <span class="text-[11px] font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.total_files') }}</span>
                        <FileVideo class="w-4 h-4 text-ink-subtle opacity-50" stroke-width="1.5" />
                    </div>
                    <div class="flex items-baseline gap-2 mt-2">
                        <span class="text-xl font-bold tracking-tight text-ink">{{ task.jobs?.length || 0 }}</span>
                        <span class="text-xs font-medium text-ink-subtle">{{ $t('task_detail.files_unit') }}</span>
                        <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded border border-emerald-500/20 ml-auto" v-if="completedJobsCount > 0">
                            {{ completedJobsCount }} 完成
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
                        <template v-if="getTotalSavings">
                            <span class="text-xl font-bold tracking-tight text-emerald-600 dark:text-emerald-400">-{{ getTotalSavings.ratio }}%</span>
                            <span class="text-xs font-medium text-ink-subtle truncate">({{ formatBytes(getTotalSavings.saved) }})</span>
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
                        <span class="text-xl font-bold tracking-tight text-ink">{{ getTaskTimeDetails.duration }}</span>
                        <span class="text-xs font-medium text-ink-subtle" v-if="task.status === 'PROCESSING'">Live</span>
                    </div>
                </div>
            </div>

            <!-- Parameters Card with JSON Inspection Toggle -->
            <div v-if="task.jobs?.[0]?.options" class="border border-hairline rounded-xl bg-surface-1 overflow-hidden flex flex-col">
                <div class="flex items-center justify-between border-b border-hairline px-5 py-3.5 bg-surface-1">
                    <h3 class="font-semibold text-xs text-ink uppercase tracking-wider flex items-center gap-2">
                        <Settings class="w-4 h-4 text-ink-subtle" stroke-width="1.5" />
                        {{ $t('task_detail.params') }}
                    </h3>

                    <!-- Developer Toggle for Raw JSON -->
                    <button
                        @click="showRawJson = !showRawJson"
                        class="text-xs font-medium text-ink-subtle hover:text-ink flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-surface-2 border border-hairline hover:bg-surface-3 transition-colors cursor-pointer"
                    >
                        <Code2 class="w-3.5 h-3.5" />
                        <span>{{ showRawJson ? $t('task_detail.hide_raw_json') : $t('task_detail.view_raw_json') }}</span>
                    </button>
                </div>

                <div class="p-5">
                    <!-- Standard Structured Key-Value Capsules -->
                    <div v-if="!showRawJson" class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-5 gap-3">
                        <div v-for="[key, val] in optionEntries(task.jobs[0].options)" :key="key" class="space-y-1 bg-surface-2/60 p-2.5 rounded-lg border border-hairline">
                            <div class="text-[10px] font-semibold text-ink-subtle uppercase tracking-wider truncate">{{ $t(`image_options.${key}`) }}</div>
                            <div class="text-xs font-semibold text-ink truncate">{{ val }}</div>
                        </div>
                    </div>

                    <!-- Raw JSON Code Inspector -->
                    <div v-else class="space-y-2">
                        <pre class="p-4 rounded-lg bg-surface-2 border border-hairline text-xs font-mono text-ink overflow-x-auto selection:bg-primary/20 leading-relaxed">{{ JSON.stringify(task.jobs[0].options, null, 2) }}</pre>
                    </div>
                </div>
            </div>

            <!-- Jobs Flow List & Filtering (Vercel / GitHub Actions Sleek Table Redesign) -->
            <div class="border border-hairline rounded-xl bg-surface-1 overflow-hidden flex flex-col">
                <!-- List Header & Filter Tabs -->
                <div class="flex flex-col sm:flex-row sm:items-center justify-between border-b border-hairline px-5 py-3 bg-surface-1 gap-3">
                    <div class="flex items-center gap-2">
                        <Sparkles class="w-4 h-4 text-ink-subtle" stroke-width="1.5" />
                        <h3 class="font-semibold text-xs text-ink uppercase tracking-wider flex items-center gap-2">
                            {{ $t('task_detail.transcoding_jobs') }}
                            <span class="text-ink-subtle font-mono text-xs font-normal">({{ task.jobs?.length || 0 }})</span>
                        </h3>
                    </div>

                    <!-- Filter Tabs (Segment Control Style) -->
                    <div class="flex items-center gap-0.5 bg-surface-2/80 p-0.5 rounded-lg border border-hairline self-start sm:self-auto">
                        <button
                            @click="activeTab = 'all'"
                            class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                            :class="activeTab === 'all' ? 'bg-surface-1 text-ink font-semibold' : 'text-ink-subtle hover:text-ink'"
                        >
                            {{ $t('task_detail.all_jobs', { count: task.jobs?.length || 0 }) }}
                        </button>
                        <button
                            @click="activeTab = 'completed'"
                            class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                            :class="activeTab === 'completed' ? 'bg-surface-1 text-ink font-semibold' : 'text-ink-subtle hover:text-ink'"
                        >
                            {{ $t('task_detail.completed_jobs', { count: completedJobsCount }) }}
                        </button>
                        <button
                            v-if="failedJobsCount > 0"
                            @click="activeTab = 'failed'"
                            class="px-3 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer"
                            :class="activeTab === 'failed' ? 'bg-surface-1 text-rose-600 font-semibold' : 'text-ink-subtle hover:text-ink'"
                        >
                            {{ $t('task_detail.failed_jobs', { count: failedJobsCount }) }}
                        </button>
                    </div>
                </div>

                <!-- Job Items List (Clean Flat Layout) -->
                <div class="divide-y divide-hairline">
                    <div
                        v-for="job in filteredJobs"
                        :key="job.id"
                        class="px-5 py-3.5 flex items-center justify-between gap-4 hover:bg-surface-2/40 transition-colors duration-150"
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
                                    <span class="text-[10px] font-mono font-semibold uppercase px-1.5 py-0.2 rounded bg-surface-2 text-ink-subtle border border-hairline">
                                        {{ job.options?.format }}
                                    </span>
                                    <Badge variant="secondary" class="rounded font-medium text-[10px] px-1.5 py-0.2 border capitalize tracking-normal" :class="getJobStatusStyle(job.status)">
                                        {{ $t('status.' + job.status) }}
                                    </Badge>
                                </div>

                                <!-- Line 2: Size, Retries & Error Detail (Inline) -->
                                <div class="flex items-center gap-2 text-xs text-ink-subtle font-medium flex-wrap">
                                    <span class="font-mono">{{ formatBytes(job.source_file?.size || 0) }}</span>

                                    <!-- Completed Output Size -->
                                    <template v-if="job.status === 'COMPLETED' && job.output_file?.size">
                                        <span>·</span>
                                        <span class="font-mono text-emerald-600 dark:text-emerald-400 font-semibold">
                                            {{ formatBytes(job.output_file.size) }} (-{{ getSavingsRatio(job.source_file?.size, job.output_file.size) }}%)
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
                                            @click.stop="openErrorModal(job)"
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
                                            @click.stop="openErrorModal(job)"
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
                                v-if="job.status === 'COMPLETED' && job.output_file_id && isImageFile(job.source_file?.name)"
                                @click="openPreview(job)"
                                class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
                                :title="$t('task_detail.preview')"
                            >
                                <Eye class="w-3.5 h-3.5 text-ink-subtle" />
                                <span class="hidden sm:inline">{{ $t('task_detail.preview') }}</span>
                            </button>

                            <!-- Diagnosis Inspector Trigger for Failed Jobs -->
                            <button
                                v-if="job.status === 'FAILED'"
                                @click="openErrorModal(job)"
                                class="h-8 px-2.5 text-xs font-medium border border-rose-500/30 bg-rose-500/5 hover:bg-rose-500/15 text-rose-600 dark:text-rose-400 rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
                                title="查看异常原因与修复建议"
                            >
                                <Info class="w-3.5 h-3.5" />
                                <span class="hidden sm:inline">诊断</span>
                            </button>

                            <!-- Per-Job Direct Retry Button -->
                            <button
                                v-if="job.status === 'FAILED' || job.status === 'CANCELLED'"
                                @click="handleDirectRetry([job.id])"
                                :disabled="isRetrying || retryingJobs[job.id]"
                                class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors disabled:opacity-50"
                                :title="$t('task_detail.retry_task')"
                            >
                                <Loader2 v-if="retryingJobs[job.id]" class="w-3.5 h-3.5 animate-spin text-primary" />
                                <RotateCcw v-else class="w-3.5 h-3.5 text-ink-subtle" />
                                <span>{{ retryingJobs[job.id] ? $t('task_detail.retrying') : '重试' }}</span>
                            </button>

                            <!-- Adjust Parameters & Retry Drawer Trigger (Targeted to this single job) -->
                            <button
                                v-if="job.status === 'FAILED' || job.status === 'CANCELLED'"
                                @click="openRetryDrawer(job.id)"
                                class="h-8 px-2.5 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink-subtle hover:text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors"
                                title="针对此作业单独调参重试"
                            >
                                <Sliders class="w-3.5 h-3.5" />
                                <span class="hidden sm:inline">调参</span>
                            </button>

                            <!-- Download Button -->
                            <button
                                v-if="job.status === 'COMPLETED'"
                                :disabled="downloadingJobs[job.id] || !job.output_file_id"
                                @click="downloadJobOutput(job)"
                                class="h-8 px-3 text-xs font-medium border border-hairline bg-surface-1 hover:bg-surface-2 text-ink rounded-lg flex items-center gap-1.5 cursor-pointer transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                            >
                                <Loader2 v-if="downloadingJobs[job.id]" class="w-3.5 h-3.5 animate-spin" />
                                <Download v-else class="w-3.5 h-3.5 text-ink-subtle" />
                                <span>{{ downloadingJobs[job.id] ? $t('task_detail.downloading') : $t('task_detail.download') }}</span>
                            </button>
                        </div>
                    </div>

                    <div v-if="filteredJobs.length === 0" class="p-8 text-center text-xs text-ink-subtle">
                        该分类下暂无作业。
                    </div>
                </div>
            </div>
        </div>

        <!-- Image Preview Modal -->
        <Dialog :open="!!selectedJobForPreview" @update:open="closePreview">
            <DialogContent class="max-w-2xl bg-surface-1 border-hairline rounded-xl p-6 shadow-xl">
                <DialogHeader class="pb-4 border-b border-hairline flex flex-row items-center justify-between">
                    <DialogTitle class="text-sm font-semibold text-ink flex items-center gap-2">
                        <Eye class="w-4 h-4 text-primary" />
                        {{ $t('task_detail.preview_title') }}
                    </DialogTitle>
                </DialogHeader>

                <div class="py-6 flex flex-col items-center justify-center min-h-[260px] space-y-4">
                    <Loader2 v-if="isPreviewLoading" class="w-8 h-8 text-primary animate-spin" />
                    <template v-else-if="previewImageUrl">
                        <div class="relative max-h-[420px] overflow-hidden rounded-lg border border-hairline bg-surface-2 p-2 shadow-xs flex items-center justify-center w-full">
                            <img :src="previewImageUrl" :alt="selectedJobForPreview?.output_file?.name || 'Preview'" class="max-h-[380px] w-auto max-w-full object-contain rounded" />
                        </div>
                        <div class="flex items-center justify-between w-full text-xs font-medium text-ink-subtle px-1">
                            <span>Format: {{ selectedJobForPreview?.options?.format }}</span>
                            <span>Output Size: {{ formatBytes(selectedJobForPreview?.output_file?.size) }}</span>
                        </div>
                    </template>
                </div>

                <div class="pt-4 border-t border-hairline flex items-center justify-end gap-3">
                    <Button variant="outline" size="sm" @click="closePreview" class="h-8.5 px-4 text-xs font-medium border-hairline">
                        {{ $t('task_detail.preview_close') }}
                    </Button>
                    <Button
                        v-if="selectedJobForPreview"
                        size="sm"
                        @click="downloadJobOutput(selectedJobForPreview)"
                        class="h-8.5 px-4 text-xs font-semibold gap-1.5 bg-primary text-primary-foreground shadow-xs"
                    >
                        <Download class="w-3.5 h-3.5" />
                        <span>{{ $t('task_detail.download') }}</span>
                    </Button>
                </div>
            </DialogContent>
        </Dialog>

        <!-- Parameter Adjustment Retry Drawer -->
        <TaskRetryDrawer
            v-model:open="showRetryDrawer"
            :task-id="taskId"
            :target-job-id="selectedJobForRetry || undefined"
            :initial-options="(task?.jobs?.find(j => j.id === selectedJobForRetry) ?? task?.jobs?.[0])?.options"
            :failed-count="failedJobsCount"
            :is-retrying="isRetrying"
            @submit="handleRetryFromDrawer"
        />

        <!-- Cancel Confirmation Dialog -->
        <Dialog :open="showCancelDialog" @update:open="val => showCancelDialog = val">
            <DialogContent class="max-w-md bg-surface-1 border-hairline rounded-xl p-6 shadow-xl space-y-4">
                <DialogHeader class="space-y-1.5">
                    <DialogTitle class="text-sm font-semibold text-ink flex items-center gap-2">
                        <AlertCircle class="w-4 h-4 text-rose-600" />
                        {{ $t('task_detail.cancel_dialog_title') }}
                    </DialogTitle>
                    <p class="text-xs text-ink-subtle leading-relaxed">
                        {{ $t('task_detail.cancel_dialog_desc') }}
                    </p>
                </DialogHeader>

                <div class="flex items-center justify-end gap-2 pt-2 border-t border-hairline">
                    <Button variant="outline" size="sm" @click="showCancelDialog = false" class="h-8 px-4 text-xs font-medium border-hairline">
                        {{ $t('common.cancel') }}
                    </Button>
                    <Button
                        size="sm"
                        @click="handleCancelTask"
                        :disabled="isCancelling"
                        class="h-8 px-4 text-xs font-semibold bg-rose-600 hover:bg-rose-700 text-white shadow-xs"
                    >
                        <Loader2 v-if="isCancelling" class="w-3.5 h-3.5 animate-spin" />
                        <span v-else>{{ $t('task_detail.confirm_cancel') }}</span>
                    </Button>
                </div>
            </DialogContent>
        </Dialog>

        <!-- Job Error Diagnostics Modal -->
        <JobErrorDialog
            v-model:open="showErrorDialog"
            :job="selectedJobForErrorModal"
            :is-retrying="isRetrying"
            @retry="(jobId) => handleDirectRetry([jobId])"
            @tune="(jobId) => openRetryDrawer(jobId)"
        />
    </div>
</template>

<style scoped>
@keyframes progress {
    0% {
        transform: translateX(-100%);
    }
    100% {
        transform: translateX(0);
    }
}
.animate-progress {
    animation: progress 2s ease-out forwards;
}
</style>
