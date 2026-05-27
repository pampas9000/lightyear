<template>
    <button
        @click="toggleTheme"
        class="group w-full flex items-center gap-3 px-3 py-2 rounded-xl text-ink-subtle hover:text-ink hover:bg-surface-2 transition-all duration-300 outline-none focus-visible:ring-2 focus-visible:ring-primary/20 cursor-pointer"
        id="theme-switcher">
        <div class="w-5 h-5 flex items-center justify-center shrink-0">
            <Sun v-if="colorMode === 'dark'" class="w-4 h-4 opacity-60 group-hover:opacity-100 transition-opacity" stroke-width="1.5" />
            <Moon v-else class="w-4 h-4 opacity-60 group-hover:opacity-100 transition-opacity" stroke-width="1.5" />
        </div>

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
