<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetDescription,
    SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select'
import Switch from '@/components/ui/switch/Switch.vue'
import Slider from '@/components/ui/slider/Slider.vue'
import {
    Sparkles, RotateCcw, Loader2, Check, Code2
} from '@lucide/vue'
import TranscodeSettings from '@/components/TranscodeSettings.vue'

const props = defineProps<{
    open: boolean
    taskId: string
    targetJobId?: string
    initialFormat?: string
    initialParams?: Record<string, any>
    failedCount?: number
    isRetrying?: boolean
}>()

const emit = defineEmits<{
    (e: 'update:open', val: boolean): void
    (e: 'submit', payload: { targetFormat: string; params: any; retryAll: boolean; jobIds?: string[] }): void
}>()

// Target Formats & Engines
const availableFormats = [
    { code: 'avif', label: 'AVIF', tag: '推荐', desc: '下一代极致压缩与高清晰度' },
    { code: 'heic', label: 'HEIC', tag: '高效', desc: '苹果生态与移动端高保真' },
    { code: 'webp', label: 'WebP', tag: '全平台兼容', desc: '高兼容性与低带宽消耗' },
    { code: 'jpeg', label: 'JPEG', tag: '经典', desc: '传统照片格式与高适应性' },
    { code: 'png', label: 'PNG', tag: '无损', desc: '无损透明通道图形' },
    { code: 'jxl', label: 'JPEG XL', tag: '极客', desc: '现代无损重建与高质量' },
]

const enginesByFormat: Record<string, Array<{ code: string; label: string }>> = {
    avif: [
        { code: 'libavif:avif', label: 'libavif (官方标准引擎)' },
    ],
    heic: [
        { code: 'libheif:heic', label: 'libheif (HEIC 官方标准)' },
    ],
    webp: [
        { code: 'libwebp:webp', label: 'libwebp (Google 官方)' },
    ],
    jpeg: [
        { code: 'libjpeg:jpeg', label: 'libjpeg-turbo (高速渲染)' },
    ],
    png: [
        { code: 'libpng:png', label: 'libpng (标准无损编码)' },
    ],
    jxl: [
        { code: 'libjxl:jxl', label: 'libjxl (JPEG XL 官方)' },
    ],
}

const templates: Record<string, Record<string, Record<string, any>>> = {
    "libavif:avif": {
        size: { quality: 65, speed: 4, sharp_yuv: true },
        balanced: { quality: 75, speed: 6, sharp_yuv: true },
        speed: { quality: 75, speed: 8, sharp_yuv: false },
    },
    "libheif:heic": {
        size: { quality: 65, lossless: false },
        balanced: { quality: 75, lossless: false },
        speed: { quality: 80, lossless: false },
    },
    "libwebp:webp": {
        size: { quality: 75, lossless: false, method: 6 },
        balanced: { quality: 80, lossless: false, method: 4 },
        speed: { quality: 80, lossless: false, method: 2 },
    },
    "libjpeg:jpeg": {
        size: { quality: 75 },
        balanced: { quality: 85 },
        speed: { quality: 85 },
    },
    "libpng:png": {
        size: {},
        balanced: {},
        speed: {},
    },
    "libjxl:jxl": {
        size: {
            lossless: false,
            distance: 1.5,
            effort: 7,
            progressive: true,
            jpeg_reconstruction: true,
            sub_mode: "distance",
        },
        balanced: {
            lossless: false,
            distance: 1.0,
            effort: 5,
            progressive: true,
            jpeg_reconstruction: true,
            sub_mode: "distance",
        },
        speed: {
            lossless: false,
            distance: 1.0,
            effort: 3,
            progressive: false,
            jpeg_reconstruction: true,
            sub_mode: "distance",
        },
    },
}

// Form state
const targetFormat = ref('avif')
const selectedEngine = ref('libavif:avif')
const selectedProfile = ref<'balanced' | 'size' | 'speed' | 'custom'>('balanced')
const expertMode = ref(false)
const retryAllJobs = ref(false)
const engineParams = ref<Record<string, any>>({ quality: 75, speed: 6, sharp_yuv: true })
const showRawJson = ref(false)

// Custom state helpers
const useCustomAlphaQuality = ref(false)
const jxlSubMode = ref<'distance' | 'quality'>('distance')

