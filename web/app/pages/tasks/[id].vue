<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { apiErrorStatus } from '~/composables/useApi'
import { useTask } from '~/composables/queries/tasks'
import { renderState } from '~/composables/queries/prefetch'
import { useTaskActions, type RetryDrawerPayload } from '~/composables/useTaskActions'
import { useJobOutputs } from '~/composables/useJobOutputs'
import { hasMultiOutputs, isActiveStatus, isOutputStored, taskStatusStyle, totalSavings } from '~/lib/task-detail'
import type { Job, Task } from '~/lib/types/task'
import { toast } from 'vue-sonner'
import { Calendar, ChevronLeft, Clock, Copy, Download, Loader2, Plus, RotateCw, Trash2, X, AlertCircle } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import TaskRetryDrawer from '~/components/TaskRetryDrawer.vue'
import JobErrorDialog from '~/components/JobErrorDialog.vue'
import TaskStatusBanner from '~/components/task/TaskStatusBanner.vue'
import TaskStatCards from '~/components/task/TaskStatCards.vue'
import TaskParamsCard from '~/components/task/TaskParamsCard.vue'
import TaskJobList from '~/components/task/TaskJobList.vue'
import JobOutputPreviewDialog from '~/components/task/JobOutputPreviewDialog.vue'
import TaskConfirmDialog from '~/components/task/TaskConfirmDialog.vue'

useHead({
    title: $t('task_detail.details_title') + ' | Transcoder',
})

const route = useRoute()
const router = useRouter()
const taskId = route.params.id as string

// Polls every 2s while the task runs, stops once it ends and pauses in a
// background tab (see useTask).
const taskQuery = useTask(taskId)
const task = computed<Task | null>(() => taskQuery.data.value ?? null)
const { loading } = renderState(taskQuery)

watch(() => taskQuery.error.value, (err) => {
    if (!err) return
    console.error("Failed to fetch task:", err)
    toast.error($t('task_detail.toast_fetch_failed'))
    if (apiErrorStatus(err) === 404) {
        router.push('/tasks')
    }
})

const {
    isRetrying, isCancelling, isDeleting, retryingJobs,
    cancelTask, deleteTask, retryJobs, retryWithOptions,
} = useTaskActions(taskId)
const {
    downloadingJobs, isDownloadingAll, download, downloadAll,
    previewJob, previewUrl, isPreviewLoading, openPreview, closePreview,
} = useJobOutputs()

const isRefreshing = ref(false)
const refreshTask = async () => {
    isRefreshing.value = true
    await taskQuery.refetch()
    setTimeout(() => {
        isRefreshing.value = false
    }, 400)
}

const savings = computed(() => totalSavings(task.value?.jobs))
const multiOutputs = computed(() => (task.value ? hasMultiOutputs(task.value) : false))
const completedCount = computed(() => task.value?.jobs?.filter(j => j.status === 'COMPLETED').length || 0)
const downloadableCount = computed(() => task.value?.jobs?.filter(isOutputStored).length || 0)
const failedCount = computed(() => task.value?.jobs?.filter(j => j.status === 'FAILED').length || 0)

const { format: formatTime, duration: formatElapsed } = useTime()

// While the task runs the duration counts up to now; afterwards it ends at
// the last update.
const timeDetails = computed(() => {
    if (!task.value) return { started: '', duration: '' }
    const active = isActiveStatus(task.value.status)
    return {
        started: formatTime(task.value.created_at, { dateStyle: 'medium', timeStyle: 'medium' }),
        duration: formatElapsed(task.value.created_at, active ? undefined : task.value.updated_at),
    }
})

const copyUuid = () => {
    if (!task.value?.id) return
    navigator.clipboard.writeText(task.value.id)
    toast.success($t('task_detail.toast_uuid_copied'))
}

// Retry drawer: for one job when opened from its row, otherwise for the task.
const showRetryDrawer = ref(false)
const selectedJobForRetry = ref<string | null>(null)
const effectiveJobForRetry = computed(() => {
    const jobs = task.value?.jobs || []
    if (selectedJobForRetry.value) {
        return jobs.find(j => j.id === selectedJobForRetry.value)
    }
    return jobs.length === 1 ? jobs[0] : undefined
})
const openRetryDrawer = (jobId?: string) => {
    selectedJobForRetry.value = jobId || null
    showRetryDrawer.value = true
}
const handleRetryFromDrawer = async (payload: RetryDrawerPayload) => {
    if (await retryWithOptions(payload)) {
        showRetryDrawer.value = false
        selectedJobForRetry.value = null
    }
}

const showCancelDialog = ref(false)
const handleCancelTask = async () => {
    if (await cancelTask()) showCancelDialog.value = false
}

const showDeleteDialog = ref(false)
const handleDeleteTask = async () => {
    if (await deleteTask()) showDeleteDialog.value = false
}

