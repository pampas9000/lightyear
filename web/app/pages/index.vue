<template>
    <div class="absolute inset-0 flex flex-col overflow-hidden bg-canvas z-10 font-sans">

        <!-- Top Bar / Header -->
        <header class="h-12 border-b border-hairline bg-surface-1 shrink-0 flex items-center justify-between px-5 z-20">
            <div class="flex items-center gap-3">
                <div
                    class="w-6 h-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center shrink-0 shadow-sm">
                    <Zap class="w-3.5 h-3.5" stroke-width="2.5" />
                </div>
                <div>
                    <h1 class="text-sm font-bold tracking-tight text-ink font-satoshi flex items-center gap-2">
                        {{ $t('wasm_sandbox.title') }}
                        <Badge
                            class="bg-primary/10 text-primary border-none text-[10px] px-1.5 py-0.5 h-auto font-bold uppercase select-none">
                            WASM Local</Badge>
                    </h1>
                </div>
            </div>

            <!-- Quick Actions -->
            <div class="flex items-center gap-2">
                <Button v-if="items.length > 0" variant="outline"
                    class="h-8 px-3 text-xs font-semibold hover:bg-surface-2 cursor-pointer transition-all duration-200 border-hairline gap-1.5"
                    @click="triggerFileInput">
                    <Upload class="w-3.5 h-3.5 text-primary" />
                    {{ $t('wasm_sandbox.add_image') }}
                </Button>
                <Button v-if="selectedItem" variant="outline"
                    class="h-8 px-3 text-xs font-semibold hover:bg-surface-2 cursor-pointer transition-all duration-200 border-hairline gap-1.5"
                    @click="triggerReplaceInput">
                    <ArrowLeftRight class="w-3.5 h-3.5 text-primary" />
                    {{ $t('wasm_sandbox.replace_selected') }}
                </Button>
                <Button v-if="items.length > 0" variant="outline"
                    class="h-8 px-3 text-xs font-semibold hover:bg-red-50 dark:hover:bg-red-950/20 cursor-pointer transition-all duration-200 border-hairline hover:border-red-200/50 hover:text-red-600 dark:hover:text-red-400 gap-1.5"
                    @click="resetSandbox">
                    <Trash2 class="w-3.5 h-3.5 text-red-500" />
                    {{ $t('wasm_sandbox.clear_queue') }}
                </Button>
            </div>
        </header>

        <!-- Main Body -->
        <div class="flex-1 flex overflow-hidden relative">

            <!-- Workspace / Left Panels -->
            <main class="flex-1 flex flex-col py-6 px-6 overflow-y-auto gap-6 bg-canvas/30 min-w-0 relative"
                @dragenter.prevent="handleDragEnter" @dragover.prevent @dragleave.prevent="handleDragLeave"
                @drop.stop.prevent="handleDrop">
                <!-- Global Drag-and-Drop Active Overlay -->
                <div v-if="isDragActive && items.length > 0"
                    class="absolute inset-6 bg-surface-1/90 backdrop-blur-md border-2 border-dashed border-primary/50 rounded-2xl z-50 flex flex-col items-center justify-center py-20 px-8 transition-all duration-300 select-none pointer-events-none shadow-inner">
                    <div
                        class="w-16 h-16 rounded-full bg-primary/10 flex items-center justify-center text-primary mb-6 shadow-sm">
                        <Upload class="w-7 h-7 text-primary animate-bounce" />
                    </div>
                    <h3 class="text-lg font-bold text-ink tracking-tight text-primary">{{
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
                    class="flex-1 border-2 border-dashed rounded-2xl flex flex-col items-center justify-center py-20 px-8 transition-all duration-300 cursor-pointer bg-surface-1/40 shadow-inner"
                    :class="isDragActive ? 'border-primary bg-primary/5 scale-[1.01]' : 'border-hairline hover:border-primary/50'"
                    @click="triggerFileInput">
                    <div class="w-16 h-16 rounded-full bg-primary/10 flex items-center justify-center text-primary mb-6 shadow-sm transition-all duration-300 hover:scale-105"
                        :class="isDragActive ? 'scale-105' : ''">
                        <Upload class="w-7 h-7" :class="isDragActive ? 'animate-bounce' : ''" />
                    </div>
                    <h3 class="text-lg font-bold text-ink tracking-tight" :class="isDragActive ? 'text-primary' : ''">
                        {{ isDragActive ? $t('wasm_sandbox.drop_to_queue') : $t('wasm_sandbox.drag_prompt') }}
                    </h3>
                    <p class="text-sm text-ink-subtle mt-2">{{ $t('wasm_sandbox.drag_sub') }}</p>
                </div>

                <!-- Active Image Previews Workspace -->
                <div v-else class="flex-1 flex flex-col min-h-0 gap-4">

                    <!-- Filmstrip Queue Panel -->
                    <div
                        class="shrink-0 flex flex-col gap-2 py-3 px-4 bg-surface-2/40 rounded-xl select-none animate-fade-in">
                        <div
                            class="flex items-center justify-between text-xs font-bold text-ink-subtle uppercase tracking-wider px-1">
                            <div class="flex items-center gap-1.5">
                                <SlidersHorizontal class="w-3.5 h-3.5 text-primary" />
                                <span>{{ $t('wasm_sandbox.processing_queue', { count: items.length }) }}</span>
                            </div>
                            <Button variant="ghost"
                                class="h-6 px-2 text-[11px] font-bold text-primary hover:bg-primary/10 border-none cursor-pointer rounded gap-1"
                                :disabled="items.filter(i => i.status === 'done').length === 0 || hasPendingChanges"
                                @click="downloadAll">
                                <Download class="w-3 h-3" />
                                {{ $t('wasm_sandbox.download_all') }}
                            </Button>
                        </div>

                        <div
                            class="flex items-end gap-4 overflow-x-auto py-2 px-1 scrollbar-thin scrollbar-thumb-hairline scrollbar-track-transparent">
                            <div v-for="item in items" :key="item.id"
                                class="flex flex-col items-center gap-2 shrink-0 cursor-pointer group"
                                @click="selectItem(item.id)">
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
                                        @click.stop="removeItem(item.id)">
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

                    <!-- Mode Switcher Tabs (Prioritizing shadcn/vue) -->
                    <div class="flex justify-center shrink-0">
                        <Tabs v-model="viewMode" class="w-auto select-none ">
                            <TabsList class="grid w-60 grid-cols-2 bg-surface-2 border border-hairline rounded-lg">
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
                    <div v-if="viewMode === 'side-by-side'"
                        class="flex-1 grid grid-cols-1 md:grid-cols-2 gap-6 min-h-0 items-start">

                        <!-- Original Image Card -->
                        <Card
                            class="border border-hairline shadow-subtle flex flex-col overflow-hidden bg-surface-1 py-0 gap-0">
                            <CardHeader
                                class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 bg-surface-1/50 overflow-visible">
                                <CardTitle
                                    class="min-w-0 truncate text-[12px] leading-none font-bold uppercase tracking-wider text-ink-subtle flex items-center gap-1.5 overflow-visible">
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
                                        class="h-6 px-2 text-[11px] font-bold text-primary hover:bg-primary/10 border-none cursor-pointer rounded-md gap-1"
                                        @click="triggerReplaceInput">
                                        <ArrowLeftRight class="w-3 h-3" />
                                        {{ $t('wasm_sandbox.replace') }}
                                    </Button>
                                </div>
                            </CardHeader>
                            <!-- Image Display Box -->
                            <div class="relative w-full preview-canvas overflow-hidden select-none group rounded-b-xl"
                                :style="{ aspectRatio: previewAspectRatio }">
                                <div class="absolute inset-0 flex items-center justify-center">
                                    <img :src="originalUrl"
                                        class="h-full w-full object-contain transition-all duration-300"
                                        alt="Original image" />
                                </div>

                                <!-- Low Profile Bottom Interactive Bar -->
                                <div class="absolute bottom-0 left-0 right-0 bg-surface-1/90 backdrop-blur-md py-1 px-2.5 text-[11px] text-ink-subtle border-t border-hairline flex items-center justify-between gap-3 cursor-pointer opacity-0 group-hover:opacity-100 transition-opacity duration-200 z-20 animate-fade-in"
                                    @click="triggerReplaceInput">
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
                            class="border border-hairline shadow-subtle flex flex-col overflow-hidden bg-surface-1 relative py-0 gap-0">
                            <CardHeader
                                class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 bg-surface-1/50 overflow-visible">
                                <CardTitle
                                    class="min-w-0 truncate text-[12px] leading-none font-bold uppercase tracking-wider text-ink-subtle flex items-center gap-1.5 overflow-visible">
                                    <Zap class="w-3.5 h-3.5 text-primary shrink-0" />
                                    {{ $t('wasm_sandbox.optimized_preview') }}
                                </CardTitle>
                                <Badge v-if="processedUrl && metrics" variant="outline"
                                    class="h-5 px-1.5 font-mono text-[10px] border-hairline shrink-0">
                                    {{ formatBytes(metrics.size) }}
                                </Badge>
                            </CardHeader>
                            <div class="relative w-full preview-canvas overflow-hidden select-none rounded-b-xl"
                                :style="{ aspectRatio: previewAspectRatio }">
                                <div v-if="isProcessing"
                                    class="absolute inset-0 bg-canvas/70 backdrop-blur-md z-10 flex flex-col items-center justify-center gap-3 animate-fade-in">
                                    <div
                                        class="w-12 h-12 rounded-xl bg-primary/10 border border-primary/20 flex items-center justify-center text-primary shadow-xs">
                                        <Cpu class="w-6 h-6 animate-pulse" />
                                    </div>
                                    <span
                                        class="text-xs font-bold text-primary uppercase tracking-widest animate-pulse">{{
                                            $t('wasm_sandbox.processing') }}</span>
                                </div>
                                <div v-else-if="processError"
                                    class="absolute inset-0 flex items-center justify-center p-4 bg-surface-2/20">
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

                    <!-- View 2: Draggable Slider Comparison -->
                    <div v-else
                        class="flex-1 flex flex-col border border-hairline shadow-subtle bg-surface-1 rounded-xl overflow-hidden relative py-0">
                        <CardHeader
                            class="h-11 px-4 py-0 pb-0 [.border-b]:!pb-0 border-b border-hairline shrink-0 flex flex-row items-center justify-between gap-2 bg-surface-1/50 overflow-visible">
                            <CardTitle
                                class="min-w-0 truncate text-[12px] leading-none font-bold uppercase tracking-wider text-ink-subtle flex items-center gap-1.5 overflow-visible">
                                <ArrowLeftRight class="w-3.5 h-3.5 text-primary shrink-0" />
                                {{ $t('wasm_sandbox.view_slider') }}
                            </CardTitle>
                            <!-- Mini overlay info -->
                            <Badge v-if="metrics" variant="outline"
                                class="h-5 px-1.5 font-mono text-[10px] border-hairline bg-canvas shrink-0">
                                {{ formatBytes(metrics.size) }} ({{ metrics.savings < 0 ? metrics.savings.toFixed(1)
                                    + '%' : '+' + metrics.savings.toFixed(1) + '%' }}) </Badge>
                        </CardHeader>
                        <div ref="sliderContainer"
                            class="relative w-full overflow-hidden select-none cursor-ew-resize preview-canvas rounded-b-xl flex items-center justify-center"
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

                </div>

                <!-- Metrics Dashboard Toolbar -->
                <Card v-if="processedUrl && metrics"
                    class="border border-hairline shadow-subtle shrink-0 bg-surface-1/60 backdrop-blur">
                    <CardContent class="py-2 px-3 grid grid-cols-3 gap-4 text-center">
                        <div class="flex flex-col items-center">
                            <span class="text-[11px] uppercase font-bold tracking-wider text-ink-subtle">{{
                                $t('wasm_sandbox.metric_speed') }}</span>
                            <span class="text-base font-bold text-ink mt-1 font-mono flex items-baseline gap-0.5">
                                {{ metrics.duration > 100 ? (metrics.duration / 1000).toFixed(2) : metrics.duration.toFixed(1) }}
                                <span class="text-xs font-medium text-ink-subtle">{{ metrics.duration > 100 ? 's' : 'ms' }}</span>
                            </span>
                        </div>
                        <div class="flex flex-col items-center border-x border-hairline">
                            <span class="text-[11px] uppercase font-bold tracking-wider text-ink-subtle">{{
                                $t('wasm_sandbox.metric_size') }}</span>
                            <span class="text-base font-bold text-ink mt-1 font-mono">
                                {{ formatBytes(metrics.size) }}
                            </span>
                        </div>
                        <div class="flex flex-col items-center">
                            <span class="text-[11px] uppercase font-bold tracking-wider text-ink-subtle">{{
                                $t('wasm_sandbox.metric_savings') }}</span>
                            <div class="mt-0.5 flex justify-center">
                                <Badge v-if="metrics.savings < 0"
                                    class="bg-emerald-50 dark:bg-emerald-950/20 text-emerald-600 dark:text-emerald-400 font-bold border border-emerald-200/40 hover:bg-emerald-50 text-xs">
                                    {{ metrics.savings.toFixed(1) }}%
                                </Badge>
                                <Badge v-else
                                    class="bg-amber-50 dark:bg-amber-950/20 text-amber-600 dark:text-amber-400 font-bold border border-amber-200/40 hover:bg-amber-50 text-xs">
                                    +{{ metrics.savings.toFixed(1) }}%
                                </Badge>
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <!-- Metadata Card (Source Info when no metrics available) -->
                <div v-if="originalMetadata && !processedUrl" class="grid grid-cols-3 gap-4 shrink-0 select-none">
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
                    <div class="space-y-3 p-4 bg-surface-2/20 border border-hairline rounded-xl">
                        <div class="flex items-center justify-between">
                            <div class="flex flex-col gap-0.5">
                                <Label class="text-xs font-bold text-ink flex items-center gap-1 select-none">
                                    <Sparkles class="w-3.5 h-3.5 text-primary" />
                                    {{ $t('wasm_sandbox.auto_process') }}
                                </Label>
                                <span class="text-xs text-ink-subtle">{{ $t('wasm_sandbox.auto_process_desc') }}</span>
                            </div>
                            <button type="button"
                                class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                                :class="autoApply ? 'bg-primary' : 'bg-surface-2 border-hairline'"
                                :disabled="!originalFile" @click="toggleAutoApply">
                                <span
                                    class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out"
                                    :class="autoApply ? 'translate-x-4' : 'translate-x-0'" />
                            </button>
                        </div>

                        <!-- Manual Run Button -->
                        <Button v-if="!autoApply"
                            class="w-full h-10 text-xs font-bold shadow-sm cursor-pointer justify-center transition-all duration-200 border-none gap-1.5"
                            :class="hasPendingChanges ? 'bg-primary text-primary-foreground hover:bg-primary/95 animate-pulse bg-gradient-to-r from-primary to-violet-600' : 'bg-surface-2 border border-hairline text-ink-subtle hover:bg-surface-2'"
                            :disabled="items.length === 0 || isProcessing || !hasPendingChanges"
                            @click="runManualProcess">
                            <Cpu v-if="isProcessing" class="w-3.5 h-3.5 animate-pulse" />
                            <Play v-else class="w-3.5 h-3.5" />
                            {{ isProcessing ? $t('wasm_sandbox.processing') : (hasPendingChanges ?
                                $t('wasm_sandbox.apply_and_reprocess') :
                                $t('wasm_sandbox.all_changes_applied')) }}
                        </Button>
                        <span v-if="originalFile"
                            class="text-xs text-ink-subtle text-center block w-full mt-1.5 select-none leading-normal">
                            {{ $t('wasm_sandbox.drag_to_replace_hint') }}
                        </span>
                    </div>

                    <!-- Format Selection -->
                    <div class="space-y-3">
                        <Label class="text-xs font-bold uppercase tracking-wider text-ink-subtle block">{{
                            $t('wasm_sandbox.target_format') }}</Label>
                        <Select v-model="targetFormat" @update:modelValue="onParameterChange">
                            <SelectTrigger
                                class="h-9 border-hairline bg-canvas text-xs focus:ring-primary/20 focus:ring-1 cursor-pointer">
                                <SelectValue placeholder="Select format" />
                            </SelectTrigger>
                            <SelectContent class="bg-surface-1 border border-hairline shadow-sm text-xs">
                                <SelectItem value="webp">WebP (Lossless Pure Rust)</SelectItem>
                                <SelectItem value="jpeg">JPEG (Quality-Aware)</SelectItem>
                                <SelectItem value="png">PNG (Lossless Standard)</SelectItem>
                                <SelectItem value="avif">AVIF (Experimental Fast)</SelectItem>
                            </SelectContent>
                        </Select>

                        <!-- Quality setting -->
                        <div v-if="targetFormat === 'jpeg' || targetFormat === 'avif'" class="space-y-2 pt-2">
                            <div class="flex justify-between text-xs">
                                <Label class="font-bold text-ink">{{ $t('wasm_sandbox.quality') }}</Label>
                                <span class="font-bold text-primary font-mono">{{ compressionQuality }}%</span>
                            </div>
                            <input v-model.number="compressionQuality" type="range" min="1" max="100"
                                class="w-full accent-primary h-1 bg-surface-2 rounded-lg cursor-pointer"
                                @change="onParameterChange" />
                            <!-- Preset Quality Buttons -->
                            <div class="grid grid-cols-4 gap-1.5 pt-1 select-none">
                                <Button v-for="preset in [30, 60, 80, 95]" :key="preset" type="button" variant="outline"
                                    class="h-8 text-xs font-bold border-hairline cursor-pointer select-none transition-all duration-200"
                                    :class="{ 'bg-primary/5 text-primary border-primary/20': compressionQuality === preset }"
                                    @click="selectQualityPreset(preset)">
                                    {{ preset }}%
                                </Button>
                            </div>
                            <span class="text-xs text-ink-subtle leading-normal block pt-1">{{
                                $t('wasm_sandbox.quality_desc') }}</span>
                        </div>

                        <!-- Webp notice -->
                        <div v-else-if="targetFormat === 'webp'"
                            class="p-3.5 bg-surface-2/30 border border-hairline rounded-lg text-xs text-ink-subtle leading-relaxed flex gap-2">
                            <Info class="w-3.5 h-3.5 shrink-0 text-primary mt-0.5" />
                            <span>{{ $t('wasm_sandbox.webp_info') }}</span>
                        </div>
                    </div>

                    <Separator class="bg-hairline" />

                    <!-- Dimensions & Rescaling -->
                    <div class="space-y-4">
                        <div class="flex justify-between items-center">
                            <Label class="text-xs font-bold uppercase tracking-wider text-ink-subtle">{{
                                $t('wasm_sandbox.resize') }}</Label>
                            <div class="flex items-center gap-2">
                                <button type="button"
                                    class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                                    :class="enableResize ? 'bg-primary' : 'bg-surface-2 border-hairline'"
                                    :disabled="items.length === 0"
                                    @click="enableResize = !enableResize; onParameterChange()">
                                    <span
                                        class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out"
                                        :class="enableResize ? 'translate-x-4' : 'translate-x-0'" />
                                </button>
                                <span class="text-xs font-bold text-ink-subtle uppercase">{{
                                    $t('wasm_sandbox.limit_max_dimension') }}</span>
                            </div>
                        </div>

                        <!-- Collapsible resize parameters -->
                        <div v-if="enableResize" class="space-y-4 pt-1 animate-fade-in duration-200">
                            <!-- Aspect Ratio Lock -->
                            <div class="flex justify-between items-center">
                                <span class="text-xs text-ink-subtle font-medium">{{
                                    $t('wasm_sandbox.max_dimension_constraint') }}</span>
                                <div class="flex items-center gap-1 cursor-pointer select-none"
                                    @click="aspectLocked = !aspectLocked">
                                    <component :is="aspectLocked ? Link : Link2Off" class="w-3 h-3"
                                        :class="aspectLocked ? 'text-primary' : 'text-ink-subtle'" />
                                    <span class="text-xs font-bold"
                                        :class="aspectLocked ? 'text-primary' : 'text-ink-subtle'">
                                        {{ aspectLocked ? $t('wasm_sandbox.lock_ratio') :
                                            $t('wasm_sandbox.unlock_ratio') }}
                                    </span>
                                </div>
                            </div>

                            <!-- Dimensions Inputs -->
                            <div class="grid grid-cols-2 gap-3">
                                <div class="space-y-1">
                                    <span class="text-xs font-bold text-ink-subtle uppercase">{{
                                        $t('wasm_sandbox.width') }}</span>
                                    <Input v-model.number="resizeWidth" type="number"
                                        class="h-9 font-mono text-xs border-hairline focus:ring-1 focus:ring-primary/20 bg-canvas"
                                        :disabled="items.length === 0" @input="handleWidthInput" />
                                </div>
                                <div class="space-y-1">
                                    <span class="text-xs font-bold text-ink-subtle uppercase">{{
                                        $t('wasm_sandbox.height') }}</span>
                                    <Input v-model.number="resizeHeight" type="number"
                                        class="h-9 font-mono text-xs border-hairline focus:ring-1 focus:ring-primary/20 bg-canvas"
                                        :disabled="items.length === 0" @input="handleHeightInput" />
                                </div>
                            </div>

                            <!-- Scaling Filter Selection -->
                            <div class="space-y-1.5">
                                <span class="text-xs font-bold text-ink-subtle uppercase">{{
                                    $t('wasm_sandbox.scaling_filter') }}</span>
                                <Select v-model="resizeFilter" @update:modelValue="onParameterChange">
                                    <SelectTrigger
                                        class="h-9 border-hairline bg-canvas text-xs focus:ring-primary/20 focus:ring-1 cursor-pointer">
                                        <SelectValue placeholder="Select filter" />
                                    </SelectTrigger>
                                    <SelectContent class="bg-surface-1 border border-hairline shadow-sm text-xs">
                                        <SelectItem value="nearest">Nearest (Fastest)</SelectItem>
                                        <SelectItem value="triangle">Triangle (Linear)</SelectItem>
                                        <SelectItem value="catmull-rom">Catmull-Rom (Smooth)</SelectItem>
                                        <SelectItem value="gaussian">Gaussian (Soft)</SelectItem>
                                        <SelectItem value="lanczos3">Lanczos3 (Sharpest / Recommended)</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                        </div>

                        <!-- Explanatory note when disabled -->
                        <span v-else class="text-xs text-ink-subtle leading-normal block pt-1 select-none font-medium">
                            {{ $t('wasm_sandbox.resize_hint') }}
                        </span>
                    </div>

                    <Separator class="bg-hairline" />

                    <!-- Rotations & Flips -->
                    <div class="space-y-3">
                        <Label class="text-xs font-bold uppercase tracking-wider text-ink-subtle block">{{
                            $t('wasm_sandbox.transform') }}</Label>
                        <div class="grid grid-cols-2 gap-2">
                            <Button variant="outline"
                                class="h-9 text-xs font-medium border-hairline hover:bg-surface-2 cursor-pointer gap-1"
                                :class="{ 'bg-primary/5 text-primary border-primary/20 font-bold': rotateAngle === 90 }"
                                :disabled="!originalFile" @click="toggleRotate(90)">
                                <RotateCw class="w-3 h-3" />
                                90°
                            </Button>
                            <Button variant="outline"
                                class="h-9 text-xs font-medium border-hairline hover:bg-surface-2 cursor-pointer gap-1"
                                :class="{ 'bg-primary/5 text-primary border-primary/20 font-bold': rotateAngle === 180 }"
                                :disabled="!originalFile" @click="toggleRotate(180)">
                                <RotateCw class="w-3 h-3" />
                                180°
                            </Button>
                            <Button variant="outline"
                                class="h-9 text-xs font-medium border-hairline hover:bg-surface-2 cursor-pointer gap-1"
                                :class="{ 'bg-primary/5 text-primary border-primary/20 font-bold': flipH }"
                                :disabled="!originalFile" @click="toggleFlip('h')">
                                <FlipHorizontal class="w-3 h-3" />
                                {{ $t('wasm_sandbox.flip_h') }}
                            </Button>
                            <Button variant="outline"
                                class="h-9 text-xs font-medium border-hairline hover:bg-surface-2 cursor-pointer gap-1"
                                :class="{ 'bg-primary/5 text-primary border-primary/20 font-bold': flipV }"
                                :disabled="!originalFile" @click="toggleFlip('v')">
                                <FlipVertical class="w-3 h-3" />
                                {{ $t('wasm_sandbox.flip_v') }}
                            </Button>
                        </div>
                    </div>

                    <Separator class="bg-hairline" />

                    <!-- Visual Enhancements -->
                    <div class="space-y-4">
                        <Label class="text-xs font-bold uppercase tracking-wider text-ink-subtle block">{{
                            $t('wasm_sandbox.adjustments') }}</Label>

                        <!-- Grayscale -->
                        <div class="flex items-center justify-between">
                            <span class="text-xs font-semibold text-ink">{{ $t('wasm_sandbox.grayscale') }}</span>
                            <button type="button"
                                class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                                :class="grayscaleEnabled ? 'bg-primary' : 'bg-surface-2 border-hairline'"
                                :disabled="!originalFile" @click="toggleGrayscale">
                                <span
                                    class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out"
                                    :class="grayscaleEnabled ? 'translate-x-4' : 'translate-x-0'" />
                            </button>
                        </div>

                        <!-- Brightness -->
                        <div class="space-y-2">
                            <div class="flex justify-between text-xs">
                                <span class="font-medium text-ink-subtle">{{ $t('wasm_sandbox.brightness') }}</span>
                                <span class="font-mono text-ink font-bold"
                                    :class="{ 'text-primary': brightness !== 0 }">
                                    {{ brightness > 0 ? '+' : '' }}{{ brightness }}
                                </span>
                            </div>
                            <input v-model.number="brightness" type="range" min="-100" max="100"
                                class="w-full accent-primary h-1 bg-surface-2 rounded-lg cursor-pointer"
                                :disabled="!originalFile" @change="onParameterChange" />
                        </div>

                        <!-- Contrast -->
                        <div class="space-y-2">
                            <div class="flex justify-between text-xs">
                                <span class="font-medium text-ink-subtle">{{ $t('wasm_sandbox.contrast') }}</span>
                                <span class="font-mono text-ink font-bold" :class="{ 'text-primary': contrast !== 0 }">
                                    {{ contrast > 0 ? '+' : '' }}{{ contrast }}
                                </span>
                            </div>
                            <input v-model.number="contrast" type="range" min="-100" max="100"
                                class="w-full accent-primary h-1 bg-surface-2 rounded-lg cursor-pointer"
                                :disabled="!originalFile" @change="onParameterChange" />
                        </div>

                        <!-- Blur -->
                        <div class="space-y-2">
                            <div class="flex justify-between text-xs">
                                <span class="font-medium text-ink-subtle">{{ $t('wasm_sandbox.blur') }}</span>
                                <span class="font-mono text-ink font-bold" :class="{ 'text-primary': blurSigma > 0 }">
                                    {{ blurSigma.toFixed(1) }} px
                                </span>
                            </div>
                            <input v-model.number="blurSigma" type="range" min="0" max="10" step="0.5"
                                class="w-full accent-primary h-1 bg-surface-2 rounded-lg cursor-pointer"
                                :disabled="!originalFile" @change="onParameterChange" />
                        </div>
                    </div>

                </div>

                <!-- Control Sticky Footer -->
                <div class="py-5 px-5 border-t border-hairline bg-surface-2/80 backdrop-blur shrink-0 space-y-2">
                    <div v-if="originalFile" class="space-y-2 mb-2">
                        <Label for="output-filename"
                            class="text-xs font-bold uppercase tracking-wider text-ink-subtle">{{
                                $t('wasm_sandbox.output_filename') }}</Label>
                        <Input id="output-filename" v-model="customFilename"
                            class="h-9 font-mono text-xs border-hairline focus:border-primary/50 focus:ring-1 focus:ring-primary/20 bg-canvas"
                            placeholder="filename" />
                    </div>
                    <Button
                        class="w-full h-10 bg-primary hover:bg-primary-hover text-primary-foreground font-semibold rounded-lg text-xs cursor-pointer shadow-sm border-none gap-2 justify-center transition-all duration-200"
                        :disabled="!processedUrl || isProcessing || hasPendingChanges" @click="downloadImage">
                        <Download class="w-3.5 h-3.5" />
                        {{ $t('wasm_sandbox.download') }}
                    </Button>
                </div>

            </aside>

        </div>

        <!-- Hidden input for file selection (always present in DOM to support dynamic image replacement) -->
        <input ref="fileInput" type="file" class="hidden" accept="image/png, image/jpeg, image/webp, image/avif"
            @change="handleFileChange" />
    </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useComputeWasm } from '~/composables/useComputeWasm'
import {
    Upload, Image, AlertTriangle, AlertCircle, Zap, Cpu, XCircle, X, Loader2,
    Download, SlidersHorizontal, RotateCw, RotateCcw, Link, Link2Off,
    FlipHorizontal, FlipVertical, Info, ArrowLeftRight, Trash2, Play, Sparkles, Check, GripVertical
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

export type BatchImageItem = {
    id: string
    file: File
    originalUrl: string
    originalBuffer: Uint8Array | null
    metadata: { width: number; height: number; format: string; size: number } | null
    processedUrl: string
    processedBlob: Blob | null
    metrics: { duration: number; size: number; savings: number } | null
    error: string
    status: 'pending' | 'processing' | 'done' | 'failed'
    customFilename: string
    processedFormat?: string
}

const { initWasm, getMetadata, editImage, isReady, error: wasmError } = useComputeWasm()
const { t } = useI18n()

useHead({
    title: t('wasm_sandbox.title') + ' | Transcoder',
})

// Original & Batch states
const items = ref<BatchImageItem[]>([])
const selectedItemId = ref<string | null>(null)
const enableResize = ref<boolean>(false)
const isReplacingActiveItem = ref<boolean>(false)
const isProcessing = ref<boolean>(false)

const selectedItem = computed(() => items.value.find(i => i.id === selectedItemId.value) || null)

const previewAspectRatio = computed(() => {
    const metadata = selectedItem.value?.metadata
    if (!metadata?.width || !metadata?.height) {
        return '4 / 3'
    }
    return `${metadata.width} / ${metadata.height}`
})




// Writable Computed Mappings for backward compatibility
const originalFile = computed({
    get: () => selectedItem.value?.file || null,
    set: (val: File | null) => {
        if (selectedItem.value && val) {
            selectedItem.value.file = val
        }
    }
})

const originalUrl = computed({
    get: () => selectedItem.value?.originalUrl || '',
    set: (val: string) => {
        if (selectedItem.value && selectedItem.value.originalUrl && selectedItem.value.originalUrl !== val) {
            URL.revokeObjectURL(selectedItem.value.originalUrl)
        }
        if (selectedItem.value) selectedItem.value.originalUrl = val
    }
})

const originalMetadata = computed({
    get: () => selectedItem.value?.metadata || null,
    set: (val: { width: number; height: number; format: string; size: number } | null) => {
        if (selectedItem.value) selectedItem.value.metadata = val
    }
})

const originalBuffer = computed({
    get: () => selectedItem.value?.originalBuffer || null,
    set: (val: Uint8Array | null) => {
        if (selectedItem.value) selectedItem.value.originalBuffer = val
    }
})

const processedUrl = computed({
    get: () => selectedItem.value?.processedUrl || '',
    set: (val: string) => {
        if (selectedItem.value && selectedItem.value.processedUrl && selectedItem.value.processedUrl !== val) {
            URL.revokeObjectURL(selectedItem.value.processedUrl)
        }
        if (selectedItem.value) selectedItem.value.processedUrl = val
    }
})

const processedBlob = computed({
    get: () => selectedItem.value?.processedBlob || null,
    set: (val: Blob | null) => {
        if (selectedItem.value) selectedItem.value.processedBlob = val
    }
})

const processError = computed({
    get: () => selectedItem.value?.error || '',
    set: (val: string) => {
        if (selectedItem.value) selectedItem.value.error = val
    }
})

const metrics = computed({
    get: () => selectedItem.value?.metrics || null,
    set: (val: { duration: number; size: number; savings: number } | null) => {
        if (selectedItem.value) selectedItem.value.metrics = val
    }
})

const customFilename = computed({
    get: () => selectedItem.value?.customFilename || '',
    set: (val: string) => {
        if (selectedItem.value) selectedItem.value.customFilename = val
    }
})

const fileInput = ref<HTMLInputElement | null>(null)

// Workbench Controls & Views
const viewMode = ref<'side-by-side' | 'slider'>('slider')
const targetFormat = ref<string>('webp')
const compressionQuality = ref<number>(80)
const resizeWidth = ref<number>(0)
const resizeHeight = ref<number>(0)
const aspectLocked = ref<boolean>(true)
const originalAspectRatio = ref<number>(1)
const resizeFilter = ref<string>('lanczos3')
const rotateAngle = ref<number>(0)
const flipH = ref<boolean>(false)
const flipV = ref<boolean>(false)
const grayscaleEnabled = ref<boolean>(false)
const brightness = ref<number>(0)
const contrast = ref<number>(0)
const blurSigma = ref<number>(0)

// Manual Apply & Trigger States
const autoApply = ref<boolean>(false)
const hasPendingChanges = ref<boolean>(false)
const dragCounter = ref<number>(0)
const isDragActive = computed(() => dragCounter.value > 0)

// Slidable Comparison Drag States
const sliderContainer = ref<HTMLElement | null>(null)
const sliderPos = ref<number>(50)
const isDragging = ref<boolean>(false)

let processDebounceTimeout: any = null

onMounted(async () => {
    try {
        await initWasm()
    } catch (e) {
        console.error('WASM load error in page:', e)
    }
})

onBeforeUnmount(() => {
    clearDebounce()
    revokeUrls()
})

const clearDebounce = () => {
    if (processDebounceTimeout) {
        clearTimeout(processDebounceTimeout)
        processDebounceTimeout = null
    }
}

const revokeItemUrls = (item: BatchImageItem) => {
    if (item.originalUrl) URL.revokeObjectURL(item.originalUrl)
    if (item.processedUrl) URL.revokeObjectURL(item.processedUrl)
}

const revokeUrls = () => {
    items.value.forEach(revokeItemUrls)
}

const resetSandbox = () => {
    revokeUrls()
    items.value = []
    selectedItemId.value = null
    isReplacingActiveItem.value = false

    // Reset controls & view mode
    viewMode.value = 'slider'
    sliderPos.value = 50
    targetFormat.value = 'webp'
    compressionQuality.value = 80
    resizeWidth.value = 0
    resizeHeight.value = 0
    aspectLocked.value = true
    rotateAngle.value = 0
    flipH.value = false
    flipV.value = false
    grayscaleEnabled.value = false
    brightness.value = 0
    contrast.value = 0
    blurSigma.value = 0
    autoApply.value = false
    hasPendingChanges.value = false
    dragCounter.value = 0
    enableResize.value = false
}

// Trigger browser file select
const triggerFileInput = () => {
    isReplacingActiveItem.value = false
    fileInput.value?.click()
}

const triggerReplaceInput = () => {
    isReplacingActiveItem.value = true
    fileInput.value?.click()
}

const handleFileChange = (e: Event) => {
    const files = (e.target as HTMLInputElement).files
    if (files && files.length > 0) {
        const firstFile = files[0]
        if (isReplacingActiveItem.value && selectedItemId.value && firstFile) {
            replaceSelectedFile(firstFile)
            isReplacingActiveItem.value = false
        } else {
            addFilesToQueue(Array.from(files))
        }
    }
}

const handleDragEnter = (e: DragEvent) => {
    dragCounter.value++
}

const handleDragLeave = (e: DragEvent) => {
    dragCounter.value--
}

const handleDrop = (e: DragEvent) => {
    dragCounter.value = 0
    const files = e.dataTransfer?.files
    if (files && files.length > 0) {
        const firstFile = files[0]
        if (isReplacingActiveItem.value && selectedItemId.value && firstFile) {
            replaceSelectedFile(firstFile)
            isReplacingActiveItem.value = false
        } else {
            addFilesToQueue(Array.from(files))
        }
    }
}

const addFilesToQueue = (files: File[]) => {
    for (const file of files) {
        const id = Math.random().toString(36).substring(2, 9)
        const url = URL.createObjectURL(file)
        const baseName = file.name.substring(0, file.name.lastIndexOf('.'))

        const newItem: BatchImageItem = {
            id,
            file,
            originalUrl: url,
            originalBuffer: null,
            metadata: null,
            processedUrl: '',
            processedBlob: null,
            metrics: null,
            error: '',
            status: 'pending',
            customFilename: `${baseName}-processed`
        }

        items.value.push(newItem)
        if (!selectedItemId.value) {
            selectedItemId.value = id
        }

        // Asynchronously load and process this item
        loadAndProcessItem(newItem)
    }
}

const replaceSelectedFile = (file: File) => {
    const activeItem = selectedItem.value
    if (!activeItem) return

    // Revoke old URLs for this active item
    revokeItemUrls(activeItem)

    const baseName = file.name.substring(0, file.name.lastIndexOf('.'))

    // Update active item attributes in-place
    activeItem.file = file
    activeItem.originalUrl = URL.createObjectURL(file)
    activeItem.originalBuffer = null
    activeItem.metadata = null
    activeItem.processedUrl = ''
    activeItem.processedBlob = null
    activeItem.metrics = null
    activeItem.error = ''
    activeItem.status = 'pending'
    activeItem.customFilename = `${baseName}-processed`

    // Load and process replaced item
    loadAndProcessItem(activeItem)
}

const loadAndProcessItem = async (item: BatchImageItem) => {
    if (!item.originalBuffer) {
        try {
            const buffer = await item.file.arrayBuffer()
            item.originalBuffer = new Uint8Array(buffer)
        } catch (err: any) {
            console.error('Failed to read file buffer:', err)
            item.error = t('wasm_sandbox.read_file_failed', { error: err.message || String(err) })
            item.status = 'failed'
            return
        }
    }

    if (!item.metadata) {
        try {
            await initWasm()
            if (isReady.value && item.originalBuffer) {
                const metaJsonStr = await getMetadata(item.originalBuffer)
                const meta = JSON.parse(metaJsonStr)
                item.metadata = {
                    width: meta.width,
                    height: meta.height,
                    format: meta.format,
                    size: item.file.size
                }

                // If this item is currently selected, update aspect ratio guidelines
                if (selectedItemId.value === item.id) {
                    originalAspectRatio.value = meta.width / meta.height
                    if (!enableResize.value) {
                        resizeWidth.value = meta.width
                        resizeHeight.value = meta.height
                    }
                }
            }
        } catch (e: any) {
            console.error('WASM metadata extract failed:', e)
            item.error = t('wasm_sandbox.parse_meta_failed', { error: e.message || String(e) })
            item.status = 'failed'
            return
        }
    }

    triggerQueueProcessing()
}

let isQueueProcessing = false
const triggerQueueProcessing = async () => {
    if (isQueueProcessing) return
    isQueueProcessing = true

    try {
        while (true) {
            const pendingItem = items.value.find(item => item.status === 'pending')
            if (!pendingItem) break

            pendingItem.status = 'processing'
            if (selectedItemId.value === pendingItem.id) {
                isProcessing.value = true
            }

            try {
                await processSingleItem(pendingItem)
                pendingItem.status = 'done'
            } catch (err: any) {
                console.error(`Error processing item ${pendingItem.id}:`, err)
                pendingItem.status = 'failed'
                pendingItem.error = err.message || String(err)
            } finally {
                if (selectedItemId.value === pendingItem.id) {
                    isProcessing.value = false
                }
            }
        }
        hasPendingChanges.value = false
    } finally {
        isQueueProcessing = false
    }
}

const processSingleItem = async (item: BatchImageItem) => {
    if (!item.originalBuffer || !isReady.value) return

    // Brief tick to allow UI threads to update
    await new Promise((resolve) => setTimeout(resolve, 10))

    // Proportional dimensions calculations under max constraint box
    let targetW = item.metadata ? item.metadata.width : 0
    let targetH = item.metadata ? item.metadata.height : 0

    if (enableResize.value && resizeWidth.value > 0 && resizeHeight.value > 0 && item.metadata) {
        const scale = Math.min(resizeWidth.value / item.metadata.width, resizeHeight.value / item.metadata.height)
        targetW = Math.round(item.metadata.width * scale)
        targetH = Math.round(item.metadata.height * scale)
    }

    const ops: any[] = []

    // Grayscale
    if (grayscaleEnabled.value) {
        ops.push({ type: 'grayscale' })
    }

    // Adjustments
    if (brightness.value !== 0 || contrast.value !== 0) {
        ops.push({
            type: 'adjust',
            brightness: brightness.value,
            contrast: contrast.value
        })
    }

    // Blur
    if (blurSigma.value > 0) {
        ops.push({
            type: 'blur',
            sigma: blurSigma.value
        })
    }

    // Resize
    if (item.metadata && targetW > 0 && targetH > 0 && (targetW !== item.metadata.width || targetH !== item.metadata.height)) {
        ops.push({
            type: 'resize',
            width: targetW,
            height: targetH,
            filter: resizeFilter.value
        })
    }

    // Flip & Rotate
    if (flipH.value) {
        ops.push({ type: 'flip', direction: 'horizontal' })
    }
    if (flipV.value) {
        ops.push({ type: 'flip', direction: 'vertical' })
    }
    if (rotateAngle.value > 0) {
        ops.push({ type: 'rotate', degree: rotateAngle.value })
    }

    const startTime = performance.now()
    const opsJson = JSON.stringify(ops)

    const resultBytes = await editImage(
        item.originalBuffer,
        opsJson,
        targetFormat.value,
        compressionQuality.value
    )

    const duration = performance.now() - startTime

    // Revoke old processed URL if existing
    if (item.processedUrl) {
        URL.revokeObjectURL(item.processedUrl)
    }

    const mime = getMimeType(targetFormat.value)
    item.processedBlob = new Blob([resultBytes as BlobPart], { type: mime })
    item.processedUrl = URL.createObjectURL(item.processedBlob)
    item.processedFormat = targetFormat.value

    const origSize = item.metadata ? item.metadata.size : item.file.size
    const savings = ((resultBytes.length - origSize) / origSize) * 100

    item.metrics = {
        duration,
        size: resultBytes.length,
        savings
    }
}

const loadOriginalImage = (file: File) => {
    if (isReplacingActiveItem.value && selectedItemId.value) {
        replaceSelectedFile(file)
        isReplacingActiveItem.value = false
    } else {
        addFilesToQueue([file])
    }
}

const removeItem = (id: string) => {
    const idx = items.value.findIndex(item => item.id === id)
    if (idx !== -1) {
        const item = items.value[idx]
        if (item) {
            revokeItemUrls(item)
            items.value.splice(idx, 1)

            if (selectedItemId.value === id) {
                if (items.value.length > 0) {
                    const fallbackItem = items.value[Math.max(0, idx - 1)]
                    if (fallbackItem) {
                        selectedItemId.value = fallbackItem.id
                    }
                    const newActive = selectedItem.value
                    if (newActive && newActive.metadata) {
                        originalAspectRatio.value = newActive.metadata.width / newActive.metadata.height
                    }
                } else {
                    selectedItemId.value = null
                }
            }
        }
    }
}

const selectItem = (id: string) => {
    selectedItemId.value = id
    const active = selectedItem.value
    if (active && active.metadata) {
        originalAspectRatio.value = active.metadata.width / active.metadata.height
        if (!enableResize.value) {
            resizeWidth.value = active.metadata.width
            resizeHeight.value = active.metadata.height
        }
    }
}

// Drag & Slide comparison logic
let containerRect: { left: number; width: number } | null = null

const handleDrag = (e: MouseEvent | TouchEvent) => {
    if (!containerRect) return
    const clientX = 'touches' in e ? (e.touches[0]?.clientX ?? 0) : (e as MouseEvent).clientX
    const offsetX = clientX - containerRect.left
    const percentage = Math.max(0, Math.min(100, (offsetX / containerRect.width) * 100))
    sliderPos.value = percentage
}

const startDrag = (e: MouseEvent | TouchEvent) => {
    e.preventDefault()
    if (!sliderContainer.value) return
    const rect = sliderContainer.value.getBoundingClientRect()
    containerRect = {
        left: rect.left,
        width: rect.width || 1
    }
    isDragging.value = true
    handleDrag(e)
    window.addEventListener('mousemove', handleDrag)
    window.addEventListener('mouseup', stopDrag)
    window.addEventListener('touchmove', handleDrag, { passive: true })
    window.addEventListener('touchend', stopDrag)
}

const stopDrag = () => {
    isDragging.value = false
    containerRect = null
    window.removeEventListener('mousemove', handleDrag)
    window.removeEventListener('mouseup', stopDrag)
    window.removeEventListener('touchmove', handleDrag)
    window.removeEventListener('touchend', stopDrag)
}

// Manual Apply Trigger & Quality Presets Handlers
const toggleAutoApply = () => {
    autoApply.value = !autoApply.value
    if (autoApply.value && hasPendingChanges.value) {
        debouncedProcess()
        hasPendingChanges.value = false
    }
}

const onParameterChange = () => {
    hasPendingChanges.value = true
    if (autoApply.value) {
        debouncedProcess()
    }
}

const selectQualityPreset = (preset: number) => {
    compressionQuality.value = preset
    onParameterChange()
}

const reprocessAllItems = () => {
    items.value.forEach(item => {
        item.status = 'pending'
    })
    triggerQueueProcessing()
}

const runManualProcess = async () => {
    if (isQueueProcessing) return
    reprocessAllItems()
    hasPendingChanges.value = false
}

// Dynamic Aspect Ratio Resizing
const handleWidthInput = () => {
    if (aspectLocked.value && originalAspectRatio.value && resizeWidth.value > 0) {
        resizeHeight.value = Math.round(resizeWidth.value / originalAspectRatio.value)
    }
    onParameterChange()
}

const handleHeightInput = () => {
    if (aspectLocked.value && originalAspectRatio.value && resizeHeight.value > 0) {
        resizeWidth.value = Math.round(resizeHeight.value * originalAspectRatio.value)
    }
    onParameterChange()
}

const toggleRotate = (angle: number) => {
    rotateAngle.value = rotateAngle.value === angle ? 0 : angle
    onParameterChange()
}

const toggleFlip = (dir: 'h' | 'v') => {
    if (dir === 'h') flipH.value = !flipH.value
    if (dir === 'v') flipV.value = !flipV.value
    onParameterChange()
}

const toggleGrayscale = () => {
    grayscaleEnabled.value = !grayscaleEnabled.value
    onParameterChange()
}

const debouncedProcess = () => {
    clearDebounce()
    processDebounceTimeout = setTimeout(() => {
        reprocessAllItems()
    }, 250)
}

const processImage = async () => {
    if (selectedItem.value) {
        selectedItem.value.status = 'pending'
        await triggerQueueProcessing()
    }
}

const getMimeType = (format: string): string => {
    switch (format.toLowerCase()) {
        case 'png': return 'image/png'
        case 'webp': return 'image/webp'
        case 'avif': return 'image/avif'
        case 'jpeg':
        case 'jpg':
        default:
            return 'image/jpeg'
    }
}

const downloadImage = () => {
    if (!processedBlob.value || !selectedItem.value) return

    const fmt = selectedItem.value.processedFormat || targetFormat.value
    const ext = fmt.toLowerCase() === 'jpeg' ? 'jpg' : fmt.toLowerCase()
    const filename = `${customFilename.value || 'processed-image'}.${ext}`

    const link = document.createElement('a')
    link.href = processedUrl.value
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
}

const downloadAll = async () => {
    const { zipSync } = await import('fflate')
    const files: Record<string, Uint8Array> = {}

    for (const item of items.value) {
        if (item.processedBlob && item.status === 'done') {
            const buffer = new Uint8Array(await item.processedBlob.arrayBuffer())
            const fmt = item.processedFormat || targetFormat.value
            const ext = fmt.toLowerCase() === 'jpeg' ? 'jpg' : fmt.toLowerCase()
            const filename = `${item.customFilename || 'processed'}.${ext}`
            files[filename] = buffer
        }
    }

    if (Object.keys(files).length === 0) return

    const zipped = zipSync(files)
    const zipBlob = new Blob([zipped], { type: 'application/zip' })
    const zipUrl = URL.createObjectURL(zipBlob)

    const link = document.createElement('a')
    link.href = zipUrl
    link.download = `transcoded-images.zip`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(zipUrl)
}

const formatBytes = (bytes: number): string => {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}
</script>

<style scoped>
/* High-performance styling overrides for ranges to match dub system theme */
input[type="range"] {
    -webkit-appearance: none;
    appearance: none;
    width: 100%;
}

input[type="range"]::-webkit-slider-thumb {
    -webkit-appearance: none;
    height: 12px;
    width: 12px;
    border-radius: 50%;
    background: var(--color-linear-violet, #5e6ad2);
    cursor: pointer;
    margin-top: -4px;
    border: none;
    box-shadow: rgba(0, 0, 0, 0.1) 0px 1px 3px 0px;
    transition: transform 0.1s ease-in-out;
}

input[type="range"]::-webkit-slider-thumb:hover {
    transform: scale(1.15);
}

.preview-canvas {
    background-color: var(--surface-2);
    background-image: radial-gradient(var(--hairline-strong) 1px, transparent 1px);
    background-size: 16px 16px;
    background-position: center;
}

input[type="range"]::-webkit-slider-runnable-track {
    width: 100%;
    height: 4px;
    background: var(--color-ash-gray, #f4f4f5);
    border-radius: 2px;
    border: none;
}
</style>