// Color & Dynamic Range Pipeline state
type ColorSpaceValue = 'srgb' | 'display_p3' | 'bt2020_pq' | 'bt2020_hlg' | 'linear'
const colorSpaceOptions: Array<{ value: ColorSpaceValue; label: string; hdr?: boolean }> = [
    { value: 'srgb', label: 'sRGB (SDR 通用)' },
    { value: 'display_p3', label: 'Display P3 (苹果广色域)' },
    { value: 'bt2020_pq', label: 'HDR10 (BT.2020 + PQ)', hdr: true },
    { value: 'bt2020_hlg', label: 'HLG (BT.2020 广播 HDR)', hdr: true },
    { value: 'linear', label: 'Linear (线性光素材)' },
]

const colorEncoding = ref<{
    color_space?: ColorSpaceValue
    chroma?: string
    nominal_bit_depth?: number
    tone_mapping?: string
    dither?: boolean
}>({
    chroma: 'auto',
    nominal_bit_depth: 10,
    tone_mapping: 'none',
    dither: true,
})
const profilePolicy = ref<string>('preserve_source')
const strictMode = ref(false)

// Wide-gamut tags and YUV controls only exist for AVIF/JXL outputs.
const supportsWideColor = computed(() => targetFormat.value === 'avif' || targetFormat.value === 'jxl' || targetFormat.value === 'heic')
const supportsChromaControl = computed(() => targetFormat.value === 'avif')
const hdrOutput = computed(
    () => supportsWideColor.value && ['bt2020_pq', 'bt2020_hlg'].includes(colorEncoding.value.color_space ?? 'srgb')
)

// Maps the legacy standalone primaries/transfer pair onto a coupled color space.
const deriveColorSpace = (primaries?: string, transfer?: string): ColorSpaceValue => {
    if (primaries === 'bt2020' && transfer === 'pq') return 'bt2020_pq'
    if (primaries === 'bt2020' && transfer === 'hlg') return 'bt2020_hlg'
    if (primaries === 'display_p3') return 'display_p3'
    if (transfer === 'linear') return 'linear'
    return 'srgb'
}

type ColorPreset = {
    key: 'preserve' | 'compat'
    label: string
    desc: string
    policy: string
    values: {
        color_space: ColorSpaceValue
        chroma: string
        nominal_bit_depth: number
        tone_mapping: string
        dither: boolean
    }
}

const colorIntent = ref<'auto' | 'sdr'>('auto')
const showAdvancedColor = ref(false)

const colorPresets = computed<ColorPreset[]>(() => [
    {
        key: 'preserve',
        label: '自动保留 (推荐)',
        desc: '不做色域转换，保留原图 ICC / CICP 与 HDR 信息',
        policy: 'preserve_source',
        values: {
            color_space: 'srgb',
            chroma: 'auto',
            nominal_bit_depth: 10,
            tone_mapping: 'none',
            dither: true,
        },
    },
    {
        key: 'compat',
        label: '标准 SDR 兼容',
        desc: '保留色彩标识，位深降到 8-bit 并将 HDR 映射到 SDR',
        policy: 'preserve_source',
        values: {
            color_space: 'srgb',
            chroma: 'yuv420',
            nominal_bit_depth: 8,
            tone_mapping: 'aces_filmic',
            dither: true,
        },
    },
])

const activeColorPreset = computed(() => {
    const cur = colorEncoding.value
    const matched = colorPresets.value.find(
        (preset) =>
            (cur.color_space ?? 'srgb') === preset.values.color_space &&
            (cur.chroma ?? 'auto') === preset.values.chroma &&
            Number(cur.nominal_bit_depth ?? 10) === preset.values.nominal_bit_depth &&
            (cur.tone_mapping ?? 'none') === preset.values.tone_mapping &&
            (cur.dither ?? false) === preset.values.dither &&
            profilePolicy.value === preset.policy,
    )
    return matched?.key ?? 'custom'
})

const isCustomColorOverridden = computed(() => activeColorPreset.value === 'custom')

const selectColorIntent = (intent: 'auto' | 'sdr') => {
    colorIntent.value = intent
    const targetPresetKey = intent === 'auto' ? 'preserve' : 'compat'
    const preset = colorPresets.value.find((p) => p.key === targetPresetKey)
    if (preset) {
        applyColorPreset(preset)
    }
}

const resetColorToRecommended = () => {
    selectColorIntent(colorIntent.value)
}

const applyColorPreset = (preset: ColorPreset) => {
    colorEncoding.value = { ...colorEncoding.value, ...preset.values }
    profilePolicy.value = preset.policy
}

const profilePolicyHint = computed(() =>
    profilePolicy.value === 'strip'
        ? '不转换像素、剥离 ICC，仅按所选色彩空间写入标签（高级用法）'
        : '不转换像素，保留原图 ICC；无 ICC 时按所选色彩空间写入标签',
)

