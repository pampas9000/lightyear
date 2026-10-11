<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from "vue";
import { useRouter } from "vue-router";
import { Uppy } from "@uppy/core";
import AwsS3 from "@uppy/aws-s3";
import { useApi, parseApiError } from "~/composables/useApi";
import { useAuth } from "~/composables/useAuth";
import type { Task, Workflow } from "~/lib/types/task";
import type { ApiResponse } from "~/lib/types/api";
import type { MultipartUploadResponse, PartSignatureResponse } from "~/lib/types/s3";
import { toast } from "vue-sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import Switch from "@/components/ui/switch/Switch.vue";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import TranscodeSettings from "@/components/TranscodeSettings.vue";
import type { SourceProbeSummary } from "@/components/TranscodeSettings.vue";
import {
    Plus,
    X,
    Check,
    Loader2,
    FileImage,
    FileVideo,
    FileCode,
    UploadCloud,
    AlertCircle,
    AlertTriangle,
    Sliders,
    Settings,
    Layers,
    Zap,
    Gauge,
    HardDrive,
    Trash2,
    RotateCcw,
    ChevronDown,
    ChevronUp,
    Sparkles,
    ShieldAlert,
    Lock,
} from "@lucide/vue";

useHead({
    title: $t("new_task.title") + " | Transcoder",
});

const router = useRouter();
const api = useApi();

// Form state
const targetFormat = ref("avif");

const defaultEngines: Record<string, string> = {
    avif: "libavif:avif",
    heic: "libheif:heic",
    webp: "libwebp:webp",
    jpeg: "libjpeg:jpeg",
    png: "libpng:png",
    jxl: "libjxl:jxl",
};

const selectedEngine = ref("libavif:avif");

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
};

const expertMode = ref(false);
const selectedProfile = ref<"size" | "balanced" | "speed" | "custom">("balanced");
const engineParams = ref<Record<string, any>>({ quality: 75, speed: 6, sharp_yuv: true });

// Color & Dynamic Range Pipeline state
type ColorSpaceValue = "srgb" | "display_p3" | "bt2020_pq" | "bt2020_hlg" | "linear";
const colorSpaceOptions: Array<{ value: ColorSpaceValue; label: string; hdr?: boolean }> = [
    { value: "srgb", label: "sRGB — 网络与普通显示器 (SDR)" },
    { value: "display_p3", label: "Display P3 — 苹果设备广色域 (SDR)" },
    { value: "bt2020_pq", label: "HDR10 — BT.2020 + PQ (需 10-bit)", hdr: true },
    { value: "bt2020_hlg", label: "HLG — BT.2020 广播 HDR (需 10-bit)", hdr: true },
    { value: "linear", label: "Linear — 线性光中间素材" },
];

const colorEncoding = ref<{
    color_space?: ColorSpaceValue;
    chroma?: string;
    nominal_bit_depth?: number;
    tone_mapping?: string;
    dither?: boolean;
}>({
    chroma: "auto",
    nominal_bit_depth: 10,
    tone_mapping: "none",
    dither: true,
});
const profilePolicy = ref<string>("preserve_source");
const strictMode = ref(false);

// Wide-gamut tags and YUV controls only exist for AVIF/JXL outputs.
const supportsWideColor = computed(
    () => targetFormat.value === "avif" || targetFormat.value === "jxl" || targetFormat.value === "heic",
);
const supportsChromaControl = computed(() => targetFormat.value === "avif");
const hdrOutput = computed(
    () =>
        supportsWideColor.value &&
        ["bt2020_pq", "bt2020_hlg"].includes(colorEncoding.value.color_space ?? "srgb"),
);

// Maps the legacy standalone primaries/transfer pair onto a coupled color space.
const deriveColorSpace = (primaries?: string, transfer?: string): ColorSpaceValue => {
    if (primaries === "bt2020" && transfer === "pq") return "bt2020_pq";
    if (primaries === "bt2020" && transfer === "hlg") return "bt2020_hlg";
    if (primaries === "display_p3") return "display_p3";
    if (transfer === "linear") return "linear";
    return "srgb";
};

// Color pipeline intent state
const colorIntent = ref<"auto" | "sdr">("auto");
const showAdvancedColor = ref(false);

type ColorPreset = {
    key: "preserve" | "compat";
    label: string;
    desc: string;
    policy: string;
    values: {
        color_space: ColorSpaceValue;
        chroma: string;
        nominal_bit_depth: number;
        tone_mapping: string;
        dither: boolean;
    };
};

const colorPresets = computed<ColorPreset[]>(() => [
    {
        key: "preserve",
        label: $t("new_task.color_pipeline.mode_auto"),
        desc: $t("new_task.color_pipeline.mode_auto_desc"),
        policy: "preserve_source",
        values: {
            color_space: "srgb",
            chroma: "auto",
            nominal_bit_depth: 10,
            tone_mapping: "none",
            dither: true,
        },
    },
    {
        key: "compat",
        label: $t("new_task.color_pipeline.mode_sdr"),
        desc: $t("new_task.color_pipeline.mode_sdr_desc"),
        policy: "preserve_source",
        values: {
            color_space: "srgb",
            chroma: "yuv420",
            nominal_bit_depth: 8,
            tone_mapping: "aces_filmic",
            dither: true,
        },
    },
]);

const activeColorPreset = computed(() => {
    const cur = colorEncoding.value;
    const matched = colorPresets.value.find(
        (preset) =>
            (cur.color_space ?? "srgb") === preset.values.color_space &&
            (cur.chroma ?? "auto") === preset.values.chroma &&
            Number(cur.nominal_bit_depth ?? 10) === preset.values.nominal_bit_depth &&
            (cur.tone_mapping ?? "none") === preset.values.tone_mapping &&
            (cur.dither ?? false) === preset.values.dither &&
            profilePolicy.value === preset.policy,
    );
    return matched?.key ?? "custom";
});

const isCustomColorOverridden = computed(() => activeColorPreset.value === "custom");

const selectColorIntent = (intent: "auto" | "sdr") => {
    colorIntent.value = intent;
    const targetPresetKey = intent === "auto" ? "preserve" : "compat";
    const preset = colorPresets.value.find((p) => p.key === targetPresetKey);
    if (preset) {
        applyColorPreset(preset);
    }
};

const resetColorToRecommended = () => {
    selectColorIntent(colorIntent.value);
};

const applyColorPreset = (preset: ColorPreset) => {
    colorEncoding.value = { ...colorEncoding.value, ...preset.values };
    profilePolicy.value = preset.policy;
};

const profilePolicyHint = computed(() =>
    profilePolicy.value === "strip"
        ? "不转换像素、剥离 ICC，仅按所选色彩空间写入标签（高级用法）"
        : "不转换像素，保留原图 ICC；无 ICC 时按所选色彩空间写入标签",
);

