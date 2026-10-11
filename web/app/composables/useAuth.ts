import type { ApiResponse } from "~/lib/types/api";

export interface User {
    id: string;
    username: string;
    email: string;
}

interface LoginData {
    user: User;
    token?: string;
}

interface RegisterData {
    user: User;
    token?: string;
}

export const useAuth = () => {
    const user = useState<User | null>("auth_user", () => null);
    const loading = useState<boolean>("auth_loading", () => false);
    const showAuthModal = useState<boolean>("show_auth_modal", () => false);
    const authToken = useCookie<string | null>("auth_token", { maxAge: 7 * 24 * 60 * 60, path: "/" });

    const isAuthenticated = computed(() => !!user.value);

    const fetchUser = async () => {
        try {
            const headers: Record<string, string> = {};
            if (authToken.value) {
                headers["Authorization"] = `Bearer ${authToken.value}`;
            }
            const response = await $fetch<ApiResponse<User>>("/api/me", { headers });
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
                authToken.value = null;
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
                if (response.data.token) {
                    authToken.value = response.data.token;
                }
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
                if (response.data.token) {
                    authToken.value = response.data.token;
                }
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
            const headers: Record<string, string> = {};
            if (authToken.value) {
                headers["Authorization"] = `Bearer ${authToken.value}`;
            }
            await $fetch("/api/logout", { method: "POST", headers });
        } catch (err) {
            // Ignore error
        } finally {
            user.value = null;
            authToken.value = null;
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
