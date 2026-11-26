<script setup lang="ts">
import { computed, ref } from 'vue'
import { Code2, Settings } from '@lucide/vue'
import { describe, formatShortBytes } from '~/lib/image-options'
import { outputSpecs } from '~/lib/task-detail'
import type { CompressionTarget, Task } from '~/lib/types/task'
import type { ImageOptions } from '~/lib/types/image-options'

// The options a task ran with, as labelled values or as raw JSON. Render it
// only when the first job has options.
const props = defineProps<{
    task: Task
}>()

const { t } = useI18n()

const showRawJson = ref(false)
const firstJob = computed(() => props.task.jobs?.[0])
const uniqueOutputSpecs = computed(() => outputSpecs(props.task.jobs || []))

// Label/value pairs for the parameters card.
const optionEntries = (o?: ImageOptions, target?: CompressionTarget, resolved?: ImageOptions) => {
    if (!o) return []
    const r = o.resize
    const resize = r ? (r.width && r.height ? `${r.width}×${r.height} ${r.fit ?? 'inside'}` : `≤${r.width ?? r.height}px`) + (r.enlarge ? ' ↑' : '') : '—'
    const qualityDisplay = (() => {
        if (o.lossless) return t('image_options.lossless')
        if (target?.max_bytes && o.quality == null && o.distance == null) {
            if (resolved?.quality != null) return `${t('image_options.auto')} (Q${resolved.quality})`
            if (resolved?.distance != null) return `${t('image_options.auto')} (d${resolved.distance})`
            return t('image_options.auto')
        }
        return String(o.quality ?? '—')
    })()
    const entries: Array<[string, string]> = [
        ['format', o.format.toUpperCase()],
        o.distance != null
            ? ['distance', String(o.distance)]
            : ['quality', qualityDisplay],
        ['effort', o.format === 'jpeg' ? '—' : String(o.effort ?? t('image_options.effort_default'))],
        ['resize', resize],
        ['color', t(`image_options.color_${o.color ?? 'keep'}`)],
        ['dynamic_range', t(`image_options.dynamic_range_${o.dynamic_range ?? 'keep'}`)],
        ['metadata', t(`image_options.metadata_${o.metadata ?? 'strip'}`)],
        ['chroma', o.chroma ? `4:${o.chroma.slice(1, 2)}:${o.chroma.slice(2)}` : t('image_options.auto')],
        ['bit_depth', o.bit_depth ? `${o.bit_depth}-bit` : t('image_options.follow_source')],
    ]
    if (target?.max_bytes) {
        entries.push(['target_size', `≤ ${formatShortBytes(target.max_bytes)}`])
    }
    return entries
}
</script>

<template>
    <div class="border border-hairline rounded-xl bg-surface-1 overflow-hidden flex flex-col">
        <div class="flex items-center justify-between border-b border-hairline px-5 py-3.5 bg-surface-1">
            <h3 class="font-semibold text-xs text-ink uppercase tracking-wider flex items-center gap-2">
                <Settings class="w-4 h-4 text-ink-subtle" stroke-width="1.5" />
                {{ $t('task_detail.params') }}
            </h3>

            <!-- Developer Toggle for Raw JSON -->
            <button
                @click="showRawJson = !showRawJson"
                class="text-xs font-medium text-ink-subtle hover:text-ink flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-surface-2 border border-hairline hover:bg-surface-3 transition-colors cursor-pointer"
            >
                <Code2 class="w-3.5 h-3.5" />
                <span>{{ showRawJson ? $t('task_detail.hide_raw_json') : $t('task_detail.view_raw_json') }}</span>
            </button>
        </div>

        <div class="p-5">
            <!-- Standard Structured Key-Value Capsules -->
            <div v-if="!showRawJson">
                <div v-if="uniqueOutputSpecs.length <= 1" class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-5 gap-3">
                    <div
                        v-for="[key, val] in optionEntries(uniqueOutputSpecs[0]?.options, uniqueOutputSpecs[0]?.target, firstJob?.resolved_options)"
                        :key="key"
                        class="space-y-1 bg-surface-2 p-2.5 rounded-lg border border-hairline"
                    >
                        <div class="text-[10px] font-semibold text-ink-subtle uppercase tracking-wider truncate">{{ $t(`image_options.${key}`) }}</div>
                        <div class="text-xs font-semibold text-ink truncate">{{ val }}</div>
                    </div>
                </div>
                <div v-else class="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
                    <div
                        v-for="spec in uniqueOutputSpecs"
                        :key="`${spec.index}-${spec.name ?? ''}`"
                        class="space-y-1 bg-surface-2 p-3 rounded-lg border border-hairline"
                    >
                        <div class="text-[10px] font-mono font-semibold text-ink-subtle uppercase tracking-wider truncate">
                            {{ spec.name || `#${spec.index + 1}` }}
                        </div>
                        <div class="text-xs font-mono font-semibold text-ink truncate">
                            {{ describe(spec.options, spec.target) }}
                        </div>
                    </div>
                </div>
            </div>

            <!-- Raw JSON Code Inspector -->
            <div v-else class="space-y-2">
                <pre class="p-4 rounded-lg bg-surface-2 border border-hairline text-xs font-mono text-ink overflow-x-auto selection:bg-primary/20 leading-relaxed">{{
                    JSON.stringify(
                        uniqueOutputSpecs.length > 1
                            ? uniqueOutputSpecs.map(s => ({ ...(s.name ? { name: s.name } : {}), options: s.options, ...(s.target ? { target: s.target } : {}) }))
                            : { options: firstJob?.options, ...(firstJob?.target ? { target: firstJob.target } : {}), ...(firstJob?.resolved_options ? { resolved_options: firstJob.resolved_options } : {}) },
                        null,
                        2,
                    )
                }}</pre>
            </div>
        </div>
    </div>
</template>