watch(
    () => colorEncoding.value.color_space,
    (space) => {
        if (space !== "bt2020_pq" && space !== "bt2020_hlg") return;
        if (Number(colorEncoding.value.nominal_bit_depth) < 10) {
            colorEncoding.value.nominal_bit_depth = 10;
        }
        colorEncoding.value.tone_mapping = "none";
    },
);

const colorSpaceHint = computed(() =>
    profilePolicy.value === "strip"
        ? "不转换像素；剥离 ICC 后仅按所选色彩空间写入标签"
        : "不转换像素；色域与传递函数已成对绑定，原图 ICC / CICP 原样透传",
);

// Drops values that are no longer offered (12-bit, yuv400, legacy standalone
// primaries/transfer, legacy policies) when a saved preset is loaded.
const normalizeLoadedColorParams = (legacy?: {
    color_space?: string;
    primaries?: string;
    transfer?: string;
    chroma?: string;
    nominal_bit_depth?: number;
    tone_mapping?: string;
    dither?: boolean | string;
}) => {
    const space = legacy?.color_space ?? deriveColorSpace(legacy?.primaries, legacy?.transfer);
    colorEncoding.value.color_space = colorSpaceOptions.some((o) => o.value === space)
        ? (space as ColorSpaceValue)
        : "srgb";

    const depth = Number(legacy?.nominal_bit_depth ?? colorEncoding.value.nominal_bit_depth ?? 10);
    colorEncoding.value.nominal_bit_depth = depth >= 10 ? 10 : 8;

    const chroma = legacy?.chroma ?? colorEncoding.value.chroma ?? "auto";
    colorEncoding.value.chroma = ["auto", "yuv444", "yuv422", "yuv420"].includes(chroma)
        ? chroma
        : "auto";

    // Legacy payloads chose a dither algorithm; anything but "none" means on.
    colorEncoding.value.tone_mapping =
        legacy?.tone_mapping && legacy.tone_mapping !== "none" ? "aces_filmic" : "none";
    colorEncoding.value.dither =
        typeof legacy?.dither === "boolean" ? legacy.dither : legacy?.dither !== "none";

    if (!["preserve_source", "strip"].includes(profilePolicy.value)) {
        profilePolicy.value = "preserve_source";
    }
};

// Workflow presets state
const workflows = ref<Workflow[]>([]);
const selectedWorkflowId = ref("none");
const workflowDirty = ref(false);
const isApplyingWorkflow = ref(false);
const showPresetSave = ref(false);
const presetName = ref("");
const isSavingPreset = ref(false);

// Auto-switch engine when target format changes
watch(targetFormat, (newFormat) => {
    if (isApplyingWorkflow.value) return;
    const defaultEngine = defaultEngines[newFormat] || "libavif:avif";
    selectedEngine.value = defaultEngine;
    resetParamsToProfile();
});

// Reset params when engine changes
watch(selectedEngine, () => {
    if (isApplyingWorkflow.value) return;
    resetParamsToProfile();
});

const isApplyingTemplate = ref(false);
const resetParamsToProfile = () => {
    isApplyingTemplate.value = true;
    const engineTemplates = templates[selectedEngine.value];
    if (engineTemplates && selectedProfile.value !== "custom") {
        const profileData = engineTemplates[selectedProfile.value];
        if (profileData) {
            engineParams.value = JSON.parse(JSON.stringify(profileData));
        }
    }
    // Initialize helper modes
    if (selectedEngine.value === "libjxl:jxl") {
        if (engineParams.value.lossless === undefined) {
            engineParams.value.lossless = false;
        }
        if (engineParams.value.jpeg_reconstruction === undefined) {
            engineParams.value.jpeg_reconstruction = true;
        }
        if (engineParams.value.sub_mode === undefined) {
            if (engineParams.value.quality !== undefined) {
                engineParams.value.sub_mode = "quality";
            } else {
                engineParams.value.sub_mode = "distance";
            }
        }
    }
    isApplyingTemplate.value = false;
};

const handleJxlMainModeChange = (losslessVal: boolean) => {
    if (selectedEngine.value !== "libjxl:jxl") return;
    engineParams.value.lossless = losslessVal;
    if (losslessVal) {
        delete engineParams.value.distance;
        delete engineParams.value.quality;
    } else {
        if (engineParams.value.sub_mode === "quality") {
            delete engineParams.value.distance;
            if (engineParams.value.quality === undefined) {
                engineParams.value.quality = 85;
            }
        } else {
            engineParams.value.sub_mode = "distance";
            delete engineParams.value.quality;
            if (engineParams.value.distance === undefined) {
                engineParams.value.distance = 1.0;
            }
        }
    }
};

const handleJxlSubModeChange = (newSubMode: string | number) => {
    if (selectedEngine.value !== "libjxl:jxl") return;
    const subModeStr = String(newSubMode);
    engineParams.value.sub_mode = subModeStr;
    if (subModeStr === "quality") {
        delete engineParams.value.distance;
        if (engineParams.value.quality === undefined) {
            engineParams.value.quality = 85;
        }
    } else {
        delete engineParams.value.quality;
        if (engineParams.value.distance === undefined) {
            engineParams.value.distance = 1.0;
        }
    }
};

const preparedEngineParams = computed(() => {
    const params = JSON.parse(JSON.stringify(engineParams.value));

    // For libavif:avif
    if (selectedEngine.value === "libavif:avif") {
        const advanced: Record<string, number> = {};
        let hasAdvanced = false;

        if (
            params.sharpness !== undefined &&
            params.sharpness !== null &&
            params.sharpness !== ""
        ) {
            advanced.sharpness = Number(params.sharpness);
            hasAdvanced = true;
        }
        if (
            params.color_sharpness !== undefined &&
            params.color_sharpness !== null &&
            params.color_sharpness !== ""
        ) {
            advanced.color_sharpness = Number(params.color_sharpness);
            hasAdvanced = true;
        }
        if (
            params.alpha_sharpness !== undefined &&
            params.alpha_sharpness !== null &&
            params.alpha_sharpness !== ""
        ) {
            advanced.alpha_sharpness = Number(params.alpha_sharpness);
            hasAdvanced = true;
        }

        delete params.sharpness;
        delete params.color_sharpness;
        delete params.alpha_sharpness;

        if (hasAdvanced) {
            params.advanced = advanced;
        }

        if (params.jobs !== undefined && params.jobs !== null && params.jobs !== "") {
            params.jobs = Number(params.jobs);
        } else {
            delete params.jobs;
        }

        if (!params.use_custom_alpha_quality) {
            delete params.alpha_quality;
        } else if (
            params.alpha_quality !== undefined &&
            params.alpha_quality !== null &&
            params.alpha_quality !== ""
        ) {
            params.alpha_quality = Number(params.alpha_quality);
        }
        delete params.use_custom_alpha_quality;

        if (params.speed !== undefined && params.speed !== null && params.speed !== "") {
            params.speed = Number(params.speed);
        }

        if (params.depth === "auto" || params.depth === undefined || params.depth === null || params.depth === "") {
            delete params.depth;
        } else {
            params.depth = Number(params.depth);
        }

        if (
            params.color_range === "auto" ||
            params.color_range === undefined ||
            params.color_range === null ||
            params.color_range === ""
        ) {
            delete params.color_range;
        }
    }

    // For libjxl:jxl
    if (selectedEngine.value === "libjxl:jxl") {
        delete params.sub_mode;

        if (params.lossless) {
            delete params.quality;
            delete params.distance;
            delete params.jpeg_reconstruction;
        } else {
            if (params.quality !== undefined && params.quality !== null && params.quality !== "") {
                params.quality = Number(params.quality);
            }
            if (
                params.distance !== undefined &&
                params.distance !== null &&
                params.distance !== ""
            ) {
                params.distance = Number(params.distance);
            }
            params.jpeg_reconstruction = !!params.jpeg_reconstruction;
        }
        if (params.effort !== undefined && params.effort !== null && params.effort !== "") {
            params.effort = Number(params.effort);
        }
        params.lossless = !!params.lossless;
    }

    // Global clean up
    for (const key in params) {
        if (params[key] === undefined || params[key] === null || params[key] === "") {
            delete params[key];
        }
    }

    return params;
});

