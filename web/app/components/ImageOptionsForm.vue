<script setup lang="ts">
import { computed, ref } from "vue";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Switch from "@/components/ui/switch/Switch.vue";
import Slider from "@/components/ui/slider/Slider.vue";
import { Eye, ChevronDown, ChevronUp } from "@lucide/vue";
import type { ImageOptions, OutputFormat } from "~/lib/types/image-options";
import type { CompressionTarget } from "~/lib/types/task";
import {
    DEFAULT_QUALITY,
    FORMATS,
    MAX_DISTANCE,
    MIN_DISTANCE,
    bitDepths,
    clampDistance,
    distanceToQuality,
    formatShortBytes,
    hasBitDepth,
    hasChroma,
    hasDistance,
    hasEffort,
    hasLossless,
    hasQuality,
    qualityToDistance,
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
    target?: CompressionTarget | null;
    targetSupport?: boolean;
    sourceProbe?: SourceProbeSummary | null;
    disabled?: boolean;
    /** Output formats to offer; defaults to all. */
    formats?: OutputFormat[];
    /** The engine repacks JPEG → lossless JXL with cjxl (server only). */
    jpegRepack?: boolean;
}>();
const emit = defineEmits<{
    (e: "update:modelValue", value: ImageOptions): void;
    (e: "update:target", value: CompressionTarget | undefined): void;
}>();

const options = computed(() => props.modelValue);
const update = (patch: Partial<ImageOptions>) =>
    emit("update:modelValue", sanitize({ ...props.modelValue, ...patch }));

const targetAllowed = computed(() => !!props.targetSupport && options.value.format !== "png");
const targetEnabled = computed(() => targetAllowed.value && !!props.target?.max_bytes);
const targetSearchActive = computed(() => targetEnabled.value && !options.value.lossless);
const targetUnit = ref<"KB" | "MB">(
    props.target?.max_bytes && props.target.max_bytes >= 1024 * 1024 && props.target.max_bytes % (1024 * 1024) === 0 ? "MB" : "KB",
);
const targetFactor = computed(() => (targetUnit.value === "MB" ? 1024 * 1024 : 1024));
const targetAmount = computed(() => {
    const bytes = props.target?.max_bytes;
    if (!bytes) return 200;
    const val = bytes / targetFactor.value;
    return Number.isInteger(val) ? val : Number(val.toFixed(1));
});

const toggleTarget = (on: boolean) => {
    if (!on) {
        emit("update:target", undefined);
        return;
    }
    const existing = props.target?.max_bytes;
    emit("update:target", { max_bytes: existing && existing > 0 ? existing : 200 * 1024 });
    update({ quality: undefined, distance: undefined });
};

const onTargetAmountInput = (event: Event) => {
    const input = event.target as HTMLInputElement;
    const val = Number(input.value);
    if (!Number.isFinite(val) || val <= 0) return;
    const bytes = Math.max(1024, Math.round(val * targetFactor.value));
    emit("update:target", { max_bytes: bytes });
};

const setTargetUnit = (unit: "KB" | "MB") => {
    targetUnit.value = unit;
};

const showAdvanced = ref(!!(props.modelValue.chroma || props.modelValue.bit_depth));
const resizeEnabled = computed(() => !!options.value.resize);
// Mirrors the cjxl bypass conditions in compute/crates/imageops/src/bypass.rs.
const repackHint = computed(() => {
    if (options.value.resize || (options.value.color ?? "keep") !== "keep") return "jxl_repack_blocked";
    return (options.value.metadata ?? "strip") === "keep" ? "jxl_repack_hint" : "jxl_repack_strip_hint";
});
const quality = computed(() => options.value.quality ?? DEFAULT_QUALITY[options.value.format] ?? 0);
// JXL only: set means the encode targets a Butteraugli distance instead of quality.
const distance = computed(() => (hasDistance(options.value) ? options.value.distance ?? null : null));
// The slider covers the useful range; the number field reaches up to 25.
const DISTANCE_SLIDER_MAX = 10;
// Unset means each encoder's own default; the slider rests mid-range until moved.
const effort = computed(() => options.value.effort);

