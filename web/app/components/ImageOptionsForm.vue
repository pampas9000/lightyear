<script setup lang="ts">
import { computed, ref } from "vue";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Switch from "@/components/ui/switch/Switch.vue";
import Slider from "@/components/ui/slider/Slider.vue";
import { Eye, ChevronDown, ChevronUp } from "@lucide/vue";
import type { ImageOptions, OutputFormat } from "~/lib/types/image-options";
import {
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
    /** Output formats to offer; defaults to all. */
    formats?: OutputFormat[];
    /** The engine repacks JPEG → lossless JXL with cjxl (server only). */
    jpegRepack?: boolean;
}>();
const emit = defineEmits<{ (e: "update:modelValue", value: ImageOptions): void }>();

const options = computed(() => props.modelValue);
const update = (patch: Partial<ImageOptions>) =>
    emit("update:modelValue", sanitize({ ...props.modelValue, ...patch }));

const showAdvanced = ref(!!(props.modelValue.chroma || props.modelValue.bit_depth));
const resizeEnabled = computed(() => !!options.value.resize);
// Mirrors the cjxl bypass conditions in compute/crates/imageops/src/bypass.rs.
const repackHint = computed(() => {
    if (options.value.resize || (options.value.color ?? "keep") !== "keep") return "jxl_repack_blocked";
    return (options.value.metadata ?? "strip") === "keep" ? "jxl_repack_hint" : "jxl_repack_strip_hint";
});const quality = computed(() => options.value.quality ?? DEFAULT_QUALITY[options.value.format] ?? 0);
// Unset means each encoder's own default; the slider rests mid-range until moved.
const effort = computed(() => options.value.effort);

const setFormat = (format: OutputFormat) => update({ format, quality: undefined });
const setResize = (patch: Partial<NonNullable<ImageOptions["resize"]>>) =>
    update({ resize: { fit: "inside", enlarge: false, ...options.value.resize, ...patch } });
const toggleResize = (on: boolean) =>
    update({ resize: on ? { width: 1600, fit: "inside", enlarge: false } : undefined });
const dimension = (value: string | number) => {
    const n = Math.round(Number(value));
    return Number.isFinite(n) && n > 0 ? Math.min(n, 16384) : undefined;
};

