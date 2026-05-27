<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { useApi } from '~/composables/useApi'
import { ref, onMounted, watch } from 'vue'
import type { Task, TaskStatus, ListTasksData } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import {
    Activity, Clock, CheckCircle2, AlertCircle,
    Plus, Loader2, Calendar, RotateCcw,
    Database, Cpu, Video, FileVideo,
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
    PaginationListItem,
    PaginationLast,
    PaginationList,
    PaginationNext,
    PaginationPrev,
} from '@/components/ui/pagination'

useHead({
    title: "Tasks | Transcoder",
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
}, { immediate: true })

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
        case 'COMPLETED': return 'bg-emerald-50 text-emerald-600 border-emerald-100 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20'
        case 'FAILED': return 'bg-red-50 text-red-600 border-red-100 dark:bg-red-500/10 dark:text-red-400 dark:border-red-500/20'
        case 'PARTIALLY_FAILED': return 'bg-orange-50 text-orange-600 border-orange-100 dark:bg-orange-500/10 dark:text-orange-400 dark:border-orange-500/20'
        case 'PROCESSING': return 'bg-blue-50 text-blue-600 border-blue-100 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20'
        case 'PENDING': return 'bg-amber-50 text-amber-600 border-amber-100 dark:bg-amber-500/10 dark:text-amber-400 dark:border-amber-500/20'
        default: return 'bg-slate-50 text-slate-500 border-slate-100 dark:bg-slate-800 dark:text-slate-400 dark:border-slate-700'
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
        <!-- Header -->
        <div class="flex items-center justify-between">
            <div class="space-y-1">
                <h1 class="text-3xl font-bold tracking-tight text-slate-900 dark:text-white">Task History</h1>
                <p class="text-xs font-medium text-slate-500 dark:text-slate-400">View and manage your entire
                    transcoding pipeline.</p>
            </div>
            <div class="flex items-center gap-3">
                <NuxtLink to="/tasks/new">
                    <Button
                        class="bg-blue-600 hover:bg-blue-700 text-white rounded-lg shadow-lg shadow-blue-600/20 border-none transition-all duration-300 gap-2 font-semibold text-xs h-9 px-4">
                        <Plus class="w-4 h-4" stroke-width="3" />
                        New Task
                    </Button>
                </NuxtLink>
            </div>
        </div>

        <!-- Filters & Search -->
        <div
            class="flex flex-col sm:flex-row gap-4 justify-between items-center bg-white dark:bg-slate-900 p-2 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm">
            <div class="flex justify-center items-center w-full sm:w-96 pl-2">
                <Search class="w-4 h-4 text-slate-400" />
                <Input v-model="searchQuery" type="text" placeholder="Search tasks by ID or format..."
                    class="w-full pl-10 h-10 border-none bg-transparent shadow-none focus-visible:ring-0 text-sm font-medium" />
            </div>
            <div class="flex gap-2 w-full sm:w-auto pr-2 items-center">
                <Select v-model="statusFilter">
                    <SelectTrigger
                        class="w-[140px] h-10 bg-transparent border-none shadow-none text-xs font-semibold text-slate-500 focus:ring-0">
                        <Filter class="w-4 h-4 mr-2" />
                        <SelectValue placeholder="Status" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectGroup>
                            <SelectItem value="ALL" class="text-xs font-medium">All Statuses</SelectItem>
                            <SelectItem value="PENDING" class="text-xs font-medium">Pending</SelectItem>
                            <SelectItem value="PROCESSING" class="text-xs font-medium">Processing</SelectItem>
                            <SelectItem value="COMPLETED" class="text-xs font-medium">Completed</SelectItem>
                            <SelectItem value="FAILED" class="text-xs font-medium">Failed</SelectItem>
                            <SelectItem value="PARTIALLY_FAILED" class="text-xs font-medium">Partially Failed</SelectItem>
                        </SelectGroup>
                    </SelectContent>
                </Select>
                <div class="w-px h-4 bg-slate-200 dark:bg-slate-800 mx-1"></div>
                <Button variant="ghost" class="h-10 text-xs font-semibold text-slate-500 gap-2">
                    <Calendar class="w-4 h-4" />
                    Date
                </Button>
            </div>
        </div>

        <!-- Tasks List -->
        <Card
            class="border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden rounded-xl bg-white dark:bg-slate-900">
            <CardContent class="p-0">
                <!-- Loading -->
                <div v-if="loading" class="p-20 flex flex-col items-center justify-center space-y-4">
                    <Loader2 class="h-8 w-8 text-blue-600 animate-spin" />
                    <p class="text-xs font-medium text-slate-400 uppercase tracking-widest">Loading task history...</p>
                </div>

                <!-- Display Task List -->
                <div v-else-if="tasks && tasks.length > 0" class="flex flex-col h-full">
                    <div class="divide-y divide-slate-50 dark:divide-slate-800">
                        <div v-for="task in tasks" :key="task.id"
                            class="p-5 flex items-center gap-4 hover:bg-slate-50/50 dark:hover:bg-slate-800/30 transition-all group">
                            <!-- File Icon -->
                            <div
                                class="w-10 h-10 rounded-lg bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-400 shrink-0">
                                <Video v-if="task.status === 'PROCESSING'" class="w-5 h-5 text-blue-500"
                                    stroke-width="2" />
                                <FileVideo v-else-if="task.status === 'COMPLETED'" class="w-5 h-5 text-slate-500"
                                    stroke-width="2" />
                                <AlertCircle v-else class="w-5 h-5 text-red-500" stroke-width="2" />
                            </div>

                            <!-- Task Details -->
                            <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-2">
                                    <span class="text-sm font-bold text-slate-900 dark:text-white truncate">Task-{{
                                        task.id.substring(0, 8) }}</span>
                                    <span class="text-xs font-medium text-slate-400 hidden sm:inline-block">{{ new
                                        Date(task.created_at).toLocaleTimeString([], {
                                            hour: '2-digit', minute:
                                        '2-digit' }) }}</span>
                                </div>
                                <div class="flex items-center gap-3 mt-1">
                                    <span class="text-xs font-bold text-slate-500 uppercase tracking-tight">H.265 / 4K
                                        / 60fps</span>
                                    <div class="w-1 h-1 rounded-full bg-slate-300"></div>
                                    <span class="text-xs font-medium text-slate-500">{{ task.jobs?.length || 0 }}
                                        files</span>
                                    <div class="w-1 h-1 rounded-full bg-slate-300"></div>
                                    <span class="text-xs font-medium text-slate-400">Started {{
                                        formatTimeAgo(task.created_at) }}</span>
                                </div>
                                <!-- Progress Bar for Processing -->
                                <div v-if="task.status === 'PROCESSING'" class="mt-2.5 flex items-center gap-3 pr-4">
                                    <div
                                        class="flex-1 h-1.5 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                                        <div class="h-full bg-blue-600 rounded-full animate-progress"
                                            style="width: 64%">
                                        </div>
                                    </div>
                                    <span class="text-xs font-bold text-blue-600 italic shrink-0">64%</span>
                                </div>
                            </div>

                            <!-- Status & Actions -->
                            <div class="flex items-center gap-2 sm:gap-4 shrink-0">
                                <Badge variant="secondary"
                                    class="rounded-full font-bold uppercase tracking-widest text-[10px] px-3 py-1 border transition-all hidden sm:flex"
                                    :class="getStatusStyle(task.status)">
                                    <Check v-if="task.status === 'COMPLETED'" class="w-3.5 h-3.5 mr-1"
                                        stroke-width="3" />
                                    <div v-else-if="task.status === 'PROCESSING'"
                                        class="w-2 h-2 rounded-full bg-blue-500 mr-2 animate-pulse"></div>
                                    <Clock v-else-if="task.status === 'PENDING'" class="w-3.5 h-3.5 mr-1"
                                        stroke-width="3" />
                                    <AlertCircle v-else-if="task.status === 'FAILED'" class="w-3.5 h-3.5 mr-1"
                                        stroke-width="3" />
                                    {{ task.status }}
                                </Badge>
                                <Button variant="ghost" size="icon" class="h-8 w-8 text-slate-400 hover:text-slate-900">
                                    <MoreVertical class="w-4 h-4" />
                                </Button>
                            </div>
                        </div>
                    </div>

                    <!-- Pagination Footer -->
                    <div
                        class="px-6 py-4 border-t border-slate-50 dark:border-slate-800 flex items-center justify-between bg-slate-50/50 dark:bg-slate-900/50">
                        <div class="text-xs font-medium text-slate-500">
                            Showing {{ (currentPage - 1) * itemsPerPage + 1 }} to {{ Math.min(currentPage *
                            itemsPerPage, totalTasks) }} of {{ totalTasks }} tasks
                        </div>
                        <Pagination v-slot="{ page }" :total="totalTasks" :sibling-count="1" show-edges
                            :default-page="1" :items-per-page="itemsPerPage" v-model:page="currentPage">
                            <PaginationList v-slot="{ items }" class="flex items-center gap-1">
                                <PaginationFirst class="w-8 h-8 rounded-lg">
                                    <template #default>
                                        <ChevronsLeft class="w-4 h-4" />
                                    </template>
                                </PaginationFirst>
                                <PaginationPrev class="w-8 h-8 rounded-lg">
                                    <template #default>
                                        <ChevronLeft class="w-4 h-4" />
                                    </template>
                                </PaginationPrev>
                                <template v-for="(item, index) in items">
                                    <PaginationListItem v-if="item.type === 'page'" :key="index" :value="item.value"
                                        as-child>
                                        <Button class="w-8 h-8 p-0 rounded-lg text-sm font-bold transition-all"
                                            :variant="item.value === currentPage ? 'default' : 'ghost'">
                                            {{ item.value }}
                                        </Button>
                                    </PaginationListItem>
                                    <PaginationEllipsis v-else :key="item.type" :index="index"
                                        class="w-8 h-8 flex items-center justify-center text-slate-400" />
                                </template>
                                <PaginationNext class="w-8 h-8 rounded-lg">
                                    <template #default>
                                        <ChevronRight class="w-4 h-4" />
                                    </template>
                                </PaginationNext>
                                <PaginationLast class="w-8 h-8 rounded-lg">
                                    <template #default>
                                        <ChevronsRight class="w-4 h-4" />
                                    </template>
                                </PaginationLast>
                            </PaginationList>
                        </Pagination>
                    </div>
                </div>

                <!-- Empty State -->
                <div v-else class="p-20 flex flex-col items-center justify-center text-center">
                    <div
                        class="w-16 h-16 rounded-2xl bg-slate-50 dark:bg-slate-800 flex items-center justify-center mb-6">
                        <FileX class="w-8 h-8 text-slate-300" />
                    </div>
                    <h3 class="text-lg font-bold text-slate-900 dark:text-white mb-2">No tasks found</h3>
                    <p class="text-sm text-slate-500 dark:text-slate-400 max-w-xs mx-auto mb-8">
                        You don't have any task history matching your criteria.
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
