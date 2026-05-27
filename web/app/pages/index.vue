<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { useApi } from '~/composables/useApi'
import { ref, onMounted, watch } from 'vue'
import type { Task, TaskStats, ListTasksData } from '~/lib/types/task'
import { TaskStatus } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import {
    Activity, Clock, CheckCircle2, AlertCircle,
    Plus, Loader2, Calendar, RotateCcw,
    Database, Cpu, Video, FileVideo,
    Check, MoreVertical, FileX, FileX2
} from '@lucide/vue'
import {
    Card,
    CardContent,
    CardHeader,
    CardTitle
} from '@/components/ui/card'
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow
} from '@/components/ui/table'
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
    // If not logged in, show empty state
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

const getStatusStyle = (status: TaskStatus) => {
    switch (status) {
        case TaskStatus.COMPLETED: return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400'
        case TaskStatus.FAILED: return 'bg-red-500/10 text-red-600 border-red-500/20 dark:text-red-400'
        case TaskStatus.PARTIALLY_FAILED: return 'bg-orange-500/10 text-orange-600 border-orange-500/20 dark:text-orange-400'
        case TaskStatus.PROCESSING: return 'bg-primary/10 text-primary border-primary/20 dark:text-primary-hover'
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
</script>

<template>
    <div class="max-w-5xl mx-auto w-full space-y-6 pb-20">
        <!-- Dashboard Header -->
        <div class="flex items-center justify-between">
            <div class="space-y-1">
                <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('nav.overview') }}</h1>
                <div class="flex items-center gap-2">
                    <div class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></div>
                    <p class="text-sm font-medium text-ink-subtle">{{ $t('dashboard.all_operational') }}</p>
                </div>
            </div>
            <div class="flex items-center gap-3">
                <Button variant="outline"
                    class="h-8 px-3 text-sm font-semibold gap-1.5 border-hairline bg-surface-1 hover:bg-surface-2 text-ink shadow-sm cursor-pointer">
                    <Calendar class="w-3.5 h-3.5 text-ink-subtle" />
                    {{ $t('dashboard.last_7_days') }}
                </Button>
            </div>
        </div>

        <!-- Stats Grid -->
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
            <!-- Total Tasks Card -->
            <Card
                class="border-hairline shadow-sm bg-surface-1 rounded-xl p-5 flex flex-col gap-0">
                <CardHeader class="flex flex-row justify-between items-center p-0 mb-3.5">
                    <CardTitle class="text-xs font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.total_tasks') }}
                    </CardTitle>
                    <RotateCcw class="w-3.5 h-3.5 text-ink-subtle opacity-60" />
                </CardHeader>
                <CardContent class="p-0">
                    <div class="flex items-baseline gap-2">
                        <span class="text-3xl font-semibold tracking-tight text-ink">{{ stats.COMPLETED +
                            stats.PROCESSING + stats.PENDING + stats.FAILED + (stats.PARTIALLY_FAILED || 0) }}</span>
                        <Badge variant="secondary"
                            class="bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-none font-semibold text-xs py-0.5 px-1.5 rounded">
                            +12%</Badge>
                    </div>
                    <!-- Placeholder Chart -->
                    <div class="mt-4 flex items-end gap-1 h-8">
                        <div v-for="h in [40, 70, 50, 90, 60, 30, 80]" :key="h"
                            class="flex-1 bg-primary/15 rounded-sm transition-all duration-300 hover:bg-primary/40"
                            :style="{ height: `${h}%` }"></div>
                    </div>
                </CardContent>
            </Card>

            <!-- Storage Usage Card -->
            <Card
                class="border-hairline shadow-sm bg-surface-1 rounded-xl p-5 flex flex-col gap-0">
                <CardHeader class="flex flex-row justify-between items-center p-0 mb-3.5">
                    <CardTitle class="text-xs font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.storage_usage') }}
                    </CardTitle>
                    <Database class="w-3.5 h-3.5 text-ink-subtle opacity-60" />
                </CardHeader>
                <CardContent class="p-0">
                    <div class="flex items-baseline gap-1.5">
                        <span class="text-3xl font-semibold tracking-tight text-ink">4.2</span>
                        <span class="text-xs font-semibold text-ink-subtle uppercase">TB / 10 TB</span>
                    </div>
                    <div class="mt-4 space-y-1.5">
                        <div class="w-full h-1 bg-surface-2 rounded-full overflow-hidden">
                            <div class="h-full bg-primary rounded-full" style="width: 42%"></div>
                        </div>
                        <div class="flex justify-between text-[10px] font-semibold text-ink-subtle uppercase">
                            <span>42% Used</span>
                            <span>5.8 TB Free</span>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <!-- Worker Nodes Card -->
            <Card
                class="border-hairline shadow-sm bg-surface-1 rounded-xl p-5 flex flex-col gap-0">
                <CardHeader class="flex flex-row justify-between items-center p-0 mb-3.5">
                    <CardTitle class="text-xs font-semibold text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.worker_nodes') }}
                    </CardTitle>
                    <Cpu class="w-3.5 h-3.5 text-ink-subtle opacity-60" />
                </CardHeader>
                <CardContent class="p-0">
                    <div class="flex items-baseline gap-1.5">
                        <span class="text-3xl font-semibold tracking-tight text-ink">12</span>
                        <span class="text-xs font-semibold text-ink-subtle uppercase">{{ $t('dashboard.worker_active') }}</span>
                    </div>
                    <div class="mt-4 flex gap-1">
                        <div v-for="i in 12" :key="i"
                            class="w-1.5 h-1.5 rounded-full bg-emerald-500 shadow-sm"></div>
                    </div>
                </CardContent>
            </Card>
        </div>

        <!-- Recent Transcoding Section -->
        <Card
            class="border-hairline shadow-sm overflow-hidden rounded-xl bg-surface-1 py-0 gap-0">
            <CardHeader
                class="flex flex-row items-center justify-between space-y-0 px-5 py-3.5 border-b border-hairline">
                <CardTitle class="font-semibold text-sm text-ink">
                    {{ $t('dashboard.recent_tasks') }}
                </CardTitle>
                <NuxtLink to="/tasks" class="text-xs font-semibold text-primary dark:text-primary-hover hover:underline">{{ $t('dashboard.view_all') }}</NuxtLink>
            </CardHeader>
            <CardContent class="p-0">
                <!-- Loading -->
                <div v-if="loading" class="p-16 flex flex-col items-center justify-center space-y-3">
                    <Loader2 class="h-6 w-6 text-primary animate-spin" />
                    <p class="text-xs font-medium text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.loading_tasks') }}</p>
                </div>

                <!-- Display Task List -->
                <div v-else-if="tasks && tasks.length > 0" class="divide-y divide-hairline">
                    <div v-for="task in tasks.slice(0, 5)" :key="task.id"
                        class="px-5 py-3 flex items-center gap-4 hover:bg-surface-2 transition-all duration-200 group">
                        <!-- File Icon -->
                        <div
                            class="w-8 h-8 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle border border-hairline shrink-0">
                            <Video v-if="task.status === 'PROCESSING'" class="w-4 h-4 text-primary" stroke-width="1.5" />
                            <FileVideo v-else-if="task.status === 'COMPLETED'" class="w-4 h-4 text-ink-subtle"
                                stroke-width="1.5" />
                            <AlertCircle v-else class="w-4 h-4 text-red-500" stroke-width="1.5" />
                        </div>

                        <!-- Task Details -->
                        <div class="flex-1 min-w-0">
                            <div class="flex items-center gap-2">
                                <span class="text-sm font-semibold text-ink truncate">Task-{{
                                    task.id.substring(0, 8) }}</span>
                                <span class="text-[10px] font-medium text-ink-subtle">{{ new
                                    Date(task.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</span>
                            </div>
                            <div class="flex items-center gap-2 mt-0.5">
                                <span class="text-xs font-medium text-ink-subtle uppercase tracking-wider">H.265 / 4K</span>
                                <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                <span class="text-xs font-medium text-ink-subtle">Started {{
                                    formatTimeAgo(task.created_at) }}</span>
                            </div>
                            <!-- Progress Bar for Processing -->
                            <div v-if="task.status === 'PROCESSING'" class="mt-1.5 flex items-center gap-2 max-w-md">
                                <div class="flex-1 h-1 bg-surface-2 rounded-full overflow-hidden">
                                    <div class="h-full bg-primary rounded-full animate-progress" style="width: 64%">
                                    </div>
                                </div>
                                <span class="text-[10px] font-semibold text-primary">64%</span>
                            </div>
                        </div>

                        <!-- Status & Actions -->
                        <div class="flex items-center gap-3 shrink-0">
                            <Badge variant="secondary"
                                class="rounded-full font-medium text-xs px-2.5 py-0.5 border capitalize tracking-normal shadow-sm transition-all"
                                :class="getStatusStyle(task.status)">
                                <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1 text-emerald-600 dark:text-emerald-400" stroke-width="2" />
                                <div v-else-if="task.status === 'PROCESSING'"
                                    class="w-1.5 h-1.5 rounded-full bg-primary mr-1.5 animate-pulse"></div>
                                {{ $t('status.' + task.status) }}
                            </Badge>
                            <Button variant="ghost" size="icon" class="h-7 w-7 text-ink-subtle hover:text-ink hover:bg-surface-2 rounded-lg cursor-pointer">
                                <MoreVertical class="w-3.5 h-3.5" />
                            </Button>
                        </div>
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