watch(
    () => colorEncoding.value.color_space,
    (space) => {
        if (space !== 'bt2020_pq' && space !== 'bt2020_hlg') return
        if (Number(colorEncoding.value.nominal_bit_depth) < 10) {
            colorEncoding.value.nominal_bit_depth = 10
        }
        colorEncoding.value.tone_mapping = 'none'
    },
)

const colorSpaceHint = computed(() =>
    profilePolicy.value === 'strip'
        ? '不转换像素；剥离 ICC 后仅按所选色彩空间写入标签'
        : '不转换像素；色域与传递函数已成对绑定，原图 ICC / CICP 原样透传',
)

// Drops values that are no longer offered (12-bit, yuv400, legacy standalone
// primaries/transfer, legacy policies) when a saved workflow is loaded.
const normalizeLoadedColorParams = (legacy?: {
    color_space?: string
    primaries?: string
    transfer?: string
    chroma?: string
    nominal_bit_depth?: number
    tone_mapping?: string
    dither?: boolean | string
}) => {
    const space = legacy?.color_space ?? deriveColorSpace(legacy?.primaries, legacy?.transfer)
    colorEncoding.value.color_space = colorSpaceOptions.some((o) => o.value === space)
        ? (space as ColorSpaceValue)
        : 'srgb'

    const depth = Number(legacy?.nominal_bit_depth ?? colorEncoding.value.nominal_bit_depth ?? 10)
    colorEncoding.value.nominal_bit_depth = depth >= 10 ? 10 : 8

    const chroma = legacy?.chroma ?? colorEncoding.value.chroma ?? 'auto'
    colorEncoding.value.chroma = ['auto', 'yuv444', 'yuv422', 'yuv420'].includes(chroma) ? chroma : 'auto'

    // Legacy payloads chose a dither algorithm; anything but "none" means on.
    colorEncoding.value.tone_mapping =
        legacy?.tone_mapping && legacy.tone_mapping !== 'none' ? 'aces_filmic' : 'none'
    colorEncoding.value.dither = typeof legacy?.dither === 'boolean' ? legacy.dither : legacy?.dither !== 'none'

    if (!['preserve_source', 'strip'].includes(profilePolicy.value)) {
        profilePolicy.value = 'preserve_source'
    }
}

// Sync props when drawer opens
watch(() => props.open, (isOpen) => {
    if (isOpen) {
        const fmt = (props.initialFormat || 'avif').toLowerCase()
        if (availableFormats.some(f => f.code === fmt)) {
            targetFormat.value = fmt
        } else {
            targetFormat.value = 'avif'
        }

        const initialEngine = props.initialParams?.engine
        if (initialEngine && enginesByFormat[targetFormat.value]?.some(e => e.code === initialEngine)) {
            selectedEngine.value = initialEngine
        } else {
            selectedEngine.value = enginesByFormat[targetFormat.value]?.[0]?.code || 'libavif:avif'
        }

        if (props.initialParams?.engine_params) {
            engineParams.value = JSON.parse(JSON.stringify(props.initialParams.engine_params))
            selectedProfile.value = 'custom'
            expertMode.value = true
        } else {
            resetParamsToProfile()
        }

        if (props.initialParams?.color) {
            normalizeLoadedColorParams(props.initialParams.color)
        }
        profilePolicy.value = props.initialParams?.profile_policy || 'preserve_source'
    }
})

// Auto update engine list when format changes
watch(targetFormat, (newFormat) => {
    const list = enginesByFormat[newFormat] || []
    if (list.length > 0 && list[0] && !list.some(e => e.code === selectedEngine.value)) {
        selectedEngine.value = list[0].code
    }
    resetParamsToProfile()
})

// Update params when engine changes
watch(selectedEngine, () => {
    resetParamsToProfile()
})

const resetParamsToProfile = () => {
    const engineTemplates = templates[selectedEngine.value]
    if (engineTemplates && selectedProfile.value !== 'custom') {
        const profileData = engineTemplates[selectedProfile.value]
        if (profileData) {
            engineParams.value = JSON.parse(JSON.stringify(profileData))
        }
    }
    // Initialize JXL mode helpers
    if (selectedEngine.value === 'libjxl:jxl') {
        if (engineParams.value.lossless === undefined) engineParams.value.lossless = false
        if (engineParams.value.jpeg_reconstruction === undefined) engineParams.value.jpeg_reconstruction = true
        if (engineParams.value.quality !== undefined) {
            jxlSubMode.value = 'quality'
        } else {
            jxlSubMode.value = 'distance'
        }
    }
}

