import tailwindcss from "@tailwindcss/vite";

export default defineNuxtConfig({
    compatibilityDate: "2024-04-03",
    srcDir: "app",
    ssr: true,
    devtools: { enabled: true },
    modules: ["shadcn-nuxt"],
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