const setFormat = (format: OutputFormat) => {
    if (format === "png" && props.target) {
        emit("update:target", undefined);
    }
    update({ format, quality: undefined, distance: undefined });
};
// Switching carries the setting across with libvips' own mapping, so the
// encode does not change until the value is moved.
const setTarget = (target: "quality" | "distance") => {
    if (target === "distance" && distance.value == null)
        update({ quality: undefined, distance: qualityToDistance(quality.value) });
    else if (target === "quality" && distance.value != null)
        update({ distance: undefined, quality: distanceToQuality(distance.value) });
};
const onDistanceInput = (event: Event) => {
    const input = event.target as HTMLInputElement;
    const n = Number(input.value);
    const next = input.value !== "" && Number.isFinite(n) ? clampDistance(n) : distance.value;
    if (next == null) return;
    update({ distance: next });
    input.value = String(next); // show the clamped value even when it equals the old one
};
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

            <div v-if="targetAllowed" class="space-y-3 pt-1 border-t border-hairline/70">
                <div class="flex items-center justify-between">
                    <Label class="text-xs font-semibold text-ink">{{ $t("image_options.target_size") }}</Label>
                    <Switch :checked="targetEnabled" :disabled="disabled" @update:checked="toggleTarget" />
                </div>
                <template v-if="targetEnabled">
                    <div class="flex items-center gap-2">
                        <Label for="target-max-bytes" class="text-[11px] text-ink-subtle shrink-0">{{ $t("image_options.target_max_bytes") }}</Label>
                        <Input
                            id="target-max-bytes"
                            type="number"
                            inputmode="decimal"
                            min="1"
                            step="any"
                            :model-value="targetAmount"
                            :disabled="disabled"
                            class="h-8 text-xs font-mono text-right tabular-nums"
                            @change="onTargetAmountInput"
                        />
                        <div class="grid grid-cols-2 gap-1 shrink-0">
                            <button
                                v-for="u in (['KB', 'MB'] as const)"
                                :key="u"
                                type="button"
                                :disabled="disabled"
                                class="h-8 px-2.5 rounded-md border text-xs font-mono transition-colors cursor-pointer disabled:opacity-40"
                                :class="chip(targetUnit === u)"
                                @click="setTargetUnit(u)"
                            >{{ u }}</button>
                        </div>
                    </div>
                    <p class="text-[11px] text-ink-subtle">
                        {{ options.lossless ? $t("image_options.target_size_check_hint") : $t("image_options.target_size_hint") }}
                    </p>
                </template>
            </div>

            <div v-if="hasQuality(options)" class="space-y-2">
                <template v-if="targetSearchActive">
                    <div class="flex justify-between items-center text-xs py-1">
                        <Label class="text-xs font-semibold text-ink">{{ $t("image_options.quality") }}</Label>
                        <span class="px-2 py-0.5 rounded-md border border-primary/35 bg-primary-soft text-primary-soft-foreground font-medium text-[11px]">
                            {{ $t("image_options.auto_from_target") }} · ≤{{ formatShortBytes(props.target?.max_bytes ?? 0) }}
                        </span>
                    </div>
                </template>
                <template v-else>
                    <div v-if="hasDistance(options)" class="grid grid-cols-2 gap-1.5" role="group" :aria-label="$t('image_options.target')">
                        <button v-for="m in ['quality', 'distance'] as const" :key="m" type="button" :disabled="disabled"
                            :aria-pressed="(distance != null) === (m === 'distance')"
                            class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                            :class="chip((distance != null) === (m === 'distance'))"
                            @click="setTarget(m)">{{ $t(`image_options.${m}`) }}</button>
                    </div>
                    <template v-if="distance != null">
                        <div class="flex justify-between items-center text-xs">
                            <Label for="jxl-distance" class="text-xs font-semibold text-ink">{{ $t("image_options.distance") }}</Label>
                            <Input id="jxl-distance" type="number" inputmode="decimal" :min="MIN_DISTANCE" :max="MAX_DISTANCE" step="0.01"
                                :model-value="distance" :disabled="disabled"
                                class="h-7 w-20 px-2 text-xs font-mono text-right tabular-nums"
                                @change="onDistanceInput" />
                        </div>
                        <Slider :model-value="[Math.min(distance, DISTANCE_SLIDER_MAX)]" :min="MIN_DISTANCE" :max="DISTANCE_SLIDER_MAX"
                            :step="0.05" :disabled="disabled" :aria-label="$t('image_options.distance')"
                            @update:model-value="(v) => v && update({ distance: v[0] })" />
                        <p class="text-[11px] text-ink-subtle">{{ $t("image_options.distance_hint") }}</p>
                    </template>
                    <template v-else>
                        <div class="flex justify-between items-center text-xs">
                            <Label class="text-xs font-semibold text-ink">{{ $t("image_options.quality") }}</Label>
                            <span class="font-mono font-medium text-ink-muted text-xs tabular-nums">{{ quality }}</span>
                        </div>
                        <Slider :model-value="[quality]" :min="1" :max="100" :step="1" :disabled="disabled"
                            @update:model-value="(v) => update({ quality: v?.[0] })" />
                    </template>
                </template>
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
                    <div class="grid gap-1.5" :class="bitDepths(options.format).length > 2 ? 'grid-cols-4' : 'grid-cols-3'">
                        <button v-for="v in [undefined, ...bitDepths(options.format)]" :key="v ?? 'auto'" type="button" :disabled="disabled"
                            class="min-h-8 px-2 py-1.5 rounded-md border text-xs leading-tight transition-colors cursor-pointer disabled:opacity-40"
                            :class="chip(options.bit_depth === v)"
                            @click="update({ bit_depth: v })">{{ v ? `${v}-bit` : $t("image_options.follow_source") }}</button>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
