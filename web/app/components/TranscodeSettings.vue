<template>
    <div class="space-y-6">
        <!-- 1. Source Probe Summary (Read-Only) -->
        <div v-if="sourceProbe" class="p-3.5 rounded-xl bg-surface-2/60 border border-hairline space-y-2">
            <div class="flex items-center justify-between text-xs">
                <span class="font-semibold text-ink flex items-center gap-1.5">
                    <Eye class="w-3.5 h-3.5 text-primary" />
                    {{ $t("new_task.source_probe.title") }}
                </span>
                <span class="text-[11px] font-mono text-ink-subtle">
                    {{ sourceProbe.width && sourceProbe.height ? `${sourceProbe.width} × ${sourceProbe.height}` : '' }}
                </span>
            </div>
            <div class="flex flex-wrap items-center gap-1.5">
                <span v-if="sourceProbe.format" class="px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-surface-3 border border-hairline text-ink uppercase">
                    {{ sourceProbe.format }}
                </span>
                <span v-if="sourceProbe.colorSpace" class="px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-surface-3 border border-hairline text-ink">
                    {{ sourceProbe.colorSpace }}
                </span>
                <span v-if="sourceProbe.bitDepth" class="px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-surface-3 border border-hairline text-ink">
                    {{ sourceProbe.bitDepth }}-bit
                </span>
                <span class="px-2 py-0.5 rounded text-[11px] font-mono font-medium border"
                    :class="sourceProbe.isHdr ? 'bg-amber-500/10 border-amber-500/20 text-amber-600 dark:text-amber-400' : 'bg-surface-3 border-hairline text-ink-subtle'">
                    {{ sourceProbe.isHdr ? 'HDR' : 'SDR' }}
                </span>
                <span v-if="sourceProbe.hasAlpha" class="px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-surface-3 border border-hairline text-ink">
                    Alpha
                </span>
                <span v-if="sourceProbe.hasIcc" class="px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-surface-3 border border-hairline text-ink">
                    ICC
                </span>
            </div>
        </div>

        <!-- 2. Target Format Selector (Top-level Lever 1) -->
        <div class="space-y-2">
            <Label class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi flex items-center justify-between">
                <span>{{ $t("new_task.target_format") }}</span>
                <span class="text-[11px] font-normal text-ink-subtle lowercase">
                    {{ selectedFormatDesc }}
                </span>
            </Label>
            <div class="grid grid-cols-2 sm:grid-cols-5 gap-2">
                <button
                    v-for="fmt in availableFormats"
                    :key="fmt.code"
                    type="button"
                    :disabled="disabled"
                    @click="setTargetFormat(fmt.code)"
                    class="relative p-2.5 rounded-xl border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer flex flex-col justify-between h-20"
                    :class="
                        targetFormat === fmt.code
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20 shadow-xs'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center justify-between w-full">
                        <span class="text-xs font-bold text-ink">{{ fmt.label }}</span>
                        <span
                            v-if="fmt.tag"
                            class="text-[9px] px-1.5 py-0.5 rounded font-mono"
                            :class="
                                fmt.tag === '推荐'
                                    ? 'bg-primary/20 text-primary font-semibold'
                                    : 'bg-surface-3 text-ink-subtle'
                            "
                        >
                            {{ fmt.tag }}
                        </span>
                    </div>
                    <span class="text-[10px] text-ink-subtle leading-tight line-clamp-2">
                        {{ fmt.desc }}
                    </span>
                </button>
            </div>
        </div>

        <!-- 3. Quality & Compression Profile (Top-level Lever 2) -->
        <div class="space-y-3 p-4 rounded-xl bg-surface-1 border border-hairline">
            <div class="flex items-center justify-between">
                <Label class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi">
                    {{ $t("new_task.optimize_template") }}
                </Label>
                <div class="flex items-center gap-2">
                    <span class="text-[11px] text-ink-subtle font-medium">{{ $t("new_task.expert_mode") }}</span>
                    <Switch
                        :checked="expertMode"
                        :disabled="disabled"
                        @update:checked="(val: boolean) => emit('update:expertMode', val)"
                    />
                </div>
            </div>

            <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
                <button
                    type="button"
                    :disabled="disabled"
                    @click="applyProfile('balanced')"
                    class="p-2.5 rounded-xl border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        selectedProfile === 'balanced'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20 shadow-xs'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center gap-1.5 text-xs font-bold text-ink">
                        <Zap class="w-3.5 h-3.5 text-amber-500" />
                        <span>{{ $t("new_task.profile_balanced") }}</span>
                    </div>
                    <span class="block text-[10px] text-ink-subtle mt-0.5">兼顾画质与体积</span>
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="applyProfile('size')"
                    class="p-2.5 rounded-xl border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        selectedProfile === 'size'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20 shadow-xs'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center gap-1.5 text-xs font-bold text-ink">
                        <HardDrive class="w-3.5 h-3.5 text-emerald-500" />
                        <span>{{ $t("new_task.profile_size") }}</span>
                    </div>
                    <span class="block text-[10px] text-ink-subtle mt-0.5">极致压缩高节省</span>
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="applyProfile('speed')"
                    class="p-2.5 rounded-xl border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        selectedProfile === 'speed'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20 shadow-xs'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center gap-1.5 text-xs font-bold text-ink">
                        <Gauge class="w-3.5 h-3.5 text-blue-500" />
                        <span>{{ $t("new_task.profile_speed") }}</span>
                    </div>
                    <span class="block text-[10px] text-ink-subtle mt-0.5">高速编码低等待</span>
                </button>

                <div
                    class="p-2.5 rounded-xl border text-left flex flex-col justify-center"
                    :class="
                        selectedProfile === 'custom'
                            ? 'border-amber-500/50 bg-amber-500/10 ring-1 ring-amber-500/20'
                            : 'border-hairline bg-surface-2/40 opacity-60'
                    "
                >
                    <div class="flex items-center gap-1.5 text-xs font-bold text-ink">
                        <Sliders class="w-3.5 h-3.5 text-amber-500" />
                        <span>{{ $t("new_task.profile_custom") }}</span>
                    </div>
                    <span class="block text-[10px] text-ink-subtle mt-0.5">手动精细微调</span>
                </div>
            </div>

            <!-- Quality Slider (When Applicable) -->
            <div v-if="hasQualityControl" class="space-y-2 pt-2 border-t border-hairline/60">
                <div class="flex justify-between items-center text-xs">
                    <Label class="font-bold text-ink">{{ $t("new_task.quality_label") }}</Label>
                    <span class="font-mono font-bold text-primary text-sm">{{ engineParams.quality ?? 75 }}</span>
                </div>
                <Slider
                    :model-value="[engineParams.quality ?? 75]"
                    :min="1"
                    :max="100"
                    :step="1"
                    :disabled="disabled"
                    @update:model-value="(val) => updateParam('quality', val?.[0])"
                />
                <p class="text-[11px] text-ink-subtle">
                    {{ $t("new_task.quality_desc") }}
                </p>
            </div>

            <!-- JXL-specific Distance/Quality Toggle -->
            <div v-if="targetFormat === 'jxl'" class="space-y-3 pt-2 border-t border-hairline/60">
                <div class="flex items-center justify-between">
                    <Label class="text-xs font-bold text-ink">{{ $t("new_task.jxl.encoding_mode") }}</Label>
                    <div class="flex items-center gap-2">
                        <span class="text-xs text-ink-subtle">
                            {{ engineParams.lossless ? $t("new_task.jxl.lossless") : $t("new_task.jxl.lossy") }}
                        </span>
                        <Switch
                            :checked="engineParams.lossless ?? false"
                            :disabled="disabled"
                            @update:checked="handleJxlLosslessToggle"
                        />
                    </div>
                </div>
                <div v-if="!engineParams.lossless" class="space-y-2">
                    <div class="flex justify-between items-center text-xs">
                        <Label class="font-bold text-ink">{{ $t("new_task.jxl.visual_distance") }}</Label>
                        <span class="font-mono font-bold text-primary text-sm">{{ engineParams.distance ?? 1.0 }}</span>
                    </div>
                    <Slider
                        :model-value="[engineParams.distance ?? 1.0]"
                        :min="0.1"
                        :max="15.0"
                        :step="0.1"
                        :disabled="disabled"
                        @update:model-value="(val) => updateParam('distance', val?.[0])"
                    />
                    <p class="text-[11px] text-ink-subtle">
                        {{ $t("new_task.jxl.distance_desc") }}
                    </p>
                </div>
                <div v-else class="space-y-1">
                    <div class="p-2.5 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-[11px] text-emerald-700 dark:text-emerald-300">
                        {{ $t("new_task.jxl.lossless_desc") }}
                    </div>
                    <p class="text-[10px] text-amber-600 dark:text-amber-400">
                        * 注：无损直通重构要求原始 DCT 字节 100% 对应，不支持同时进行 HDR 映射、色调修改或尺寸缩放。
                    </p>
                </div>
            </div>
        </div>

        <!-- 4. Dimension & Resolution Limits (Top-level Lever 3) -->
        <div class="p-4 rounded-xl bg-surface-1 border border-hairline space-y-3">
            <div class="flex items-center justify-between">
                <div class="space-y-0.5">
                    <Label class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi flex items-center gap-1.5">
                        <Maximize2 class="w-3.5 h-3.5 text-primary" />
                        {{ $t("new_task.dimensions.title") }}
                    </Label>
                    <p class="text-[11px] text-ink-subtle">
                        {{ $t("new_task.dimensions.subtitle") }}
                    </p>
                </div>
            </div>

            <!-- Resize Mode Tabs -->
            <div class="grid grid-cols-3 gap-2">
                <button
                    type="button"
                    :disabled="disabled"
                    @click="setResizeMode('original')"
                    class="p-2.5 rounded-lg border text-center transition-all duration-200 disabled:opacity-40 cursor-pointer text-xs font-semibold"
                    :class="
                        resizeMode === 'original'
                            ? 'border-primary/50 bg-primary/10 text-primary ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 text-ink hover:bg-surface-3'
                    "
                >
                    {{ $t("new_task.dimensions.mode_original") }}
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="setResizeMode('long_edge')"
                    class="p-2.5 rounded-lg border text-center transition-all duration-200 disabled:opacity-40 cursor-pointer text-xs font-semibold"
                    :class="
                        resizeMode === 'long_edge'
                            ? 'border-primary/50 bg-primary/10 text-primary ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 text-ink hover:bg-surface-3'
                    "
                >
                    {{ $t("new_task.dimensions.mode_long_edge") }}
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="setResizeMode('custom')"
                    class="p-2.5 rounded-lg border text-center transition-all duration-200 disabled:opacity-40 cursor-pointer text-xs font-semibold"
                    :class="
                        resizeMode === 'custom'
                            ? 'border-primary/50 bg-primary/10 text-primary ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 text-ink hover:bg-surface-3'
                    "
                >
                    {{ $t("new_task.dimensions.mode_custom") }}
                </button>
            </div>

            <!-- Long edge controls -->
            <div v-if="resizeMode === 'long_edge'" class="space-y-2 pt-1">
                <div class="flex flex-wrap gap-1.5">
                    <button
                        v-for="preset in [3840, 2560, 1920, 1280]"
                        :key="preset"
                        type="button"
                        :disabled="disabled"
                        @click="setLongEdge(preset)"
                        class="px-2.5 py-1 rounded-md text-xs font-mono font-medium border transition-colors cursor-pointer"
                        :class="
                            maxLongEdge === preset
                                ? 'bg-primary text-primary-foreground border-primary'
                                : 'bg-surface-2 border-hairline text-ink hover:bg-surface-3'
                        "
                    >
                        {{ preset === 3840 ? '4K' : preset === 2560 ? '2K' : preset === 1920 ? '1080P' : '720P' }} ({{ preset }}px)
                    </button>
                </div>
            </div>

            <!-- Custom Width x Height controls -->
            <div v-if="resizeMode === 'custom'" class="grid grid-cols-2 gap-3 pt-1">
                <div class="space-y-1">
                    <Label class="text-[11px] font-medium text-ink-subtle">{{ $t("new_task.dimensions.max_width") }} (px)</Label>
                    <Input
                        type="number"
                        :min="1"
                        :max="16384"
                        :model-value="maxWidth"
                        :disabled="disabled"
                        @update:model-value="val => setCustomDimension(Number(val), maxHeight)"
                        class="h-9 rounded-lg bg-surface-2 border-hairline text-xs font-mono text-ink"
                    />
                </div>
                <div class="space-y-1">
                    <Label class="text-[11px] font-medium text-ink-subtle">{{ $t("new_task.dimensions.max_height") }} (px)</Label>
                    <Input
                        type="number"
                        :min="1"
                        :max="16384"
                        :model-value="maxHeight"
                        :disabled="disabled"
                        @update:model-value="val => setCustomDimension(maxWidth, Number(val))"
                        class="h-9 rounded-lg bg-surface-2 border-hairline text-xs font-mono text-ink"
                    />
                </div>
            </div>

            <p v-if="resizeMode !== 'original'" class="text-[11px] text-ink-subtle">
                {{ $t("new_task.dimensions.aspect_ratio_hint") }}
            </p>
        </div>

        <!-- 5. Color & Delivery Target (Top-level Lever 4 - No disable switch, default auto) -->
        <div class="p-4 rounded-xl bg-surface-1 border border-hairline space-y-3">
            <div class="space-y-1">
                <div class="flex items-center justify-between">
                    <h4 class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi flex items-center gap-1.5">
                        <Sparkles class="w-3.5 h-3.5 text-primary" />
                        {{ $t("new_task.color_pipeline.title") }}
                    </h4>
                    <span class="text-[11px] text-emerald-600 dark:text-emerald-400 font-medium flex items-center gap-1">
                        <Check class="w-3 h-3" />
                        {{ $t("new_task.color_pipeline.auto_guarantee") }}
                    </span>
                </div>
                <p class="text-[11px] text-ink-subtle">
                    {{ $t("new_task.color_pipeline.subtitle_v2") }}
                </p>
            </div>

            <!-- Intent Selector: Auto / Standard SDR -->
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5 pt-1">
                <button
                    type="button"
                    :disabled="disabled"
                    @click="selectColorIntent('auto')"
                    class="text-left p-3 rounded-lg border transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        colorIntent === 'auto'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center justify-between">
                        <span class="block text-xs font-bold text-ink">
                            {{ $t("new_task.color_pipeline.mode_auto") }}
                        </span>
                        <span class="text-[10px] px-1.5 py-0.5 rounded font-mono bg-primary/20 text-primary font-semibold">
                            {{ $t("new_task.jxl.recommended") }}
                        </span>
                    </div>
                    <span class="block text-[11px] text-ink-subtle leading-snug mt-1">
                        {{ $t("new_task.color_pipeline.mode_auto_desc_v2") }}
                    </span>
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="selectColorIntent('sdr')"
                    class="text-left p-3 rounded-lg border transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        colorIntent === 'sdr'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <span class="block text-xs font-bold text-ink">
                        {{ $t("new_task.color_pipeline.mode_sdr") }}
                    </span>
                    <span class="block text-[11px] text-ink-subtle leading-snug mt-1">
                        {{ $t("new_task.color_pipeline.mode_sdr_desc_v2") }}
                    </span>
                </button>
            </div>

            <!-- Dithering Status Notice (Automatic When Downsampling Bit Depth) -->
            <div v-if="colorEncoding.nominal_bit_depth === 8 && colorEncoding.dither"
                class="p-2 rounded-lg bg-surface-2/70 border border-hairline flex items-center justify-between text-[11px] text-ink-subtle">
                <span class="flex items-center gap-1.5">
                    <Sparkles class="w-3.5 h-3.5 text-primary" />
                    {{ $t("new_task.color_pipeline.dither_active_notice") }}
                </span>
                <span class="font-mono text-ink text-[10px] bg-surface-3 px-1.5 py-0.5 rounded">
                    Blue Noise
                </span>
            </div>
        </div>

        <!-- 5. Metadata Policy (Top-level Lever 4) -->
        <div class="p-4 rounded-xl bg-surface-1 border border-hairline space-y-3">
            <div class="flex items-center justify-between">
                <div class="space-y-0.5">
                    <Label class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi flex items-center gap-1.5">
                        <FileText class="w-3.5 h-3.5 text-primary" />
                        {{ $t("new_task.metadata.title") }}
                    </Label>
                    <p class="text-[11px] text-ink-subtle">
                        {{ $t("new_task.metadata.subtitle") }}
                    </p>
                </div>
            </div>

            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                <button
                    type="button"
                    :disabled="disabled"
                    @click="emit('update:profilePolicy', 'preserve_source')"
                    class="p-3 rounded-lg border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        profilePolicy === 'preserve_source'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink">{{ $t("new_task.metadata.policy_preserve") }}</span>
                        <span class="text-[9px] px-1.5 py-0.5 rounded font-mono bg-primary/20 text-primary font-semibold">
                            {{ $t("new_task.jxl.recommended") }}
                        </span>
                    </div>
                    <span class="block text-[11px] text-ink-subtle mt-1 leading-snug">
                        {{ $t("new_task.metadata.policy_preserve_desc") }}
                    </span>
                </button>

                <button
                    type="button"
                    :disabled="disabled"
                    @click="emit('update:profilePolicy', 'strip')"
                    class="p-3 rounded-lg border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="
                        profilePolicy === 'strip'
                            ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20'
                            : 'border-hairline bg-surface-2 hover:bg-surface-3'
                    "
                >
                    <span class="block text-xs font-bold text-ink">{{ $t("new_task.metadata.policy_strip") }}</span>
                    <span class="block text-[11px] text-ink-subtle mt-1 leading-snug">
                        {{ $t("new_task.metadata.policy_strip_desc") }}
                    </span>
                </button>
            </div>
        </div>

        <!-- 6. Collapsible Advanced Overrides (Bit Depth & Chroma Subsampling) -->
        <div class="border border-hairline rounded-xl overflow-hidden bg-surface-1">
            <button
                type="button"
                @click="showAdvancedColor = !showAdvancedColor"
                class="w-full px-4 py-3 flex items-center justify-between text-xs font-bold text-ink hover:bg-surface-2/60 transition-colors cursor-pointer"
            >
                <div class="flex items-center gap-2">
                    <Sliders class="w-3.5 h-3.5 text-ink-subtle" />
                    <span>{{ $t("new_task.color_pipeline.advanced_overrides") }}</span>
                </div>
                <component :is="showAdvancedColor ? ChevronUp : ChevronDown" class="w-4 h-4 text-ink-subtle" />
            </button>

            <div v-if="showAdvancedColor" class="p-4 border-t border-hairline space-y-4 bg-surface-2/20">
                <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <!-- Bit Depth Control -->
                    <div class="space-y-1.5">
                        <Label class="text-xs font-bold text-ink">{{ $t("new_task.color_pipeline.bit_depth_label") }}</Label>
                        <Select
                            :model-value="String(supportsWideColor ? (colorEncoding.nominal_bit_depth ?? 10) : 8)"
                            @update:model-value="val => handleBitDepthChange(Number(val))"
                            :disabled="disabled || !supportsWideColor"
                        >
                            <SelectTrigger class="h-9 rounded-lg bg-surface-2 border-hairline text-ink text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                <SelectItem value="10">{{ $t("new_task.color_pipeline.bit_depth_10") }}</SelectItem>
                                <SelectItem value="8">{{ $t("new_task.color_pipeline.bit_depth_8") }}</SelectItem>
                            </SelectContent>
                        </Select>
                        <p class="text-[11px] text-ink-subtle">
                            {{ $t("new_task.color_pipeline.bit_depth_hint") }}
                        </p>
                    </div>

                    <!-- Chroma Subsampling (AVIF only) -->
                    <div v-if="supportsChromaControl" class="space-y-1.5">
                        <Label class="text-xs font-bold text-ink">
                            {{ $t("new_task.color_pipeline.chroma_label") }}
                        </Label>
                        <Select
                            :model-value="colorEncoding.chroma || 'auto'"
                            :disabled="disabled"
                            @update:model-value="val => updateColorEncoding('chroma', val)"
                        >
                            <SelectTrigger class="h-9 rounded-lg bg-surface-2 border-hairline text-ink text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                <SelectItem value="auto">{{ $t("new_task.color_pipeline.chroma_auto") }}</SelectItem>
                                <SelectItem value="yuv444">{{ $t("new_task.color_pipeline.chroma_444") }}</SelectItem>
                                <SelectItem value="yuv422">{{ $t("new_task.color_pipeline.chroma_422") }}</SelectItem>
                                <SelectItem value="yuv420">{{ $t("new_task.color_pipeline.chroma_420") }}</SelectItem>
                            </SelectContent>
                        </Select>
                        <p class="text-[11px] text-ink-subtle">
                            {{ $t("new_task.color_pipeline.chroma_fidelity_warning") }}
                        </p>
                    </div>
                </div>
            </div>
        </div>

        <!-- 7. Strict Mode (expert mode only) -->
        <div
            v-if="expertMode"
            class="p-4 rounded-xl bg-surface-1 border border-hairline flex items-center justify-between"
        >
            <div class="space-y-0.5">
                <h4 class="text-xs font-bold text-ink uppercase tracking-wider font-satoshi">
                    {{ $t("new_task.strict_mode.title") }}
                </h4>
                <p class="text-[11px] text-ink-subtle">
                    {{ $t("new_task.strict_mode.desc") }}
                </p>
            </div>
            <Switch
                :checked="strictMode"
                :disabled="disabled"
                @update:checked="(val: boolean) => emit('update:strictMode', val)"
                :aria-label="$t('new_task.strict_mode.title')"
            />
        </div>
    </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import Switch from "@/components/ui/switch/Switch.vue";
