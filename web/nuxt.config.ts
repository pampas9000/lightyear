import tailwindcss from "@tailwindcss/vite";

// The wasm-vips sandbox needs SharedArrayBuffer, which browsers only enable
// for cross-origin isolated pages. `credentialless` (rather than require-corp)
// keeps cross-origin images such as presigned S3 previews loading. It is set
// on every route so client-side navigation into the sandbox stays isolated.
const crossOriginIsolation = {
    "Cross-Origin-Opener-Policy": "same-origin",
    "Cross-Origin-Embedder-Policy": "credentialless",
};

export default defineNuxtConfig({
    compatibilityDate: "2026-05-02",
    srcDir: "app",
    ssr: true,
    devtools: { enabled: true },
    modules: ["shadcn-nuxt", "@nuxtjs/i18n", "@vueuse/nuxt"],
    shadcn: {
        prefix: "",
        componentDir: "./app/components/ui",
    },
    i18n: {
        locales: [
            { code: "en-US", name: "English", file: "en-US.json" },
            { code: "zh-Hans", name: "简体中文", file: "zh-Hans.json" },
            { code: "zh-Hant", name: "繁體中文", file: "zh-Hant.json" },
            { code: "ja-JP", name: "日本語", file: "ja-JP.json" },
            { code: "de-DE", name: "Deutsch", file: "de-DE.json" },
            { code: "fr-FR", name: "Français", file: "fr-FR.json" },
        ],
        langDir: "locales",
        defaultLocale: "en-US",
        strategy: "no_prefix",
    },
    css: ["~/assets/styles/main.css"],
    vite: {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        plugins: [tailwindcss() as any],
        worker: {
            format: "es",
        },
        optimizeDeps: {
            // wasm-vips locates its pthread workers via `import.meta.url`;
            // pre-bundling it into /_nuxt/deps/ breaks those relative URLs.
            exclude: ["wasm-vips"],
        },
        server: {
            headers: crossOriginIsolation,
        },
    },
    typescript: {
        strict: true,
    },
    nitro: {
        routeRules: {
            "/**": { headers: crossOriginIsolation },
            "/api/**": { proxy: "http://localhost:8080/api/**" },
        },
    },
});
