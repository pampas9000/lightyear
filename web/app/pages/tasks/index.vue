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
    ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight
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

useHead({
    title: $t('nav.tasks') + ' | Transcoder',
})

const { isAuthenticated } = useAuth()
const api = useApi()

const tasks = ref<Task[] | null>(null)
const loading = ref(true)
const searchQuery = ref("")

// Pagination & Filtering state
const currentPage = ref(1)
const itemsPerPage = 10
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
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400'
        case 'FAILED': return 'bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400'
        case 'PARTIALLY_FAILED': return 'bg-orange-500/10 text-orange-600 border-orange-500/20 dark:text-orange-400'
        case 'PROCESSING': return 'bg-primary/10 text-primary border-primary/20 dark:text-primary-hover'
        case 'PENDING': return 'bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-400'
        default: return 'bg-surface-2 text-ink-subtle border-hairline'
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
    if (!task.jobs || task.jobs.length === 0) return 'UNKNOWN'
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
    <div class="max-w-5xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-8 py-8 pb-24">
        <!-- Header -->
        <div class="flex items-center justify-between">
            <div class="space-y-1.5">
                <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('dashboard.title') }}</h1>
                <p class="text-xs font-medium text-ink-subtle">{{ $t('dashboard.subtitle') }}</p>
            </div>
            <div class="flex items-center gap-3">
                <NuxtLink to="/tasks/new">
                    <Button
                        class="bg-primary hover:bg-primary-hover text-primary-foreground rounded-lg shadow-sm border-none transition-all duration-200 gap-1.5 font-medium text-xs h-8.5 px-3 cursor-pointer">
                        <Plus class="w-3.5 h-3.5" stroke-width="2.5" />
                        {{ $t('nav.new_task') }}
                    </Button>
                </NuxtLink>
            </div>
        </div>

        <!-- Filters & Search -->
        <div
            class="flex flex-col sm:flex-row gap-3 justify-between items-center bg-surface-1 p-2 rounded-xl border border-hairline shadow-sm">
            <div class="flex items-center w-full sm:w-80 pl-2 relative">
                <Search class="w-3.5 h-3.5 text-ink-subtle absolute left-2" />
                <Input v-model="searchQuery" type="text" :placeholder="$t('tasks.search_placeholder')"
                    class="w-full pl-7 h-8 border-none bg-transparent shadow-none focus-visible:ring-0 text-xs font-medium" />
            </div>
            <div class="flex gap-2 w-full sm:w-auto pr-2 items-center justify-end">
                <Select v-model="statusFilter">
                    <SelectTrigger
                        class="w-[130px] h-8 bg-transparent border-none shadow-none text-xs font-semibold text-ink-subtle hover:text-ink focus:ring-0 cursor-pointer">
                        <Filter class="w-3.5 h-3.5 mr-1.5" />
                        <SelectValue :placeholder="$t('tasks.status_placeholder')" />
                    </SelectTrigger>
                    <SelectContent class="bg-surface-1 border-hairline rounded-lg">
                        <SelectGroup>
                            <SelectItem value="ALL" class="text-xs font-medium">{{ $t('tasks.all_statuses') }}</SelectItem>
                            <SelectItem value="PENDING" class="text-xs font-medium">{{ $t('status.PENDING') }}</SelectItem>
                            <SelectItem value="PROCESSING" class="text-xs font-medium">{{ $t('status.PROCESSING') }}</SelectItem>
                            <SelectItem value="COMPLETED" class="text-xs font-medium">{{ $t('status.COMPLETED') }}</SelectItem>
                            <SelectItem value="FAILED" class="text-xs font-medium">{{ $t('status.FAILED') }}</SelectItem>
                            <SelectItem value="PARTIALLY_FAILED" class="text-xs font-medium">{{ $t('status.PARTIALLY_FAILED') }}</SelectItem>
                        </SelectGroup>
                    </SelectContent>
                </Select>
                <div class="w-px h-3 bg-hairline mx-1"></div>
                <Button variant="ghost" class="h-8 text-xs font-medium text-ink-subtle hover:text-ink hover:bg-surface-2 gap-1.5 cursor-pointer">
                    <Calendar class="w-3.5 h-3.5" />
                    {{ $t('tasks.date_filter') }}
                </Button>
            </div>
        </div>

        <!-- Tasks List -->
        <div class="border border-hairline shadow-sm overflow-hidden rounded-xl bg-surface-1">
            <!-- Loading -->
            <div v-if="loading" class="p-20 flex flex-col items-center justify-center space-y-3">
                <Loader2 class="h-7 w-7 text-primary animate-spin" />
                <p class="text-xs font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.loading_tasks') }}</p>
            </div>

            <!-- Display Task List -->
            <div v-else-if="tasks && tasks.length > 0" class="flex flex-col h-full">
                <div class="divide-y divide-hairline">
                    <NuxtLink v-for="task in tasks" :key="task.id" :to="'/tasks/' + task.id"
                        class="px-6 py-3.5 sm:px-8 sm:py-4 flex items-center gap-5 hover:bg-surface-2 transition-all duration-200 group cursor-pointer block">
                        <!-- Dynamic Flow Icon -->
                        <div
                            class="w-10 h-10 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle border border-hairline shrink-0 shadow-sm transition-transform hover:scale-105 duration-200">
                            <Loader2 v-if="task.status === 'PROCESSING' || task.status === 'PENDING'" class="w-5 h-5 text-primary animate-spin" />
                            <FileImage v-else-if="getMediaFlow(task).target === 'image'" class="w-5 h-5 text-ink-subtle"
                                stroke-width="1.5" />
                            <FileVideo v-else class="w-5 h-5 text-ink-subtle"
                                stroke-width="1.5" />
                        </div>

                        <!-- Task Details -->
                        <div class="flex-1 min-w-0 space-y-1.5">
                            <div class="flex items-center gap-2.5">
                                <span class="text-sm font-semibold text-ink group-hover:text-primary transition-colors">Task-{{ task.id.substring(0, 8) }}</span>
                                <span class="text-xs font-semibold text-ink-subtle bg-surface-2 px-2 py-0.5 rounded border border-hairline hidden sm:inline-block font-mono">
                                    {{ new Date(task.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}
                                </span>
                            </div>
                            <div class="flex items-center gap-2.5 text-xs text-ink-subtle font-medium flex-wrap">
                                <span class="uppercase tracking-wider font-semibold text-primary/80 bg-primary/5 px-2 py-0.5 rounded border border-primary/10 text-xs">
                                    {{ getTaskSummaryLabel(task) }}
                                </span>
                                <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                <span class="bg-surface-2 px-2 py-0.5 rounded border border-hairline text-xs">
                                    {{ $t('tasks.files_count', { count: task.jobs?.length || 0 }) }}
                                </span>
                                <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                <span>
                                    {{ task.status === 'COMPLETED' ? $t('tasks.completed_time', { time: formatTimeAgo(task.updated_at) }) : $t('tasks.started_time', { time: formatTimeAgo(task.created_at) }) }}
                                </span>
                            </div>
                            <!-- Progress Bar for Processing -->
                            <div v-if="task.status === 'PROCESSING'" class="mt-2 flex items-center gap-3 max-w-md pr-4" @click.stop.prevent>
                                <div
                                    class="flex-1 h-1.5 bg-surface-2 rounded-full overflow-hidden">
                                    <div class="h-full bg-primary rounded-full animate-progress"
                                        :style="{ width: `${getTaskProgress(task)}%` }">
                                    </div>
                                </div>
                                <span class="text-xs font-semibold text-primary shrink-0">
                                    {{ $t('tasks.progress_summary', { done: getTaskCompletedCount(task), total: task.jobs?.length || 0, percent: getTaskProgress(task) }) }}
                                </span>
                            </div>
                            <!-- Failed Hint if failed or partially failed -->
                            <div v-if="task.status === 'FAILED' || task.status === 'PARTIALLY_FAILED'" class="mt-1 flex items-center gap-1.5 text-red-500 font-semibold text-xs">
                                <AlertCircle class="w-3.5 h-3.5 shrink-0" />
                                <span>{{ $t('tasks.failed_count', { count: getTaskFailedCount(task) }) }}</span>
                            </div>
                        </div>

                        <!-- Status & Actions -->
                        <div class="flex items-center gap-2 sm:gap-4 shrink-0" @click.stop.prevent>
                            <Badge variant="secondary"
                                class="rounded-full font-medium text-xs px-3 py-1 border capitalize tracking-normal transition-all hidden sm:flex"
                                :class="getStatusStyle(task.status)">
                                <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1 text-emerald-600 dark:text-emerald-400"
                                    stroke-width="2" />
                                <div v-else-if="task.status === 'PROCESSING'"
                                    class="w-1.5 h-1.5 rounded-full bg-primary mr-1.5 animate-pulse"></div>
                                <Clock v-else-if="task.status === 'PENDING'" class="w-3 h-3 mr-1"
                                    stroke-width="2" />
                                <AlertCircle v-else-if="task.status === 'FAILED'" class="w-3.5 h-3.5 mr-1"
                                    stroke-width="2" />
                                {{ $t('status.' + task.status) }}
                            </Badge>
                            <Button variant="ghost" size="icon" class="h-8.5 w-8.5 text-ink-subtle hover:text-ink hover:bg-surface-2 rounded-lg cursor-pointer">
                                <MoreVertical class="w-4 h-4" />
                            </Button>
                        </div>
                    </NuxtLink>
                </div>

                <!-- Pagination Footer -->
                <div
                    class="px-6 py-4 border-t border-hairline flex flex-col sm:flex-row gap-4 items-center justify-between bg-surface-1">
                    <div class="text-xs font-semibold text-ink-subtle whitespace-nowrap">
                        {{ $t('tasks.showing', { from: (currentPage - 1) * itemsPerPage + 1, to: Math.min(currentPage * itemsPerPage, totalTasks), total: totalTasks }) }}
                    </div>
                    <Pagination :total="totalTasks" :sibling-count="1" show-edges
                        :default-page="1" :items-per-page="itemsPerPage" v-model:page="currentPage">
                        <PaginationList v-slot="{ items }" class="flex items-center gap-1.5">
                            <PaginationFirst class="w-8 h-8 p-0 rounded-lg cursor-pointer" size="icon">
                                <ChevronsLeft class="w-4 h-4" />
                            </PaginationFirst>
                            <PaginationPrev class="w-8 h-8 p-0 rounded-lg cursor-pointer" size="icon">
                                <ChevronLeft class="w-4 h-4" />
                            </PaginationPrev>
                            <template v-for="(item, index) in items">
                                <PaginationItem v-if="item.type === 'page'" :key="index" :value="item.value"
                                    :isActive="item.value === currentPage"
                                    class="w-8 h-8 rounded-lg cursor-pointer font-bold text-xs"
                                    :class="item.value === currentPage ? 'bg-primary text-primary-foreground hover:bg-primary-hover border-none' : ''">
                                    {{ item.value }}
                                </PaginationItem>
                                <PaginationEllipsis v-else :key="item.type" :index="index"
                                    class="w-8 h-8 flex items-center justify-center text-ink-subtle" />
                            </template>
                            <PaginationNext class="w-8 h-8 p-0 rounded-lg cursor-pointer" size="icon">
                                <ChevronRight class="w-4 h-4" />
                            </PaginationNext>
                            <PaginationLast class="w-8 h-8 p-0 rounded-lg cursor-pointer" size="icon">
                                <ChevronsRight class="w-4 h-4" />
                            </PaginationLast>
                        </PaginationList>
                    </Pagination>
                </div>
            </div>

            <!-- Empty State -->
            <div v-else class="p-16 flex flex-col items-center justify-center text-center">
                <div
                    class="w-12 h-12 rounded-xl bg-surface-2 flex items-center justify-center mb-4 border border-hairline">
                    <FileX class="w-6 h-6 text-ink-subtle" />
                </div>
                <h3 class="text-base font-semibold text-ink mb-1">{{ $t('dashboard.no_tasks') }}</h3>
                <p class="text-sm text-ink-subtle max-w-xs mx-auto mb-6">
                    {{ $t('dashboard.no_tasks_desc') }}
                </p>
                <NuxtLink to="/tasks/new">
                    <Button
                        class="bg-primary hover:bg-primary-hover text-primary-foreground font-semibold px-6 h-9 rounded-lg transition-all cursor-pointer">
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