const selectedJobForErrorModal = ref<Job | null>(null)
const showErrorDialog = ref(false)
const openErrorModal = (job: Job) => {
    selectedJobForErrorModal.value = job
    showErrorDialog.value = true
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
                    v-if="downloadableCount > 0"
                    @click="downloadAll(task?.jobs)"
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

                <!-- Delete Task Button (finished tasks only; active ones are cancelled first) -->
                <button
                    v-if="task && task.status !== 'PROCESSING' && task.status !== 'PENDING'"
                    @click="showDeleteDialog = true"
                    :disabled="isDeleting"
                    class="h-8 px-2.5 rounded-lg border border-hairline bg-surface-1 hover:bg-rose-500/10 hover:border-rose-500/30 text-ink-subtle hover:text-rose-600 dark:hover:text-rose-400 text-xs font-medium flex items-center gap-1.5 transition-all cursor-pointer disabled:opacity-50"
                    :title="$t('task_detail.delete_task')"
                >
                    <Loader2 v-if="isDeleting" class="w-3.5 h-3.5 animate-spin" />
                    <Trash2 v-else class="w-3.5 h-3.5" />
                    <span class="hidden sm:inline">{{ $t('task_detail.delete_task') }}</span>
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
                        <Badge variant="secondary" class="rounded-full font-medium text-xs px-2.5 py-0.5 border capitalize tracking-normal flex items-center gap-1.5" :class="taskStatusStyle(task.status)">
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
                        <span>{{ $t('task_detail.started_at') }} {{ timeDetails.started }}</span>
                    </div>
                    <div class="w-px h-3 bg-hairline-strong hidden sm:block"></div>
                    <div class="flex items-center gap-1.5">
                        <Clock class="w-3.5 h-3.5 text-ink-subtle" />
                        <span>
                            {{ task.status === 'PROCESSING' || task.status === 'PENDING' ? $t('task_detail.elapsed') : $t('task_detail.processed') }}: {{ timeDetails.duration }}
                        </span>
                    </div>
                </div>
            </div>

            <TaskStatusBanner
                :task="task"
                :savings="savings"
                :completed-count="completedCount"
                :failed-count="failedCount"
                :downloadable-count="downloadableCount"
                :is-retrying="isRetrying"
                :is-downloading-all="isDownloadingAll"
                @retry="retryJobs()"
                @tune="openRetryDrawer()"
                @download-all="downloadAll(task.jobs)"
            />

            <TaskStatCards
                :task="task"
                :savings="savings"
                :completed-count="completedCount"
                :duration="timeDetails.duration"
            />

            <TaskParamsCard v-if="task.jobs?.[0]?.options" :task="task" />

            <TaskJobList
                :jobs="task.jobs || []"
                :completed-count="completedCount"
                :failed-count="failedCount"
                :has-multi-outputs="multiOutputs"
                :is-retrying="isRetrying"
                :retrying-jobs="retryingJobs"
                :downloading-jobs="downloadingJobs"
                @preview="openPreview"
                @diagnose="openErrorModal"
                @retry="jobId => retryJobs([jobId])"
                @tune="openRetryDrawer"
                @download="download"
            />
        </div>

        <JobOutputPreviewDialog
            :job="previewJob"
            :url="previewUrl"
            :loading="isPreviewLoading"
            @close="closePreview"
            @download="download"
        />

        <TaskRetryDrawer
            v-model:open="showRetryDrawer"
            :task-id="taskId"
            :target-job-id="effectiveJobForRetry?.id"
            :initial-options="effectiveJobForRetry?.options ?? task?.jobs?.[0]?.options"
            :initial-target="effectiveJobForRetry?.target ?? task?.jobs?.[0]?.target"
            :failed-count="failedCount"
            :is-retrying="isRetrying"
            @submit="handleRetryFromDrawer"
        />

        <TaskConfirmDialog
            v-model:open="showCancelDialog"
            :icon="AlertCircle"
            :title="$t('task_detail.cancel_dialog_title')"
            :description="$t('task_detail.cancel_dialog_desc')"
            :confirm-label="$t('task_detail.confirm_cancel')"
            :pending="isCancelling"
            spinner-only
            @confirm="handleCancelTask"
        />

        <TaskConfirmDialog
            v-model:open="showDeleteDialog"
            :icon="Trash2"
            :title="$t('task_detail.delete_dialog_title')"
            :description="$t('task_detail.delete_dialog_desc')"
            :confirm-label="$t('task_detail.confirm_delete')"
            :pending="isDeleting"
            @confirm="handleDeleteTask"
        />

        <JobErrorDialog
            v-model:open="showErrorDialog"
            :job="selectedJobForErrorModal"
            :is-retrying="isRetrying"
            @retry="(jobId) => retryJobs([jobId])"
            @tune="(jobId) => openRetryDrawer(jobId)"
        />
    </div>
</template>
