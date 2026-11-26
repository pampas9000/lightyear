import { toast } from "vue-sonner";
import { useTaskMutations, type RetryTaskBody } from "~/composables/queries/tasks";
import type { CompressionTarget } from "~/lib/types/task";
import type { ImageOptions } from "~/lib/types/image-options";

export interface RetryDrawerPayload {
    options?: ImageOptions;
    target?: CompressionTarget | null;
    retryAll: boolean;
    jobIds?: string[];
}

/**
 * Cancel, delete and retry for one task, with their toasts. A successful
 * cancel or retry puts the returned task in the cache, which starts or stops
 * polling by its status, and refreshes lists and stats. Each action resolves
 * to whether it succeeded.
 */
export const useTaskActions = (taskId: string) => {
    const { t } = useI18n();
    const router = useRouter();
    const { retry, cancel, remove } = useTaskMutations();

    const isRetrying = computed(() => retry.isPending.value);
    const isCancelling = computed(() => cancel.isPending.value);
    const isDeleting = computed(() => remove.isPending.value);
    /** The jobs of the retry in flight, by id. */
    const retryingJobs = computed<Record<string, boolean>>(() => {
        if (!retry.isPending.value) return {};
        const ids = retry.variables.value?.body.job_ids ?? [];
        return Object.fromEntries(ids.map((id) => [id, true]));
    });

    const cancelTask = async () => {
        try {
            await cancel.mutateAsync(taskId);
            toast.success(t("task_detail.toast_cancel_success"));
            return true;
        } catch (err: any) {
            console.error("Failed to cancel task:", err);
            toast.error(err.data?.message || t("task_detail.toast_cancel_failed"));
            return false;
        }
    };

    // Only finished tasks can be deleted; the API answers 409 for active ones.
    const deleteTask = async () => {
        try {
            await remove.mutateAsync(taskId);
            toast.success(t("task_detail.toast_delete_success"));
            router.push("/tasks");
            return true;
        } catch (err: any) {
            console.error("Failed to delete task:", err);
            toast.error(err.data?.message || t("task_detail.toast_delete_failed"));
            return false;
        }
    };

    /** Retries the given jobs with their options, or every failed job when none are given. */
    const retryJobs = async (jobIds?: string[]) => {
        const body: RetryTaskBody = {};
        if (jobIds && jobIds.length > 0) {
            body.job_ids = jobIds;
        }
        try {
            await retry.mutateAsync({ taskId, body });
            if (jobIds && jobIds.length === 1) {
                toast.success(t("task_detail.toast_retry_single_success"));
            } else {
                toast.success(t("task_detail.toast_retry_all_success"));
            }
            return true;
        } catch (err: any) {
            console.error("Failed to retry task:", err);
            toast.error(err.data?.message || t("task_detail.toast_retry_failed"));
            return false;
        }
    };

    /** Retries with options changed in TaskRetryDrawer. */
    const retryWithOptions = async (payload: RetryDrawerPayload) => {
        const body: RetryTaskBody = { retry_all: payload.retryAll };
        if (payload.options) body.options = payload.options;
        if (payload.target !== undefined) body.target = payload.target;
        if (payload.jobIds && payload.jobIds.length > 0) {
            body.job_ids = payload.jobIds;
            body.retry_all = false;
        }
        try {
            await retry.mutateAsync({ taskId, body });
            toast.success(t("task_detail.toast_retry_success"));
            return true;
        } catch (err: any) {
            console.error("Failed to retry task:", err);
            toast.error(err.data?.message || t("task_detail.toast_retry_failed"));
            return false;
        }
    };

    return {
        isRetrying,
        isCancelling,
        isDeleting,
        retryingJobs,
        cancelTask,
        deleteTask,
        retryJobs,
        retryWithOptions,
    };
};
