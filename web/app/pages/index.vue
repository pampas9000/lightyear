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
    title: "Overview | Transcoder",
})

const stats = ref<TaskStats>({
    PENDING: 0,
    PROCESSING: 0,
    COMPLETED: 0,
    FAILED: 0
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
}, { immediate: true })

onMounted(() => {
    fetchDashboardData()
})

const getStatusStyle = (status: TaskStatus) => {
    switch (status) {
        case TaskStatus.COMPLETED: return 'bg-slate-50 text-slate-600 border-slate-100 dark:bg-slate-800 dark:text-slate-400'
        case TaskStatus.FAILED: return 'bg-red-50 text-red-600 border-red-100 dark:bg-red-900/20 dark:text-red-400'
        case TaskStatus.PROCESSING: return 'bg-blue-50 text-blue-600 border-blue-100 dark:bg-blue-900/20 dark:text-blue-400'
        default: return 'bg-slate-50 text-slate-500 border-slate-100'
    }
}

const formatTimeAgo = (date: string) => {
    const seconds = Math.floor((new Date().getTime() - new Date(date).getTime()) / 1000)
    if (seconds < 60) return 'just now'
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return `${minutes}m ago`
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return `${hours}h ago`
    return new Date(date).toLocaleDateString()
}
</script>

<template>
    <div class="max-w-6xl mx-auto w-full space-y-8 pb-20">
        <!-- Dashboard Header -->
        <div class="flex items-center justify-between">
            <div class="space-y-1">
                <h1 class="text-3xl font-bold tracking-tight text-slate-900 dark:text-white">Overview</h1>
                <div class="flex items-center gap-2">
                    <div class="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></div>
                    <p class="text-xs font-medium text-slate-500 dark:text-slate-400">All systems operational and
                        rendering optimally.</p>
                </div>
            </div>
            <div class="flex items-center gap-3">
                <Button variant="outline"
                    class="h-9 px-4 text-xs font-semibold gap-2 border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 shadow-sm">
                    <Calendar class="w-4 h-4 text-slate-400" />
                    Last 7 Days
                </Button>
            </div>
        </div>

        <!-- Stats Grid -->
        <div class="grid grid-cols-1 md:grid-cols-3 gap-5">
            <!-- Total Tasks Card -->
            <Card
                class="border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden bg-white dark:bg-slate-900 gap-4">
                <CardHeader class="flex justify-between items-start mb-4">
                    <CardTitle class="text-sm font-bold text-slate-400 uppercase tracking-wider">Total Tasks
                    </CardTitle>
                    <RotateCcw class="w-4 h-4 text-slate-300" />
                </CardHeader>
                <CardContent>
                    <div class="flex items-baseline gap-3">
                        <span class="text-4xl font-bold text-slate-900 dark:text-white">{{ stats.COMPLETED +
                            stats.PROCESSING + stats.PENDING + stats.FAILED }}</span>
                        <Badge variant="secondary"
                            class="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-400 border-none font-bold text-[10px] py-0 px-2">
                            +12%</Badge>
                    </div>
                    <!-- Placeholder Chart -->
                    <div class="mt-6 flex items-end gap-1.5 h-10">
                        <div v-for="h in [40, 70, 50, 90, 60, 30, 80]" :key="h"
                            class="flex-1 bg-blue-100 dark:bg-blue-900/30 rounded-t-sm transition-all duration-500 hover:bg-blue-600"
                            :style="{ height: `${h}%` }"></div>
                    </div>
                </CardContent>
            </Card>

            <!-- Storage Usage Card -->
            <Card
                class="border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden bg-white dark:bg-slate-900 gap-4">
                <CardHeader class="flex justify-between items-start mb-4">
                    <CardTitle class="text-sm font-bold text-slate-400 uppercase tracking-wider">Storage Usage
                    </CardTitle>
                    <Database class="w-4 h-4 text-slate-300" />
                </CardHeader>
                <CardContent>
                    <div class="flex items-baseline gap-2">
                        <span class="text-4xl font-bold text-slate-900 dark:text-white">4.2</span>
                        <span class="text-xs font-bold text-slate-400 uppercase">TB / 10 TB</span>
                    </div>
                    <div class="mt-6 space-y-2">
                        <div class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                            <div class="h-full bg-blue-600 rounded-full" style="width: 42%"></div>
                        </div>
                        <div class="flex justify-between text-[10px] font-bold text-slate-400 uppercase">
                            <span>42% Used</span>
                            <span>5.8 TB Free</span>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <!-- Worker Nodes Card -->
            <Card
                class="border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden bg-white dark:bg-slate-900 gap-4">
                <CardHeader class="flex justify-between items-start mb-4">
                    <CardTitle class="text-sm font-bold text-slate-400 uppercase tracking-wider">Worker Nodes
                    </CardTitle>
                    <Cpu class="w-4 h-4 text-slate-300" />
                </CardHeader>
                <CardContent>
                    <div class="flex items-baseline gap-2">
                        <span class="text-4xl font-bold text-slate-900 dark:text-white">12</span>
                        <span class="text-xs font-bold text-slate-400 uppercase">Active</span>
                    </div>
                    <div class="mt-6 flex gap-1.5">
                        <div v-for="i in 12" :key="i"
                            class="w-2.5 h-2.5 rounded-full bg-emerald-500 shadow-sm shadow-emerald-500/20"></div>
                    </div>
                </CardContent>
            </Card>
        </div>

        <!-- Recent Transcoding Section -->
        <Card
            class="border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden rounded-xl bg-white dark:bg-slate-900">
            <CardHeader
                class="flex flex-row items-center justify-between space-y-0 px-6 border-b border-slate-50 dark:border-slate-800">
                <CardTitle class="font-bold text-slate-900 dark:text-white">
                    Recent Transcoding
                </CardTitle>
                <NuxtLink to="/tasks" class="text-xs font-semibold text-blue-600 hover:underline">View All</NuxtLink>
            </CardHeader>
            <CardContent class="p-0">
                <!-- Loading -->
                <div v-if="loading" class="p-20 flex flex-col items-center justify-center space-y-4">
                    <Loader2 class="h-8 w-8 text-blue-600 animate-spin" />
                    <p class="text-xs font-medium text-slate-400 uppercase tracking-widest">Fetching tasks...</p>
                </div>

                <!-- Display Task List -->
                <div v-else-if="tasks && tasks.length > 0" class="divide-y divide-slate-50 dark:divide-slate-800">
                    <div v-for="task in tasks.slice(0, 5)" :key="task.id"
                        class="p-5 flex items-center gap-4 hover:bg-slate-50/50 dark:hover:bg-slate-800/30 transition-all group">
                        <!-- File Icon -->
                        <div
                            class="w-10 h-10 rounded-lg bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-400">
                            <Video v-if="task.status === 'PROCESSING'" class="w-5 h-5 text-blue-500" stroke-width="2" />
                            <FileVideo v-else-if="task.status === 'COMPLETED'" class="w-5 h-5 text-slate-500"
                                stroke-width="2" />
                            <AlertCircle v-else class="w-5 h-5 text-red-500" stroke-width="2" />
                        </div>

                        <!-- Task Details -->
                        <div class="flex-1 min-w-0">
                            <div class="flex items-center gap-2">
                                <span class="text-sm font-bold text-slate-900 dark:text-white truncate">Task-{{
                                    task.id.substring(0, 8) }}</span>
                                <span class="text-[10px] font-medium text-slate-400">{{ new
                                    Date(task.created_at).toLocaleTimeString() }}</span>
                            </div>
                            <div class="flex items-center gap-3 mt-0.5">
                                <span class="text-[10px] font-bold text-slate-400 uppercase tracking-tighter">H.265 / 4K
                                    / 60fps</span>
                                <div class="w-1 h-1 rounded-full bg-slate-300"></div>
                                <span class="text-[10px] font-medium text-slate-400">Started {{
                                    formatTimeAgo(task.created_at) }}</span>
                            </div>
                            <!-- Progress Bar for Processing -->
                            <div v-if="task.status === 'PROCESSING'" class="mt-2 flex items-center gap-3">
                                <div class="flex-1 h-1 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                                    <div class="h-full bg-blue-600 rounded-full animate-progress" style="width: 64%">
                                    </div>
                                </div>
                                <span class="text-[10px] font-black text-blue-600 italic">64%</span>
                            </div>
                        </div>

                        <!-- Status & Actions -->
                        <div class="flex items-center gap-4">
                            <Badge variant="secondary"
                                class="rounded-full font-bold uppercase tracking-widest text-[9px] px-3 py-1 border transition-all"
                                :class="getStatusStyle(task.status)">
                                <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1" stroke-width="3" />
                                <div v-else-if="task.status === 'PROCESSING'"
                                    class="w-1.5 h-1.5 rounded-full bg-blue-500 mr-2 animate-pulse"></div>
                                {{ task.status }}
                            </Badge>
                            <Button variant="ghost" size="icon" class="h-8 w-8 text-slate-400 hover:text-slate-900">
                                <MoreVertical class="w-4 h-4" />
                            </Button>
                        </div>
                    </div>
                </div>

                <!-- Empty State -->
                <div v-else class="p-20 flex flex-col items-center justify-center text-center">
                    <div
                        class="w-16 h-16 rounded-2xl bg-slate-50 dark:bg-slate-800 flex items-center justify-center mb-6">
                        <FileX class="w-8 h-8 text-slate-300" />
                    </div>
                    <h3 class="text-lg font-bold text-slate-900 dark:text-white mb-2">No active tasks</h3>
                    <p class="text-sm text-slate-500 dark:text-slate-400 max-w-xs mx-auto mb-8">
                        You haven't created any transcoding tasks yet. Click the button below to start.
                    </p>
                    <NuxtLink to="/tasks/new">
                        <Button
                            class="bg-slate-900 dark:bg-white text-white dark:text-slate-900 font-bold px-8 h-12 rounded-xl active:scale-95 transition-all">
                            Create First Task
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
