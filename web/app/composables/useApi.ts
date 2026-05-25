import type { ApiResponse } from "~/lib/types/api";
import { useAuth } from "./useAuth";

/**
 * Typed API composable.
 *
 * Creates a `$fetch` instance pre-configured with the API base URL and
 * automatic 401 handling. All backend endpoints return an `ApiResponse<T>`
 * envelope, so consumers should type the generic as:
 *
 *   api<ApiResponse<MyData>>('/endpoint')
 */
export const useApi = () => {
    const apiFetch = $fetch.create({
        baseURL: "/api",
        async onResponseError({ response }) {
            if (response.status === 401) {
                const { logout, showAuthModal } = useAuth();
                logout();
                showAuthModal.value = true;
            }
        },
    });

    return apiFetch;
};