// Selectable chips share one look: white at rest, primary-tinted when chosen.
const chip = (on: boolean) =>
    on
        ? "border-primary/45 bg-primary-soft text-primary-soft-foreground font-medium"
        : "border-hairline bg-surface-1 text-ink-muted hover:bg-surface-2 hover:text-ink";

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
        <div v-if="sourceProbe" class="px-3.5 py-3 rounded-lg bg-surface-2 flex items-center justify-between text-xs">
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
            <Label class="text-xs font-semibold text-ink">{{ $t("image_options.format") }}</Label>
            <div class="grid grid-cols-3 @lg:grid-cols-6 gap-2">
                <button
                    v-for="fmt in formats ?? FORMATS"
                    :key="fmt"
                    type="button"
                    :disabled="disabled"
                    class="flex flex-col justify-start px-2.5 py-2 rounded-lg border text-left transition-colors disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    :class="options.format === fmt ? 'border-primary/45 bg-primary-soft' : 'border-hairline bg-surface-1 hover:bg-surface-2'"
                    @click="setFormat(fmt)"
                >
                    <span class="block text-xs font-semibold" :class="options.format === fmt ? 'text-primary-soft-foreground' : 'text-ink'">{{ fmt.toUpperCase() }}</span>
                    <span class="block text-[11px] text-ink-subtle leading-snug mt-0.5">{{ $t(`image_options.formats.${fmt}`) }}</span>
                </button>
            </div>
        </div>

        <!-- Compression -->
        <div class="space-y-4 p-4 rounded-lg bg-surface-1 border border-hairline">
            <div v-if="hasLossless(options.format)" class="flex items-center justify-between">
                <Label class="text-xs font-semibold text-ink">{{ $t("image_options.lossless") }}</Label>
                <Switch :checked="!!options.lossless" :disabled="disabled" @update:checked="(v: boolean) => update({ lossless: v })" />
            </div>
            <p v-if="jpegRepack && options.lossless && options.format === 'jxl'" class="text-[11px] text-ink-subtle">
                {{ $t(`image_options.${repackHint}`) }}
            </p>

            <div v-if="hasQuality(options)" class="space-y-2">
                <div class="flex justify-between items-center text-xs">
                    <Label class="text-xs font-semibold text-ink">{{ $t("image_options.quality") }}</Label>
                    <span class="font-mono font-medium text-ink-muted text-xs tabular-nums">{{ quality }}</span>
                </div>
                <Slider :model-value="[quality]" :min="1" :max="100" :step="1" :disabled="disabled"
                    @update:model-value="(v) => update({ quality: v?.[0] })" />
            </div>

            <div v-if="hasEffort(options.format)" class="space-y-2">
                <div class="flex justify-between items-center text-xs">
                    <Label class="text-xs font-semibold text-ink">{{ $t("image_options.effort") }}</Label>
                    <span v-if="effort == null" class="font-medium text-ink-muted text-xs">{{ $t("image_options.effort_default") }}</span>
                    <span v-else class="flex items-center gap-2">
                        <button type="button" :disabled="disabled"
                            class="text-[11px] text-ink-subtle underline-offset-2 hover:text-ink hover:underline"
                            @click="update({ effort: undefined })">{{ $t("image_options.effort_default") }}</button>
                        <span class="font-mono font-medium text-ink-muted text-xs tabular-nums">{{ effort }}</span>
                    </span>
                </div>
                <Slider :model-value="[effort ?? 5]" :min="1" :max="10" :step="1" :disabled="disabled"
                    @update:model-value="(v) => update({ effort: v?.[0] })" />
                <p class="text-[11px] text-ink-subtle">{{ $t("image_options.effort_hint") }}</p>
            </div>
        </div>

        <!-- Resize -->
        <div class="space-y-3 p-4 rounded-lg bg-surface-1 border border-hairline">
            <div class="flex items-center justify-between">
                <Label class="text-xs font-semibold text-ink">{{ $t("image_options.resize") }}</Label>
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
                        class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                        :class="chip((options.resize.fit ?? 'inside') === fit.value)"
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
                <Label class="text-xs font-semibold text-ink">{{ $t("image_options.color") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['keep', 'srgb'] as const" :key="v" type="button" :disabled="disabled"
                        class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                        :class="chip((options.color ?? 'keep') === v)"
                        @click="update({ color: v })">{{ $t(`image_options.color_${v}`) }}</button>
                </div>
            </div>
            <div class="space-y-1.5">
                <Label class="text-xs font-semibold text-ink">{{ $t("image_options.dynamic_range") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['keep', 'sdr'] as const" :key="v" type="button" :disabled="disabled"
                        class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                        :class="chip((options.dynamic_range ?? 'keep') === v)"
                        @click="update({ dynamic_range: v })">{{ $t(`image_options.dynamic_range_${v}`) }}</button>
                </div>
            </div>
            <div class="space-y-1.5">
                <Label class="text-xs font-semibold text-ink">{{ $t("image_options.metadata") }}</Label>
                <div class="grid grid-cols-2 gap-1.5">
                    <button v-for="v in ['strip', 'keep'] as const" :key="v" type="button" :disabled="disabled"
                        class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                        :class="chip((options.metadata ?? 'strip') === v)"
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
                    <Label class="text-xs font-semibold text-ink">{{ $t("image_options.chroma") }}</Label>
                    <div class="grid grid-cols-3 gap-1.5">
                        <button v-for="v in [undefined, '420', '444'] as const" :key="v ?? 'auto'" type="button" :disabled="disabled"
                            class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                            :class="chip(options.chroma === v)"
                            @click="update({ chroma: v })">{{ v ? `4:${v.slice(1, 2)}:${v.slice(2)}` : $t("image_options.auto") }}</button>
                    </div>
                </div>
                <div v-if="hasBitDepth(options.format)" class="space-y-1.5">
                    <Label class="text-xs font-semibold text-ink">{{ $t("image_options.bit_depth") }}</Label>
                    <div class="grid grid-cols-3 gap-1.5">
                        <button v-for="v in [undefined, 8, 10] as const" :key="v ?? 'auto'" type="button" :disabled="disabled"
                            class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                            :class="chip(options.bit_depth === v)"
                            @click="update({ bit_depth: v })">{{ v ? `${v}-bit` : $t("image_options.follow_source") }}</button>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
