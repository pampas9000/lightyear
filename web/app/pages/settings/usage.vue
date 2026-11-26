<script setup lang="ts">
import { computed } from 'vue'
import { useAuth } from '~/composables/useAuth'
import { parseApiError } from '~/composables/useApi'
import { useUsage } from '~/composables/queries/usage'
import { renderState } from '~/composables/queries/prefetch'
import { Gauge, Loader2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'

const { t, locale } = useI18n()

useHead({
    title: t('usage.title') + ' | Lightyear',
})

const { isAuthenticated, showAuthModal } = useAuth()

const usageQuery = useUsage()
const usage = computed(() => usageQuery.data.value ?? null)
const { loading, error: queryError } = renderState(usageQuery)
const error = computed(() => queryError.value ? parseApiError(queryError.value).userMessage : null)
const fetchUsage = () => usageQuery.refetch()

const number = (n: number) => new Intl.NumberFormat(locale.value).format(n)

const bytes = (n: number) => {
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let v = n
    let i = 0
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024
        i++
    }
    const digits = i === 0 || v >= 100 ? 0 : 1
    return `${new Intl.NumberFormat(locale.value, { maximumFractionDigits: digits }).format(v)} ${units[i]}`
}

const percent = (used: number, limit: number | null) =>
    limit ? Math.min(100, Math.round((used / limit) * 100)) : 0

const barClass = (p: number) =>
    p >= 100 ? 'bg-rose-500' : p >= 80 ? 'bg-amber-500' : 'bg-primary'

// Usage periods are calendar months in UTC.
const { format: formatTime, plainDate } = useTime()

const monthLabel = computed(() => {
    if (!usage.value) return ''
    return formatTime(usage.value.period_start, { year: 'numeric', month: 'long' }, 'UTC')
})

const resetsOn = computed(() => {
    if (!usage.value) return ''
    return formatTime(usage.value.period_end, { dateStyle: 'medium' }, 'UTC')
})

const dayLabel = (date: string) => plainDate(date, { month: 'short', day: 'numeric' })

const unitsPercent = computed(() =>
    usage.value ? percent(usage.value.units.used + usage.value.units.reserved, usage.value.units.limit) : 0)
const storagePercent = computed(() =>
    usage.value ? percent(usage.value.storage.used_bytes, usage.value.storage.limit_bytes) : 0)
</script>

