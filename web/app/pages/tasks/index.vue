<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { useApi } from '~/composables/useApi'
import { ref, onMounted, watch } from 'vue'
import type { Task, TaskStatus, ListTasksData } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import {
    Activity, Clock, CheckCircle2, AlertCircle,
    Plus, Loader2, Calendar, RotateCcw,
    Database, Cpu, Video, FileVideo, FileImage,
    Check, MoreVertical, FileX, Filter, Search,
    ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight,
    ArrowUpRight
} from '@lucide/vue'
import {
    Card,
    CardContent,
} from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
    Select,
    SelectContent,
    SelectGroup,
    SelectItem,
    SelectLabel,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import {
    Pagination,
    PaginationEllipsis,
    PaginationFirst,
    PaginationItem,
    PaginationLast,
    PaginationList,
    PaginationNext,
    PaginationPrev,
} from '@/components/ui/pagination'

import { toast } from 'vue-sonner'

useHead({
    title: $t('nav.tasks') + ' | Transcoder',
})

const { isAuthenticated } = useAuth()
const api = useApi()

const tasks = ref<Task[] | null>(null)
const loading = ref(true)
const searchQuery = ref("")
const retryingTasks = ref<Record<string, boolean>>({})

const retryTask = async (taskObj: Task) => {
    retryingTasks.value[taskObj.id] = true
    try {
        const res = await api<ApiResponse<Task>>(`/tasks/${taskObj.id}/retry`, {
            method: 'POST',
            body: {}
        })
        if (res.success) {
            toast.success($t('task_detail.toast_retry_success'))
            fetchTasks()
        }
    } catch (err: any) {
        console.error('Failed to retry task:', err)
        toast.error(err.data?.message || $t('task_detail.toast_retry_failed'))
    } finally {
        retryingTasks.value[taskObj.id] = false
    }
}

// Pagination & Filtering state
const currentPage = ref(1)
const itemsPerPage = 12
const totalTasks = ref(0)
const statusFilter = ref("ALL")

const fetchTasks = async () => {
    if (!isAuthenticated.value) {
        loading.value = false
        return
    }

    loading.value = true
    try {
        const query: Record<string, string | number> = { count: itemsPerPage, page: currentPage.value }
        if (statusFilter.value !== 'ALL') query.status = statusFilter.value

        const res = await api<ApiResponse<ListTasksData>>('/tasks', { query })
        if (res.success) {
            tasks.value = res.data.tasks || []
            totalTasks.value = res.data.total || 0
        }
    } catch (err) {
        console.error("Failed to fetch tasks:", err)
    } finally {
        loading.value = false
    }
}

watch(isAuthenticated, (val) => {
    if (val) fetchTasks()
})

watch([currentPage, statusFilter], () => {
    if (isAuthenticated.value) {
        fetchTasks()
    }
})

onMounted(() => {
    fetchTasks()
})

const getStatusStyle = (status: TaskStatus | string) => {
    switch (status) {
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20'
        case 'FAILED': return 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/20'
        case 'CANCELLED': return 'bg-zinc-500/10 text-zinc-600 dark:text-zinc-400 border border-zinc-500/20'
        case 'PARTIALLY_FAILED': return 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border border-orange-500/20'
        case 'PROCESSING': return 'bg-primary/10 text-primary border border-primary/20'
        case 'PENDING': return 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20'
        default: return 'bg-surface-2 text-ink-subtle border border-hairline'
    }
}

const formatTimeAgo = (date: string) => {
    const seconds = Math.floor((new Date().getTime() - new Date(date).getTime()) / 1000)
    if (seconds < 60) return $t('common.just_now')
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return $t('common.minutes_ago', { n: minutes })
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return $t('common.hours_ago', { n: hours })
    return new Date(date).toLocaleDateString()
}

const getTaskSummaryLabel = (task: Task) => {
    if (!task.jobs || task.jobs.length === 0 || !task.jobs[0]) return 'UNKNOWN'
    const job = task.jobs[0]
    const engine = job.params?.engine
    if (engine) {
        const cleanEngine = engine.split(':')[0]
        return `${job.target_format} (${cleanEngine})`
    }
    return job.target_format
}

