<template>
    <div class="absolute inset-0 flex flex-col overflow-hidden bg-canvas z-10 font-sans">

        <!-- Toolbar actions live in the layout header -->
        <ClientOnly>
            <Teleport to="#header-actions">
                <Button v-if="items.length > 0" variant="outline" size="sm"
                    class="h-8 px-3 text-xs font-medium border-hairline-strong shadow-xs hover:bg-surface-2 cursor-pointer gap-1.5"
                    @click="triggerFileInput">
                    <Upload class="w-3.5 h-3.5 text-ink-subtle" />
                    {{ $t('wasm_sandbox.add_image') }}
                </Button>
                <Button v-if="selectedItem" variant="outline" size="sm"
                    class="h-8 px-3 text-xs font-medium border-hairline-strong shadow-xs hover:bg-surface-2 cursor-pointer gap-1.5"
                    @click="triggerReplaceInput">
                    <ArrowLeftRight class="w-3.5 h-3.5 text-ink-subtle" />
                    {{ $t('wasm_sandbox.replace_selected') }}
                </Button>
                <Button v-if="items.length > 0" variant="ghost" size="sm"
                    class="h-8 px-3 text-xs font-medium text-ink-subtle hover:bg-destructive/10 hover:text-destructive cursor-pointer gap-1.5"
                    @click="resetSandbox">
                    <Trash2 class="w-3.5 h-3.5" />
                    {{ $t('wasm_sandbox.clear_queue') }}
                </Button>
            </Teleport>
        </ClientOnly>

        <!-- Main Body -->
        <div class="flex-1 flex overflow-hidden relative">

            <!-- Workspace / Left Panels -->
            <main class="flex-1 flex flex-col p-6 overflow-y-auto gap-4 bg-surface-2 dark:bg-canvas min-w-0 relative"
                @dragenter.prevent="handleDragEnter" @dragover.prevent @dragleave.prevent="handleDragLeave"
                @drop.stop.prevent="handleDrop">
                <!-- Global Drag-and-Drop Active Overlay -->
                <div v-if="isDragActive && items.length > 0"
                    class="absolute inset-6 bg-surface-1/95 border-[1.5px] border-dashed border-primary rounded-xl z-50 flex flex-col items-center justify-center py-20 px-8 select-none pointer-events-none">
                    <div
                        class="w-12 h-12 rounded-full bg-primary-soft flex items-center justify-center text-primary mb-4">
                        <Upload class="w-5 h-5" />
                    </div>
                    <h3 class="text-base font-semibold text-ink">{{
                        $t('wasm_sandbox.drop_to_queue') }}</h3>
                    <p class="text-sm text-ink-subtle mt-2">{{ $t('wasm_sandbox.formats_supported') }}</p>
                </div>

                <!-- WASM Load Error Alert -->
                <div v-if="wasmError"
                    class="p-4 shrink-0 bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900/30 rounded-xl flex gap-3 text-red-700 dark:text-red-400">
                    <AlertCircle class="w-5 h-5 shrink-0 mt-0.5" />
                    <div>
                        <h3 class="text-sm font-semibold">{{ $t('wasm_sandbox.load_failed') }}</h3>
                        <p class="text-xs mt-1 text-red-600/90 dark:text-red-400/80">{{ wasmError.message || wasmError
                            }}</p>
                    </div>
                </div>

                <!-- Empty State Upload Zone -->
                <div v-if="items.length === 0"
                    class="flex-1 border-[1.5px] border-dashed rounded-xl flex flex-col items-center justify-center py-20 px-8 transition-colors duration-200 cursor-pointer bg-surface-1"
                    :class="isDragActive ? 'border-primary bg-primary-soft' : 'border-hairline-tertiary hover:border-primary/60'"
                    @click="triggerFileInput">
                    <div class="w-12 h-12 rounded-full bg-primary-soft flex items-center justify-center text-primary mb-4">
                        <Upload class="w-5 h-5" />
                    </div>
                    <h3 class="text-base font-semibold text-ink">
                        {{ isDragActive ? $t('wasm_sandbox.drop_to_queue') : $t('wasm_sandbox.drag_prompt') }}
                    </h3>
                    <p class="text-sm text-ink-subtle mt-2">{{ $t('wasm_sandbox.drag_sub') }}</p>
                </div>

                <!-- Active Image Previews Workspace -->
                <div v-else class="flex-1 flex flex-col min-h-0 gap-4">

                    <!-- Filmstrip Queue Panel -->
                    <SandboxFilmstrip :items="items" :selected-item-id="selectedItemId"
                        :has-pending-changes="hasPendingChanges" @select="queue.selectItem"
                        @remove="queue.removeItem" @download-all="queue.downloadAll" />

                    <!-- Mode Switcher Tabs (Prioritizing shadcn/vue) -->
                    <div class="flex justify-center shrink-0">
                        <Tabs v-model="viewMode" class="w-auto select-none ">
                            <TabsList class="grid w-60 grid-cols-2 bg-surface-3 rounded-lg">
                                <TabsTrigger value="side-by-side"
                                    class=" text-xs font-semibold rounded-md cursor-pointer transition-all duration-200">
                                    {{ $t('wasm_sandbox.view_side_by_side') }}
                                </TabsTrigger>
                                <TabsTrigger value="slider"
                                    class="text-xs font-semibold rounded-md cursor-pointer transition-all duration-200">
                                    {{ $t('wasm_sandbox.view_slider') }}
                                </TabsTrigger>
                            </TabsList>
                        </Tabs>
                    </div>

                    <!-- View 1: Side by Side -->
                    <SandboxSideBySide v-if="viewMode === 'side-by-side'" :original-url="originalUrl"
                        :processed-url="processedUrl" :original-metadata="originalMetadata" :metrics="metrics"
                        :is-processing="isProcessing" :process-error="processError"
                        :preview-aspect-ratio="previewAspectRatio" @replace="triggerReplaceInput" />

                    <!-- View 2: Draggable Slider Comparison -->
                    <SandboxSliderCompare v-else v-model:slider-pos="sliderPos" :original-url="originalUrl"
                        :processed-url="processedUrl" :metrics="metrics" :is-processing="isProcessing"
                        :preview-aspect-ratio="previewAspectRatio" />

                </div>

                <!-- Metrics Dashboard Toolbar -->
                <SandboxMetricsBar v-if="processedUrl && metrics" :metrics="metrics" />

                <!-- Metadata Card (Source Info when no metrics available) -->
                <SandboxSourceInfo v-if="originalMetadata && !processedUrl" :original-metadata="originalMetadata" />

                <!-- Memory Warning -->
                <div v-if="originalMetadata && originalMetadata.size > 10 * 1024 * 1024"
                    class="p-3 bg-amber-50 dark:bg-amber-950/20 border border-amber-200 dark:border-amber-900/30 rounded-xl flex gap-2.5 text-amber-700 dark:text-amber-400 shrink-0">
                    <AlertTriangle class="w-4 h-4 shrink-0 mt-0.5" />
                    <p class="text-xs leading-normal">{{ $t('wasm_sandbox.memory_warning') }}</p>
                </div>
            </main>

            <!-- Control Sidebar Panel (Right) -->
            <aside class="w-80 shrink-0 border-l border-hairline bg-surface-1 flex flex-col overflow-hidden relative">

                <!-- Scrollable controls -->
                <div class="flex-1 overflow-y-auto py-6 px-5 space-y-6">

                    <!-- Run Mode & Trigger -->
                    <SandboxRunPanel :auto-apply="autoApply" :has-pending-changes="hasPendingChanges"
                        :is-processing="isProcessing" :has-items="items.length > 0" :has-file="!!originalFile"
                        @toggle-auto-apply="queue.toggleAutoApply" @run="queue.runManualProcess" />

                    <ImageOptionsForm v-model="options" :formats="BROWSER_FORMATS" :disabled="isProcessing" />

                </div>

                <!-- Control Sticky Footer -->
                <SandboxDownloadFooter v-model:filename="customFilename" :has-file="!!originalFile"
                    :disabled="!processedUrl || isProcessing || hasPendingChanges"
                    @download="queue.downloadSelected(customFilename)" />

            </aside>

        </div>

        <!-- Hidden input for file selection (always present in DOM to support dynamic image replacement) -->
        <input ref="fileInput" type="file" class="hidden"
            accept="image/png, image/jpeg, image/webp, image/jxl, image/avif" @change="handleFileChange" />
    </div>
