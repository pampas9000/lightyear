<template>
    <DropdownMenu>
        <DropdownMenuTrigger as-child>
            <button
                class="group w-full flex items-center gap-3 px-3 py-2 rounded-xl text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800/50 transition-all duration-300 outline-none focus-visible:ring-2 focus-visible:ring-blue-500/20">
                <div class="w-5 h-5 flex items-center justify-center shrink-0">
                    <Globe class="w-4 h-4 opacity-60 group-hover:opacity-100 transition-opacity" />
                </div>

                <span class="text-sm font-medium flex-1 text-left truncate">
                    {{ currentLocaleName }}
                </span>

                <ChevronDown
                    class="w-3.5 h-3.5 opacity-30 group-data-[state=open]:rotate-180 group-data-[state=open]:opacity-100 transition-all duration-300" />
            </button>
        </DropdownMenuTrigger>

        <DropdownMenuContent align="start" :side-offset="8"
            class="w-52 p-1 bg-white/95 dark:bg-slate-900/95 backdrop-blur-xl border-slate-200/50 dark:border-slate-800/50 shadow-2xl rounded-2xl">
            <div class="px-3 py-2 text-[10px] font-bold uppercase tracking-[0.2em] text-slate-400 dark:text-slate-500">
                {{ $t('nav.select_language') }}
            </div>
            <DropdownMenuSeparator class="mx-1 my-1 opacity-50" />
            <DropdownMenuRadioGroup :model-value="currentLocale" @update:model-value="handleLocaleChange">
                <DropdownMenuRadioItem v-for="locale in locales" :key="locale.code" :value="locale.code"
                    class="group flex items-center justify-between py-2.5 pl-9 pr-3 rounded-xl cursor-pointer transition-all duration-200 focus:bg-blue-50 dark:focus:bg-blue-500/10 focus:text-blue-600 dark:focus:text-blue-400 data-[state=checked]:text-blue-600 dark:data-[state=checked]:text-blue-400">
                    <template #indicator-icon>
                        <Check class="size-3.5 stroke-3" />
                    </template>
                    <span class="text-sm font-medium">{{ locale.name }}</span>
                    <span
                        class="text-[10px] font-mono font-bold opacity-20 group-hover:opacity-40 transition-opacity">{{
                            locale.code?.split('-')[0]?.toUpperCase() }}</span>
                </DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
        </DropdownMenuContent>
    </DropdownMenu>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Globe, ChevronDown, Check } from '@lucide/vue'
import {
    DropdownMenu,
    DropdownMenuTrigger,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuRadioGroup,
    DropdownMenuRadioItem
} from '@/components/ui/dropdown-menu'
import type { LocaleObject } from '@nuxtjs/i18n'

const { locale: currentLocale, locales: i18nLocales, setLocale } = useI18n()

// Ensure locales is always treated as an array of objects
const locales = computed(() => i18nLocales.value as LocaleObject[])

const currentLocaleName = computed(() => {
    return locales.value.find(l => l.code === currentLocale.value)?.name || 'Language'
})

const allowedLocales = ['en-US', 'zh-Hans', 'zh-Hant', 'ja-JP', 'de-DE', 'fr-FR'] as const
type AllowedLocale = (typeof allowedLocales)[number]

const handleLocaleChange = (value: any) => {
    if (typeof value !== 'string' && typeof value !== 'number') return

    const code = String(value)
    if (!(allowedLocales as readonly string[]).includes(code)) {
        console.error('Language not supported:', code)
        return
    }
    setLocale(code as AllowedLocale)
}
</script>
