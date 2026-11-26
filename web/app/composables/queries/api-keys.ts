import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { useApi } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { prefetchOnServer } from "./prefetch";
import { queryKeys } from "~/lib/query-keys";
import type { ApiResponse } from "~/lib/types/api";
import type { ApiKey, ApiKeyScope, CreateApiKeyData, ListApiKeysData } from "~/lib/types/api-key";

/** The user's API keys, revoked ones included. */
export const useApiKeys = () => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: queryKeys.apiKeys.all,
        // At most 20 keys are active; one page of 100 also covers the revoked ones.
        queryFn: async () =>
            (await api<ApiResponse<ListApiKeysData>>("/api-keys", { query: { limit: 100 } })).data.items,
        enabled: isAuthenticated,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};

/** Create and revoke. The cached list is updated from the response, then refetched. */
export const useApiKeyMutations = () => {
    const api = useApi();
    const queryClient = useQueryClient();
    const setKeys = (update: (keys: ApiKey[]) => ApiKey[]) =>
        queryClient.setQueryData<ApiKey[]>(queryKeys.apiKeys.all, (keys) => update(keys ?? []));
    const refresh = () => queryClient.invalidateQueries({ queryKey: queryKeys.apiKeys.all });

    const create = useMutation({
        mutationFn: async (body: { name: string; scopes: ApiKeyScope[] }) =>
            (await api<ApiResponse<CreateApiKeyData>>("/api-keys", { method: "POST", body })).data,
        onSuccess: (data) => {
            setKeys((keys) => [data.api_key, ...keys]);
            void refresh();
        },
    });

    const revoke = useMutation({
        mutationFn: async (id: string) =>
            (await api<ApiResponse<ApiKey>>(`/api-keys/${id}`, { method: "DELETE" })).data,
        onSuccess: (revoked) => {
            setKeys((keys) => keys.map((k) => (k.id === revoked.id ? revoked : k)));
            void refresh();
        },
    });

    return { create, revoke };
};
