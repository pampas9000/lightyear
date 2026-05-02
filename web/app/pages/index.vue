<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { useApi } from '~/composables/useApi'
import { ref, onMounted, watch } from 'vue'

const { user, isAuthenticated } = useAuth()
const api = useApi()

useHead({
    title: $t("dashboard.title") + " | Transcoder",
})

const stats = ref({
    PENDING: 0,
    PROCESSING: 0,
    COMPLETED: 0,
    FAILED: 0
})

const tasks = ref<any[] | null>(null)
const loading = ref(true)

const fetchDashboardData = async () => {
    // If not logged in, show empty state
    if (!isAuthenticated.value) {
        loading.value = false
        return
    }

    loading.value = true
    try {
        const [statsRes, tasksRes]: any = await Promise.all([
            api('/tasks/stats'),
            api('/tasks')
        ])

        if (statsRes.success) {
            stats.value = statsRes.data
        }
        if (tasksRes.success) {
            tasks.value = tasksRes.data
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

const getStatusColor = (status: string) => {
    switch (status) {
        case 'COMPLETED': return 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20 border-emerald-100 dark:border-emerald-800'
        case 'FAILED': return 'text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 border-red-100 dark:border-red-800'
        case 'PROCESSING': return 'text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-900/20 border-blue-100 dark:border-blue-800'
        default: return 'text-slate-600 dark:text-slate-400 bg-slate-50 dark:bg-slate-900/20 border-slate-100 dark:border-slate-800'
    }
}
</script>

<template>
    <div class="max-w-300 mx-auto w-full">
        <!-- Header -->
        <div class="mb-10 flex justify-between items-end">
            <div>
                <h1 class="text-3xl font-semibold tracking-tight text-slate-900 dark:text-white mb-2">{{
                    $t('dashboard.title') }}
                </h1>
                <p class="text-sm text-slate-500 dark:text-slate-400">{{ $t('dashboard.subtitle') }}</p>
            </div>
            <div class="hidden sm:block">
                <NuxtLink to="/tasks/new"
                    class="inline-flex items-center justify-center rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 transition-all active:scale-[0.98]">
                    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
                        class="mr-2">
                        <path d="M5 12h14" />
                        <path d="M12 5v14" />
                    </svg>
                    {{ $t('nav.new_task') }}
                </NuxtLink>
            </div>
        </div>

        <!-- Stats Grid -->
        <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 mb-10">
            <div
                class="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-soft border border-slate-100 dark:border-slate-800">
                <div class="text-[11px] font-bold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-2">
                    {{ $t('dashboard.stats.active') }}</div>
                <div class="text-3xl font-bold text-slate-900 dark:text-white">{{ stats.PROCESSING }}</div>
            </div>
            <div
                class="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-soft border border-slate-100 dark:border-slate-800">
                <div class="text-[11px] font-bold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-2">
                    {{ $t('dashboard.stats.queued') }}</div>
                <div class="text-3xl font-bold text-slate-900 dark:text-white">{{ stats.PENDING }}</div>
            </div>
            <div
                class="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-soft border border-slate-100 dark:border-slate-800">
                <div class="text-[11px] font-bold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-2">
                    {{ $t('dashboard.stats.completed') }}</div>
                <div class="text-3xl font-bold text-slate-900 dark:text-white">{{ stats.COMPLETED }}</div>
            </div>
            <div
                class="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-soft border border-slate-100 dark:border-slate-800">
                <div class="text-[11px] font-bold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-2">
                    {{ $t('dashboard.stats.failed') }}</div>
                <div class="text-3xl font-bold text-red-600 dark:text-red-400">{{ stats.FAILED }}</div>
            </div>
        </div>

        <!-- Task Table -->
        <div
            class="bg-white dark:bg-slate-900 rounded-xl shadow-soft border border-slate-100 dark:border-slate-800 overflow-hidden">
            <div
                class="px-6 py-4 border-b border-slate-100 dark:border-slate-800 flex justify-between items-center bg-slate-50/50 dark:bg-slate-900/50">
                <h2 class="text-sm font-bold uppercase tracking-widest text-slate-500 dark:text-slate-400">{{
                    $t('dashboard.recent_tasks') }}
                </h2>
                <NuxtLink to="/tasks"
                    class="text-xs font-bold text-blue-600 dark:text-blue-400 hover:text-blue-700 uppercase tracking-tight">
                    {{ $t('dashboard.view_all') }}</NuxtLink>
            </div>

            <!-- Loading -->
            <div v-if="loading" class="p-12 flex flex-col items-center justify-center space-y-4">
                <svg class="animate-spin h-8 w-8 text-blue-600" xmlns="http://www.w3.org/2000/svg" fill="none"
                    viewBox="0 0 24 24">
                    <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                    <path class="opacity-75" fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z">
                    </path>
                </svg>
                <p class="text-sm font-medium text-slate-500">{{ $t('dashboard.loading_tasks') }}</p>
            </div>

            <!-- Display Task List -->
            <div v-else-if="tasks && tasks.length > 0" class="overflow-x-auto">
                <table class="w-full text-left border-collapse">
                    <thead>
                        <tr
                            class="text-[11px] font-bold text-slate-500 uppercase tracking-wider border-b border-slate-100 dark:border-slate-800">
                            <th class="px-6 py-3">ID</th>
                            <th class="px-6 py-3">{{ $t('common.status') }}</th>
                            <th class="px-6 py-3 text-right">{{ $t('common.items') }}</th>
                            <th class="px-6 py-3 text-right">{{ $t('common.created_at') }}</th>
                        </tr>
                    </thead>
                    <tbody class="divide-y divide-slate-50 dark:divide-slate-800">
                        <tr v-for="task in tasks.slice(0, 10)" :key="task.id"
                            class="hover:bg-slate-50 dark:hover:bg-slate-800/50 transition-colors cursor-pointer group">
                            <td class="px-6 py-4">
                                <div class="flex items-center gap-3">
                                    <div class="w-2 h-2 rounded-full"
                                        :class="task.status === 'COMPLETED' ? 'bg-emerald-500' : task.status === 'FAILED' ? 'bg-red-500' : 'bg-blue-500'">
                                    </div>
                                    <span
                                        class="text-xs font-mono font-medium text-slate-600 dark:text-slate-400 group-hover:text-blue-600 transition-colors">{{
                                            task.id.substring(0, 8) }}...</span>
                                </div>
                            </td>
                            <td class="px-6 py-4">
                                <span
                                    class="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-bold uppercase tracking-tight border"
                                    :class="getStatusColor(task.status)">
                                    {{ $t(`status.${task.status}`) }}
                                </span>
                            </td>
                            <td class="px-6 py-4 text-right">
                                <span class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{
                                    task.jobs?.length || 0 }} files</span>
                            </td>
                            <td class="px-6 py-4 text-right">
                                <span class="text-[11px] font-medium text-slate-500 whitespace-nowrap">{{ new
                                    Date(task.created_at).toLocaleString() }}</span>
                            </td>
                        </tr>
                    </tbody>
                </table>
            </div>

            <!-- Task List is empty -->
            <div v-else class="p-6 text-center py-20">
                <div
                    class="mx-auto w-14 h-14 rounded-2xl bg-slate-50 dark:bg-slate-800 flex items-center justify-center mb-6 text-slate-400 shadow-inner">
                    <svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                        <rect width="18" height="18" x="3" y="4" rx="2" ry="2" />
                        <line x1="16" x2="16" y1="2" y2="6" />
                        <line x1="8" x2="8" y1="2" y2="6" />
                        <line x1="3" x2="21" y1="10" y2="10" />
                        <path d="m9 16 2 2 4-4" />
                    </svg>
                </div>
                <h3 class="text-base font-bold text-slate-900 dark:text-white mb-2">{{ $t('dashboard.no_tasks') }}</h3>
                <p class="text-sm text-slate-500 max-w-xs mx-auto mb-8">{{ $t('dashboard.no_tasks_desc') }}</p>
                <NuxtLink to="/tasks/new"
                    class="inline-flex items-center justify-center rounded-lg bg-blue-600 px-6 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 transition-all active:scale-[0.98]">
                    {{ $t('dashboard.create_first') }}
                </NuxtLink>
            </div>
        </div>
    </div>
</template>