import Slider from "@/components/ui/slider/Slider.vue";
import { Input } from "@/components/ui/input";
import {
    Zap,
    HardDrive,
    Gauge,
    Sliders,
    Sparkles,
    Eye,
    Check,
    FileText,
    Maximize2,
    ChevronDown,
    ChevronUp,
} from "@lucide/vue";

export type ColorSpaceValue = "srgb" | "display_p3" | "bt2020_pq" | "bt2020_hlg" | "linear";

export interface SourceProbeSummary {
    format?: string;
    width?: number;
    height?: number;
    bitDepth?: number;
    colorSpace?: string;
    isHdr?: boolean;
    hasAlpha?: boolean;
    hasIcc?: boolean;
}

export interface ColorEncodingState {
    color_space?: ColorSpaceValue;
    chroma?: string;
    nominal_bit_depth?: number;
    tone_mapping?: string;
    dither?: boolean;
}

const props = defineProps<{
    targetFormat: string;
    selectedEngine: string;
    selectedProfile: "size" | "balanced" | "speed" | "custom";
    engineParams: Record<string, any>;
    colorEncoding: ColorEncodingState;
    profilePolicy: string;
    strictMode: boolean;
    expertMode: boolean;
    sourceProbe?: SourceProbeSummary | null;
    disabled?: boolean;
}>();

