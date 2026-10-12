<script setup lang="ts">
import { ref, watch } from 'vue'
import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetDescription,
    SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { RotateCcw, Loader2, Check, Code2, SlidersHorizontal } from '@lucide/vue'
import ImageOptionsForm from '@/components/ImageOptionsForm.vue'
import type { ImageOptions } from '~/lib/types/image-options'
import { defaultOptions, sanitize } from '~/lib/image-options'

const props = defineProps<{
    open: boolean
    taskId: string
    targetJobId?: string
    initialOptions?: ImageOptions
    failedCount?: number
    isRetrying?: boolean
}>()

const emit = defineEmits<{
    (e: 'update:open', val: boolean): void
    (e: 'submit', payload: { options?: ImageOptions; retryAll: boolean; jobIds?: string[] }): void
}>()

const options = ref<ImageOptions>(defaultOptions())
const changeOptions = ref(false)
const retryAllJobs = ref(false)
const showRawJson = ref(false)

// Start from the job's own options every time the drawer opens.
watch(
    () => props.open,
    (open) => {
        if (!open) return
        options.value = sanitize(props.initialOptions ?? defaultOptions())
        changeOptions.value = false
        retryAllJobs.value = false
        showRawJson.value = false
    },
)

const handleSubmit = () => {
    emit('submit', {
        options: changeOptions.value ? sanitize(options.value) : undefined,
        retryAll: retryAllJobs.value,
        jobIds: props.targetJobId ? [props.targetJobId] : undefined,
    })
}
</script>

<template>
    <Sheet :open="open" @update:open="val => emit('update:open', val)">
        <SheetContent class="w-full sm:max-w-xl bg-surface-1 border-l border-hairline p-0 flex flex-col h-full shadow-2xl overflow-hidden font-sans text-ink">
            <SheetHeader class="px-6 py-4 border-b border-hairline space-y-1 bg-surface-1/80 backdrop-blur-md shrink-0">
                <div class="flex items-center justify-between">
                    <div class="flex items-center gap-2.5">
                        <div class="w-7 h-7 rounded-md bg-primary text-primary-foreground flex items-center justify-center shadow-sm shrink-0">
                            <SlidersHorizontal class="w-4 h-4 stroke-[2.5]" />
                        </div>
                        <SheetTitle class="text-sm font-bold text-ink tracking-tight">
                            {{ targetJobId ? '重试单个作业' : '重试任务' }}
                        </SheetTitle>
                    </div>
                    <Badge class="bg-primary/10 text-primary border-none font-mono text-[10px] font-bold px-1.5 py-0.5 uppercase select-none">
                        {{ targetJobId ? `Job-${targetJobId.substring(0, 8)}` : `Task-${taskId.substring(0, 8)}` }}
                    </Badge>
                </div>
                <SheetDescription class="text-xs text-ink-subtle leading-relaxed pt-0.5">
                    可沿用原参数，也可修改输出参数后重试。提交后作业会重新排队执行。
                </SheetDescription>
            </SheetHeader>

            <div class="flex-1 overflow-y-auto px-6 py-6 space-y-6">
                <!-- Scope -->
                <div v-if="!targetJobId" class="p-3.5 rounded-xl bg-surface-2 border border-hairline space-y-2">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink flex items-center gap-1.5">
                            <RotateCcw class="w-3.5 h-3.5 text-primary" />
                            重试范围
                        </span>
                        <span class="text-[11px] font-mono font-bold text-ink-subtle">
                            {{ failedCount ? `${failedCount} 个失败作业` : '全任务' }}
                        </span>
                    </div>
                    <div class="grid grid-cols-2 gap-2 pt-1">
                        <button v-for="all in [false, true]" :key="String(all)" type="button" @click="retryAllJobs = all"
                            class="px-3 py-2 rounded-lg text-xs font-semibold border transition-all text-left flex items-center justify-between cursor-pointer"
                            :class="retryAllJobs === all
                                ? 'bg-surface-1 border-primary text-primary shadow-xs ring-1 ring-primary font-bold'
                                : 'bg-transparent border-hairline text-ink-subtle hover:bg-surface-2 hover:text-ink'">
                            <span>{{ all ? '全部已结束的作业' : '仅失败的作业' }}</span>
                            <Check v-if="retryAllJobs === all" class="w-3.5 h-3.5 text-primary stroke-[2.5]" />
                        </button>
                    </div>
                </div>

                <!-- Same or changed options -->
                <div class="grid grid-cols-2 gap-2">
                    <button v-for="change in [false, true]" :key="String(change)" type="button" @click="changeOptions = change"
                        class="px-3 py-2 rounded-lg text-xs font-semibold border transition-all text-left flex items-center justify-between cursor-pointer"
                        :class="changeOptions === change
                            ? 'bg-surface-1 border-primary text-primary shadow-xs ring-1 ring-primary font-bold'
                            : 'bg-transparent border-hairline text-ink-subtle hover:bg-surface-2 hover:text-ink'">
                        <span>{{ change ? '修改参数' : '沿用原参数' }}</span>
                        <Check v-if="changeOptions === change" class="w-3.5 h-3.5 text-primary stroke-[2.5]" />
                    </button>
                </div>

                <ImageOptionsForm v-if="changeOptions" v-model="options" :disabled="isRetrying" />

                <div v-if="changeOptions" class="pt-2">
                    <button type="button" @click="showRawJson = !showRawJson"
                        class="text-xs font-mono font-semibold text-ink-subtle hover:text-primary flex items-center gap-1.5 cursor-pointer transition-colors">
                        <Code2 class="w-3.5 h-3.5" />
                        <span>{{ showRawJson ? '收起 JSON' : '查看提交的 options' }}</span>
                    </button>
                    <div v-if="showRawJson" class="mt-2 p-3 rounded-lg bg-surface-2 text-ink font-mono text-[11px] overflow-x-auto border border-hairline shadow-inner">
                        <pre>{{ JSON.stringify(sanitize(options), null, 2) }}</pre>
                    </div>
                </div>
            </div>

            <SheetFooter class="px-6 py-3.5 border-t border-hairline bg-surface-1/80 backdrop-blur-md shrink-0 flex flex-row items-center justify-between gap-3">
                <Button type="button" variant="outline" size="sm" @click="emit('update:open', false)"
                    class="h-8 px-4 text-xs font-semibold border-hairline hover:bg-surface-2 text-ink cursor-pointer transition-all duration-200">
                    取消
                </Button>
                <Button type="button" size="sm" @click="handleSubmit" :disabled="isRetrying"
                    class="h-8 px-5 text-xs font-semibold bg-primary text-primary-foreground hover:bg-primary-hover gap-2 shadow-xs cursor-pointer rounded-lg transition-all duration-200 disabled:opacity-50">
                    <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                    <RotateCcw v-else class="w-3.5 h-3.5 stroke-[2.2]" />
                    <span>{{ isRetrying ? '正在提交...' : '确认重试' }}</span>
                </Button>
            </SheetFooter>
        </SheetContent>
    </Sheet>
</template>