<template>
    <div class="w-full max-w-4xl mx-auto space-y-6 pb-12 font-sans">
        <header>
            <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('usage.title') }}</h1>
            <p class="mt-1 text-sm text-ink-subtle max-w-2xl">{{ $t('usage.description') }}</p>
        </header>

        <div v-if="!isAuthenticated && !loading"
            class="border border-hairline rounded-xl bg-surface-1 p-10 flex flex-col items-center gap-3 text-center">
            <Gauge class="w-6 h-6 text-ink-subtle" stroke-width="1.75" />
            <p class="text-sm text-ink-subtle">{{ $t('usage.sign_in_required') }}</p>
            <Button size="sm" variant="outline" class="cursor-pointer" @click="showAuthModal = true">{{ $t('common.login') }}</Button>
        </div>

        <div v-else-if="loading" class="p-10 flex justify-center">
            <Loader2 class="h-5 w-5 text-primary animate-spin" />
        </div>

        <div v-else-if="error || !usage"
            class="border border-hairline rounded-xl bg-surface-1 p-10 flex flex-col items-center gap-3 text-center">
            <p class="text-sm text-ink-subtle">{{ error || $t('usage.load_failed') }}</p>
            <Button size="sm" variant="outline" class="cursor-pointer" @click="fetchUsage">{{ $t('usage.retry') }}</Button>
        </div>

        <template v-else>
            <div class="grid gap-4 sm:grid-cols-2">
                <!-- Processing units -->
                <section class="rounded-xl border border-hairline bg-surface-1 p-5 space-y-3" data-testid="usage-units">
                    <div class="flex items-baseline justify-between gap-2">
                        <h2 class="text-sm font-semibold text-ink">{{ $t('usage.units_title') }}</h2>
                        <span class="text-xs text-ink-subtle">{{ monthLabel }}</span>
                    </div>
                    <p class="text-2xl font-semibold tabular-nums text-ink">
                        {{ number(usage.units.used) }}
                        <span class="text-sm font-normal text-ink-subtle">
                            <template v-if="usage.units.limit !== null">/ {{ number(usage.units.limit) }}</template>
                            <template v-else>· {{ $t('usage.unlimited') }}</template>
                        </span>
                    </p>
                    <div v-if="usage.units.limit !== null" class="h-1.5 rounded-full bg-surface-2 overflow-hidden"
                        role="progressbar" :aria-valuenow="unitsPercent" aria-valuemin="0" aria-valuemax="100"
                        :aria-label="$t('usage.units_title')">
                        <div class="h-full rounded-full transition-[width]" :class="barClass(unitsPercent)"
                            :style="{ width: unitsPercent + '%' }" />
                    </div>
                    <p class="text-xs text-ink-subtle">
                        <template v-if="usage.units.reserved > 0">
                            {{ $t('usage.reserved', { n: number(usage.units.reserved) }) }} ·
                        </template>
                        {{ $t('usage.resets', { date: resetsOn }) }}
                    </p>
                    <p class="text-xs text-ink-subtle">{{ $t('usage.units_help') }}</p>
                </section>

                <!-- Storage -->
                <section class="rounded-xl border border-hairline bg-surface-1 p-5 space-y-3" data-testid="usage-storage">
                    <h2 class="text-sm font-semibold text-ink">{{ $t('usage.storage_title') }}</h2>
                    <p class="text-2xl font-semibold tabular-nums text-ink">
                        {{ bytes(usage.storage.used_bytes) }}
                        <span class="text-sm font-normal text-ink-subtle">
                            <template v-if="usage.storage.limit_bytes !== null">/ {{ bytes(usage.storage.limit_bytes) }}</template>
                            <template v-else>· {{ $t('usage.unlimited') }}</template>
                        </span>
                    </p>
                    <div v-if="usage.storage.limit_bytes !== null" class="h-1.5 rounded-full bg-surface-2 overflow-hidden"
                        role="progressbar" :aria-valuenow="storagePercent" aria-valuemin="0" aria-valuemax="100"
                        :aria-label="$t('usage.storage_title')">
                        <div class="h-full rounded-full transition-[width]" :class="barClass(storagePercent)"
                            :style="{ width: storagePercent + '%' }" />
                    </div>
                    <p class="text-xs text-ink-subtle">{{ $t('usage.storage_help') }}</p>
                </section>
            </div>

            <!-- Month totals -->
            <section class="rounded-xl border border-hairline bg-surface-1 p-5">
                <dl class="grid grid-cols-2 sm:grid-cols-4 gap-4">
                    <div>
                        <dt class="text-xs text-ink-subtle">{{ $t('usage.outputs') }}</dt>
                        <dd class="mt-1 text-lg font-semibold tabular-nums text-ink">{{ number(usage.totals.outputs) }}</dd>
                    </div>
                    <div>
                        <dt class="text-xs text-ink-subtle">{{ $t('usage.reused_outputs') }}</dt>
                        <dd class="mt-1 text-lg font-semibold tabular-nums text-ink">{{ number(usage.totals.reused_outputs) }}</dd>
                    </div>
                    <div>
                        <dt class="text-xs text-ink-subtle">{{ $t('usage.failed_attempts') }}</dt>
                        <dd class="mt-1 text-lg font-semibold tabular-nums text-ink">{{ number(usage.totals.failed_attempts) }}</dd>
                    </div>
                    <div>
                        <dt class="text-xs text-ink-subtle">{{ $t('usage.fetched') }}</dt>
                        <dd class="mt-1 text-lg font-semibold tabular-nums text-ink">{{ bytes(usage.totals.fetched_bytes) }}</dd>
                    </div>
                </dl>
            </section>

            <!-- Daily -->
            <section class="rounded-xl border border-hairline bg-surface-1 overflow-hidden">
                <h2 class="px-5 py-3 text-sm font-semibold text-ink border-b border-hairline">{{ $t('usage.daily_title') }}</h2>
                <p v-if="usage.daily.length === 0" class="p-10 text-center text-sm text-ink-subtle">{{ $t('usage.daily_empty') }}</p>
                <div v-else class="overflow-x-auto">
                    <table class="w-full text-sm">
                        <thead>
                            <tr class="text-xs text-ink-subtle text-left">
                                <th scope="col" class="px-3 sm:px-5 py-2 font-medium whitespace-nowrap">{{ $t('usage.col_date') }}</th>
                                <th scope="col" class="px-3 sm:px-5 py-2 font-medium text-right">{{ $t('usage.col_units') }}</th>
                                <th scope="col" class="px-3 sm:px-5 py-2 font-medium text-right">{{ $t('usage.outputs') }}</th>
                                <th scope="col" class="hidden sm:table-cell px-3 sm:px-5 py-2 font-medium text-right">{{ $t('usage.reused_outputs') }}</th>
                                <th scope="col" class="hidden sm:table-cell px-3 sm:px-5 py-2 font-medium text-right">{{ $t('usage.failed_attempts') }}</th>
                            </tr>
                        </thead>
                        <tbody class="divide-y divide-hairline">
                            <tr v-for="day in [...usage.daily].reverse()" :key="day.date" class="tabular-nums text-ink">
                                <td class="px-3 sm:px-5 py-2 whitespace-nowrap">{{ dayLabel(day.date) }}</td>
                                <td class="px-3 sm:px-5 py-2 text-right">{{ number(day.units) }}</td>
                                <td class="px-3 sm:px-5 py-2 text-right">{{ number(day.outputs) }}</td>
                                <td class="hidden sm:table-cell px-3 sm:px-5 py-2 text-right">{{ number(day.reused_outputs) }}</td>
                                <td class="hidden sm:table-cell px-3 sm:px-5 py-2 text-right">{{ number(day.failed_attempts) }}</td>
                            </tr>
                        </tbody>
                    </table>
                </div>
            </section>
        </template>
    </div>
</template>
