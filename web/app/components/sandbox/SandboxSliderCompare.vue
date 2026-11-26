<script setup lang="ts">
import { ArrowLeftRight, Cpu } from "@lucide/vue";
import { Badge } from "@/components/ui/badge";
import { CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytes, previewCanvasClass, type SandboxMetrics } from "~/lib/sandbox";

// The source over the result, split by a line the user drags.
defineProps<{
    originalUrl: string;
    processedUrl: string;
    metrics: SandboxMetrics | null;
    isProcessing: boolean;
    previewAspectRatio: string;
}>();

/** Where the split sits, in percent from the left. */
const sliderPos = defineModel<number>("sliderPos", { required: true });

const sliderContainer = ref<HTMLElement | null>(null);
const isDragging = ref(false);
let containerRect: { left: number; width: number } | null = null;

const handleDrag = (e: MouseEvent | TouchEvent) => {
    if (!containerRect) return;
    const clientX = "touches" in e ? (e.touches[0]?.clientX ?? 0) : (e as MouseEvent).clientX;
    const offsetX = clientX - containerRect.left;
    sliderPos.value = Math.max(0, Math.min(100, (offsetX / containerRect.width) * 100));
};

const stopDrag = () => {
    isDragging.value = false;
    containerRect = null;
    window.removeEventListener("mousemove", handleDrag);
    window.removeEventListener("mouseup", stopDrag);
    window.removeEventListener("touchmove", handleDrag);
    window.removeEventListener("touchend", stopDrag);
};

const startDrag = (e: MouseEvent | TouchEvent) => {
    e.preventDefault();
    if (!sliderContainer.value) return;
    const rect = sliderContainer.value.getBoundingClientRect();
    containerRect = { left: rect.left, width: rect.width || 1 };
    isDragging.value = true;
    handleDrag(e);
    window.addEventListener("mousemove", handleDrag);
    window.addEventListener("mouseup", stopDrag);
    window.addEventListener("touchmove", handleDrag, { passive: true });
    window.addEventListener("touchend", stopDrag);
};

onBeforeUnmount(stopDrag);
</script>

<template>
    <div
        class="flex-1 flex flex-col border border-hairline shadow-xs bg-surface-1 rounded-xl overflow-hidden relative py-0">
        <CardHeader
            class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 overflow-visible">
            <CardTitle
                class="min-w-0 truncate text-xs leading-none font-medium text-ink-muted flex items-center gap-1.5 overflow-visible">
                <ArrowLeftRight class="w-3.5 h-3.5 text-ink-subtle shrink-0" />
                {{ $t('wasm_sandbox.view_slider') }}
            </CardTitle>
            <!-- Mini overlay info -->
            <Badge v-if="metrics" variant="outline"
                class="h-5 px-1.5 font-mono text-[10px] border-hairline bg-canvas shrink-0">
                {{ formatBytes(metrics.size) }} ({{ metrics.savings < 0 ? metrics.savings.toFixed(1)
                    + '%' : '+' + metrics.savings.toFixed(1) + '%' }}) </Badge>
        </CardHeader>
        <div ref="sliderContainer"
            :class="previewCanvasClass" class="relative w-full overflow-hidden select-none cursor-ew-resize rounded-b-xl flex items-center justify-center"
            :style="{ aspectRatio: previewAspectRatio }" @mousedown="startDrag" @touchstart="startDrag">
            <!-- Background (Processed image, right side) -->
            <img :src="processedUrl"
                class="absolute inset-0 h-full w-full object-contain pointer-events-none select-none"
                :class="{ 'transition-all duration-300': !isDragging }"
                :style="{ opacity: sliderPos === 100 ? 0 : 1 }" alt="Processed image" />

            <!-- Foreground Wrapper (Clipped vertically on the screen) -->
            <div class="absolute inset-0 pointer-events-none select-none"
                :class="{ 'transition-all duration-300': !isDragging }"
                :style="{ clipPath: `inset(0 ${100 - sliderPos}% 0 0)`, opacity: sliderPos === 0 ? 0 : 1 }">
                <!-- Foreground (Original image, left side) -->
                <img :src="originalUrl"
                    class="absolute inset-0 h-full w-full object-contain pointer-events-none select-none"
                    alt="Original image" />
            </div>

            <!-- Split Line -->
            <div class="absolute top-0 bottom-0 w-[2px] bg-white shadow-[0_0_8px_rgba(0,0,0,0.25)] z-20 pointer-events-none"
                :style="{ left: sliderPos + '%' }">
                <!-- Circular Slider Button -->
                <div
                    class="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-8 h-8 rounded-full bg-white border border-border shadow-[0_4px_12px_rgba(0,0,0,0.1),_0_2px_4px_rgba(0,0,0,0.05)] flex items-center justify-center text-primary pointer-events-auto hover:scale-105 active:scale-95 transition-all duration-200 cursor-ew-resize">
                    <ArrowLeftRight class="w-4 h-4" />
                </div>
            </div>

            <!-- Small Loader Indicator when re-processing -->
            <div v-if="isProcessing"
                class="absolute bottom-3 right-3 bg-canvas/85 backdrop-blur-md px-2.5 py-1 rounded-md border border-hairline z-30 flex items-center gap-1.5 text-[11px] font-bold text-primary">
                <Cpu class="w-3.5 h-3.5 text-primary animate-pulse" />
                {{ $t('wasm_sandbox.processing') }}
            </div>
        </div>
    </div>
</template>
