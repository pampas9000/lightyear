import { useQuery } from "@tanstack/vue-query";
import { useApi } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import { prefetchOnServer } from "./prefetch";
import { queryKeys } from "~/lib/query-keys";
import type { ApiResponse } from "~/lib/types/api";
import type { UsageSummary } from "~/lib/types/usage";

/** This month's usage and quotas. */
export const useUsage = () => {
    const api = useApi();
    const { isAuthenticated } = useAuth();
    const query = useQuery({
        queryKey: queryKeys.usage.all,
        queryFn: async () => (await api<ApiResponse<UsageSummary>>("/usage")).data,
        enabled: isAuthenticated,
    });
    prefetchOnServer(query, isAuthenticated);
    return query;
};