const getTaskProgress = (task: Task) => {
    if (!task.jobs || task.jobs.length === 0) return 0
    const sum = task.jobs.reduce((acc, job) => acc + (job.progress || 0), 0)
    return Math.round(sum / task.jobs.length)
}

const getTaskCompletedCount = (task: Task) => {
    return task.jobs?.filter(j => j.status === 'COMPLETED').length || 0
}

const getTaskFailedCount = (task: Task) => {
    return task.jobs?.filter(j => j.status === 'FAILED').length || 0
}

const getTaskDominantErrorCode = (task: Task) => {
    const failedJobs = task.jobs?.filter(j => j.status === 'FAILED') || []
    if (failedJobs.length === 0 || !failedJobs[0]) return null
    return failedJobs[0].error_code || null
}

const getMediaFlow = (task: Task) => {
    if (!task.jobs || task.jobs.length === 0) return { source: 'unknown', target: 'unknown' }
    
    const isImageExt = (ext: string) => ['png', 'jpg', 'jpeg', 'webp', 'avif', 'jxl', 'gif', 'apng', 'heic', 'heif'].includes(ext.toLowerCase())
    const isVideoExt = (ext: string) => ['mp4', 'mkv', 'webm', 'mov', 'avi', 'flv'].includes(ext.toLowerCase())
    
    let hasImageInput = false
    let hasVideoInput = false
    let hasImageOutput = false
    let hasVideoOutput = false
    
    task.jobs.forEach(job => {
        const inputExt = job.input_path?.split('.').pop() || ''
        if (isImageExt(inputExt)) hasImageInput = true
        if (isVideoExt(inputExt)) hasVideoInput = true
        
        const targetFmt = job.target_format?.toLowerCase() || ''
        if (isImageExt(targetFmt)) hasImageOutput = true
        if (isVideoExt(targetFmt)) hasVideoOutput = true
    })
    
    const source = (hasImageInput && hasVideoInput) ? 'mixed' : (hasImageInput ? 'image' : (hasVideoInput ? 'video' : 'unknown'))
    const target = (hasImageOutput && hasVideoOutput) ? 'mixed' : (hasImageOutput ? 'image' : (hasVideoOutput ? 'video' : 'unknown'))
    
    return { source, target }
}
</script>