// Reset params when profile changes
watch(selectedProfile, (newProfile) => {
    if (newProfile !== "custom") {
        resetParamsToProfile();
    }
});

// Check if manually modified params match any template profile
const checkProfileMatch = () => {
    if (isApplyingTemplate.value) return;
    const engineTemplates = templates[selectedEngine.value];
    if (!engineTemplates) return;

    let matchedProfile: "size" | "balanced" | "speed" | "custom" = "custom";
    for (const profile of ["size", "balanced", "speed"] as const) {
        const templateData = engineTemplates[profile];
        if (!templateData) continue;

        let match = true;
        for (const key in templateData) {
            if (engineParams.value[key] !== templateData[key]) {
                match = false;
                break;
            }
        }

        // Also check if any additional configured key has a non-default/active value
        if (match) {
            for (const key in engineParams.value) {
                if (key === "mode" || key === "use_custom_alpha_quality") continue;
                if (templateData[key] === undefined) {
                    const val = engineParams.value[key];
                    if (val !== undefined && val !== null && val !== "") {
                        // Check if it represents an active setting (like jobs set, sharpness set, yuv not auto, etc.)
                        if (key === "yuv" && val === "auto") continue;
                        if (
                            (key === "sharpness" ||
                                key === "color_sharpness" ||
                                key === "alpha_sharpness") &&
                            Number(val) === 0
                        )
                            continue;
                        match = false;
                        break;
                    }
                }
            }
        }

        if (match) {
            matchedProfile = profile;
            break;
        }
    }
    selectedProfile.value = matchedProfile;
};

// Deep watch engineParams for manual changes
watch(
    engineParams,
    () => {
        checkProfileMatch();
    },
    { deep: true },
);

// QueueItem type
type QueueItem = {
    id: string; // Client ID
    source: "local" | "recent";
    uppyId?: string;
    serverFileId?: string;
    name: string;
    size: number;
    progress: number;
    status: "queued" | "uploading" | "uploaded" | "error" | "deleting" | "aborting";
    type?: string;
    error?: string;
    hash?: string;
    probe?: SourceProbeSummary;
};

// useAuth composable hook
const { isAuthenticated, showAuthModal, user } = useAuth();

// Uppy and Task Creation state
const files = ref<QueueItem[]>([]);
const globalProgress = ref(0);
const isCreatingTask = ref(false);
const taskCreated = ref(false);
const uploadedFileIds = ref<Record<string, string>>({});
const idempotencyKey = ref("");

// Probe summary from the first uploaded file in current queue
const firstFileProbe = computed<SourceProbeSummary | null>(() => {
    const item = files.value.find((f) => f.probe);
    return item?.probe || null;
});

const isUploading = computed(() => {
    return files.value.some((f) => f.status === "uploading" || f.status === "queued");
});

const isFormDisabled = computed(() => isCreatingTask.value || taskCreated.value);
const isParamsDisabled = computed(() => isCreatingTask.value || taskCreated.value);

const isSubmitDisabled = computed(() => {
    if (isUploading.value) return true;
    if (isCreatingTask.value) return true;
    if (taskCreated.value) return true;
    if (files.value.length === 0) return true;
    return !files.value.every((f) => f.status === "uploaded" && f.serverFileId);
});

// Authentication guard
const checkAuthBeforeAction = (): boolean => {
    if (!isAuthenticated.value) {
        showAuthModal.value = true;
        return false;
    }
    return true;
};

// Draft Box handling
const getDraftKey = () => {
    const uId = user.value?.id || "anonymous";
    return `task_new_draft:${uId}:v1`;
};

const pendingSubmission = ref<{ idempotencyKey: string; requestBody: string } | null>(null);

const saveDraft = () => {
    if (!isAuthenticated.value) return;
    const selectedServerFileIds = files.value
        .filter((f) => f.status === "uploaded" && f.serverFileId)
        .map((f) => f.serverFileId as string);

    const draft = {
        selectedServerFileIds,
        targetFormat: targetFormat.value,
        engine: selectedEngine.value,
        engineParams: engineParams.value,
        updatedAt: Date.now(),
        pendingSubmission: pendingSubmission.value,
    };
    localStorage.setItem(getDraftKey(), JSON.stringify(draft));
};

const clearDraft = () => {
    localStorage.removeItem(getDraftKey());
};

const isReconciling = ref(false);

const loadAndReconcileDraft = async () => {
    if (!isAuthenticated.value) return;
    const stored = localStorage.getItem(getDraftKey());
    if (!stored) return;
    try {
        const draft = JSON.parse(stored);
        if (!draft) return;
        if (draft.targetFormat) targetFormat.value = draft.targetFormat;
        if (draft.selectedEngine && defaultEngines[targetFormat.value] === draft.selectedEngine) {
            selectedEngine.value = draft.selectedEngine;
        }
        if (draft.engineParams) {
            Object.assign(engineParams.value, draft.engineParams);
        }
        if (draft.pendingSubmission) {
            pendingSubmission.value = draft.pendingSubmission;
            idempotencyKey.value = draft.pendingSubmission.idempotencyKey;
        }

        isReconciling.value = true;
        const res = await api<ApiResponse<{ files: any[] }>>("/drafts/list");
        const serverFiles = res?.data?.files || [];
        const serverFileMap = new Map(serverFiles.map((f) => [f.id, f]));

        if (draft.selectedServerFileIds && draft.selectedServerFileIds.length > 0) {
            const reconciledFiles: QueueItem[] = [];
            for (const serverId of draft.selectedServerFileIds) {
                const sf = serverFileMap.get(serverId);
                if (sf) {
                    reconciledFiles.push({
                        id: `recent-${sf.id}`,
                        source: "recent",
                        serverFileId: sf.id,
                        name: sf.name,
                        size: sf.size,
                        progress: 100,
                        status: "uploaded",
                        type: sf.mime_type || "",
                        hash: sf.hash || "",
                    });
                    uploadedFileIds.value[`recent-${sf.id}`] = sf.id;
                } else if (draft.pendingSubmission) {
                    // Reconstruct from draft for pending submissions
                    reconciledFiles.push({
                        id: `recent-${serverId}`,
                        source: "recent",
                        serverFileId: serverId,
                        name: "Pending file",
                        size: 0,
                        progress: 100,
                        status: "uploaded",
                        type: "",
                    });
                    uploadedFileIds.value[`recent-${serverId}`] = serverId;
                }
            }
            files.value = reconciledFiles;
        }
    } catch (e) {
        console.error("Failed to load draft:", e);
    } finally {
        isReconciling.value = false;
    }
};

