<template>
    <DropdownMenu>
        <DropdownMenuTrigger as-child>
            <button
                class="group w-full flex items-center gap-2.5 px-2.5 h-8 rounded-md text-ink-muted hover:text-ink hover:bg-surface-2 data-[state=open]:bg-surface-2 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 cursor-pointer">
                <Globe class="w-4 h-4 shrink-0" stroke-width="1.75" />

                <span class="text-sm font-medium flex-1 text-left truncate">
                    {{ currentLocaleName }}
                </span>

                <ChevronDown
                    class="w-3.5 h-3.5 opacity-40 group-data-[state=open]:rotate-180 group-data-[state=open]:opacity-100 transition-all duration-200" />
            </button>
        </DropdownMenuTrigger>

        <DropdownMenuContent align="start" :side-offset="8"
            class="w-48 p-1 bg-popover border border-hairline shadow-md rounded-lg">
            <div class="px-2 py-1.5 text-xs font-medium text-ink-subtle">
                {{ $t('nav.select_language') }}
            </div>
            <DropdownMenuSeparator class="mx-1 my-1 bg-hairline" />
            <DropdownMenuRadioGroup :model-value="currentLocale" @update:model-value="handleLocaleChange">
                <DropdownMenuRadioItem v-for="locale in locales" :key="locale.code" :value="locale.code"
                    class="group flex items-center justify-between py-1.5 pl-8 pr-2 rounded-md cursor-pointer transition-colors text-sm text-ink focus:bg-surface-2 data-[state=checked]:font-medium">
                    <template #indicator-icon>
                        <Check class="size-3.5 stroke-3 text-primary" />
                    </template>
                    <span>{{ locale.name }}</span>
                    <span
                        class="text-xs font-mono font-medium opacity-40 group-hover:opacity-60 transition-opacity">{{
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
