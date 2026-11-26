<script setup lang="ts">
import { ArrowLeftRight, Cpu, Image, XCircle, Zap } from "@lucide/vue";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, previewCanvasClass, type SandboxMetadata, type SandboxMetrics } from "~/lib/sandbox";

// The source and the result next to each other.
defineProps<{
    originalUrl: string;
    processedUrl: string;
    originalMetadata: SandboxMetadata | null;
    metrics: SandboxMetrics | null;
    isProcessing: boolean;
    processError: string;
    previewAspectRatio: string;
}>();

const emit = defineEmits<{
    replace: [];
}>();
</script>

<template>
    <div
        class="flex-1 grid grid-cols-1 md:grid-cols-2 gap-6 min-h-0 items-start">

        <!-- Original Image Card -->
        <Card
            class="border border-hairline shadow-xs flex flex-col overflow-hidden bg-surface-1 py-0 gap-0">
            <CardHeader
                class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 overflow-visible">
                <CardTitle
                    class="min-w-0 truncate text-xs leading-none font-medium text-ink-muted flex items-center gap-1.5 overflow-visible">
                    <Image class="w-3.5 h-3.5 shrink-0" />
                    {{ $t('wasm_sandbox.source_image') }}
                </CardTitle>
                <div class="flex items-center gap-1.5 shrink-0">
                    <Badge v-if="originalMetadata" variant="outline"
                        class="h-5 px-1.5 font-mono text-[10px] uppercase border-hairline">
                        {{ originalMetadata.format }} · {{ originalMetadata.width }}x{{
                            originalMetadata.height }}
                    </Badge>
                    <Button variant="ghost"
                        class="h-6 px-2 text-xs font-medium text-primary hover:bg-primary-soft hover:text-primary border-none cursor-pointer rounded-md gap-1"
                        @click="emit('replace')">
                        <ArrowLeftRight class="w-3 h-3" />
                        {{ $t('wasm_sandbox.replace') }}
                    </Button>
                </div>
            </CardHeader>
            <!-- Image Display Box -->
            <div :class="previewCanvasClass" class="relative w-full overflow-hidden select-none group rounded-b-xl"
                :style="{ aspectRatio: previewAspectRatio }">
                <div class="absolute inset-0 flex items-center justify-center">
                    <img :src="originalUrl"
                        class="h-full w-full object-contain transition-all duration-300"
                        alt="Original image" />
                </div>

                <!-- Low Profile Bottom Interactive Bar -->
                <div class="absolute bottom-0 left-0 right-0 bg-surface-1/90 backdrop-blur-md py-1 px-2.5 text-[11px] text-ink-subtle border-t border-hairline flex items-center justify-between gap-3 cursor-pointer opacity-0 group-hover:opacity-100 transition-opacity duration-200 z-20 animate-fade-in"
                    @click="emit('replace')">
                    <span class="font-medium truncate">{{ $t('wasm_sandbox.click_to_replace') }}</span>
                    <span
                        class="text-[10px] text-primary font-semibold flex items-center gap-0.5 shrink-0">
                        {{ $t('wasm_sandbox.change_image') }}
                        <ArrowLeftRight class="w-2.5 h-2.5" />
                    </span>
                </div>
            </div>
        </Card>

        <!-- Processed Image Card -->
        <Card
            class="border border-hairline shadow-xs flex flex-col overflow-hidden bg-surface-1 relative py-0 gap-0">
            <CardHeader
                class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 overflow-visible">
                <CardTitle
                    class="min-w-0 truncate text-xs leading-none font-medium text-ink-muted flex items-center gap-1.5 overflow-visible">
                    <Zap class="w-3.5 h-3.5 text-ink-subtle shrink-0" />
                    {{ $t('wasm_sandbox.optimized_preview') }}
                </CardTitle>
                <Badge v-if="processedUrl && metrics" variant="outline"
                    class="h-5 px-1.5 font-mono text-[10px] border-hairline shrink-0">
                    {{ formatBytes(metrics.size) }}
                </Badge>
            </CardHeader>
            <div :class="previewCanvasClass" class="relative w-full overflow-hidden select-none rounded-b-xl"
                :style="{ aspectRatio: previewAspectRatio }">
                <div v-if="isProcessing"
                    class="absolute inset-0 bg-canvas/70 backdrop-blur-md z-10 flex flex-col items-center justify-center gap-3 animate-fade-in">
                    <div
                        class="w-10 h-10 rounded-full bg-primary-soft flex items-center justify-center text-primary">
                        <Cpu class="w-5 h-5 animate-pulse" />
                    </div>
                    <span
                        class="text-xs font-medium text-ink-muted">{{
                            $t('wasm_sandbox.processing') }}</span>
                </div>
                <div v-else-if="processError"
                    class="absolute inset-0 flex items-center justify-center p-4">
                    <div class="text-center max-w-sm text-red-600 dark:text-red-400">
                        <XCircle class="w-7 h-7 mx-auto mb-2 text-red-500" />
                        <p class="text-xs font-bold">{{ $t('wasm_sandbox.process_failed') }}</p>
                        <p class="text-xs mt-1 opacity-80 leading-relaxed font-mono truncate-3-lines">
                            {{ processError }}</p>
                    </div>
                </div>
                <div v-else-if="processedUrl" class="absolute inset-0 flex items-center justify-center">
                    <img :src="processedUrl"
                        class="h-full w-full object-contain transition-all duration-300"
                        alt="Processed image" />
                </div>
            </div>
        </Card>
    </div>
</template>
