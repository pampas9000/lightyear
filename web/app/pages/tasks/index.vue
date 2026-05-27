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
    if (seconds < 60) return 'just now'
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return `${minutes}m ago`
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return `${hours}h ago`
    return new Date(date).toLocaleDateString()
}
</script>

<template>
    <div class="max-w-5xl mx-auto w-full space-y-6 pb-20">
        <!-- Header -->
        <div class="flex items-center justify-between">
            <div class="space-y-1">
                <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('dashboard.title') }}</h1>
                <p class="text-xs font-medium text-ink-subtle">{{ $t('dashboard.subtitle') }}</p>
            </div>
            <div class="flex items-center gap-3">
                <NuxtLink to="/tasks/new">
                    <Button
                        class="bg-primary hover:bg-primary-hover text-primary-foreground rounded-lg shadow-sm border-none transition-all duration-200 gap-1.5 font-medium text-xs h-8 px-3 cursor-pointer">
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
                <Input v-model="searchQuery" type="text" placeholder="Search tasks by ID..."
                    class="w-full pl-7 h-8 border-none bg-transparent shadow-none focus-visible:ring-0 text-xs font-medium" />
            </div>
            <div class="flex gap-2 w-full sm:w-auto pr-2 items-center justify-end">
                <Select v-model="statusFilter">
                    <SelectTrigger
                        class="w-[130px] h-8 bg-transparent border-none shadow-none text-xs font-semibold text-ink-subtle hover:text-ink focus:ring-0 cursor-pointer">
                        <Filter class="w-3.5 h-3.5 mr-1.5" />
                        <SelectValue placeholder="Status" />
                    </SelectTrigger>
                    <SelectContent class="bg-surface-1 border-hairline rounded-lg">
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
                <div class="w-px h-3 bg-hairline mx-1"></div>
                <Button variant="ghost" class="h-8 text-xs font-medium text-ink-subtle hover:text-ink hover:bg-surface-2 gap-1.5 cursor-pointer">
                    <Calendar class="w-3.5 h-3.5" />
                    Date
                </Button>
            </div>
        </div>

        <!-- Tasks List -->
        <Card
            class="border-hairline shadow-sm overflow-hidden rounded-xl bg-surface-1 py-0 gap-0">
            <CardContent class="p-0">
                <!-- Loading -->
                <div v-if="loading" class="p-16 flex flex-col items-center justify-center space-y-3">
                    <Loader2 class="h-6 w-6 text-primary animate-spin" />
                    <p class="text-[10px] font-medium text-ink-subtle uppercase tracking-wider">{{ $t('dashboard.loading_tasks') }}</p>
                </div>

                <!-- Display Task List -->
                <div v-else-if="tasks && tasks.length > 0" class="flex flex-col h-full">
                    <div class="divide-y divide-hairline">
                        <div v-for="task in tasks" :key="task.id"
                            class="px-5 py-3 flex items-center gap-4 hover:bg-surface-2 transition-all duration-200 group">
                            <!-- File Icon -->
                            <div
                                class="w-8 h-8 rounded-lg bg-surface-2 flex items-center justify-center text-ink-subtle border border-hairline shrink-0">
                                <Video v-if="task.status === 'PROCESSING'" class="w-4 h-4 text-primary"
                                    stroke-width="1.5" />
                                <FileVideo v-else-if="task.status === 'COMPLETED'" class="w-4 h-4 text-ink-subtle"
                                    stroke-width="1.5" />
                                <AlertCircle v-else class="w-4 h-4 text-red-500" stroke-width="1.5" />
                            </div>

                            <!-- Task Details -->
                            <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-2">
                                    <span class="text-xs font-semibold text-ink truncate">Task-{{
                                        task.id.substring(0, 8) }}</span>
                                    <span class="text-[9px] font-medium text-ink-subtle hidden sm:inline-block">{{ new
                                        Date(task.created_at).toLocaleTimeString([], {
                                            hour: '2-digit', minute: '2-digit' }) }}</span>
                                </div>
                                <div class="flex items-center gap-2 mt-0.5">
                                    <span class="text-[10px] font-medium text-ink-subtle uppercase tracking-wider">H.265 / 4K</span>
                                    <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                    <span class="text-[10px] font-medium text-ink-subtle">{{ task.jobs?.length || 0 }}
                                        files</span>
                                    <div class="w-1 h-1 rounded-full bg-hairline-strong"></div>
                                    <span class="text-[10px] font-medium text-ink-subtle">Started {{
                                        formatTimeAgo(task.created_at) }}</span>
                                </div>
                                <!-- Progress Bar for Processing -->
                                <div v-if="task.status === 'PROCESSING'" class="mt-1.5 flex items-center gap-2 max-w-md pr-4">
                                    <div
                                        class="flex-1 h-1 bg-surface-2 rounded-full overflow-hidden">
                                        <div class="h-full bg-primary rounded-full animate-progress"
                                            style="width: 64%">
                                        </div>
                                    </div>
                                    <span class="text-[9px] font-semibold text-primary shrink-0">64%</span>
                                </div>
                            </div>

                            <!-- Status & Actions -->
                            <div class="flex items-center gap-2 sm:gap-3 shrink-0">
                                <Badge variant="secondary"
                                    class="rounded-full font-medium text-xs px-2.5 py-0.5 border capitalize tracking-normal shadow-sm transition-all hidden sm:flex"
                                    :class="getStatusStyle(task.status)">
                                    <Check v-if="task.status === 'COMPLETED'" class="w-3 h-3 mr-1 text-emerald-600 dark:text-emerald-400"
                                        stroke-width="2" />
                                    <div v-else-if="task.status === 'PROCESSING'"
                                        class="w-1.5 h-1.5 rounded-full bg-primary mr-1.5 animate-pulse"></div>
                                    <Clock v-else-if="task.status === 'PENDING'" class="w-3 h-3 mr-1"
                                        stroke-width="2" />
                                    <AlertCircle v-else-if="task.status === 'FAILED'" class="w-3.5 h-3.5 mr-1"
                                        stroke-width="2" />
                                    {{ task.status.toLowerCase() }}
                                </Badge>
                                <Button variant="ghost" size="icon" class="h-7 w-7 text-ink-subtle hover:text-ink hover:bg-surface-2 rounded-lg cursor-pointer">
                                    <MoreVertical class="w-3.5 h-3.5" />
                                </Button>
                            </div>
                        </div>
                    </div>

                    <!-- Pagination Footer -->
                    <div
                        class="px-5 py-3 border-t border-hairline flex items-center justify-between bg-surface-1">
                        <div class="text-[11px] font-medium text-ink-subtle">
                            Showing {{ (currentPage - 1) * itemsPerPage + 1 }} to {{ Math.min(currentPage *
                            itemsPerPage, totalTasks) }} of {{ totalTasks }} tasks
                        </div>
                        <Pagination v-slot="{ page }" :total="totalTasks" :sibling-count="1" show-edges
                            :default-page="1" :items-per-page="itemsPerPage" v-model:page="currentPage">
                            <PaginationList v-slot="{ items }" class="flex items-center gap-1">
                                <PaginationFirst class="w-7 h-7 rounded-lg cursor-pointer">
                                    <template #default>
                                        <ChevronsLeft class="w-3.5 h-3.5" />
                                    </template>
                                </PaginationFirst>
                                <PaginationPrev class="w-7 h-7 rounded-lg cursor-pointer">
                                    <template #default>
                                        <ChevronLeft class="w-3.5 h-3.5" />
                                    </template>
                                </PaginationPrev>
                                <template v-for="(item, index) in items">
                                    <PaginationListItem v-if="item.type === 'page'" :key="index" :value="item.value"
                                        as-child>
                                        <Button class="w-7 h-7 p-0 rounded-lg text-xs font-semibold transition-all cursor-pointer"
                                            :variant="item.value === currentPage ? 'default' : 'ghost'">
                                            {{ item.value }}
                                        </Button>
                                    </PaginationListItem>
                                    <PaginationEllipsis v-else :key="item.type" :index="index"
                                        class="w-7 h-7 flex items-center justify-center text-ink-subtle" />
                                </template>
                                <PaginationNext class="w-7 h-7 rounded-lg cursor-pointer">
                                    <template #default>
                                        <ChevronRight class="w-3.5 h-3.5" />
                                    </template>
                                </PaginationNext>
                                <PaginationLast class="w-7 h-7 rounded-lg cursor-pointer">
                                    <template #default>
                                        <ChevronsRight class="w-3.5 h-3.5" />
                                    </template>
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
                    <h3 class="text-sm font-semibold text-ink mb-1">{{ $t('dashboard.no_tasks') }}</h3>
                    <p class="text-xs text-ink-subtle max-w-xs mx-auto mb-6">
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