const selectProfile = (profile: 'balanced' | 'size' | 'speed' | 'custom') => {
    selectedProfile.value = profile
    if (profile !== 'custom') {
        resetParamsToProfile()
    }
}

const preparedEngineParams = computed(() => {
    const params = JSON.parse(JSON.stringify(engineParams.value))

    if (selectedEngine.value === 'libavif:avif') {
        if (!useCustomAlphaQuality.value) {
            delete params.alpha_quality
        }
        if (params.depth === 'auto' || params.depth === undefined || params.depth === null || params.depth === '') {
            delete params.depth
        } else {
            params.depth = Number(params.depth)
        }
        if (
            params.color_range === 'auto' ||
            params.color_range === undefined ||
            params.color_range === null ||
            params.color_range === ''
        ) {
            delete params.color_range
        }
    }

    if (selectedEngine.value === 'libjxl:jxl') {
        if (params.lossless) {
            delete params.distance
            delete params.quality
        } else {
            if (jxlSubMode.value === 'quality') {
                delete params.distance
                if (params.quality === undefined) params.quality = 85
            } else {
                delete params.quality
                if (params.distance === undefined) params.distance = 1.0
            }
        }
        delete params.sub_mode
    }

    return params
})

const handleSubmit = () => {
    const paramsPayload: Record<string, any> = {
        engine: selectedEngine.value,
        engine_params: preparedEngineParams.value,
    }

    paramsPayload.color = {
        color_space: colorEncoding.value.color_space,
        chroma: colorEncoding.value.chroma,
        nominal_bit_depth: Number(colorEncoding.value.nominal_bit_depth),
        tone_mapping: colorEncoding.value.tone_mapping,
        dither: colorEncoding.value.dither,
    }
    paramsPayload.profile_policy = profilePolicy.value

    if (strictMode.value) {
        paramsPayload.strict = true
    }

    emit('submit', {
        targetFormat: targetFormat.value.toUpperCase(),
        params: paramsPayload,
        retryAll: props.targetJobId ? false : retryAllJobs.value,
        jobIds: props.targetJobId ? [props.targetJobId] : undefined,
    })
}
</script>