const emit = defineEmits<{
    (e: "update:targetFormat", val: string): void;
    (e: "update:selectedEngine", val: string): void;
    (e: "update:selectedProfile", val: "size" | "balanced" | "speed" | "custom"): void;
    (e: "update:engineParams", val: Record<string, any>): void;
    (e: "update:colorEncoding", val: ColorEncodingState): void;
    (e: "update:profilePolicy", val: string): void;
    (e: "update:strictMode", val: boolean): void;
    (e: "update:expertMode", val: boolean): void;
}>();

const availableFormats = [
    { code: "avif", label: "AVIF", tag: "推荐", desc: "下一代极致压缩与高清晰度" },
    { code: "heic", label: "HEIC", tag: "高效", desc: "苹果生态与移动端高保真" },
    { code: "webp", label: "WebP", tag: "全平台兼容", desc: "高兼容性与低带宽消耗" },
    { code: "jpeg", label: "JPEG", tag: "经典", desc: "传统照片格式与高适应性" },
    { code: "png", label: "PNG", tag: "无损", desc: "无损透明通道图形" },
    { code: "jxl", label: "JPEG XL", tag: "极客", desc: "现代无损重建与高质量" },
];

const colorIntent = ref<"auto" | "sdr">("auto");
const showAdvancedColor = ref(false);

