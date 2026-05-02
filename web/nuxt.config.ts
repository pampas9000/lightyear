import tailwindcss from "@tailwindcss/vite";

export default defineNuxtConfig({
    compatibilityDate: "2026-05-02",
    srcDir: "app",
    ssr: true,
    devtools: { enabled: true },
    modules: ["shadcn-nuxt", "@nuxtjs/i18n"],
    i18n: {
        locales: [
            { code: "en-US", name: "English", file: "en-US.json" },
            { code: "zh-Hans", name: "简体中文", file: "zh-Hans.json" },
        ],
        langDir: "locales",
        defaultLocale: "en-US",
        strategy: "no_prefix",
    },
    css: ["~/assets/styles/main.css"],
    vite: {
        plugins: [tailwindcss()],
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
