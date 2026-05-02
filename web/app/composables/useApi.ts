import { useAuth } from "./useAuth";

export const useApi = () => {
    const apiFetch = $fetch.create({
        baseURL: "/api",
        // withCredentials: true, // Note: fetch automatically sends cookies for same-origin by default. If cross-origin, uncomment this.
        async onResponseError({ response }) {
            if (response.status === 401) {
                // Handle unauthorized
                const { logout, showAuthModal } = useAuth();
                logout();
                showAuthModal.value = true;
            }
        },
    });

    return apiFetch;
};
