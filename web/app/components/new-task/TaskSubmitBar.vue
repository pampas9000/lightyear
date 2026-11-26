<script setup lang="ts">
import { Loader2 } from "@lucide/vue";
import { Button } from "@/components/ui/button";

// Overall upload progress and the button that creates the task.
defineProps<{
    fileCount: number;
    isUploading: boolean;
    isCreatingTask: boolean;
    globalProgress: number;
    isSubmitDisabled: boolean;
    submitButtonText: string;
}>();

const emit = defineEmits<{
    submit: [];
}>();
</script>

<template>
    <div class="pt-6 mt-6 border-t border-hairline">
        <div
            v-if="isUploading"
            class="space-y-3 mb-6 max-w-md ml-auto animate-in fade-in duration-300"
        >
            <div class="flex justify-between items-end">
                <span class="text-xs font-semibold text-ink-muted">
                    {{ $t("new_task.uploading", { count: fileCount }) }}
                </span>
                <span
                    class="text-xl font-semibold text-primary dark:text-primary-hover"
                    >{{ globalProgress }}%</span
                >
            </div>
            <div class="h-1.5 w-full rounded-full bg-surface-3 overflow-hidden">
                <div
                    class="h-full bg-primary rounded-full transition-all duration-500 ease-out shadow-xs"
                    :style="{ width: `${globalProgress}%` }"
                />
            </div>
        </div>

        <div class="flex justify-end">
            <Button
                @click="emit('submit')"
                :disabled="isSubmitDisabled"
                class="rounded-lg px-6 h-9 text-xs font-semibold bg-primary hover:bg-primary-hover text-primary-foreground shadow-sm transition-all duration-200 cursor-pointer"
            >
                <Loader2
                    v-if="isUploading || isCreatingTask"
                    class="mr-2 h-3.5 w-3.5 animate-spin"
                />
                {{ submitButtonText }}
            </Button>
        </div>
    </div>
</template>