// Watchers for draft box
watch(
    [targetFormat, selectedEngine, files, engineParams],
    () => {
        saveDraft();
    },
    { deep: true },
);

watch(
    () => user.value?.id,
    (newUid, oldUid) => {
        if (newUid !== oldUid) {
            if (uppy) {
                uppy.cancelAll();
            }
            files.value = [];
            uploadedFileIds.value = {};
            pendingSubmission.value = null;
            idempotencyKey.value = "";
            if (newUid) {
                loadAndReconcileDraft();
                fetchRecentFiles();
                fetchWorkflows();
            }
        }
    },
);

// Recent uploads (Draft files)
const recentFiles = ref<any[]>([]);
const filteredRecentFiles = computed(() => {
    return recentFiles.value.filter(
        (rf) => !files.value.some((f) => f.serverFileId === rf.id)
    );
});
const isLoadingRecent = ref(false);

const fetchRecentFiles = async () => {
    if (!isAuthenticated.value) return;
    isLoadingRecent.value = true;
    try {
        const res = await api<ApiResponse<{ files: any[] }>>("/drafts/list");
        if (res && res.data && res.data.files) {
            recentFiles.value = res.data.files;
        }
    } catch (err) {
        console.error("Failed to fetch recent files:", err);
    } finally {
        isLoadingRecent.value = false;
    }
};

const addRecentFile = (file: any) => {
    if (!checkAuthBeforeAction()) return;
    if (files.value.some((f) => f.serverFileId === file.id)) {
        toast.info("文件已在处理队列中");
        return;
    }
    files.value.push({
        id: `recent-${file.id}`,
        source: "recent",
        serverFileId: file.id,
        name: file.name,
        size: file.size,
        progress: 100,
        status: "uploaded",
        type: file.mime_type || "",
    });
    uploadedFileIds.value[`recent-${file.id}`] = file.id;
    saveDraft();
};

const deleteRecentFile = async (fileId: string) => {
    if (!checkAuthBeforeAction()) return;
    try {
        await api(`/drafts/${fileId}/delete`, { method: "POST" });
        recentFiles.value = recentFiles.value.filter((f) => f.id !== fileId);
        const idx = files.value.findIndex((f) => f.serverFileId === fileId);
        if (idx !== -1) {
            const item = files.value[idx];
            if (item) {
                const clientId = item.id;
                files.value.splice(idx, 1);
                delete uploadedFileIds.value[clientId];
            }
        }
        toast.success("文件物理删除成功");
        saveDraft();
    } catch (err) {
        console.error("Failed to delete file:", err);
        toast.error("删除文件失败");
    }
};

let uppy: Uppy;

onMounted(() => {
    uppy = new Uppy({
        restrictions: {
            maxNumberOfFiles: 50,
            allowedFileTypes: ["image/*", "video/*"],
        },
        autoProceed: true,
    });

    uppy.use(AwsS3, {
        shouldUseMultipart: true,
        limit: 4,
        createMultipartUpload: async (file) => {
            if (!file) {
                throw new Error("No file provided for upload");
            }
            if (file.meta.uploadId && file.meta.key) {
                return {
                    uploadId: file.meta.uploadId as string,
                    key: file.meta.key as string,
                };
            }
            const hash = (file.meta.hash as string) || "";
            const res = await api<MultipartUploadResponse>("/upload/multipart/init", {
                method: "POST",
                body: { filename: file.name, type: file.type, hash },
            });
            return { uploadId: res.uploadId, key: res.key };
        },
        listParts: async (file, { uploadId, key }) => {
            const res = await api<any>(
                `/upload/multipart/${uploadId}/list-parts?key=${encodeURIComponent(key)}`,
            );
            return res;
        },
        signPart: async (file, { uploadId, key, partNumber }) => {
            const res = await api<PartSignatureResponse>(
                `/upload/multipart/${uploadId}/sign-part/${partNumber}?key=${encodeURIComponent(key)}`,
            );
            return res; // contains { url }
        },
        abortMultipartUpload: async (file, { uploadId, key }) => {
            await api(`/upload/multipart/${uploadId}/abort`, {
                method: "POST",
                body: { key },
            });
        },
        completeMultipartUpload: async (file, { uploadId, key, parts }) => {
            const res = await api<any>(`/upload/multipart/${uploadId}/complete`, {
                method: "POST",
                body: { key, parts },
            });
            return res;
        },
    });

    uppy.on("file-added", (file) => {
        if (!checkAuthBeforeAction()) {
            uppy.removeFile(file.id);
            return;
        }
        const hash = (file.meta.hash as string) || "";
        const probe = file.meta.probe as SourceProbeSummary | undefined;
        files.value.push({
            id: file.id,
            source: "local",
            uppyId: file.id,
            name: file.name,
            size: file.size || 0,
            progress: 0,
            status: "uploading",
            type: file.type,
            hash: hash,
            probe,
        });
    });

    uppy.on("file-removed", (file) => {
        files.value = files.value.filter((f) => f.id !== file.id);
    });

    uppy.on("upload-progress", (file, progress) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "uploading";
            const p = progress as any;
            f.progress = Math.round((p.bytesUploaded / p.bytesTotal) * 100);
        }
    });

    uppy.on("progress", (progress) => {
        globalProgress.value = progress;
    });

    uppy.on("upload-success", (file, response) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "uploaded";
            f.progress = 100;
        }
        const fileId = response.body?.fileId || (response.body as any)?.file_id;
        if (fileId) {
            uploadedFileIds.value[file.id] = fileId;
            if (f) {
                f.serverFileId = fileId;
            }
        }
        saveDraft();
    });

    uppy.on("upload-error", (file, error) => {
        if (!file) return;
        const f = files.value.find((f) => f.id === file.id);
        if (f) {
            f.status = "error";
            f.error = parseApiError(error).userMessage;
        }
    });

    if (isAuthenticated.value) {
        loadAndReconcileDraft();
        fetchRecentFiles();
        fetchWorkflows();
    }
});

