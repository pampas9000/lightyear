import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { useApi } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { prefetchOnServer } from "./prefetch";
import { queryKeys } from "~/lib/query-keys";
import type { ApiResponse, ListData } from "~/lib/types/api";
import type { DraftFile } from "~/lib/types/s3";

/**
 * Uploaded files not yet used by a task. Prefetched on the server like every
 * query a page renders: a server fetch left running could otherwise finish
 * before the cache is dehydrated and hand the browser data the server did
 * not render.
 */
export const useDraftFiles = () => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: queryKeys.drafts.all,
        queryFn: async () => (await api<ApiResponse<ListData<DraftFile>>>("/drafts/list")).data.items,
        enabled: isAuthenticated,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

/** Deletes a draft's stored file and drops it from the cached list. */
export const useDeleteDraft = () => {
    const api = useApi();
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (fileId: string) => {
            await api<ApiResponse<unknown>>(`/drafts/${fileId}/delete`, { method: "POST" });
        },
        onSuccess: (_data, fileId) => {
            queryClient.setQueryData<DraftFile[]>(queryKeys.drafts.all, (files) =>
                files?.filter((f) => f.id !== fileId),
            );
            void queryClient.invalidateQueries({ queryKey: queryKeys.drafts.all });
        },
    });
};
