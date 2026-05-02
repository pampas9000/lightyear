interface User {
    id: string;
    username: string;
    email: string;
}

export const useAuth = () => {
    const user = useState<User | null>("auth_user", () => null);
    const loading = useState<boolean>("auth_loading", () => false);
    const showAuthModal = useState<boolean>("show_auth_modal", () => false);

    const isAuthenticated = computed(() => !!user.value);

    const fetchUser = async () => {
        try {
            const response = await $fetch<any>("/api/me");
            if (response.success) {
                user.value = response.data;
            } else {
                user.value = null;
            }
        } catch (err) {
            user.value = null;
        }
    };

    const login = async (username: string, password: string) => {
        loading.value = true;
        try {
            const response = await $fetch<any>("/api/login", {
                method: "POST",
                body: { username, password },
            });

            if (response.success) {
                user.value = response.data.user;
                // Optionally save response.data.token if needed for cross-origin, but we rely on cookie.
                return { success: true };
            }
            return { success: false, message: response.message };
        } catch (err: any) {
            return { success: false, message: err.data?.message || "Login failed" };
        } finally {
            loading.value = false;
        }
    };

    const register = async (username: string, email: string, password: string) => {
        loading.value = true;
        try {
            const response = await $fetch<any>("/api/register", {
                method: "POST",
                body: { username, email, password },
            });

            if (response.success) {
                user.value = response.data.user;
                return { success: true };
            }
            return { success: false, message: response.message };
        } catch (err: any) {
            return { success: false, message: err.data?.message || "Registration failed" };
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