onUnmounted(() => {
    if (uppy) {
        const u = uppy as any;
        if (typeof u.close === "function") {
            u.close();
        } else if (typeof u.destroy === "function") {
            u.destroy();
        }
    }
});

const getSHA256 = async (file: File): Promise<string> => {
    const buffer = await file.arrayBuffer();
    const hashBuffer = await crypto.subtle.digest("SHA-256", buffer);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
};

const probeImageFile = (file: File): Promise<SourceProbeSummary> => {
    return new Promise((resolve) => {
        const ext = file.name.split(".").pop()?.toLowerCase() || "";
        const summary: SourceProbeSummary = {
            format: ext || file.type.split("/")[1] || "unknown",
            colorSpace: "sRGB",
            bitDepth: 8,
            isHdr: false,
            hasAlpha: file.type.includes("png") || file.type.includes("webp") || file.type.includes("avif"),
            hasIcc: false,
        };

        if (typeof window === "undefined" || !file.type.startsWith("image/")) {
            return resolve(summary);
        }

        const img = new Image();
        const url = URL.createObjectURL(file);
        img.onload = () => {
            summary.width = img.naturalWidth;
            summary.height = img.naturalHeight;
            URL.revokeObjectURL(url);
            resolve(summary);
        };
        img.onerror = () => {
            URL.revokeObjectURL(url);
            resolve(summary);
        };
        img.src = url;
    });
};

const processSingleFile = async (file: File) => {
    try {
        const hash = await getSHA256(file);
        const probe = await probeImageFile(file);

        // Local duplicate prevention
        if (files.value.some((f) => f.hash === hash)) {
            toast.warning(`文件 '${file.name}' 已经存在于当前列表中`);
            return;
        }

        const res = await api<MultipartUploadResponse>("/upload/multipart/init", {
            method: "POST",
            body: { filename: file.name, type: file.type, hash },
        });

        if (res.fastUpload) {
            const serverId = res.fileId || "";
            if (!serverId) {
                throw new Error("Fast upload response is missing fileId");
            }
            const clientFileId = `fast-${Math.random().toString(36).substring(2, 9)}`;
            files.value.push({
                id: clientFileId,
                source: "local",
                serverFileId: serverId,
                name: file.name,
                size: file.size,
                progress: 100,
                status: "uploaded",
                type: file.type || "",
                hash: hash,
                probe,
            });
            uploadedFileIds.value[clientFileId] = serverId;
            toast.success(`文件 '${file.name}' 秒传成功！`);
            saveDraft();
            return;
        }

        if (!res.uploadId || !res.key) {
            throw new Error("Multipart upload response is missing uploadId or key");
        }

        uppy.addFile({
            name: file.name,
            type: file.type,
            data: file,
            meta: {
                hash,
                uploadId: res.uploadId,
                key: res.key,
                probe,
            },
        });
    } catch (err: any) {
        console.error("Failed to process file:", err);
        const errMsg = parseApiError(err).userMessage || err.message || String(err);
        toast.error(`初始化文件 '${file.name}' 上传失败: ${errMsg}`);
    }
};

const handleFileSelect = (event: Event) => {
    if (!checkAuthBeforeAction()) return;
    const input = event.target as HTMLInputElement;
    if (input.files) {
        Array.from(input.files).forEach((file) => {
            processSingleFile(file);
        });
    }
    input.value = "";
};

const removeFile = async (id: string) => {
    const idx = files.value.findIndex((f) => f.id === id);
    if (idx === -1) return;
    const f = files.value[idx];
    if (!f) return;

    files.value.splice(idx, 1);
    if (f.uppyId) {
        try {
            uppy.removeFile(f.uppyId);
        } catch (e) {
            console.error(e);
        }
    }
    if (f.serverFileId) {
        delete uploadedFileIds.value[f.id];
        f.status = "deleting";
        try {
            await api(`/drafts/${f.serverFileId}/delete`, { method: "POST" });
        } catch (err) {
            console.error("Failed to delete file on server:", err);
        }
    }
    saveDraft();
};

const buildParamsPayload = () => {
    const paramsPayload: Record<string, any> = {
        engine: selectedEngine.value,
        engine_params: preparedEngineParams.value,
    };

    paramsPayload.color = {
        color_space: colorEncoding.value.color_space,
        chroma: colorEncoding.value.chroma,
        nominal_bit_depth: Number(colorEncoding.value.nominal_bit_depth),
        tone_mapping: colorEncoding.value.tone_mapping,
        dither: colorEncoding.value.dither,
    };
    paramsPayload.profile_policy = profilePolicy.value;

    if (strictMode.value) {
        paramsPayload.strict = true;
    }

    return paramsPayload;
};

const generateRequestSnapshot = () => {
    const sortedIds = files.value
        .filter((f) => f.status === "uploaded" && f.serverFileId)
        .map((f) => f.serverFileId as string);

    const items = sortedIds.map((id) => ({ file_id: id }));

    if (selectedWorkflowId.value !== "none" && !workflowDirty.value) {
        return JSON.stringify({
            items,
            workflow_id: selectedWorkflowId.value,
        });
    }

    return JSON.stringify({
        items,
        target_format: targetFormat.value.toUpperCase(),
        params: buildParamsPayload(),
    });
};

const fetchWorkflows = async () => {
    if (!isAuthenticated.value) return;
    try {
        const res = await api<ApiResponse<Workflow[]>>("/workflows");
        if (res?.data) workflows.value = res.data;
    } catch (err) {
        console.error("Failed to fetch workflows:", err);
    }
};

const applyWorkflow = async (id: string) => {
    const wf = workflows.value.find((w) => w.id === id);
    if (!wf) {
        selectedWorkflowId.value = "none";
        workflowDirty.value = false;
        return;
    }
    isApplyingWorkflow.value = true;
    try {
        const fmt = (wf.target_format || "avif").toLowerCase();
        if (defaultEngines[fmt]) {
            targetFormat.value = fmt;
            selectedEngine.value = defaultEngines[fmt];
        }
        selectedProfile.value = "custom";

        const params = (wf.params || {}) as Record<string, any>;
        engineParams.value = JSON.parse(JSON.stringify(params.engine_params || {}));

        if (selectedEngine.value === "libjxl:jxl" && engineParams.value.quality !== undefined) {
            engineParams.value.sub_mode = "quality";
        }
        if (engineParams.value.alpha_quality !== undefined) {
            engineParams.value.use_custom_alpha_quality = true;
        }

        if (params.color) {
            normalizeLoadedColorParams(params.color);
        }
        profilePolicy.value = params.profile_policy || "preserve_source";
        strictMode.value = !!params.strict;
        selectedWorkflowId.value = wf.id;
        workflowDirty.value = false;
        toast.success(`已应用预设「${wf.name}」`);
    } catch (err) {
        console.error("Failed to apply workflow:", err);
    } finally {
        await nextTick();
        isApplyingWorkflow.value = false;
    }
};

