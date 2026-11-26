<script setup lang="ts">
import { Download } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// Output filename and the download button for the selected result.
defineProps<{
    hasFile: boolean;
    disabled: boolean;
}>();

const filename = defineModel<string>("filename", { required: true });

const emit = defineEmits<{
    download: [];
}>();
</script>

<template>
    <div class="p-5 border-t border-hairline bg-surface-1 shrink-0 space-y-2">
        <div v-if="hasFile" class="space-y-2 mb-2">
            <Label for="output-filename"
                class="text-xs font-medium text-ink-muted">{{
                    $t('wasm_sandbox.output_filename') }}</Label>
            <Input id="output-filename" v-model="filename"
                class="h-9 font-mono text-xs bg-surface-1"
                :placeholder="$t('wasm_sandbox.output_filename_placeholder')" />
        </div>
        <Button
            class="w-full h-9 bg-primary hover:bg-primary-hover text-primary-foreground font-medium rounded-md text-sm cursor-pointer shadow-xs border-none gap-2 justify-center transition-colors"
            :disabled="disabled" @click="emit('download')">
            <Download class="w-3.5 h-3.5" />
            {{ $t('wasm_sandbox.download') }}
        </Button>
    </div>
</template>
