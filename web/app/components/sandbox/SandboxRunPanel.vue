<script setup lang="ts">
import { Cpu, Play, Sparkles } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";

// Auto-apply switch, or the button that applies changed options by hand.
defineProps<{
    autoApply: boolean;
    hasPendingChanges: boolean;
    isProcessing: boolean;
    hasItems: boolean;
    hasFile: boolean;
}>();

const emit = defineEmits<{
    "toggle-auto-apply": [];
    run: [];
}>();
</script>

<template>
    <div class="space-y-3 p-4 bg-surface-1 border border-hairline rounded-xl">
        <div class="flex items-center justify-between">
            <div class="flex flex-col gap-0.5">
                <Label class="text-xs font-semibold text-ink flex items-center gap-1.5 select-none">
                    <Sparkles class="w-3.5 h-3.5 text-primary" />
                    {{ $t('wasm_sandbox.auto_process') }}
                </Label>
                <span class="text-xs text-ink-subtle">{{ $t('wasm_sandbox.auto_process_desc') }}</span>
            </div>
            <button type="button"
                class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                :class="autoApply ? 'bg-primary' : 'bg-surface-4'"
                :disabled="!hasFile" @click="emit('toggle-auto-apply')">
                <span
                    class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out"
                    :class="autoApply ? 'translate-x-4' : 'translate-x-0'" />
            </button>
        </div>

        <!-- Manual Run Button -->
        <Button v-if="!autoApply"
            class="w-full h-9 text-xs font-medium cursor-pointer justify-center transition-colors gap-1.5"
            :class="hasPendingChanges ? 'bg-primary text-primary-foreground hover:bg-primary-hover shadow-xs' : 'bg-surface-2 text-ink-subtle shadow-none disabled:opacity-100'"
            :disabled="!hasItems || isProcessing || !hasPendingChanges"
            @click="emit('run')">
            <Cpu v-if="isProcessing" class="w-3.5 h-3.5 animate-pulse" />
            <Play v-else class="w-3.5 h-3.5" />
            {{ isProcessing ? $t('wasm_sandbox.processing') : (hasPendingChanges ?
                $t('wasm_sandbox.apply_and_reprocess') :
                $t('wasm_sandbox.all_changes_applied')) }}
        </Button>
        <span v-if="hasFile"
            class="text-xs text-ink-subtle text-center block w-full mt-1.5 select-none leading-normal">
            {{ $t('wasm_sandbox.drag_to_replace_hint') }}
        </span>
    </div>
</template>
