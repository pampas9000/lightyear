<script setup lang="ts">
import { UploadCloud } from "@lucide/vue";

// Covers the files card while files are dragged over it.
defineProps<{
    active: boolean;
}>();

const emit = defineEmits<{
    leave: [];
    drop: [event: DragEvent];
}>();
</script>

<template>
    <Transition
        enter-active-class="transition duration-200 ease-out"
        enter-from-class="opacity-0 scale-95"
        enter-to-class="opacity-100 scale-100"
        leave-active-class="transition duration-150 ease-in"
        leave-from-class="opacity-100 scale-100"
        leave-to-class="opacity-0 scale-95"
    >
        <div
            v-if="active"
            class="absolute inset-0 bg-canvas/90 backdrop-blur-[1px] z-50 flex flex-col items-center justify-center gap-4 transition-all duration-300 border border-dashed border-primary/50 rounded-xl pointer-events-auto"
            @dragleave.prevent="emit('leave')"
            @dragover.prevent
            @drop.stop.prevent="emit('drop', $event)"
        >
            <div
                class="h-12 w-12 rounded-full bg-primary-soft flex items-center justify-center text-primary"
            >
                <UploadCloud class="w-6 h-6" />
            </div>
            <p class="text-xs font-semibold text-primary tracking-wide">
                {{ $t("new_task.drop_files") }}
            </p>
        </div>
    </Transition>
</template>
