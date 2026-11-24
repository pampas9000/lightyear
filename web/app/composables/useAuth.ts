import type { ApiResponse } from "~/lib/types/api";

export interface User {
    id: string;
    username: string;
    email: string;
}

// The session lives only in the HttpOnly `session_id` cookie set by the API;
// responses carry no token and page scripts never handle the session ID.
interface LoginData {
    user: User;
}

interface RegisterData {
    user: User;
}

export const useAuth = () => {
    const user = useState<User | null>("auth_user", () => null);
    const loading = useState<boolean>("auth_loading", () => false);
    const showAuthModal = useState<boolean>("show_auth_modal", () => false);
    // During SSR this forwards the browser's cookies to the API.
    const requestFetch = useRequestFetch();

    const isAuthenticated = computed(() => !!user.value);

    const fetchUser = async () => {
        try {
            const response = await requestFetch<ApiResponse<User>>("/api/me");
            if (response.success && response.data) {
                user.value = response.data;
            } else {
                user.value = null;
            }
        } catch (err: unknown) {
            const fetchErr = err as { status?: number; statusCode?: number };
            const status = fetchErr?.status || fetchErr?.statusCode;
            if (status === 401) {
                user.value = null;
            }
        }
    };

    const login = async (username: string, password: string) => {
        loading.value = true;
        try {
            const response = await $fetch<ApiResponse<LoginData>>("/api/login", {
                method: "POST",
                body: { username, password },
            });

            if (response.success) {
                user.value = response.data.user;
                return { success: true as const };
            }
            return { success: false as const, message: response.message };
        } catch (err: unknown) {
            const message = err instanceof Error ? err.message : "Login failed";
            return { success: false as const, message };
        } finally {
            loading.value = false;
        }
    };

    const register = async (username: string, email: string, password: string) => {
        loading.value = true;
        try {
            const response = await $fetch<ApiResponse<RegisterData>>("/api/register", {
                method: "POST",
                body: { username, email, password },
            });

            if (response.success) {
                user.value = response.data.user;
                return { success: true as const };
            }
            return { success: false as const, message: response.message };
        } catch (err: unknown) {
            const message = err instanceof Error ? err.message : "Registration failed";
            return { success: false as const, message };
        } finally {
            loading.value = false;
        }
    };

    const logout = async () => {
        try {
            await $fetch("/api/logout", { method: "POST" });
        } catch (err) {
            // Ignore error
        } finally {
            user.value = null;
        }
    };

    return {
        user,
        loading,
        showAuthModal,
        isAuthenticated,
        fetchUser,
        login,
        register,
        logout,
    };
};
