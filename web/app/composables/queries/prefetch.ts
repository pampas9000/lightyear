import type { MaybeRefOrGetter, Ref } from "vue";

/**
 * Fetches a query while the page renders on the server, so its data is in
 * the HTML and in the dehydrated cache the browser starts from. Call it in
 * setup, next to the useQuery it prefetches. A disabled query is skipped
 * (its suspense would never settle) and a failed fetch is left for the
 * browser to retry.
 */
export const prefetchOnServer = (
    query: { suspense: () => Promise<unknown> },
    enabled: MaybeRefOrGetter<boolean> = true,
) => {
    onServerPrefetch(async () => {
        if (!toValue(enabled)) return;
        await query.suspense().catch(() => undefined);
    });
};

/**
 * The loading flag and error a page should render for a query. dehydrate
 * sends only successful queries, so a fetch that failed on the server reaches
 * the browser as a query still loading, which fetches again. The server
 * renders that failure as loading too, or hydration would not match.
 */
export const renderState = (query: { isLoading: Ref<boolean>; isError: Ref<boolean>; error: Ref<unknown> }) => ({
    loading: computed(() => query.isLoading.value || (import.meta.server && query.isError.value)),
    error: computed(() => (import.meta.server ? null : query.error.value)),
});