<template>
    <div class="w-full max-w-7xl mx-auto space-y-5 pb-12 font-sans">
        <!-- Top Header Bar (WASM Local Transcoding Sandbox Style) -->
        <header class="h-14 border border-hairline bg-surface-1 rounded-xl shrink-0 flex items-center justify-between px-5 z-20 shadow-subtle">
            <div class="flex items-center gap-3">
                <div class="w-7 h-7 rounded-lg bg-primary text-primary-foreground flex items-center justify-center shrink-0 shadow-sm">
                    <Zap class="w-4 h-4" stroke-width="2.5" />
                </div>
                <div>
                    <h1 class="text-sm font-bold tracking-tight text-ink font-satoshi flex items-center gap-2">
                        {{ $t('dashboard.title') }}
                        <Badge class="bg-primary/10 text-primary border-none text-[10px] px-1.5 py-0.5 h-auto font-mono font-bold uppercase select-none">
                            {{ totalTasks }} {{ $t('nav.tasks') }}
                        </Badge>
                    </h1>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <NuxtLink to="/tasks/new">
                    <Button size="sm" class="h-8 px-3 text-xs font-semibold gap-1.5 bg-primary text-primary-foreground hover:bg-primary-hover shadow-xs rounded-lg cursor-pointer transition-all duration-200">
                        <Plus class="w-3.5 h-3.5" stroke-width="2.5" />
                        <span>{{ $t('nav.new_task') }}</span>
                    </Button>
                </NuxtLink>
            </div>
        </header>

        <!-- Filters & Search Toolbar (Local Transcoding Action Toolbar) -->
        <div class="flex flex-col sm:flex-row gap-2.5 justify-between items-center bg-surface-2/40 p-2 rounded-xl border border-hairline shadow-subtle backdrop-blur-md">
            <div class="flex items-center w-full sm:w-80 pl-2 relative">
                <Search class="w-3.5 h-3.5 text-primary absolute left-2.5" />
                <Input v-model="searchQuery" type="text" :placeholder="$t('tasks.search_placeholder')"
                    class="w-full pl-8 h-8 border border-hairline bg-surface-1 focus-visible:border-primary/50 focus-visible:ring-1 focus-visible:ring-primary/20 text-xs font-medium rounded-lg shadow-2xs transition-all" />
            </div>
            <div class="flex gap-2 w-full sm:w-auto pr-1 items-center justify-end">
                <Select v-model="statusFilter">
                    <SelectTrigger class="w-[140px] h-8 bg-surface-1 border-hairline shadow-2xs text-xs font-semibold text-ink hover:bg-surface-2 focus:ring-0 cursor-pointer rounded-lg transition-all">
                        <Filter class="w-3.5 h-3.5 mr-1.5 text-primary" />
                        <SelectValue :placeholder="$t('tasks.status_placeholder')" />
                    </SelectTrigger>
                    <SelectContent class="bg-surface-1 border-hairline rounded-lg text-ink shadow-md">
                        <SelectGroup>
                            <SelectItem value="ALL" class="text-xs font-medium cursor-pointer">{{ $t('tasks.all_statuses') }}</SelectItem>
                            <SelectItem value="PENDING" class="text-xs font-medium cursor-pointer">{{ $t('status.PENDING') }}</SelectItem>
                            <SelectItem value="PROCESSING" class="text-xs font-medium cursor-pointer">{{ $t('status.PROCESSING') }}</SelectItem>
                            <SelectItem value="COMPLETED" class="text-xs font-medium cursor-pointer">{{ $t('status.COMPLETED') }}</SelectItem>
                            <SelectItem value="FAILED" class="text-xs font-medium cursor-pointer">{{ $t('status.FAILED') }}</SelectItem>
                            <SelectItem value="PARTIALLY_FAILED" class="text-xs font-medium cursor-pointer">{{ $t('status.PARTIALLY_FAILED') }}</SelectItem>
                        </SelectGroup>
                    </SelectContent>
                </Select>
            </div>
        </div>

        <!-- High Density Task List Table/Rows (Local Transcoding Design Style) -->
        <div class="border border-hairline shadow-subtle overflow-hidden rounded-xl bg-surface-1">
            <!-- Loading -->
            <div v-if="loading" class="p-16 flex flex-col items-center justify-center space-y-2">
                <Loader2 class="h-6 w-6 text-primary animate-spin" />
                <p class="text-xs font-bold text-ink-subtle uppercase tracking-wider font-mono">{{ $t('dashboard.loading_tasks') }}</p>
            </div>

            <!-- Display Task List (Compact Rows) -->
            <div v-else-if="tasks && tasks.length > 0" class="flex flex-col">
                <div class="divide-y divide-hairline">
                    <NuxtLink
                        v-for="task in tasks"
                        :key="task.id"
                        :to="'/tasks/' + task.id"
                        class="px-5 py-3 flex items-center gap-4 hover:bg-surface-2/60 transition-all duration-150 group cursor-pointer block"
                    >
                        <!-- Dynamic Flow Icon -->
                        <div class="w-9 h-9 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle border border-hairline shrink-0 shadow-xs group-hover:border-primary/40 transition-colors">
                            <Loader2 v-if="task.status === 'PROCESSING' || task.status === 'PENDING'" class="w-4 h-4 text-primary animate-spin" />
                            <FileImage v-else-if="getMediaFlow(task).target === 'image'" class="w-4 h-4 text-ink-subtle" stroke-width="1.8" />
                            <FileVideo v-else class="w-4 h-4 text-ink-subtle" stroke-width="1.8" />
                        </div>

                        <!-- Main Task Flow Info -->
                        <div class="flex-1 min-w-0 flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                            <!-- Left: Task ID & Format Capsule -->
                            <div class="min-w-0 space-y-1">
                                <div class="flex items-center gap-2">
                                    <span class="text-xs font-mono font-bold text-ink group-hover:text-primary transition-colors">
                                        Task-{{ task.id.substring(0, 8) }}
                                    </span>
                                    <span class="text-[10px] font-mono font-medium text-ink-subtle bg-surface-2 px-1.5 py-0.5 rounded border border-hairline">
                                        {{ new Date(task.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}
                                    </span>
                                </div>
                                <div class="flex items-center gap-2 text-xs text-ink-subtle font-medium flex-wrap">
                                    <span class="uppercase tracking-wider font-bold text-primary bg-primary/10 px-1.5 py-0.5 rounded border border-primary/20 text-[10px] font-mono">
                                        {{ getTaskSummaryLabel(task) }}
                                    </span>
                                    <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                    <span class="text-xs text-ink-subtle font-mono">
                                        {{ $t('tasks.files_count', { count: task.jobs?.length || 0 }) }}
                                    </span>
                                    <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                    <span class="font-mono text-xs">
                                        {{ task.status === 'COMPLETED' ? $t('tasks.completed_time', { time: formatTimeAgo(task.updated_at) }) : $t('tasks.started_time', { time: formatTimeAgo(task.created_at) }) }}
                                    </span>
                                </div>
                            </div>

                            <!-- Middle: Processing Progress (if active) -->
                            <div v-if="task.status === 'PROCESSING'" class="flex items-center gap-2 w-44 shrink-0" @click.stop.prevent>
                                <div class="flex-1 h-1.5 bg-surface-2 rounded-full overflow-hidden border border-hairline">
                                    <div class="h-full bg-primary rounded-full animate-progress" :style="{ width: `${getTaskProgress(task)}%` }"></div>
                                </div>
                                <span class="text-[10px] font-mono font-bold text-primary shrink-0">{{ getTaskProgress(task) }}%</span>
                            </div>

                            <!-- Failed notice if errors -->
                            <div v-if="task.status === 'FAILED' || task.status === 'PARTIALLY_FAILED'" class="flex items-center gap-1.5 shrink-0 font-mono">
                                <span class="flex items-center gap-1 text-rose-600 dark:text-rose-400 font-bold text-xs">
                                    <AlertCircle class="w-3.5 h-3.5" />
                                    <span>{{ $t('tasks.failed_count', { count: getTaskFailedCount(task) }) }}</span>
                                </span>
                                <span v-if="getTaskDominantErrorCode(task)" class="text-[10px] font-mono px-1.5 py-0.2 rounded bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/20">
                                    {{ getTaskDominantErrorCode(task) }}
                                </span>
                            </div>
                        </div>

                        <!-- Right: Status Badge & Link Icon -->
                        <div class="flex items-center gap-2.5 shrink-0" @click.stop.prevent>
                            <button
                                v-if="task.status === 'FAILED' || task.status === 'PARTIALLY_FAILED'"
                                @click="retryTask(task)"
                                :disabled="retryingTasks[task.id]"
                                class="h-7 px-2.5 rounded-lg bg-rose-500/10 hover:bg-rose-500/20 text-rose-600 dark:text-rose-400 border border-rose-500/20 text-xs font-bold flex items-center gap-1.5 transition-all cursor-pointer disabled:opacity-50 active:scale-95 shadow-2xs"
                                :title="$t('task_detail.retry_failed_jobs')"
                            >
                                <Loader2 v-if="retryingTasks[task.id]" class="w-3 h-3 animate-spin" />
                                <RotateCcw v-else class="w-3 h-3 stroke-[2.2]" />
                                <span>重试</span>
                            </button>
                            <Badge variant="secondary"
                                class="rounded-full font-mono text-[11px] font-bold px-2.5 py-0.5 border uppercase tracking-wide transition-all"
                                :class="getStatusStyle(task.status)">
                                <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1 text-emerald-600 dark:text-emerald-400" stroke-width="2.5" />
                                <div v-else-if="task.status === 'PROCESSING'" class="w-1.5 h-1.5 rounded-full bg-primary mr-1.5 animate-pulse"></div>
                                {{ $t('status.' + task.status) }}
                            </Badge>
                            <ArrowUpRight class="w-4 h-4 text-ink-subtle group-hover:text-primary transition-colors" />
                        </div>
                    </NuxtLink>
                </div>

                <!-- Compact Pagination Footer -->
                <div class="px-5 py-3 border-t border-hairline flex flex-col sm:flex-row gap-3 items-center justify-between bg-surface-1/50">
                    <div class="text-xs font-mono font-medium text-ink-subtle whitespace-nowrap">
                        {{ $t('tasks.showing', { from: (currentPage - 1) * itemsPerPage + 1, to: Math.min(currentPage * itemsPerPage, totalTasks), total: totalTasks }) }}
                    </div>
                    <Pagination :total="totalTasks" :sibling-count="1" show-edges
                        :default-page="1" :items-per-page="itemsPerPage" v-model:page="currentPage">
                        <PaginationList v-slot="{ items }" class="flex items-center gap-1">
                            <PaginationFirst class="w-7 h-7 p-0 rounded-md cursor-pointer border-hairline hover:bg-surface-2" size="icon">
                                <ChevronsLeft class="w-3.5 h-3.5" />
                            </PaginationFirst>
                            <PaginationPrev class="w-7 h-7 p-0 rounded-md cursor-pointer border-hairline hover:bg-surface-2" size="icon">
                                <ChevronLeft class="w-3.5 h-3.5" />
                            </PaginationPrev>
                            <template v-for="(item, index) in items">
                                <PaginationItem v-if="item.type === 'page'" :key="index" :value="item.value"
                                    :isActive="item.value === currentPage"
                                    class="w-7 h-7 rounded-md cursor-pointer font-bold font-mono text-xs transition-all"
                                    :class="item.value === currentPage ? 'bg-primary text-primary-foreground hover:bg-primary-hover border-none shadow-xs' : 'border border-hairline hover:bg-surface-2'">
                                    {{ item.value }}
                                </PaginationItem>
                                <PaginationEllipsis v-else :key="item.type" :index="index"
                                    class="w-7 h-7 flex items-center justify-center text-ink-subtle text-xs font-mono" />
                            </template>
                            <PaginationNext class="w-7 h-7 p-0 rounded-md cursor-pointer border-hairline hover:bg-surface-2" size="icon">
                                <ChevronRight class="w-3.5 h-3.5" />
                            </PaginationNext>
                            <PaginationLast class="w-7 h-7 p-0 rounded-md cursor-pointer border-hairline hover:bg-surface-2" size="icon">
                                <ChevronsRight class="w-3.5 h-3.5" />
                            </PaginationLast>
                        </PaginationList>
                    </Pagination>
                </div>
            </div>

            <!-- Empty State -->
            <div v-else class="p-12 flex flex-col items-center justify-center text-center">
                <div class="w-10 h-10 rounded-xl bg-surface-2 flex items-center justify-center mb-3 border border-hairline shadow-xs">
                    <FileX class="w-5 h-5 text-ink-subtle" />
                </div>
                <h3 class="text-sm font-bold text-ink mb-1 font-satoshi">{{ $t('dashboard.no_tasks') }}</h3>
                <p class="text-xs text-ink-subtle max-w-xs mx-auto mb-4">
                    {{ $t('dashboard.no_tasks_desc') }}
                </p>
                <NuxtLink to="/tasks/new">
                    <Button size="sm" class="bg-primary hover:bg-primary-hover text-primary-foreground font-semibold px-4 h-8 text-xs rounded-lg transition-all cursor-pointer shadow-xs">
                        {{ $t('dashboard.create_first') }}
                    </Button>
                </NuxtLink>
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