<template>
    <Sheet :open="open" @update:open="val => emit('update:open', val)">
        <SheetContent class="w-full sm:max-w-xl bg-surface-1 border-l border-hairline p-0 flex flex-col h-full shadow-2xl overflow-hidden font-sans text-ink">
            <!-- Sheet Header (WASM Local Transcoding Style) -->
            <SheetHeader class="px-6 py-4 border-b border-hairline space-y-1 bg-surface-1/80 backdrop-blur-md shrink-0">
                <div class="flex items-center justify-between">
                    <div class="flex items-center gap-2.5">
                        <div class="w-7 h-7 rounded-md bg-primary text-primary-foreground flex items-center justify-center shadow-sm shrink-0">
                            <SlidersHorizontal class="w-4 h-4 stroke-[2.5]" />
                        </div>
                        <SheetTitle class="text-sm font-bold text-ink font-satoshi tracking-tight">
                            {{ targetJobId ? '单独作业调参重试' : '重新调节转码参数并重试' }}
                        </SheetTitle>
                    </div>
                    <Badge class="bg-primary/10 text-primary border-none font-mono text-[10px] font-bold px-1.5 py-0.5 uppercase select-none">
                        {{ targetJobId ? `Job-${targetJobId.substring(0, 8)}` : `Task-${taskId.substring(0, 8)}` }}
                    </Badge>
                </div>
                <SheetDescription class="text-xs text-ink-subtle leading-relaxed pt-0.5">
                    修改转码输出格式与 Codec 编码参数。提交后后端将自动重置并重新调度转码任务。
                </SheetDescription>
            </SheetHeader>

            <!-- Scrollable Content -->
            <div class="flex-1 overflow-y-auto px-6 py-6 space-y-6">
                <!-- Scope Selection Card -->
                <div v-if="targetJobId" class="p-3.5 rounded-xl bg-primary/5 border border-primary/20 space-y-1">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-primary flex items-center gap-1.5 font-satoshi">
                            <RotateCcw class="w-3.5 h-3.5" />
                            单独作业调参重试
                        </span>
                        <span class="text-[11px] font-mono font-bold text-primary">
                            Job-{{ targetJobId.substring(0, 8) }}
                        </span>
                    </div>
                    <p class="text-[11px] text-ink-subtle">当前仅针对选中的该单项作业应用新参数并触发重试，任务内其他作业保持不变。</p>
                </div>

                <div v-else class="p-3.5 rounded-xl bg-surface-2/40 border border-hairline space-y-2">
                    <div class="flex items-center justify-between">
                        <span class="text-xs font-bold text-ink flex items-center gap-1.5 font-satoshi">
                            <RotateCcw class="w-3.5 h-3.5 text-primary" />
                            重试目标范围
                        </span>
                        <span class="text-[11px] font-mono font-bold text-ink-subtle">
                            {{ failedCount ? `${failedCount} 个失败作业` : '全任务' }}
                        </span>
                    </div>

                    <div class="grid grid-cols-2 gap-2 pt-1">
                        <button
                            type="button"
                            @click="retryAllJobs = false"
                            class="px-3 py-2 rounded-lg text-xs font-semibold border transition-all text-left flex items-center justify-between cursor-pointer"
                            :class="!retryAllJobs
                                ? 'bg-surface-1 border-primary text-primary shadow-xs ring-1 ring-primary font-bold'
                                : 'bg-transparent border-hairline text-ink-subtle hover:bg-surface-2 hover:text-ink'"
                        >
                            <span>仅失败的作业</span>
                            <Check v-if="!retryAllJobs" class="w-3.5 h-3.5 text-primary stroke-[2.5]" />
                        </button>

                        <button
                            type="button"
                            @click="retryAllJobs = true"
                            class="px-3 py-2 rounded-lg text-xs font-semibold border transition-all text-left flex items-center justify-between cursor-pointer"
                            :class="retryAllJobs
                                ? 'bg-surface-1 border-primary text-primary shadow-xs ring-1 ring-primary font-bold'
                                : 'bg-transparent border-hairline text-ink-subtle hover:bg-surface-2 hover:text-ink'"
                        >
                            <span>重置全部作业</span>
                            <Check v-if="retryAllJobs" class="w-3.5 h-3.5 text-primary stroke-[2.5]" />
                        </button>
                    </div>
                </div>

                <!-- Transcode Settings (Shared Form Component) -->
                <TranscodeSettings
                    v-model:targetFormat="targetFormat"
                    v-model:selectedEngine="selectedEngine"
                    v-model:selectedProfile="selectedProfile"
                    v-model:engineParams="engineParams"
                    v-model:colorEncoding="colorEncoding"
                    v-model:profilePolicy="profilePolicy"
                    v-model:strictMode="strictMode"
                    v-model:expertMode="expertMode"
                    :disabled="isRetrying"
                />

                <!-- Config Code Inspection Pill -->
                    <div class="pt-2">
                        <button
                            type="button"
                            @click="showRawJson = !showRawJson"
                            class="text-xs font-mono font-semibold text-ink-subtle hover:text-primary flex items-center gap-1.5 cursor-pointer transition-colors"
                        >
                            <Code2 class="w-3.5 h-3.5" />
                            <span>{{ showRawJson ? '收起配置 JSON' : '查看生成的 Engine Payload' }}</span>
                        </button>

                    <div v-if="showRawJson" class="mt-2 p-3 rounded-lg bg-surface-2 text-ink font-mono text-[11px] overflow-x-auto border border-hairline shadow-inner">
                        <pre>{{ JSON.stringify({ target_format: targetFormat.toUpperCase(), params: { engine: selectedEngine, engine_params: preparedEngineParams } }, null, 2) }}</pre>
                    </div>
                </div>
            </div>

            <!-- Sheet Footer -->
            <SheetFooter class="px-6 py-3.5 border-t border-hairline bg-surface-1/80 backdrop-blur-md shrink-0 flex flex-row items-center justify-between gap-3">
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    @click="emit('update:open', false)"
                    class="h-8 px-4 text-xs font-semibold border-hairline hover:bg-surface-2 text-ink cursor-pointer transition-all duration-200"
                >
                    取消
                </Button>

                <Button
                    type="button"
                    size="sm"
                    @click="handleSubmit"
                    :disabled="isRetrying"
                    class="h-8 px-5 text-xs font-semibold bg-primary text-primary-foreground hover:bg-primary-hover gap-2 shadow-xs cursor-pointer rounded-lg transition-all duration-200 disabled:opacity-50"
                >
                    <Loader2 v-if="isRetrying" class="w-3.5 h-3.5 animate-spin" />
                    <RotateCcw v-else class="w-3.5 h-3.5 stroke-[2.2]" />
                    <span>{{ isRetrying ? '正在触发重试...' : '确认新参数并触发重试' }}</span>
                </Button>
            </SheetFooter>
        </SheetContent>
    </Sheet>
</template>
