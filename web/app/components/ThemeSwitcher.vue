<template>
    <button
        @click="toggleTheme"
        class="group w-full flex items-center gap-2.5 px-2.5 h-8 rounded-md text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 cursor-pointer"
        id="theme-switcher">
        <Sun v-if="colorMode === 'dark'" class="w-4 h-4 shrink-0" stroke-width="1.75" />
        <Moon v-else class="w-4 h-4 shrink-0" stroke-width="1.75" />

        <span class="text-sm font-medium flex-1 text-left truncate">
            {{ colorMode === 'dark' ? $t('common.light_mode') : $t('common.dark_mode') }}
        </span>
    </button>
</template>

<script setup lang="ts">
import { useColorMode } from '@vueuse/core'
import { Sun, Moon } from '@lucide/vue'

const colorMode = useColorMode({
    modes: {
        light: 'light',
        dark: 'dark'
    }
})

const toggleTheme = () => {
    colorMode.value = colorMode.value === 'dark' ? 'light' : 'dark'
}
</script>