const savePreset = async () => {
    if (!checkAuthBeforeAction()) return;
    const name = presetName.value.trim();
    if (!name) {
        toast.error("请输入预设名称");
        return;
    }
    isSavingPreset.value = true;
    try {
        await api("/workflows", {
            method: "POST",
            body: {
                name,
                targetFormat: targetFormat.value.toUpperCase(),
                params: buildParamsPayload(),
            },
        });
        toast.success(`预设「${name}」已保存`);
        presetName.value = "";
        showPresetSave.value = false;
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to save workflow:", err);
        toast.error("保存预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

const updatePreset = async () => {
    if (selectedWorkflowId.value === "none") return;
    const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
    if (!wf) return;
    isSavingPreset.value = true;
    try {
        await api(`/workflows/${wf.id}`, {
            method: "PUT",
            body: {
                name: wf.name,
                targetFormat: targetFormat.value.toUpperCase(),
                params: buildParamsPayload(),
            },
        });
        workflowDirty.value = false;
        toast.success(`预设「${wf.name}」已更新为当前参数`);
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to update workflow:", err);
        toast.error("更新预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

const deletePreset = async () => {
    if (selectedWorkflowId.value === "none") return;
    const wf = workflows.value.find((w) => w.id === selectedWorkflowId.value);
    if (!wf) return;
    if (!window.confirm(`确认删除预设「${wf.name}」？已创建的任务不受影响。`)) return;
    isSavingPreset.value = true;
    try {
        await api(`/workflows/${wf.id}`, { method: "DELETE" });
        selectedWorkflowId.value = "none";
        workflowDirty.value = false;
        toast.success(`预设「${wf.name}」已删除`);
        await fetchWorkflows();
    } catch (err) {
        console.error("Failed to delete workflow:", err);
        toast.error("删除预设失败", { description: parseApiError(err).userMessage });
    } finally {
        isSavingPreset.value = false;
    }
};

// Mark the applied workflow as modified as soon as any parameter is tweaked.
// The task still submits explicit params in that case; the link only survives
// while the form matches the stored preset.
watch(
    [
        targetFormat,
        selectedEngine,
        profilePolicy,
        strictMode,
        engineParams,
        colorEncoding,
    ],
    () => {
        if (isApplyingWorkflow.value) return;
        if (selectedWorkflowId.value !== "none") {
            workflowDirty.value = true;
        }
    },
    { deep: true },
);

const submitTask = async () => {
    if (!checkAuthBeforeAction()) return;

    const currentSnapshot = generateRequestSnapshot();
    let key = idempotencyKey.value;
    if (pendingSubmission.value && pendingSubmission.value.requestBody === currentSnapshot) {
        key = pendingSubmission.value.idempotencyKey;
    } else {
        key = crypto.randomUUID();
        idempotencyKey.value = key;
        pendingSubmission.value = {
            idempotencyKey: key,
            requestBody: currentSnapshot,
        };
        saveDraft();
    }

    const body = JSON.parse(currentSnapshot);
    body.idempotency_key = key;

    isCreatingTask.value = true;
    try {
        const response = await api<ApiResponse<Task>>("/tasks", {
            method: "POST",
            body,
        });

        clearDraft();
        pendingSubmission.value = null;
        idempotencyKey.value = "";

        taskCreated.value = true;
        if (response && response.data && response.data.id) {
            router.push(`/tasks/${response.data.id}`);
        } else {
            router.push("/tasks");
        }
    } catch (err) {
        console.error("Failed to create task:", err);
        toast.error($t("new_task.task_creation_failed"), {
            description: parseApiError(err).userMessage,
        });
    } finally {
        isCreatingTask.value = false;
    }
};

const handleAction = () => {
    submitTask();
};

const submitButtonText = computed(() => {
    if (isCreatingTask.value) return $t("new_task.creating_task");
    if (taskCreated.value) return $t("new_task.task_created");

    const totalCount = files.value.length;
    const uploadingCount = files.value.filter((f) => f.status === "uploading").length;

    if (uploadingCount > 0) {
        const finishedCount = files.value.filter((f) => f.status === "uploaded").length;
        return `正在上传 ${finishedCount}/${totalCount}`;
    }

    const successfulCount = files.value.filter((f) => f.status === "uploaded").length;
    if (successfulCount > 0) {
        return `开始处理 ${successfulCount} 个文件`;
    }

    return $t("new_task.start_processing");
});

// Drag and drop state
const dragCounter = ref(0);
const isDragActive = computed(() => dragCounter.value > 0);

const handleDragEnter = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value++;
};

const handleDragLeave = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value = Math.max(0, dragCounter.value - 1);
};

const handleDragOver = (e: DragEvent) => {
    e.preventDefault();
};

const handleDrop = (e: DragEvent) => {
    if (isFormDisabled.value) return;
    dragCounter.value = 0;
    if (e.dataTransfer?.files) {
        Array.from(e.dataTransfer.files).forEach((file) => {
            processSingleFile(file);
        });
    }
};
</script>

<template>
    <div class="max-w-3xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-8 py-8 pb-24">
        <!-- Header -->
        <div class="flex flex-col gap-1">
            <h1 class="text-xl font-semibold tracking-tight text-ink font-sans">
                {{ $t("new_task.title") }}
            </h1>
            <p class="text-xs text-ink-subtle font-medium">
                {{ $t("new_task.subtitle") }}
            </p>
        </div>

        <div class="grid gap-6">
            <!-- Parameters Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
            >
                <CardHeader
                    class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between"
                >
                    <div class="space-y-0.5">
                        <CardTitle
                            class="text-sm font-semibold flex items-center gap-1.5 text-ink tracking-tight"
                        >
                            <Settings class="w-4 h-4 text-primary" />
                            {{ $t("new_task.params") }}
                        </CardTitle>
                        <p class="text-xs text-ink-subtle font-medium">
                            {{ $t("new_task.params_subtitle") }}
                        </p>
                    </div>
                    <button
                        @click="expertMode = !expertMode"
                        :disabled="isParamsDisabled"
                        class="flex items-center gap-1 px-2.5 py-1 rounded-lg border text-xs font-semibold transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                        :class="
                            expertMode
                                ? 'bg-primary/10 text-primary dark:text-primary-hover border-primary/20 dark:border-primary/30'
                                : 'bg-surface-2 text-ink-subtle border-hairline hover:bg-surface-3'
                        "
                    >
                        <Sliders class="w-3 h-3" />
                        {{ $t("new_task.expert_mode") }}
                    </button>
                </CardHeader>
                <CardContent class="p-5 space-y-6">
                    <!-- Workflow Presets -->
                    <div
                        v-if="isAuthenticated"
                        class="p-3.5 rounded-xl bg-surface-2/40 border border-hairline space-y-2.5"
                    >
                        <div class="flex items-center justify-between">
                            <Label class="text-xs font-semibold text-ink flex items-center gap-1.5">
                                <Layers class="w-3.5 h-3.5 text-primary" />
                                我的转码预设 (Workflows)
                            </Label>
                            <button
                                type="button"
                                :disabled="isParamsDisabled"
                                @click="showPresetSave = !showPresetSave"
                                class="text-xs font-semibold text-primary hover:text-primary-hover cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                {{ showPresetSave ? "取消保存" : "保存当前参数为预设" }}
                            </button>
                        </div>
                        <div class="flex gap-2">
                            <Select
                                :model-value="selectedWorkflowId"
                                @update:model-value="val => applyWorkflow(String(val ?? 'none'))"
                                :disabled="isParamsDisabled || workflows.length === 0"
                            >
                                <SelectTrigger
                                    class="h-9 flex-1 rounded-lg bg-surface-2 border-hairline text-ink text-xs"
                                >
                                    <SelectValue
                                        :placeholder="
                                            workflows.length === 0
                                                ? '暂无预设，可先保存当前参数'
                                                : '选择预设以一键套用'
                                        "
                                    />
                                </SelectTrigger>
                                <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                    <SelectItem value="none">不使用预设（手动参数）</SelectItem>
                                    <SelectItem v-for="wf in workflows" :key="wf.id" :value="wf.id">
                                        {{ wf.name }} · {{ wf.target_format }}
                                    </SelectItem>
                                </SelectContent>
                            </Select>
                            <Button
                                v-if="selectedWorkflowId !== 'none'"
                                type="button"
                                size="sm"
                                variant="outline"
                                :disabled="isSavingPreset || isParamsDisabled"
                                @click="updatePreset"
                                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold cursor-pointer"
                            >
                                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                                <span v-else>更新预设</span>
                            </Button>
                            <Button
                                v-if="selectedWorkflowId !== 'none'"
                                type="button"
                                size="sm"
                                variant="outline"
                                :disabled="isSavingPreset || isParamsDisabled"
                                @click="deletePreset"
                                class="h-9 px-3 rounded-lg border-hairline text-xs font-semibold text-red-500 hover:text-red-600 hover:bg-red-500/5 cursor-pointer"
                            >
                                删除
                            </Button>
                        </div>
                        <p
                            v-if="selectedWorkflowId !== 'none' && workflowDirty"
                            class="text-[11px] font-medium text-amber-600 dark:text-amber-400"
                        >
                            参数已修改：提交将使用当前参数；可点击「更新预设」覆盖原预设。
                        </p>
                        <div v-if="showPresetSave" class="flex gap-2 animate-in fade-in duration-200">
                            <Input
                                v-model="presetName"
                                :disabled="isSavingPreset"
                                placeholder="预设名称，例如：AVIF 画质优先"
                                class="h-9 flex-1 rounded-lg bg-surface-2 border-hairline text-xs"
                                @keyup.enter="savePreset"
                            />
                            <Button
                                type="button"
                                size="sm"
                                :disabled="isSavingPreset || !presetName.trim()"
                                @click="savePreset"
                                class="h-9 px-4 rounded-lg bg-primary text-primary-foreground hover:bg-primary-hover text-xs font-semibold cursor-pointer gap-1.5"
                            >
                                <Loader2 v-if="isSavingPreset" class="w-3.5 h-3.5 animate-spin" />
                                <span>保存</span>
                            </Button>
                        </div>
                    </div>

                    <!-- Transcode Settings Form Component -->
                    <TranscodeSettings
                        v-model:targetFormat="targetFormat"
                        v-model:selectedEngine="selectedEngine"
                        v-model:selectedProfile="selectedProfile"
                        v-model:engineParams="engineParams"
                        v-model:colorEncoding="colorEncoding"
                        v-model:profilePolicy="profilePolicy"
                        v-model:strictMode="strictMode"
                        v-model:expertMode="expertMode"
                        :sourceProbe="firstFileProbe"
                        :disabled="isParamsDisabled"
                    />
                </CardContent>
            </Card>

            <!-- Files Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden relative bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
                :class="{
                    'border-primary ring-1 ring-primary/20 bg-surface-2/10': isDragActive,
                }"
                @dragenter.prevent="handleDragEnter"
                @dragover.prevent="handleDragOver"
                @drop.stop.prevent="handleDrop"
            >
                <!-- Drag overlay -->
                <Transition
                    enter-active-class="transition duration-200 ease-out"
                    enter-from-class="opacity-0 scale-95"
                    enter-to-class="opacity-100 scale-100"
                    leave-active-class="transition duration-150 ease-in"
                    leave-from-class="opacity-100 scale-100"
                    leave-to-class="opacity-0 scale-95"
                >
                    <div
                        v-if="isDragActive"
                        class="absolute inset-0 bg-canvas/90 backdrop-blur-[1px] z-50 flex flex-col items-center justify-center gap-4 transition-all duration-300 border border-dashed border-primary/50 rounded-xl pointer-events-auto"
                        @dragleave.prevent="handleDragLeave"
                        @dragover.prevent="handleDragOver"
                        @drop.stop.prevent="handleDrop"
                    >
                        <div
                            class="h-12 w-12 rounded-lg bg-surface-2 flex items-center justify-center text-primary shadow-sm border border-hairline animate-bounce"
                        >
                            <UploadCloud class="w-6 h-6" />
                        </div>
                        <p class="text-xs font-semibold text-primary tracking-wide">
                            {{ $t("new_task.drop_files") }}
                        </p>
                    </div>
                </Transition>

                <CardHeader
                    class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between space-y-0"
                >
                    <CardTitle class="text-sm font-semibold text-ink tracking-tight">
                        {{ $t("new_task.media_files") }}
                    </CardTitle>
                    <div class="relative">
                        <input
                            type="file"
                            multiple
                            class="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed z-10"
                            @change="handleFileSelect"
                            :disabled="isFormDisabled"
                        />
                        <Button
                            variant="secondary"
                            size="sm"
                            class="rounded-lg px-3 h-8 gap-1.5 font-semibold bg-surface-2 text-ink border border-hairline hover:bg-surface-3 cursor-pointer"
                            :disabled="isFormDisabled"
                        >
                            <Plus class="w-3.5 h-3.5" />
                            {{ $t("new_task.add_files") }}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent class="p-5">
                    <div v-if="files.length > 0" class="space-y-2">
                        <div
                            v-for="file in files"
                            :key="file.id"
                            class="group flex items-center justify-between p-3 rounded-lg border border-hairline bg-surface-2/40 hover:bg-surface-2 transition-all duration-200"
                        >
                            <div class="flex items-center space-x-3 truncate flex-1 pr-4">
                                <div
                                    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface-1 border border-hairline text-ink-subtle shadow-xs group-hover:text-primary group-hover:border-primary/20 transition-all"
                                >
                                    <FileImage
                                        v-if="file.type?.startsWith('image/')"
                                        class="w-4 h-4"
                                    />
                                    <FileVideo
                                        v-else-if="file.type?.startsWith('video/')"
                                        class="w-4 h-4"
                                    />
                                    <FileCode v-else class="w-4 h-4" />
                                </div>
                                <div class="flex-1 truncate">
                                    <p class="text-xs font-semibold text-ink truncate">
                                        {{ file.name }}
                                    </p>
                                    <p
                                        v-if="file.status === 'error' && file.error"
                                        class="text-xs font-medium text-red-500 mt-1 truncate"
                                        :title="file.error"
                                    >
                                        {{ file.error }}
                                    </p>
                                    <p v-else class="text-xs font-medium text-ink-subtle mt-0.5">
                                        {{ (file.size / 1024 / 1024).toFixed(2) }} MB
                                    </p>
                                </div>
                            </div>

                            <div class="flex items-center space-x-4">
                                <div
                                    v-if="file.status === 'uploading'"
                                    class="w-24 flex flex-col items-end gap-1 shrink-0"
                                >
                                    <span class="text-xs font-semibold text-primary"
                                        >{{ file.progress }}%</span
                                    >
                                    <div
                                        class="h-1 w-full rounded-full bg-surface-2 overflow-hidden"
                                    >
                                        <div
                                            class="h-full bg-primary transition-all duration-500 ease-out"
                                            :style="{ width: `${file.progress}%` }"
                                        />
                                    </div>
                                </div>

                                <Button
                                    v-if="
                                        !isCreatingTask &&
                                        file.status !== 'deleting' &&
                                        file.status !== 'aborting'
                                    "
                                    variant="ghost"
                                    size="icon"
                                    @click="removeFile(file.id)"
                                    class="h-7 w-7 rounded-lg text-ink-subtle hover:text-red-500 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-all cursor-pointer"
                                >
                                    <X class="w-3.5 h-3.5" />
                                </Button>
                                <div
                                    v-if="file.status === 'uploaded'"
                                    class="w-7 h-7 rounded-full bg-emerald-500/10 flex items-center justify-center text-emerald-600 dark:text-emerald-400"
                                >
                                    <Check
                                        class="w-3.5 h-3.5 text-emerald-600 dark:text-emerald-400"
                                        stroke-width="2.5"
                                    />
                                </div>
                                <div
                                    v-else-if="file.status === 'error'"
                                    class="w-7 h-7 rounded-full bg-red-500/10 flex items-center justify-center text-red-500"
                                >
                                    <AlertCircle class="w-3.5 h-3.5" />
                                </div>
                            </div>
                        </div>
                    </div>

                    <div
                        v-else
                        class="flex flex-col items-center justify-center py-16 px-6 text-center border-2 border-dashed border-hairline rounded-xl bg-surface-2/20"
                    >
                        <div
                            class="h-10 w-10 rounded-lg bg-surface-2 flex items-center justify-center mb-4 text-ink-subtle shadow-xs border border-hairline"
                        >
                            <UploadCloud class="w-5 h-5 opacity-70" />
                        </div>
                        <p class="text-sm font-semibold text-ink mb-1">
                            {{ $t("new_task.no_files") }}
                        </p>
                        <p class="text-sm text-ink-subtle font-medium max-w-xs">
                            {{ $t("new_task.no_files_desc") }}
                        </p>
                    </div>

                    <div v-if="files.length > 0" class="pt-6 mt-6 border-t border-hairline">
                        <div
                            v-if="isUploading"
                            class="space-y-3 mb-6 max-w-md ml-auto animate-in fade-in duration-300"
                        >
                            <div class="flex justify-between items-end">
                                <span class="text-xs font-semibold text-ink-muted">
                                    {{ $t("new_task.uploading", { count: files.length }) }}
                                </span>
                                <span
                                    class="text-xl font-semibold text-primary dark:text-primary-hover"
                                    >{{ globalProgress }}%</span
                                >
                            </div>
                            <div class="h-1.5 w-full rounded-full bg-surface-2 overflow-hidden">
                                <div
                                    class="h-full bg-primary rounded-full transition-all duration-500 ease-out shadow-xs"
                                    :style="{ width: `${globalProgress}%` }"
                                />
                            </div>
                        </div>

                        <div class="flex justify-end">
                            <Button
                                @click="handleAction"
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

                    <!-- Recent Uploads Section -->
                    <div class="mt-8 pt-6 border-t border-hairline">
                        <div class="flex items-center justify-between mb-4">
                            <h3
                                class="text-xs font-semibold uppercase tracking-wider text-ink-muted flex items-center gap-1.5"
                            >
                                <HardDrive class="w-3.5 h-3.5" />
                                未提交的历史文件草稿
                            </h3>
                            <Button
                                variant="ghost"
                                size="sm"
                                class="h-7 px-2 text-xs font-semibold text-primary hover:bg-primary/5 cursor-pointer"
                                @click="fetchRecentFiles"
                                :disabled="isLoadingRecent"
                            >
                                <Loader2 v-if="isLoadingRecent" class="mr-1 h-3 w-3 animate-spin" />
                                刷新
                            </Button>
                        </div>

                        <div
                            v-if="filteredRecentFiles.length > 0"
                            class="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-48 overflow-y-auto pr-1"
                        >
                            <div
                                v-for="file in filteredRecentFiles"
                                :key="file.id"
                                class="flex items-center justify-between p-2 rounded-lg border border-hairline bg-surface-2/20 hover:bg-surface-2 transition-all"
                            >
                                <div
                                    class="flex items-center space-x-2 truncate flex-1 min-w-0 mr-2"
                                >
                                    <FileImage
                                        v-if="file.mime_type?.startsWith('image/')"
                                        class="w-3.5 h-3.5 shrink-0 text-ink-subtle"
                                    />
                                    <FileVideo
                                        v-else-if="file.mime_type?.startsWith('video/')"
                                        class="w-3.5 h-3.5 shrink-0 text-ink-subtle"
                                    />
                                    <FileCode v-else class="w-3.5 h-3.5 shrink-0 text-ink-subtle" />
                                    <span
                                        class="text-xs font-medium text-ink truncate"
                                        :title="file.name"
                                        >{{ file.name }}</span
                                    >
                                </div>
                                <div class="flex items-center space-x-1 shrink-0">
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        class="h-6 px-1.5 text-xs text-primary hover:bg-primary/5 font-semibold cursor-pointer"
                                        @click="addRecentFile(file)"
                                    >
                                        添加
                                    </Button>
                                    <Button
                                        variant="ghost"
                                        size="icon"
                                        class="h-6 w-6 text-ink-subtle hover:text-red-500 hover:bg-red-500/5 cursor-pointer"
                                        @click="deleteRecentFile(file.id)"
                                    >
                                        <Trash2 class="w-3.5 h-3.5" />
                                    </Button>
                                </div>
                            </div>
                        </div>
                        <div
                            v-else
                            class="text-center py-6 text-xs text-ink-subtle border border-dashed border-hairline rounded-lg"
                        >
                            无未提交的历史文件草稿
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    </div>
</template>
