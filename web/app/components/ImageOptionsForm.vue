<script setup lang="ts">
import { computed, ref } from "vue";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Switch from "@/components/ui/switch/Switch.vue";
import Slider from "@/components/ui/slider/Slider.vue";
import { Eye, ChevronDown, ChevronUp } from "@lucide/vue";
import type { ImageOptions, OutputFormat } from "~/lib/types/image-options";
import {
    DEFAULT_EFFORT,
    DEFAULT_QUALITY,
    FORMATS,
    hasBitDepth,
    hasChroma,
    hasEffort,
    hasLossless,
    hasQuality,
    sanitize,
} from "~/lib/image-options";

export interface SourceProbeSummary {
    format?: string;
    width?: number;
    height?: number;
    hasAlpha?: boolean;
}

const props = defineProps<{
    modelValue: ImageOptions;
    sourceProbe?: SourceProbeSummary | null;
    disabled?: boolean;
}>();
const emit = defineEmits<{ (e: "update:modelValue", value: ImageOptions): void }>();

const options = computed(() => props.modelValue);
const update = (patch: Partial<ImageOptions>) =>
    emit("update:modelValue", sanitize({ ...props.modelValue, ...patch }));

const showAdvanced = ref(!!(props.modelValue.chroma || props.modelValue.bit_depth));
const resizeEnabled = computed(() => !!options.value.resize);
const quality = computed(() => options.value.quality ?? DEFAULT_QUALITY[options.value.format] ?? 0);
const effort = computed(() => options.value.effort ?? DEFAULT_EFFORT);

const setFormat = (format: OutputFormat) => update({ format, quality: undefined });
const setResize = (patch: Partial<NonNullable<ImageOptions["resize"]>>) =>
    update({ resize: { fit: "inside", enlarge: false, ...options.value.resize, ...patch } });
const toggleResize = (on: boolean) =>
    update({ resize: on ? { width: 1600, fit: "inside", enlarge: false } : undefined });
const dimension = (value: string | number) => {
    const n = Math.round(Number(value));
    return Number.isFinite(n) && n > 0 ? Math.min(n, 16384) : undefined;
};

type Choice<T> = { value: T; key: string };
const fits: Choice<"inside" | "cover" | "fill">[] = [
    { value: "inside", key: "inside" },
    { value: "cover", key: "cover" },
    { value: "fill", key: "fill" },
];
</script>

