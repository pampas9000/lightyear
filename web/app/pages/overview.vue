<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { useApi } from '~/composables/useApi'
import { ref, onMounted, watch, computed } from 'vue'
import type { Task, TaskStats, ListTasksData } from '~/lib/types/task'
import { TaskStatus } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import {
    Activity, Clock, CheckCircle2, AlertCircle,
    Plus, Loader2, Calendar, RotateCcw,
    Database, Cpu, Video, FileVideo,
    Check, MoreVertical, FileX, FileX2, ArrowUpRight,
    Zap, SlidersHorizontal
} from '@lucide/vue'
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle
} from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

const { user, isAuthenticated } = useAuth()
const api = useApi()

useHead({
    title: $t('nav.overview') + ' | Transcoder',
})

const stats = ref<TaskStats>({
    PENDING: 0,
    PROCESSING: 0,
    COMPLETED: 0,
    FAILED: 0,
    PARTIALLY_FAILED: 0
})

const tasks = ref<Task[] | null>(null)
const loading = ref(true)

const fetchDashboardData = async () => {
    if (!isAuthenticated.value) {
        loading.value = false
        return
    }

    loading.value = true
    try {
        const [statsRes, tasksRes] = await Promise.all([
            api<ApiResponse<TaskStats>>('/tasks/stats'),
            api<ApiResponse<ListTasksData>>('/tasks')
        ])

        if (statsRes.success) {
            stats.value = statsRes.data
        }
        if (tasksRes.success) {
            tasks.value = tasksRes.data.tasks || []
        }
    } catch (err) {
        console.error("Failed to fetch dashboard data:", err)
    } finally {
        loading.value = false
    }
}

watch(isAuthenticated, (val) => {
    if (val) fetchDashboardData()
})

onMounted(() => {
    fetchDashboardData()
})

