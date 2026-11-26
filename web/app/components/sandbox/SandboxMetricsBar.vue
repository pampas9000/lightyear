<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatBytes, type SandboxMetrics } from "~/lib/sandbox";

// Time, size and savings of the selected result.
defineProps<{
    metrics: SandboxMetrics;
}>();
</script>

<template>
    <Card
        class="border border-hairline shadow-none shrink-0 bg-surface-1 py-0">
        <CardContent class="py-3 px-3 grid grid-cols-3 gap-4 text-center">
            <div class="flex flex-col items-center">
                <span class="text-xs font-medium text-ink-subtle">{{
                    $t('wasm_sandbox.metric_speed') }}</span>
                <span class="text-base font-semibold text-ink mt-1 font-mono tabular-nums flex items-baseline gap-0.5">
                    {{ metrics.duration > 100 ? (metrics.duration / 1000).toFixed(2) :
                        metrics.duration.toFixed(1) }}
                    <span class="text-xs font-medium text-ink-subtle">{{ metrics.duration > 100 ? 's' : 'ms'
                    }}</span>
                </span>
            </div>
            <div class="flex flex-col items-center border-x border-hairline">
                <span class="text-xs font-medium text-ink-subtle">{{
                    $t('wasm_sandbox.metric_size') }}</span>
                <span class="text-base font-semibold text-ink mt-1 font-mono tabular-nums">
                    {{ formatBytes(metrics.size) }}
                </span>
            </div>
            <div class="flex flex-col items-center">
                <span class="text-xs font-medium text-ink-subtle">{{
                    $t('wasm_sandbox.metric_savings') }}</span>
                <div class="mt-0.5 flex justify-center">
                    <Badge v-if="metrics.savings < 0"
                        class="bg-emerald-50 dark:bg-emerald-950/20 text-emerald-600 dark:text-emerald-400 font-bold border border-emerald-200/40 hover:bg-emerald-50 text-xs">
                        {{ metrics.savings.toFixed(1) }}%
                    </Badge>
                    <Badge v-else
                        class="bg-amber-50 dark:bg-amber-950/20 text-amber-600 dark:text-amber-400 font-bold border border-amber-200/40 hover:bg-amber-50 text-xs">
                        +{{ metrics.savings.toFixed(1) }}%
                    </Badge>
                </div>
            </div>
        </CardContent>
    </Card>
</template>