// Resize / Dimension scale limits
const resizeMode = ref<"original" | "long_edge" | "custom">("original");
const maxLongEdge = ref<number>(1920);
const maxWidth = ref<number>(1920);
const maxHeight = ref<number>(1080);

const setResizeMode = (mode: "original" | "long_edge" | "custom") => {
    resizeMode.value = mode;
    syncResizeOps();
};

const setLongEdge = (edge: number) => {
    maxLongEdge.value = edge;
    syncResizeOps();
};

const setCustomDimension = (w: number, h: number) => {
    maxWidth.value = w;
    maxHeight.value = h;
    syncResizeOps();
};

const syncResizeOps = () => {
    const currentParams = { ...props.engineParams };
    if (resizeMode.value === "original") {
        delete currentParams.resize_limit;
    } else if (resizeMode.value === "long_edge") {
        currentParams.resize_limit = {
            mode: "long_edge",
            long_edge: maxLongEdge.value,
        };
    } else {
        currentParams.resize_limit = {
            mode: "custom",
            max_width: maxWidth.value,
            max_height: maxHeight.value,
        };
    }
    emit("update:engineParams", currentParams);
};

const supportsWideColor = computed(
    () => props.targetFormat === "avif" || props.targetFormat === "jxl" || props.targetFormat === "heic",
);
const supportsChromaControl = computed(() => props.targetFormat === "avif");

