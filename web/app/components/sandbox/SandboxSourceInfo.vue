<script setup lang="ts">
import { Badge } from "@/components/ui/badge";
import { formatBytes, type SandboxMetadata } from "~/lib/sandbox";

// Format, dimensions and size of the selected source before it has a result.
defineProps<{
    originalMetadata: SandboxMetadata;
}>();
</script>

<template>
    <div class="grid grid-cols-3 gap-4 shrink-0 select-none">
        <div
            class="border border-hairline py-2 px-3 flex justify-between items-center rounded-xl bg-surface-1 text-xs">
            <span class="text-ink-subtle font-medium">{{ $t('wasm_sandbox.meta_format') }}</span>
            <Badge variant="outline" class="uppercase text-[11px] font-mono border-hairline bg-canvas">{{
                originalMetadata.format }}</Badge>
        </div>
        <div
            class="border border-hairline py-2 px-3 flex justify-between items-center rounded-xl bg-surface-1 text-xs">
            <span class="text-ink-subtle font-medium">{{ $t('wasm_sandbox.meta_dimensions') }}</span>
            <span class="text-ink font-bold font-mono text-xs">{{ originalMetadata.width }}x{{
                originalMetadata.height }}</span>
        </div>
        <div
            class="border border-hairline py-2 px-3 flex justify-between items-center rounded-xl bg-surface-1 text-xs">
            <span class="text-ink-subtle font-medium">{{ $t('wasm_sandbox.meta_size') }}</span>
            <span class="text-ink font-bold font-mono text-xs">{{ formatBytes(originalMetadata.size)
            }}</span>
        </div>
    </div>
</template>