<template>
    <div class="space-y-6 @container">
        <!-- Source summary -->
        <div v-if="sourceProbe" class="p-3.5 rounded-xl bg-surface-2/60 border border-hairline flex items-center justify-between text-xs">
            <span class="font-semibold text-ink flex items-center gap-1.5">
                <Eye class="w-3.5 h-3.5 text-primary" />
                {{ $t("image_options.source") }}
            </span>
            <span class="font-mono text-ink-subtle">
                <span v-if="sourceProbe.format" class="uppercase">{{ sourceProbe.format }}</span>
                <span v-if="sourceProbe.width && sourceProbe.height"> · {{ sourceProbe.width }} × {{ sourceProbe.height }}</span>
                <span v-if="sourceProbe.hasAlpha"> · Alpha</span>
            </span>
        </div>

        <!-- Format -->
        <div class="space-y-2">
            <Label class="text-xs font-bold text-ink uppercase tracking-wider">{{ $t("image_options.format") }}</Label>
            <div class="grid grid-cols-3 @lg:grid-cols-6 gap-2">
                <button
                    v-for="fmt in FORMATS"
                    :key="fmt"
                    type="button"
                    :disabled="disabled"
                    class="p-2.5 rounded-xl border text-left transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="options.format === fmt ? 'border-primary/50 bg-primary/10 ring-1 ring-primary/20' : 'border-hairline bg-surface-2 hover:bg-surface-3'"
                    @click="setFormat(fmt)"
                >
                    <span class="block text-xs font-bold text-ink">{{ fmt.toUpperCase() }}</span>
                    <span class="block text-[10px] text-ink-subtle leading-tight mt-0.5">{{ $t(`image_options.formats.${fmt}`) }}</span>
                </button>
            </div>
        </div>

        <!-- Compression -->
        <div class="space-y-4 p-4 rounded-xl bg-surface-1 border border-hairline">
            <div v-if="hasLossless(options.format)" class="flex items-center justify-between">
                <Label class="text-xs font-bold text-ink">{{ $t("image_options.lossless") }}</Label>
                <Switch :checked="!!options.lossless" :disabled="disabled" @update:checked="(v: boolean) => update({ lossless: v })" />
            </div>
            <p v-if="options.lossless && options.format === 'jxl'" class="text-[11px] text-ink-subtle">
                {{ $t("image_options.jxl_lossless_hint") }}
            </p>

            <div v-if="hasQuality(options)" class="space-y-2">
                <div class="flex justify-between items-center text-xs">
                    <Label class="font-bold text-ink">{{ $t("image_options.quality") }}</Label>
                    <span class="font-mono font-bold text-primary text-sm">{{ quality }}</span>
                </div>
                <Slider :model-value="[quality]" :min="1" :max="100" :step="1" :disabled="disabled"
                    @update:model-value="(v) => update({ quality: v?.[0] })" />
            </div>

            <div v-if="hasEffort(options.format)" class="space-y-2">
                <div class="flex justify-between items-center text-xs">
                    <Label class="font-bold text-ink">{{ $t("image_options.effort") }}</Label>
                    <span class="font-mono font-bold text-primary text-sm">{{ effort }}</span>
                </div>
                <Slider :model-value="[effort]" :min="1" :max="10" :step="1" :disabled="disabled"
                    @update:model-value="(v) => update({ effort: v?.[0] })" />
                <p class="text-[11px] text-ink-subtle">{{ $t("image_options.effort_hint") }}</p>
            </div>
        </div>

        <!-- Resize -->
        <div class="space-y-3 p-4 rounded-xl bg-surface-1 border border-hairline">
            <div class="flex items-center justify-between">
                <Label class="text-xs font-bold text-ink">{{ $t("image_options.resize") }}</Label>
                <Switch :checked="resizeEnabled" :disabled="disabled" @update:checked="toggleResize" />
            </div>
            <template v-if="options.resize">
                <div class="grid grid-cols-2 gap-3">
                    <div class="space-y-1">
                        <Label class="text-[11px] text-ink-subtle">{{ $t("image_options.width") }}</Label>
                        <Input type="number" min="1" max="16384" :model-value="options.resize.width ?? ''" :disabled="disabled"
                            class="h-8 text-xs" @update:model-value="(v) => setResize({ width: dimension(v) })" />
                    </div>
                    <div class="space-y-1">
                        <Label class="text-[11px] text-ink-subtle">{{ $t("image_options.height") }}</Label>
                        <Input type="number" min="1" max="16384" :model-value="options.resize.height ?? ''" :disabled="disabled"
                            class="h-8 text-xs" @update:model-value="(v) => setResize({ height: dimension(v) })" />
                    </div>
                </div>
                <div v-if="options.resize.width && options.resize.height" class="grid grid-cols-3 gap-2">
                    <button v-for="fit in fits" :key="fit.value" type="button" :disabled="disabled"
                        class="px-2 py-1.5 rounded-lg border text-xs transition-all cursor-pointer disabled:opacity-40"
                        :class="(options.resize.fit ?? 'inside') === fit.value ? 'border-primary/50 bg-primary/10 text-ink font-semibold' : 'border-hairline bg-surface-2 text-ink-subtle'"
                        @click="setResize({ fit: fit.value })">
                        {{ $t(`image_options.fit.${fit.key}`) }}
                    </button>
                </div>
                <div class="flex items-center justify-between">
                    <span class="text-[11px] text-ink-subtle">{{ $t("image_options.enlarge") }}</span>
                    <Switch :checked="!!options.resize.enlarge" :disabled="disabled" @update:checked="(v: boolean) => setResize({ enlarge: v })" />
                </div>
            </template>
        </div>

        <!-- Color, HDR, metadata -->
        <div class="grid gap-3 @lg:grid-cols-3">
            <div class="space-y-1.5">
                <Label class="text-[11px] font-semibold text-ink">{{ $t("image_options.color") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['keep', 'srgb'] as const" :key="v" type="button" :disabled="disabled"
                        class="px-2 py-1.5 rounded-lg border text-xs cursor-pointer disabled:opacity-40"
                        :class="(options.color ?? 'keep') === v ? 'border-primary/50 bg-primary/10 font-semibold text-ink' : 'border-hairline bg-surface-2 text-ink-subtle'"
                        @click="update({ color: v })">{{ $t(`image_options.color_${v}`) }}</button>
                </div>
            </div>
            <div class="space-y-1.5">
                <Label class="text-[11px] font-semibold text-ink">{{ $t("image_options.dynamic_range") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['keep', 'sdr'] as const" :key="v" type="button" :disabled="disabled"
                        class="px-2 py-1.5 rounded-lg border text-xs cursor-pointer disabled:opacity-40"
                        :class="(options.dynamic_range ?? 'keep') === v ? 'border-primary/50 bg-primary/10 font-semibold text-ink' : 'border-hairline bg-surface-2 text-ink-subtle'"
                        @click="update({ dynamic_range: v })">{{ $t(`image_options.dynamic_range_${v}`) }}</button>
                </div>
            </div>
            <div class="space-y-1.5">
                <Label class="text-[11px] font-semibold text-ink">{{ $t("image_options.metadata") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['strip', 'keep'] as const" :key="v" type="button" :disabled="disabled"
                        class="px-2 py-1.5 rounded-lg border text-xs cursor-pointer disabled:opacity-40"
                        :class="(options.metadata ?? 'strip') === v ? 'border-primary/50 bg-primary/10 font-semibold text-ink' : 'border-hairline bg-surface-2 text-ink-subtle'"
                        @click="update({ metadata: v })">{{ $t(`image_options.metadata_${v}`) }}</button>
                </div>
            </div>
        </div>
        <p class="text-[11px] text-ink-subtle -mt-3">{{ $t("image_options.hdr_hint") }}</p>

        <!-- Advanced -->
        <div v-if="hasChroma(options) || hasBitDepth(options.format)" class="space-y-3">
            <button type="button" class="flex items-center gap-1 text-xs font-semibold text-ink-subtle hover:text-ink cursor-pointer"
                @click="showAdvanced = !showAdvanced">
                <component :is="showAdvanced ? ChevronUp : ChevronDown" class="w-3.5 h-3.5" />
                {{ $t("image_options.advanced") }}
            </button>
            <div v-if="showAdvanced" class="grid gap-3 @md:grid-cols-2">
                <div v-if="hasChroma(options)" class="space-y-1.5">
                    <Label class="text-[11px] font-semibold text-ink">{{ $t("image_options.chroma") }}</Label>
                    <div class="grid grid-cols-3 gap-1.5">
                        <button v-for="v in [undefined, '420', '444'] as const" :key="v ?? 'auto'" type="button" :disabled="disabled"
                            class="px-2 py-1.5 rounded-lg border text-xs cursor-pointer disabled:opacity-40"
                            :class="options.chroma === v ? 'border-primary/50 bg-primary/10 font-semibold text-ink' : 'border-hairline bg-surface-2 text-ink-subtle'"
                            @click="update({ chroma: v })">{{ v ? `4:${v.slice(1, 2)}:${v.slice(2)}` : $t("image_options.auto") }}</button>
                    </div>
                </div>
                <div v-if="hasBitDepth(options.format)" class="space-y-1.5">
                    <Label class="text-[11px] font-semibold text-ink">{{ $t("image_options.bit_depth") }}</Label>
                    <div class="grid grid-cols-3 gap-1.5">
                        <button v-for="v in [undefined, 8, 10] as const" :key="v ?? 'auto'" type="button" :disabled="disabled"
                            class="px-2 py-1.5 rounded-lg border text-xs cursor-pointer disabled:opacity-40"
                            :class="options.bit_depth === v ? 'border-primary/50 bg-primary/10 font-semibold text-ink' : 'border-hairline bg-surface-2 text-ink-subtle'"
                            @click="update({ bit_depth: v })">{{ v ? `${v}-bit` : $t("image_options.follow_source") }}</button>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
