import tailwindcss from "@tailwindcss/vite";

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
            // @jsquash/jxl glue resolves its .wasm via `new URL(..., import.meta.url)`;
            // pre-bundling it into /_nuxt/deps/ breaks that path in dev (404 -> invalid wasm magic).
            // Excluding keeps the raw ESM source served so the relative wasm URL is correct.
            exclude: ["@jsquash/jxl", "wasm-feature-detect"],
        },
    },
    typescript: {
        strict: true,
    },
    nitro: {
        routeRules: {
            "/api/**": { proxy: "http://localhost:8080/api/**" },
        },
    },
});
