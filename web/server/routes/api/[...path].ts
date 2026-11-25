// Proxies /api/** to the Go API at the origin runtimeConfig.apiBase (NUXT_API_BASE,
// default http://localhost:8080), read per request so a deployment sets it
// without rebuilding. Nitro route rules are fixed at build time, so this is
// a server route rather than a `proxy` route rule.
//
// proxyRequest forwards the browser's headers, Origin included, unchanged:
// the API checks Origin on state-changing requests. `redirect: "manual"`
// passes API redirects (the OAuth callback's 303 to FRONTEND_URL) and their
// Set-Cookie through to the browser instead of following them server-side.
export default defineEventHandler((event) => {
    const base = new URL(useRuntimeConfig(event).apiBase);
    const target = new URL(event.path, base);
    // event.path is resolved like a URL, so "/api/../v1" would leave /api;
    // only /api itself is proxied.
    if (target.origin !== base.origin || !(target.pathname === "/api" || target.pathname.startsWith("/api/"))) {
        throw createError({ statusCode: 400, statusMessage: "Bad Request" });
    }
    return proxyRequest(event, target.toString(), {
        fetchOptions: { redirect: "manual" },
    });
});
