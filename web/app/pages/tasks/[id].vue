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
    Sparkles, Cpu
} from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

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
        case 'FAILED': return 'bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400'
        case 'PARTIALLY_FAILED': return 'bg-orange-500/10 text-orange-600 border-orange-500/20 dark:text-orange-400'
        case 'PROCESSING': return 'bg-primary/10 text-primary border-primary/20 dark:text-primary-hover'
        case 'PENDING': return 'bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400'
        default: return 'bg-surface-2 text-ink-subtle border-hairline'
    }
}

const getJobStatusStyle = (status: string) => {
    switch (status) {
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400'
        case 'FAILED': return 'bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400'
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
            totalInput += job.input_file?.size || 0
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
    if (base.length > 20) {
        return `${base.substring(0, 8)}...${base.substring(base.length - 8)}.${ext}`
    }
    return name
}

const formatParamKey = (key: string) => {
    return key.replace(/_/g, ' ')
}

const formatParamValue = (key: string, val: any) => {
    if (typeof val === 'boolean') {
        return val ? $t('common.enabled') : $t('common.disabled')
    }
    if (key === 'quality' || key === 'alpha_quality') {
        return `${val}%`
    }
    return String(val)
}

const downloadJobOutput = async (job: Job) => {
    if (!job.output_file_id || job.status !== 'COMPLETED') return
    
    downloadingJobs.value[job.id] = true
    try {
        const res = await api<ApiResponse<{ url: string }>>(`/files/${job.output_file_id}/download`)
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
</script>

<template>
    <div class="max-w-5xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-8 py-8 pb-24">
        <!-- Navigation & Security Badge -->
        <div class="flex items-center justify-between">
            <NuxtLink to="/tasks" class="flex items-center gap-1.5 text-xs font-semibold text-ink-subtle hover:text-ink transition-colors cursor-pointer group">
                <ChevronLeft class="w-4 h-4 transition-transform group-hover:-translate-x-0.5" />
                {{ $t('task_detail.back_to_tasks') }}
            </NuxtLink>
            
            <div class="flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400 bg-emerald-500/5 px-2.5 py-1 rounded-full border border-emerald-500/10 font-semibold tracking-wide">
                <ShieldCheck class="w-3.5 h-3.5" />
                {{ $t('task_detail.secure_ownership') }}
            </div>
        </div>

        <!-- Task Info & Loading -->
        <div v-if="loading" class="p-24 flex flex-col items-center justify-center space-y-3 bg-surface-1 border border-hairline rounded-xl shadow-sm">
            <Loader2 class="h-8 w-8 text-primary animate-spin" />
            <p class="text-xs font-medium text-ink-subtle uppercase tracking-wider">{{ $t('common.loading') }}</p>
        </div>

        <div v-else-if="task" class="space-y-8">
            <!-- Header Block -->
            <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-hairline pb-6">
                <div class="space-y-1.5">
                    <div class="flex items-center gap-2.5">
                        <h1 class="text-xl font-semibold tracking-tight text-ink">Task-{{ task.id.substring(0, 8) }}</h1>
                        <Badge variant="secondary" class="rounded-full font-medium text-xs px-2.5 py-0.5 border capitalize tracking-normal" :class="getStatusStyle(task.status)">
                            {{ $t('status.' + task.status) }}
                        </Badge>
                    </div>
                    <p class="text-xs font-mono text-ink-subtle">UUID: {{ task.id }}</p>
                </div>

                <!-- Details Block -->
                <div class="flex flex-wrap gap-4 text-xs font-semibold text-ink-subtle">
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

            <!-- Overview Stats Cards -->
            <div class="grid grid-cols-2 md:grid-cols-4 gap-6">
                <!-- Target Format -->
                <div class="border border-hairline shadow-sm bg-surface-1 rounded-xl p-6 flex flex-col justify-between min-h-[112px] transition-all hover:shadow-md duration-200">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.target_format') }}</span>
                        <Layers class="w-4 h-4 text-ink-subtle opacity-60" stroke-width="1.75" />
                    </div>
                    <div class="flex items-baseline gap-1.5 mt-4">
                        <span class="text-3xl font-bold tracking-tight text-ink leading-none">{{ task.jobs?.[0]?.target_format || 'Unknown' }}</span>
                        <span class="text-xs font-bold text-ink-subtle uppercase leading-none" v-if="task.jobs?.[0]?.params?.engine">({{ task.jobs[0].params.engine.split(':')[0] }})</span>
                    </div>
                </div>

                <!-- Total Files -->
                <div class="border border-hairline shadow-sm bg-surface-1 rounded-xl p-6 flex flex-col justify-between min-h-[112px] transition-all hover:shadow-md duration-200">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.total_files') }}</span>
                        <FileVideo class="w-4 h-4 text-ink-subtle opacity-60" stroke-width="1.75" />
                    </div>
                    <div class="flex items-baseline gap-1.5 mt-4">
                        <span class="text-3xl font-bold tracking-tight text-ink leading-none">{{ task.jobs?.length || 0 }}</span>
                        <span class="text-xs font-bold text-ink-subtle uppercase leading-none">{{ $t('task_detail.files_unit') }}</span>
                    </div>
                </div>

                <!-- Space Savings -->
                <div class="border border-hairline shadow-sm bg-surface-1 rounded-xl p-6 flex flex-col justify-between min-h-[112px] transition-all hover:shadow-md duration-200">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.total_savings') }}</span>
                        <TrendingDown class="w-4 h-4 text-ink-subtle opacity-60" stroke-width="1.75" />
                    </div>
                    <div class="flex items-baseline gap-1.5 mt-4">
                        <template v-if="getTotalSavings">
                            <span class="text-3xl font-bold tracking-tight text-emerald-600 dark:text-emerald-400 leading-none">-{{ getTotalSavings.ratio }}%</span>
                            <span class="text-xs font-bold text-ink-subtle uppercase leading-none">{{ $t('task_detail.saved_bytes', { bytes: formatBytes(getTotalSavings.saved) }) }}</span>
                        </template>
                        <template v-else-if="task.status === 'PROCESSING' || task.status === 'PENDING'">
                            <span class="text-xs font-semibold text-ink-subtle animate-pulse">{{ $t('task_detail.calculating') }}</span>
                        </template>
                        <template v-else>
                            <span class="text-3xl font-bold tracking-tight text-ink leading-none">—</span>
                        </template>
                    </div>
                </div>

                <!-- Task Progress -->
                <div class="border border-hairline shadow-sm bg-surface-1 rounded-xl p-6 flex flex-col justify-between min-h-[112px] transition-all hover:shadow-md duration-200">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.progress') }}</span>
                        <Cpu class="w-4 h-4 text-ink-subtle opacity-60" stroke-width="1.75" />
                    </div>
                    <div class="flex items-baseline gap-1.5 mt-4">
                        <span class="text-3xl font-bold tracking-tight text-ink leading-none" :class="task.status === 'PROCESSING' ? 'text-primary' : ''">{{ getTaskProgress }}%</span>
                        <span class="text-xs font-bold text-ink-subtle uppercase leading-none animate-pulse" v-if="task.status === 'PROCESSING'">{{ $t('task_detail.status_processing') }}</span>
                        <span class="text-xs font-bold text-ink-subtle uppercase leading-none" v-else-if="task.status === 'COMPLETED'">{{ $t('task_detail.status_completed') }}</span>
                    </div>
                </div>
            </div>

            <!-- Parameters Card -->
            <div v-if="task.jobs?.[0]?.params?.engine_params" class="border border-hairline rounded-xl bg-surface-1 shadow-sm overflow-hidden flex flex-col">
                <div class="flex items-center justify-between border-b border-hairline px-6 py-4.5 bg-surface-1">
                    <h3 class="font-semibold text-sm text-ink flex items-center gap-2">
                        <Settings class="w-4 h-4 text-ink-subtle" stroke-width="1.75" />
                        {{ $t('task_detail.params') }}
                    </h3>
                </div>
                <div class="p-6 sm:p-8">
                    <!-- High-contrast aligned grid to guarantee padding and element boundaries -->
                    <div class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-5 gap-6 sm:gap-8">
                        <div class="space-y-2">
                            <div class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ $t('task_detail.engine') }}</div>
                            <div class="text-xs font-semibold text-ink font-mono bg-surface-2 px-2.5 py-1 rounded border border-hairline w-fit">{{ task.jobs[0].params.engine }}</div>
                        </div>
                        <div v-for="(val, key) in task.jobs[0].params.engine_params" :key="key" class="space-y-2">
                            <div class="text-xs font-bold text-ink-subtle uppercase tracking-wider">{{ formatParamKey(key) }}</div>
                            <div class="text-xs font-semibold text-ink">{{ formatParamValue(key, val) }}</div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- Jobs Flow List -->
            <div class="border border-hairline rounded-xl bg-surface-1 shadow-sm overflow-hidden flex flex-col">
                <div class="flex items-center justify-between border-b border-hairline px-6 py-4.5 bg-surface-1">
                    <h3 class="font-semibold text-sm text-ink flex items-center gap-2">
                        <Sparkles class="w-4 h-4 text-ink-subtle" stroke-width="1.75" />
                        {{ $t('task_detail.transcoding_jobs') }}
                    </h3>
                    <span class="text-xs font-bold text-ink-subtle uppercase tracking-wider bg-surface-2 px-2.5 py-1 rounded-full border border-hairline">
                        {{ $t('task_detail.jobs_completed', { done: task.jobs?.filter(j => j.status === 'COMPLETED').length || 0, total: task.jobs?.length || 0 }) }}
                    </span>
                </div>
                <div class="divide-y divide-hairline">
                    <div v-for="job in task.jobs" :key="job.id" class="px-6 py-3.5 sm:px-8 sm:py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-6 hover:bg-surface-2 transition-all duration-200">
                        <!-- Left: File Details Flow -->
                        <div class="flex items-center gap-4.5 min-w-0 flex-1">
                            <!-- File Icon -->
                            <div class="w-10 h-10 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle border border-hairline shrink-0 shadow-sm transition-transform hover:scale-105 duration-200">
                                <Loader2 v-if="job.status === 'PROCESSING' || job.status === 'PENDING'" class="w-5 h-5 text-primary animate-spin" />
                                <FileImage v-else-if="isImageFile(job.input_file?.name || job.input_path)" class="w-5 h-5 text-ink-subtle" stroke-width="1.5" />
                                <FileVideo v-else class="w-5 h-5 text-ink-subtle" stroke-width="1.5" />
                            </div>

                            <!-- Flow Metadata -->
                            <div class="min-w-0 flex-1 space-y-1.5">
                                <!-- Prominent Original Filename -->
                                <div class="flex items-center gap-2">
                                    <span class="text-sm font-semibold text-ink truncate max-w-[280px] sm:max-w-[450px]" :title="job.input_file?.name || job.input_path">
                                        {{ formatFilename(job.input_file?.name || job.input_path.split('/').pop()) }}
                                    </span>
                                </div>

                                <!-- Flow Details Underneath with distinct top margin spacing -->
                                <div class="flex items-center gap-2 text-xs text-ink-subtle font-medium flex-wrap">
                                    <span class="bg-surface-2 px-2 py-0.5 rounded border border-hairline text-xs">{{ formatBytes(job.input_file?.size || 0) }}</span>
                                    <span class="text-primary font-semibold text-sm">→</span>
                                    <span class="text-primary font-semibold uppercase tracking-wider text-xs bg-primary/5 px-2 py-0.5 rounded border border-primary/10">{{ job.target_format }}</span>
                                    
                                    <template v-if="job.status === 'COMPLETED' && job.output_file?.size">
                                        <span class="text-primary font-semibold text-sm">→</span>
                                        <span class="bg-emerald-500/5 text-emerald-600 dark:text-emerald-400 px-2.5 py-0.5 rounded border border-emerald-500/10 text-xs font-semibold">
                                            {{ formatBytes(job.output_file.size) }}
                                        </span>
                                        <div class="w-1 h-1 rounded-full bg-hairline-strong shrink-0"></div>
                                        <span class="text-emerald-600 dark:text-emerald-400 font-semibold text-xs hover:opacity-85 transition-opacity">
                                            -{{ getSavingsRatio(job.input_file?.size, job.output_file.size) }}%
                                        </span>
                                    </template>
                                    <template v-else-if="job.status === 'PROCESSING' || job.status === 'PENDING'">
                                        <span class="text-primary font-semibold text-sm">→</span>
                                        <span class="text-primary font-semibold animate-pulse text-xs">{{ $t('task_detail.job_processing') }}</span>
                                    </template>
                                    <template v-else>
                                        <span class="text-primary font-semibold text-sm">→</span>
                                        <span class="text-destructive font-semibold text-xs bg-destructive/5 px-2 py-0.5 rounded border border-destructive/10">{{ $t('task_detail.job_failed') }}</span>
                                    </template>
                                </div>
                            </div>
                        </div>

                        <!-- Right: Badges & CTA Download -->
                        <div class="flex items-center justify-between sm:justify-end gap-4 shrink-0 border-t border-hairline sm:border-none pt-4 sm:pt-0">
                            <!-- Status Badges -->
                            <Badge variant="secondary" class="rounded-full font-medium text-xs px-3 py-1 border capitalize tracking-normal transition-all" :class="getJobStatusStyle(job.status)">
                                {{ $t('status.' + job.status) }}
                            </Badge>

                            <!-- Download Outlined Button -->
                            <Button
                                size="sm"
                                variant="outline"
                                :disabled="job.status !== 'COMPLETED' || downloadingJobs[job.id] || !job.output_file_id"
                                @click="downloadJobOutput(job)"
                                class="h-8.5 px-4 text-xs font-semibold gap-2 border-hairline bg-surface-1 hover:bg-surface-2 text-ink shadow-sm cursor-pointer disabled:opacity-30 disabled:cursor-not-allowed rounded-lg"
                            >
                                <Loader2 v-if="downloadingJobs[job.id]" class="w-3.5 h-3.5 animate-spin" />
                                <Download v-else class="w-3.5 h-3.5" />
                                <span>{{ downloadingJobs[job.id] ? $t('task_detail.downloading') : $t('task_detail.download') }}</span>
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
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