const getStatusStyle = (status: TaskStatus | string) => {
    switch (status) {
        case 'COMPLETED': return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20'
        case 'FAILED': return 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/20'
        case 'CANCELLED': return 'bg-surface-2 text-ink-subtle border border-hairline'
        case 'PARTIALLY_FAILED': return 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border border-orange-500/20'
        case 'PROCESSING': return 'bg-primary-soft text-primary-soft-foreground border border-primary/20'
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

const maxDailyCount = computed(() => {
    if (!stats.value.daily_chart || stats.value.daily_chart.length === 0) return 1
    const max = Math.max(...stats.value.daily_chart)
    return max > 0 ? max : 1
})

const formatBytes = (bytes: number, decimals = 1) => {
    if (!bytes || bytes === 0) return { value: '0', unit: 'B' }
    const k = 1024
    const dm = decimals < 0 ? 0 : decimals
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return {
        value: parseFloat((bytes / Math.pow(k, i)).toFixed(dm)).toString(),
        unit: sizes[i]
    }
}

const storageUsedInfo = computed(() => {
    return formatBytes(stats.value.storage_used || 0)
})

const storageLimitInfo = computed(() => {
    return formatBytes(stats.value.storage_limit || 10995116277760)
})

const storagePercentage = computed(() => {
    if (!stats.value.storage_limit) return 0
    return Math.min(100, Math.round(((stats.value.storage_used || 0) / stats.value.storage_limit) * 100))
})

const getTaskTargetFormat = (task: Task) => {
    if (task.jobs && task.jobs.length > 0) {
        return task.jobs[0]?.options?.format?.toUpperCase() || 'Unknown'
    }
    return 'Unknown'
}

const getTaskProgress = (task: Task) => {
    if (!task.jobs || task.jobs.length === 0) return 0
    const totalProgress = task.jobs.reduce((acc, job) => acc + (job.progress || 0), 0)
    return Math.round(totalProgress / task.jobs.length)
}
</script>

<template>
    <div class="w-full max-w-7xl mx-auto space-y-6 pb-12 font-sans">
        <!-- Dashboard Top Header Bar (WASM Local Transcode Style) -->
        <header class="flex flex-wrap items-center justify-between gap-4">
            <div>
                <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('nav.overview') }}</h1>
                <p class="mt-1 flex items-center gap-2 text-sm text-ink-subtle">
                    <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
                    {{ $t('dashboard.all_operational') }}
                </p>
            </div>
            <div class="flex items-center gap-2">
                <Button variant="outline" size="sm" class="h-8 px-3 text-xs gap-1.5 cursor-pointer">
                    <Calendar class="w-3.5 h-3.5 text-ink-subtle" />
                    <span>{{ $t('dashboard.last_7_days') }}</span>
                </Button>
                <NuxtLink to="/tasks/new">
                    <Button size="sm" class="h-8 px-3 text-xs gap-1.5 cursor-pointer">
                        <Plus class="w-3.5 h-3.5" stroke-width="2.25" />
                        <span>{{ $t('nav.new_task') }}</span>
                    </Button>
                </NuxtLink>
            </div>
        </header>

        <!-- High Information Density Stats Cards (Local Transcoding Design) -->
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
            <!-- Total Tasks Card -->
            <Card class="border border-hairline shadow-xs bg-surface-1 rounded-xl p-4 flex flex-col justify-between ">
                <div class="flex items-center justify-between">
                    <span class="text-xs font-medium text-ink-muted flex items-center gap-1.5">
                        <RotateCcw class="w-3.5 h-3.5 text-ink-subtle" />
                        {{ $t('dashboard.total_tasks') }}
                    </span>
                    <Badge variant="secondary"
                        :class="[
                            (stats.growth_rate || 0) >= 0 
                                ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400' 
                                : 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20',
                            'border-transparent font-medium text-[11px] tabular-nums py-0.5 px-1.5 rounded-md'
                        ]">
                        {{ (stats.growth_rate || 0) >= 0 ? '+' : '' }}{{ stats.growth_rate !== undefined ? stats.growth_rate.toFixed(0) : '0' }}%
                    </Badge>
                </div>
                <div class="mt-4 flex items-baseline justify-between">
                    <span class="text-3xl font-semibold tracking-tight text-ink leading-none tabular-nums">
                        {{ stats.COMPLETED + stats.PROCESSING + stats.PENDING + stats.FAILED + (stats.PARTIALLY_FAILED || 0) }}
                    </span>
                </div>
                <!-- Mini Activity Chart -->
                <div class="mt-5 flex items-end gap-1 h-8">
                    <div v-for="(count, idx) in stats.daily_chart || [0, 0, 0, 0, 0, 0, 0]" :key="idx"
                        class="flex-1 bg-primary/25 hover:bg-primary rounded-[3px] transition-colors"
                        :style="{ height: `${Math.max(15, (count / maxDailyCount) * 100)}%` }"
                        :title="`${count} tasks`"></div>
                </div>
            </Card>

            <!-- Storage Usage Card -->
            <Card class="border border-hairline shadow-xs bg-surface-1 rounded-xl p-4 flex flex-col justify-between ">
                <div class="flex items-center justify-between">
                    <span class="text-xs font-medium text-ink-muted flex items-center gap-1.5">
                        <Database class="w-3.5 h-3.5 text-ink-subtle" />
                        {{ $t('dashboard.storage_usage') }}
                    </span>
                    <Badge variant="outline" class="text-[11px] font-medium border-transparent bg-surface-2 text-ink-subtle">
                        {{ storagePercentage }}% USED
                    </Badge>
                </div>
                <div class="mt-4 flex items-baseline justify-between">
                    <div class="flex items-baseline gap-1.5">
                        <span class="text-3xl font-semibold tracking-tight text-ink leading-none tabular-nums">{{ storageUsedInfo.value }}</span>
                        <span class="text-sm text-ink-subtle">{{ storageUsedInfo.unit }} / {{ storageLimitInfo.value }} {{ storageLimitInfo.unit }}</span>
                    </div>
                </div>
                <div class="mt-4 space-y-1.5">
                    <div class="w-full h-1.5 bg-surface-3 rounded-full overflow-hidden">
                        <div class="h-full bg-primary rounded-full transition-all duration-500" :style="{ width: `${storagePercentage}%` }"></div>
                    </div>
                    <div class="flex justify-between text-xs text-ink-subtle tabular-nums">
                        <span>{{ storagePercentage }}% Used</span>
                        <span>{{ formatBytes(Math.max(0, (stats.storage_limit || 10995116277760) - (stats.storage_used || 0))).value }} {{ formatBytes(Math.max(0, (stats.storage_limit || 10995116277760) - (stats.storage_used || 0))).unit }} Free</span>
                    </div>
                </div>
            </Card>

            <!-- Worker Nodes Card -->
            <Card class="border border-hairline shadow-xs bg-surface-1 rounded-xl p-4 flex flex-col justify-between ">
                <div class="flex items-center justify-between">
                    <span class="text-xs font-medium text-ink-muted flex items-center gap-1.5">
                        <Cpu class="w-3.5 h-3.5 text-ink-subtle" />
                        {{ $t('dashboard.worker_nodes') }}
                    </span>
                    <Badge variant="outline" class="text-[11px] font-medium border-transparent bg-surface-2 text-ink-subtle">
                        WASM ENGINE
                    </Badge>
                </div>
                <div class="mt-4 flex items-baseline justify-between">
                    <div class="flex items-baseline gap-2">
                        <span class="text-3xl font-semibold tracking-tight text-ink leading-none tabular-nums">{{ stats.active_workers || 0 }}</span>
                        <span class="text-sm text-ink-subtle">{{ $t('dashboard.worker_active') }}</span>
                    </div>
                </div>
                <div class="mt-5 flex items-center justify-between px-2.5 py-2 rounded-md bg-surface-2">
                    <div class="flex items-center gap-1.5">
                        <div v-if="(stats.active_workers || 0) === 0"
                            class="w-2 h-2 rounded-full bg-amber-500 shadow-xs animate-pulse" title="No active workers"></div>
                        <div v-else v-for="i in Math.min(6, (stats.active_workers || 0))" :key="i"
                            class="w-2 h-2 rounded-full bg-emerald-500 shadow-xs" title="Active worker"></div>
                    </div>
                    <span class="text-xs text-ink-subtle">Cluster Normal</span>
                </div>
            </Card>
        </div>

        <!-- Recent Transcoding Section (Local Transcoding Design Style) -->
        <Card class="border border-hairline shadow-xs overflow-hidden rounded-xl bg-surface-1 py-0 gap-0">
            <CardHeader class="h-11 px-5 py-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2">
                <CardTitle class="min-w-0 truncate text-xs leading-none font-medium text-ink-muted flex items-center gap-2">
                    <SlidersHorizontal class="w-3.5 h-3.5 text-ink-subtle shrink-0" />
                    {{ $t('dashboard.recent_tasks') }}
                </CardTitle>
                <NuxtLink to="/tasks" class="text-xs font-medium text-ink-muted hover:text-ink flex items-center gap-1 transition-colors">
                    <span>{{ $t('dashboard.view_all') }}</span>
                    <ArrowUpRight class="w-3.5 h-3.5" />
                </NuxtLink>
            </CardHeader>
            <CardContent class="p-0">
                <!-- Loading -->
                <div v-if="loading" class="p-12 flex flex-col items-center justify-center space-y-2">
                    <Loader2 class="h-6 w-6 text-primary animate-spin" />
                    <p class="text-xs text-ink-subtle">{{ $t('dashboard.loading_tasks') }}</p>
                </div>

                <!-- Display Task List (Dense Rows in Local Transcode Style) -->
                <div v-else-if="tasks && tasks.length > 0" class="divide-y divide-hairline">
                    <NuxtLink v-for="task in tasks.slice(0, 8)" :key="task.id" :to="'/tasks/' + task.id"
                        class="px-5 py-3 flex items-center gap-4 hover:bg-surface-2 transition-colors group cursor-pointer block">
                        <!-- Task Icon -->
                        <div class="w-9 h-9 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle shrink-0">
                            <Video v-if="task.status === 'PROCESSING'" class="w-4 h-4 text-primary animate-pulse" stroke-width="2" />
                            <FileVideo v-else-if="task.status === 'COMPLETED'" class="w-4 h-4 text-ink-subtle" stroke-width="1.8" />
                            <AlertCircle v-else class="w-4 h-4 text-red-500" stroke-width="1.8" />
                        </div>

                        <!-- Main Info Flow -->
                        <div class="flex-1 min-w-0 flex items-center justify-between gap-4">
                            <div class="min-w-0 space-y-1">
                                <div class="flex items-center gap-2">
                                    <span class="text-xs font-mono font-medium text-ink">Task-{{ task.id.substring(0, 8) }}</span>
                                    <span class="text-[11px] text-ink-subtle tabular-nums">
                                        {{ new Date(task.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}
                                    </span>
                                </div>
                                <div class="flex items-center gap-2 text-xs text-ink-subtle font-medium">
                                    <span class="font-medium text-ink-muted bg-surface-2 px-1.5 py-0.5 rounded text-[10px] font-mono uppercase">
                                        {{ getTaskTargetFormat(task) }}
                                    </span>
                                    <span class="text-xs text-ink-subtle font-mono">Started {{ formatTimeAgo(task.created_at) }}</span>
                                </div>
                            </div>

                            <!-- Live Progress for Active Tasks -->
                            <div v-if="task.status === 'PROCESSING'" class="hidden md:flex items-center gap-2 w-36 shrink-0">
                                <div class="flex-1 h-1.5 bg-surface-3 rounded-full overflow-hidden">
                                    <div class="h-full bg-primary rounded-full transition-all duration-300" :style="{ width: `${getTaskProgress(task)}%` }"></div>
                                </div>
                                <span class="text-[10px] font-mono font-bold text-primary shrink-0">{{ getTaskProgress(task) }}%</span>
                            </div>
                        </div>

                        <!-- Status Badge -->
                        <div class="shrink-0">
                            <Badge variant="secondary"
                                class="rounded-full text-[11px] font-medium px-2 py-0.5 border"
                                :class="getStatusStyle(task.status)">
                                <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1 text-emerald-600 dark:text-emerald-400" stroke-width="2.5" />
                                <div v-else-if="task.status === 'PROCESSING'" class="w-1.5 h-1.5 rounded-full bg-primary mr-1.5 animate-pulse"></div>
                                {{ $t('status.' + task.status) }}
                            </Badge>
                        </div>
                    </NuxtLink>
                </div>

                <!-- Empty State -->
                <div v-else class="p-12 flex flex-col items-center justify-center text-center">
                    <div class="w-10 h-10 rounded-full bg-surface-2 flex items-center justify-center mb-3">
                        <FileX class="w-5 h-5 text-ink-subtle" />
                    </div>
                    <h3 class="text-sm font-semibold text-ink mb-1">{{ $t('dashboard.no_tasks') }}</h3>
                    <p class="text-xs text-ink-subtle max-w-xs mx-auto mb-4">
                        {{ $t('dashboard.no_tasks_desc') }}
                    </p>
                    <NuxtLink to="/tasks/new">
                        <Button size="sm" class="bg-primary hover:bg-primary-hover text-primary-foreground font-semibold px-4 h-8 text-xs rounded-lg transition-all cursor-pointer shadow-xs">
                            {{ $t('dashboard.create_first') }}
                        </Button>
                    </NuxtLink>
                </div>
            </CardContent>
        </Card>
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

