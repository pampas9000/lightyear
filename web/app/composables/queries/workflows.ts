import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { useApi } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { prefetchOnServer } from "./prefetch";
import { queryKeys } from "~/lib/query-keys";
import type { ApiResponse, ListData } from "~/lib/types/api";
import type { Workflow, WorkflowOutput } from "~/lib/types/task";

/** The user's saved option sets (one page of 100). */
export const useWorkflows = () => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: queryKeys.workflows.all,
        queryFn: async () =>
            (await api<ApiResponse<ListData<Workflow>>>("/workflows", { query: { limit: 100 } })).data.items,
        enabled: isAuthenticated,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

interface WorkflowBody {
    name: string;
    outputs: WorkflowOutput[];
}

/** Create, update and delete; each refetches the workflow list before it settles. */
export const useWorkflowMutations = () => {
    const api = useApi();
    const queryClient = useQueryClient();
    const refresh = () => queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all });

    const create = useMutation({
        mutationFn: async (body: WorkflowBody) => {
            await api<ApiResponse<Workflow>>("/workflows", { method: "POST", body });
        },
        onSuccess: refresh,
    });
    const update = useMutation({
        mutationFn: async ({ id, body }: { id: string; body: WorkflowBody }) => {
            await api<ApiResponse<Workflow>>(`/workflows/${id}`, { method: "PUT", body });
        },
        onSuccess: refresh,
    });
    const remove = useMutation({
        mutationFn: async (id: string) => {
            await api<ApiResponse<unknown>>(`/workflows/${id}`, { method: "DELETE" });
        },
        onSuccess: refresh,
    });

    return { create, update, remove };
};