</template>

<script setup lang="ts">
import { useSandboxQueue } from '~/composables/useSandboxQueue'
import ImageOptionsForm from '@/components/ImageOptionsForm.vue'
import SandboxFilmstrip from '@/components/sandbox/SandboxFilmstrip.vue'
import SandboxSideBySide from '@/components/sandbox/SandboxSideBySide.vue'
import SandboxSliderCompare from '@/components/sandbox/SandboxSliderCompare.vue'
import SandboxMetricsBar from '@/components/sandbox/SandboxMetricsBar.vue'
import SandboxSourceInfo from '@/components/sandbox/SandboxSourceInfo.vue'
import SandboxRunPanel from '@/components/sandbox/SandboxRunPanel.vue'
import SandboxDownloadFooter from '@/components/sandbox/SandboxDownloadFooter.vue'
import { BROWSER_FORMATS } from '~/lib/vips-ops'
import { Upload, AlertTriangle, AlertCircle, ArrowLeftRight, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

const { t } = useI18n()

useHead({
    title: t('wasm_sandbox.title') + ' | Transcoder',
})

const queue = useSandboxQueue()
const { wasmError, items, selectedItemId, selectedItem, isProcessing, options, autoApply, hasPendingChanges } = queue

const previewAspectRatio = computed(() => {
    const metadata = selectedItem.value?.metadata
    if (!metadata?.width || !metadata?.height) {
        return '4 / 3'
    }
    return `${metadata.width} / ${metadata.height}`
})

// The selected item's fields, for the template.
const originalFile = computed(() => selectedItem.value?.file || null)
const originalUrl = computed(() => selectedItem.value?.originalUrl || '')
const originalMetadata = computed(() => selectedItem.value?.metadata || null)
const processedUrl = computed(() => selectedItem.value?.processedUrl || '')
const processError = computed(() => selectedItem.value?.error || '')
const metrics = computed(() => selectedItem.value?.metrics || null)
const customFilename = computed({
    get: () => selectedItem.value?.customFilename || '',
    set: (val: string) => {
        if (selectedItem.value) selectedItem.value.customFilename = val
    }
})

// Workbench views
const viewMode = ref<'side-by-side' | 'slider'>('slider')
const sliderPos = ref<number>(50)

// The file picker either adds to the queue or replaces the selected image.
const fileInput = ref<HTMLInputElement | null>(null)
const isReplacingActiveItem = ref<boolean>(false)

const dragCounter = ref<number>(0)
const isDragActive = computed(() => dragCounter.value > 0)

const resetSandbox = () => {
    queue.reset()
    isReplacingActiveItem.value = false
    viewMode.value = 'slider'
    sliderPos.value = 50
    dragCounter.value = 0
}

const triggerFileInput = () => {
    isReplacingActiveItem.value = false
    fileInput.value?.click()
}

const triggerReplaceInput = () => {
    isReplacingActiveItem.value = true
    fileInput.value?.click()
}

const takeFiles = (files: FileList | null | undefined) => {
    if (!files || files.length === 0) return
    const firstFile = files[0]
    if (isReplacingActiveItem.value && selectedItemId.value && firstFile) {
        queue.replaceSelected(firstFile)
        isReplacingActiveItem.value = false
    } else {
        queue.addFiles(Array.from(files))
    }
}

const handleFileChange = (e: Event) => {
    takeFiles((e.target as HTMLInputElement).files)
}

const handleDragEnter = () => {
    dragCounter.value++
}

const handleDragLeave = () => {
    dragCounter.value--
}

const handleDrop = (e: DragEvent) => {
    dragCounter.value = 0
    takeFiles(e.dataTransfer?.files)
}
</script>
