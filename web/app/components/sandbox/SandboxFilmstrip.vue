<script setup lang="ts">
import { AlertCircle, Check, Cpu, Download, SlidersHorizontal, X } from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { formatBytes, type BatchImageItem } from "~/lib/sandbox";

// Thumbnails of the queued images with each one's status; picks the selected image.
defineProps<{
    items: BatchImageItem[];
    selectedItemId: string | null;
    hasPendingChanges: boolean;
}>();

const emit = defineEmits<{
    select: [id: string];
    remove: [id: string];
    "download-all": [];
}>();
</script>

<template>
    <div
        class="shrink-0 flex flex-col gap-2 py-3 px-4 bg-surface-1 border border-hairline rounded-xl select-none animate-fade-in">
        <div
            class="flex items-center justify-between text-xs font-medium text-ink-muted px-1">
            <div class="flex items-center gap-1.5">
                <SlidersHorizontal class="w-3.5 h-3.5 text-ink-subtle" />
                <span>{{ $t('wasm_sandbox.processing_queue', { count: items.length }) }}</span>
            </div>
            <Button variant="ghost"
                class="h-6 px-2 text-xs font-medium text-primary hover:bg-primary-soft hover:text-primary border-none cursor-pointer rounded-md gap-1"
                :disabled="items.filter(i => i.status === 'done').length === 0 || hasPendingChanges"
                @click="emit('download-all')">
                <Download class="w-3 h-3" />
                {{ $t('wasm_sandbox.download_all') }}
            </Button>
        </div>

        <div
            class="flex items-end gap-4 overflow-x-auto py-2 px-1 scrollbar-thin scrollbar-thumb-hairline scrollbar-track-transparent">
            <div v-for="item in items" :key="item.id"
                class="flex flex-col items-center gap-2 shrink-0 cursor-pointer group"
                @click="emit('select', item.id)">
                <!-- Thumbnail Container -->
                <div class="relative w-28 h-20 rounded-lg border overflow-hidden transition-all duration-200 bg-surface-2 flex items-center justify-center"
                    :class="selectedItemId === item.id ? 'border-primary ring-1 ring-primary shadow-sm' : 'border-hairline group-hover:border-ink-subtle group-hover:scale-102'">
                    <!-- Image (Contain center, with slight dimming on non-selected) -->
                    <img :src="item.originalUrl"
                        class="w-full h-full object-cover transition-opacity duration-200"
                        :class="selectedItemId === item.id ? 'opacity-100' : 'opacity-85 group-hover:opacity-95'" />

                    <!-- Hover Close / Remove Button -->
                    <button
                        class="absolute top-1.5 right-1.5 w-5 h-5 rounded-full bg-surface-1/90 backdrop-blur-md text-ink-subtle hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/45 border border-hairline flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all duration-150 z-20 cursor-pointer shadow-xs"
                        @click.stop="emit('remove', item.id)">
                        <X class="w-3 h-3" />
                    </button>

                    <!-- Status Badge/Indicator (Sleek High-Contrast Badges) -->
                    <div class="absolute top-1.5 left-1.5 z-25 flex items-center justify-center">
                        <!-- Pending -->
                        <div v-if="item.status === 'pending'"
                            class="w-5 h-5 rounded-full bg-surface-1/90 backdrop-blur-md border border-hairline text-ink-subtle flex items-center justify-center shadow-xs">
                            <div class="w-1.5 h-1.5 rounded-full bg-ink-subtle/60 animate-pulse" />
                        </div>
                        <!-- Processing -->
                        <div v-else-if="item.status === 'processing'"
                            class="w-5 h-5 rounded-full bg-primary/15 border border-primary/30 text-primary flex items-center justify-center shadow-xs backdrop-blur-md">
                            <Cpu class="w-3 h-3 animate-pulse" />
                        </div>
                        <!-- Done -->
                        <div v-else-if="item.status === 'done'"
                            class="w-5 h-5 rounded-full bg-emerald-500 dark:bg-emerald-600 text-white flex items-center justify-center shadow-xs border border-emerald-600/10">
                            <Check class="w-3.5 h-3.5" stroke-width="3" />
                        </div>
                        <!-- Failed -->
                        <div v-else-if="item.status === 'failed'"
                            class="w-5 h-5 rounded-full bg-red-500 dark:bg-red-600 text-white flex items-center justify-center shadow-xs border border-red-600/10">
                            <AlertCircle class="w-3.5 h-3.5" stroke-width="2.5" />
                        </div>
                    </div>

                    <!-- Size Badge (Subtle Glassmorphism) -->
                    <div v-if="item.status === 'done' && item.metrics"
                        class="absolute bottom-1 right-1 bg-surface-1/90 backdrop-blur-xs text-[9px] font-bold text-ink-subtle px-1 py-0.5 rounded border border-hairline select-none font-mono shadow-xs">
                        {{ formatBytes(item.metrics.size) }}
                    </div>
                </div>

                <!-- Filename below thumbnail -->
                <span class="text-[10px] font-mono text-ink-muted truncate w-28 text-center select-none"
                    :class="selectedItemId === item.id ? 'text-primary font-bold' : ''">
                    {{ item.file.name }}
                </span>
            </div>
        </div>
    </div>
</template>
