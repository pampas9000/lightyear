import {
    QueryClient,
    VueQueryPlugin,
    dehydrate,
    hydrate,
    type DehydratedState,
} from "@tanstack/vue-query";
import { apiErrorStatus } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";

// Server data goes through TanStack Query (.agent/rules/code-rules.md).
// One QueryClient per request on the server and one per tab in the browser.
// Pages prefetch during server rendering (prefetchOnServer); the cache is
// dehydrated into the payload once the app has rendered and hydrated into
// the browser's client before the app is created, the way TanStack's Nuxt
// guide does it.
export default defineNuxtPlugin({
    name: "vue-query",
    setup(nuxtApp) {
        const state = useState<DehydratedState | null>("vue-query", () => null);

        const queryClient = new QueryClient({
            defaultOptions: {
                queries: {
                    // Fresh long enough that hydration and an immediate
                    // revisit do not refetch.
                    staleTime: 5_000,
                    // A 4xx (401, 404, 409) will not change on retry.
                    retry: (failureCount, error) => {
                        const status = apiErrorStatus(error);
                        if (status >= 400 && status < 500) return false;
                        return failureCount < 2;
                    },
                },
            },
        });

        nuxtApp.vueApp.use(VueQueryPlugin, { queryClient });

        if (import.meta.server) {
            nuxtApp.hooks.hook("app:rendered", () => {
                state.value = dehydrate(queryClient);
            });
        }

        if (import.meta.client) {
            if (state.value) hydrate(queryClient, state.value);

            // Cached data belongs to the signed-in user: drop it when they
            // sign out or another user signs in.
            const { user } = useAuth();
            watch(
                () => user.value?.id,
                (id, previous) => {
                    if (previous && id !== previous) queryClient.resetQueries();
                },
            );
        }
    },
});
