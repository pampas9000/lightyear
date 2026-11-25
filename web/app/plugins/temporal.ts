// Every date and time in the app is a Temporal value (see
// .agent/rules/code-rules.md). Runtimes with native Temporal use it as is;
// only the others download the polyfill, which `import()` keeps in a chunk
// of its own. The plugin is async and runs first, so Temporal is installed
// before any other plugin, page or component runs, on the server and in
// the browser alike.
//
// temporal-polyfill (fullcalendar) rather than @js-temporal/polyfill: it
// follows the finished spec, is several times smaller (about 20 kB gzipped
// against 50+ kB, no JSBI), and its shim installs Temporal and the
// Temporal-aware Intl.DateTimeFormat the same way a native runtime has them.
export default defineNuxtPlugin({
    name: "temporal",
    enforce: "pre",
    async setup() {
        if (typeof globalThis.Temporal !== "undefined") return;
        const { install } = await import("temporal-polyfill/shim");
        install();
    },
});
