import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import type { MaybeRefOrGetter } from "vue";
import { useApi } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { prefetchOnServer } from "./prefetch";
import { queryKeys, type TaskListParams } from "~/lib/query-keys";
import type { ApiResponse } from "~/lib/types/api";
import type { CompressionTarget, ListTasksData, Task, TaskStats } from "~/lib/types/task";
import type { ImageOptions } from "~/lib/types/image-options";

/** How often a running task's detail is polled. */
export const TASK_POLL_MS = 2000;

/** PENDING and PROCESSING tasks still change; the others are final. */
export const isTaskActive = (status: string | undefined) => status === "PENDING" || status === "PROCESSING";

/** One page of the signed-in user's tasks, newest first. */
export const useTaskList = (params: MaybeRefOrGetter<TaskListParams>) => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: computed(() => queryKeys.tasks.list(toValue(params))),
        queryFn: async () => {
            const { page, limit, status } = toValue(params);
            const res = await api<ApiResponse<ListTasksData>>("/tasks", {
                query: { page, limit, ...(status ? { status } : {}) },
            });
            return res.data;
        },
        enabled: isAuthenticated,
        // Keep the previous page on screen (and its total) while the next loads.
        placeholderData: keepPreviousData,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

/** Task counts, storage and the 7-day chart for the overview. */
export const useTaskStats = () => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: queryKeys.tasks.stats(),
        queryFn: async () => (await api<ApiResponse<TaskStats>>("/tasks/stats")).data,
        enabled: isAuthenticated,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

/**
 * One task with its jobs. While the task is PENDING or PROCESSING it is
 * polled every TASK_POLL_MS; polling stops once it ends and pauses while
 * the tab is in the background (refetchIntervalInBackground is false).
 */
export const useTask = (id: MaybeRefOrGetter<string>) => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: computed(() => queryKeys.tasks.detail(toValue(id))),
        queryFn: async () => (await api<ApiResponse<Task>>(`/tasks/${toValue(id)}`)).data,
        enabled: isAuthenticated,
        refetchInterval: (q) => (isTaskActive(q.state.data?.status) ? TASK_POLL_MS : false),
        refetchIntervalInBackground: false,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

export interface RetryTaskBody {
    job_ids?: string[];
    retry_all?: boolean;
    options?: ImageOptions;
    target?: CompressionTarget | null;
}

/**
 * Retry, cancel and delete. Each success updates the task's cached detail
 * and invalidates the lists and stats, which share its data, and usage,
 * which retries and cancellations change.
 */
export const useTaskMutations = () => {
    const api = useApi();
    const queryClient = useQueryClient();

    const invalidateSummaries = () =>
        Promise.all([
            queryClient.invalidateQueries({ queryKey: queryKeys.tasks.lists() }),
            queryClient.invalidateQueries({ queryKey: queryKeys.tasks.stats() }),
            queryClient.invalidateQueries({ queryKey: queryKeys.usage.all }),
        ]);

    const onTaskChanged = (task: Task) => {
        queryClient.setQueryData(queryKeys.tasks.detail(task.id), task);
        void invalidateSummaries();
    };

    const retry = useMutation({
        mutationFn: async ({ taskId, body }: { taskId: string; body: RetryTaskBody }) =>
            (await api<ApiResponse<Task>>(`/tasks/${taskId}/retry`, { method: "POST", body })).data,
        onSuccess: onTaskChanged,
    });

    const cancel = useMutation({
        mutationFn: async (taskId: string) =>
            (await api<ApiResponse<Task>>(`/tasks/${taskId}/cancel`, { method: "POST" })).data,
        onSuccess: onTaskChanged,
    });

    // Only finished tasks can be deleted; the API answers 409 for active ones.
    const remove = useMutation({
        mutationFn: async (taskId: string) => {
            await api<ApiResponse<unknown>>(`/tasks/${taskId}`, { method: "DELETE" });
        },
        onSuccess: (_data, taskId) => {
            queryClient.removeQueries({ queryKey: queryKeys.tasks.detail(taskId), exact: true });
            void invalidateSummaries();
        },
    });

    return { retry, cancel, remove };
};

/** Creates a task from uploaded drafts; the drafts it used leave the draft list. */
export const useCreateTask = () => {
    const api = useApi();
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (body: Record<string, unknown>) =>
            (await api<ApiResponse<Task>>("/tasks", { method: "POST", body })).data,
        onSuccess: (task) => {
            queryClient.setQueryData(queryKeys.tasks.detail(task.id), task);
            void Promise.all([
                queryClient.invalidateQueries({ queryKey: queryKeys.tasks.lists() }),
                queryClient.invalidateQueries({ queryKey: queryKeys.tasks.stats() }),
                queryClient.invalidateQueries({ queryKey: queryKeys.drafts.all }),
                queryClient.invalidateQueries({ queryKey: queryKeys.usage.all }),
            ]);
        },
    });
};