const hasQualityControl = computed(() => {
    return props.targetFormat !== "png" && props.targetFormat !== "jxl" && props.engineParams.quality !== undefined;
});

const selectedFormatDesc = computed(() => {
    return availableFormats.find((f) => f.code === props.targetFormat)?.desc ?? "";
});

const setTargetFormat = (fmt: string) => {
    emit("update:targetFormat", fmt);
};

const updateParam = (key: string, val: any) => {
    emit("update:engineParams", {
        ...props.engineParams,
        [key]: val,
    });
    emit("update:selectedProfile", "custom");
};

const handleJxlLosslessToggle = (lossless: boolean) => {
    emit("update:engineParams", {
        ...props.engineParams,
        lossless,
        ...(lossless ? { distance: undefined } : { distance: 1.0 }),
    });
    emit("update:selectedProfile", "custom");
};

const updateColorEncoding = (key: keyof ColorEncodingState, val: any) => {
    emit("update:colorEncoding", {
        ...props.colorEncoding,
        [key]: val,
    });
};

const handleBitDepthChange = (depth: number) => {
    emit("update:colorEncoding", {
        ...props.colorEncoding,
        nominal_bit_depth: depth,
        // When downsampling to 8-bit, ensure dithering is automatically engaged to prevent banding
        dither: depth === 8 ? true : props.colorEncoding.dither,
    });
};

const selectColorIntent = (intent: "auto" | "sdr") => {
    colorIntent.value = intent;
    if (intent === "auto") {
        emit("update:colorEncoding", {
            color_space: undefined,
            chroma: "auto",
            nominal_bit_depth: supportsWideColor.value ? 10 : 8,
            tone_mapping: "none",
            dither: true,
        });
    } else {
        emit("update:colorEncoding", {
            color_space: "srgb",
            chroma: "yuv420",
            nominal_bit_depth: 8,
            tone_mapping: "aces_filmic",
            dither: true,
        });
    }
    emit("update:profilePolicy", "preserve_source");
};

const applyProfile = (profile: "size" | "balanced" | "speed") => {
    emit("update:selectedProfile", profile);
};
</script>
