<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Uppy } from '@uppy/core'
import AwsS3 from '@uppy/aws-s3'
import { useApi, parseApiError } from '~/composables/useApi'
import type { Task } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import type { MultipartUploadResponse, PartSignatureResponse } from '~/lib/types/s3'
import { toast } from 'vue-sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select'
import {
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger
} from '@/components/ui/tabs'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
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
    Sliders,
    Settings,
    Layers,
    Zap,
    Gauge,
    HardDrive
} from '@lucide/vue'

useHead({
    title: $t('new_task.title') + ' | Transcoder',
})

const router = useRouter()
const api = useApi()

// Form state
const targetFormat = ref('avif')

const defaultEngines: Record<string, string> = {
    avif: 'libavif:avif',
    webp: 'libwebp:webp',
    jpeg: 'libjpeg:jpeg',
    png: 'libpng:png',
    jxl: 'libjxl:jxl'
}

const selectedEngine = ref('libavif:avif')

const templates: Record<string, Record<string, Record<string, any>>> = {
    'libavif:avif': {
        size: { quality: 65, speed: 4, sharp_yuv: true },
        balanced: { quality: 75, speed: 6, sharp_yuv: true },
        speed: { quality: 75, speed: 8, sharp_yuv: false }
    },
    'libheif:avif': {
        size: { quality: 60, chroma_downsampling: 'sharp-yuv' },
        balanced: { quality: 70, chroma_downsampling: 'sharp-yuv' },
        speed: { quality: 75, chroma_downsampling: 'average' }
    },
    'libwebp:webp': {
        size: { quality: 75, lossless: false, method: 6 },
        balanced: { quality: 80, lossless: false, method: 4 },
        speed: { quality: 80, lossless: false, method: 2 }
    },
    'libjpeg:jpeg': {
        size: { quality: 75 },
        balanced: { quality: 85 },
        speed: { quality: 85 }
    },
    'libpng:png': {
        size: { compression_level: 9 },
        balanced: { compression_level: 6 },
        speed: { compression_level: 1 }
    },
    'libjxl:jxl': {
        size: { distance: 1.5, effort: 7, progressive: true },
        balanced: { distance: 1.0, effort: 5, progressive: true },
        speed: { distance: 1.0, effort: 3, progressive: false }
    }
}

const expertMode = ref(false)
const selectedProfile = ref<'size' | 'balanced' | 'speed' | 'custom'>('balanced')
const engineParams = ref<Record<string, any>>({ quality: 75, speed: 6, sharp_yuv: true })

// Auto-switch engine when target format changes
watch(targetFormat, (newFormat) => {
    const defaultEngine = defaultEngines[newFormat] || 'libavif:avif'
    selectedEngine.value = defaultEngine
    resetParamsToProfile()
})

// Reset params when engine changes
watch(selectedEngine, () => {
    resetParamsToProfile()
})

const isApplyingTemplate = ref(false)
const resetParamsToProfile = () => {
    isApplyingTemplate.value = true
    const engineTemplates = templates[selectedEngine.value]
    if (engineTemplates && selectedProfile.value !== 'custom') {
        const profileData = engineTemplates[selectedProfile.value]
        if (profileData) {
            engineParams.value = JSON.parse(JSON.stringify(profileData))
        }
    }
    // Initialize helper modes
    if (selectedEngine.value === 'libjxl:jxl') {
        if (engineParams.value.quality !== undefined) {
            engineParams.value.mode = 'quality'
        } else {
            engineParams.value.mode = 'distance'
        }
    }
    isApplyingTemplate.value = false
}

const handleJxlModeChange = (newMode: string | number) => {
    if (selectedEngine.value !== 'libjxl:jxl') return
    const modeStr = String(newMode)
    engineParams.value.mode = modeStr
    if (modeStr === 'quality') {
        delete engineParams.value.distance
        if (engineParams.value.quality === undefined) {
            engineParams.value.quality = 85
        }
    } else {
        delete engineParams.value.quality
        if (engineParams.value.distance === undefined) {
            engineParams.value.distance = 1.0
        }
    }
}

const preparedEngineParams = computed(() => {
    const params = JSON.parse(JSON.stringify(engineParams.value))

    // For libavif:avif
    if (selectedEngine.value === 'libavif:avif') {
        const advanced: Record<string, number> = {}
        let hasAdvanced = false

        if (params.sharpness !== undefined && params.sharpness !== null && params.sharpness !== '') {
            advanced.sharpness = Number(params.sharpness)
            hasAdvanced = true
        }
        if (params.color_sharpness !== undefined && params.color_sharpness !== null && params.color_sharpness !== '') {
            advanced.color_sharpness = Number(params.color_sharpness)
            hasAdvanced = true
        }
        if (params.alpha_sharpness !== undefined && params.alpha_sharpness !== null && params.alpha_sharpness !== '') {
            advanced.alpha_sharpness = Number(params.alpha_sharpness)
            hasAdvanced = true
        }

        delete params.sharpness
        delete params.color_sharpness
        delete params.alpha_sharpness

        if (hasAdvanced) {
            params.advanced = advanced
        }

        if (params.jobs !== undefined && params.jobs !== null && params.jobs !== '') {
            params.jobs = Number(params.jobs)
        } else {
            delete params.jobs
        }

        if (!params.use_custom_alpha_quality) {
            delete params.alpha_quality
        } else if (params.alpha_quality !== undefined && params.alpha_quality !== null && params.alpha_quality !== '') {
            params.alpha_quality = Number(params.alpha_quality)
        }
        delete params.use_custom_alpha_quality

        if (params.speed !== undefined && params.speed !== null && params.speed !== '') {
            params.speed = Number(params.speed)
        }
    }

    // For libjxl:jxl
    if (selectedEngine.value === 'libjxl:jxl') {
        delete params.mode

        if (params.quality !== undefined && params.quality !== null && params.quality !== '') {
            params.quality = Number(params.quality)
        }
        if (params.distance !== undefined && params.distance !== null && params.distance !== '') {
            params.distance = Number(params.distance)
        }
        if (params.effort !== undefined && params.effort !== null && params.effort !== '') {
            params.effort = Number(params.effort)
        }
    }

    // Global clean up
    for (const key in params) {
        if (params[key] === undefined || params[key] === null || params[key] === '') {
            delete params[key]
        }
    }

    return params
})

// Reset params when profile changes
watch(selectedProfile, (newProfile) => {
    if (newProfile !== 'custom') {
        resetParamsToProfile()
    }
})

// Check if manually modified params match any template profile
const checkProfileMatch = () => {
    if (isApplyingTemplate.value) return
    const engineTemplates = templates[selectedEngine.value]
    if (!engineTemplates) return

    let matchedProfile: 'size' | 'balanced' | 'speed' | 'custom' = 'custom'
    for (const profile of ['size', 'balanced', 'speed'] as const) {
        const templateData = engineTemplates[profile]
        if (!templateData) continue

        let match = true
        for (const key in templateData) {
            if (engineParams.value[key] !== templateData[key]) {
                match = false
                break
            }
        }

        // Also check if any additional configured key has a non-default/active value
        if (match) {
            for (const key in engineParams.value) {
                if (key === 'mode' || key === 'use_custom_alpha_quality') continue
                if (templateData[key] === undefined) {
                    const val = engineParams.value[key]
                    if (val !== undefined && val !== null && val !== '') {
                        // Check if it represents an active setting (like jobs set, sharpness set, yuv not auto, etc.)
                        if (key === 'yuv' && val === 'auto') continue
                        if ((key === 'sharpness' || key === 'color_sharpness' || key === 'alpha_sharpness') && Number(val) === 0) continue
                        match = false
                        break
                    }
                }
            }
        }

        if (match) {
            matchedProfile = profile
            break
        }
    }
    selectedProfile.value = matchedProfile
}

// Deep watch engineParams for manual changes
watch(engineParams, () => {
    checkProfileMatch()
}, { deep: true })



// Uppy and Task Creation state
const files = ref<any[]>([])
const isUploading = ref(false)
const globalProgress = ref(0)
const uploadSucceeded = ref(false)
const isCreatingTask = ref(false)
const taskCreated = ref(false)
const uploadedFileIds = ref<Record<string, string>>({})
const idempotencyKey = ref('')
const uploadStarted = ref(false)

const isFormDisabled = computed(() => uploadStarted.value || isCreatingTask.value || taskCreated.value)
const isParamsDisabled = computed(() => isUploading.value || isCreatingTask.value || taskCreated.value)
const isSubmitDisabled = computed(() => {
    if (isUploading.value) return true
    if (isCreatingTask.value) return true
    if (taskCreated.value) return true
    if (!uploadSucceeded.value && files.value.length === 0) return true
    return false
})

let uppy: Uppy

onMounted(() => {
    uppy = new Uppy({
        restrictions: {
            maxNumberOfFiles: 50,
            allowedFileTypes: ['image/*', 'video/*'],
        },
        autoProceed: false,
    })

    uppy.use(AwsS3, {
        shouldUseMultipart: true,
        limit: 4,
        createMultipartUpload: async (file) => {
            if (!file) {
                throw new Error('No file provided for upload')
            }
            const res = await api<MultipartUploadResponse>('/s3/multipart', {
                method: 'POST',
                body: { filename: file.name, type: file.type },
            })
            return { uploadId: res.uploadId, key: res.key }
        },
        listParts: async (file, { uploadId, key }) => {
            const res = await api<any>(`/s3/multipart/${uploadId}?key=${encodeURIComponent(key)}`)
            return res
        },
        signPart: async (file, { uploadId, key, partNumber }) => {
            const res = await api<PartSignatureResponse>(`/s3/multipart/${uploadId}/${partNumber}?key=${encodeURIComponent(key)}`)
            return res // contains { url }
        },
        abortMultipartUpload: async (file, { uploadId, key }) => {
            await api(`/s3/multipart/${uploadId}?key=${encodeURIComponent(key)}`, {
                method: 'DELETE',
            })
        },
        completeMultipartUpload: async (file, { uploadId, key, parts }) => {
            const res = await api<any>(`/s3/multipart/${uploadId}/complete`, {
                method: 'POST',
                body: { key, parts },
            })
            return res
        },
    })

    uppy.on('file-added', (file) => {
        files.value.push({
            id: file.id,
            name: file.name,
            size: file.size,
            progress: 0,
            status: 'pending',
        })
    })

    uppy.on('file-removed', (file) => {
        files.value = files.value.filter((f) => f.id !== file.id)
    })

    uppy.on('upload-progress', (file, progress) => {
        if (!file) return
        const f = files.value.find((f) => f.id === file.id)
        if (f) {
            // Using type assertion to avoid TS errors on Uppy plugin properties
            const p = progress as any
            f.progress = Math.round((p.bytesUploaded / p.bytesTotal) * 100)
        }
    })

    uppy.on('progress', (progress) => {
        globalProgress.value = progress
    })

    uppy.on('upload-success', (file, response) => {
        if (!file) return
        const f = files.value.find((f) => f.id === file.id)
        if (f) {
            f.status = 'success'
        }

        // Extract fileId from response body (returned from complete multipart upload)
        const fileId = response.body?.fileId || (response.body as any)?.file_id
        if (fileId) {
            uploadedFileIds.value[file.id] = fileId
        }
    })

    uppy.on('upload-error', (file, error) => {
        if (!file) return
        const f = files.value.find((f) => f.id === file.id)
        if (f) {
            f.status = 'error'
            f.error = parseApiError(error).userMessage
        }
    })

    uppy.on('complete', (result) => {
        isUploading.value = false
        if (result.failed && result.failed.length > 0) {
            console.error('Some uploads failed:', result.failed)
            uploadSucceeded.value = false

            toast.error($t('new_task.upload_failed'), {
                description: $t('new_task.upload_failed_desc', { count: result.failed.length }),
                position: "top-right",
            })
            return
        }

        const uploadedCount = Object.keys(uploadedFileIds.value).length
        if (uploadedCount !== files.value.length) {
            console.error('Uploaded file IDs mismatch:', uploadedFileIds.value, files.value)
            uploadSucceeded.value = false
            toast.error($t('new_task.upload_failed'), {
                description: 'Uploaded files count mismatch or upload IDs are invalid.',
                position: "top-right",
            })
            return
        }

        uploadSucceeded.value = true
        submitTask()
    })
})

onUnmounted(() => {
    if (uppy) {
        // Handle potential type mismatch for close/destroy
        const u = uppy as any
        if (typeof u.close === 'function') {
            u.close()
        } else if (typeof u.destroy === 'function') {
            u.destroy()
        }
    }
})

const handleFileSelect = (event: Event) => {
    const input = event.target as HTMLInputElement
    if (input.files) {
        Array.from(input.files).forEach((file) => {
            try {
                uppy.addFile({
                    name: file.name,
                    type: file.type,
                    data: file,
                })
            } catch (err) {
                console.error('Error adding file:', err)
            }
        })
    }
    input.value = ''
}

const removeFile = (id: string) => {
    uppy.removeFile(id)
}

const startUpload = () => {
    if (files.value.length === 0) return
    uploadStarted.value = true
    if (!idempotencyKey.value) {
        idempotencyKey.value = crypto.randomUUID()
    }
    isUploading.value = true
    uppy.upload()
}

const submitTask = async () => {
    const uploadedCount = Object.keys(uploadedFileIds.value).length
    const hasMismatchedFiles = files.value.some(f => !uploadedFileIds.value[f.id])
    if (files.value.length === 0 || uploadedCount !== files.value.length || hasMismatchedFiles) {
        toast.error($t('new_task.upload_failed'), {
            description: 'Cannot submit task: uploaded files count mismatch or some files have not been uploaded successfully.'
        })
        return
    }

    const items = files.value.map(f => ({
        file_id: uploadedFileIds.value[f.id]
    }))

    isCreatingTask.value = true
    try {
        const response = await api<ApiResponse<Task>>('/tasks', {
            method: 'POST',
            body: {
                items,
                target_format: targetFormat.value.toUpperCase(),
                params: {
                    engine: selectedEngine.value,
                    engine_params: preparedEngineParams.value
                },
                idempotency_key: idempotencyKey.value
            }
        })

        taskCreated.value = true
        router.push('/')
    } catch (err) {
        console.error('Failed to create task:', err)
        toast.error($t('new_task.task_creation_failed'), {
            description: parseApiError(err).userMessage
        })
    } finally {
        isCreatingTask.value = false
    }
}

const handleAction = () => {
    if (uploadSucceeded.value) {
        submitTask()
    } else {
        startUpload()
    }
}

const submitButtonText = computed(() => {
    if (isUploading.value) return $t('new_task.processing')
    if (isCreatingTask.value) return $t('new_task.creating_task')
    if (taskCreated.value) return $t('new_task.task_created')
    if (uploadSucceeded.value && !taskCreated.value) return $t('new_task.retry_creation', 'Retry Creating Task')
    return $t('new_task.start_processing')
})

// Drag and drop state
const dragCounter = ref(0)
const isDragActive = computed(() => dragCounter.value > 0)

const handleDragEnter = (e: DragEvent) => {
    if (isFormDisabled.value) return
    dragCounter.value++
}

const handleDragLeave = (e: DragEvent) => {
    if (isFormDisabled.value) return
    dragCounter.value = Math.max(0, dragCounter.value - 1)
}

const handleDragOver = (e: DragEvent) => {
    e.preventDefault()
}

const handleDrop = (e: DragEvent) => {
    if (isFormDisabled.value) return
    dragCounter.value = 0
    if (e.dataTransfer?.files) {
        Array.from(e.dataTransfer.files).forEach((file) => {
            try {
                uppy.addFile({
                    name: file.name,
                    type: file.type,
                    data: file,
                })
            } catch (err) {
                console.error('Error adding file:', err)
            }
        })
    }
}
</script>

<template>
    <div class="max-w-3xl mx-auto w-full px-4 sm:px-6 lg:px-8 space-y-8 py-8 pb-24">
        <!-- Header -->
        <div class="flex flex-col gap-1">
            <h1 class="text-xl font-semibold tracking-tight text-ink font-sans">
                {{ $t('new_task.title') }}
            </h1>
            <p class="text-xs text-ink-subtle font-medium">
                {{ $t('new_task.subtitle') }}
            </p>
        </div>

        <div class="grid gap-6">
            <!-- Parameters Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0">
                <CardHeader
                    class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between">
                    <div class="space-y-0.5">
                        <CardTitle class="text-sm font-semibold flex items-center gap-1.5 text-ink tracking-tight">
                            <Settings class="w-4 h-4 text-primary" />
                            {{ $t('new_task.params') }}
                        </CardTitle>
                        <p class="text-[10px] text-ink-subtle font-medium">
                            Configure image optimization and transcoding parameters
                        </p>
                    </div>
                    <button @click="expertMode = !expertMode" :disabled="isParamsDisabled"
                        class="flex items-center gap-1 px-2.5 py-1 rounded-lg border text-[11px] font-semibold transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                        :class="expertMode ? 'bg-primary/10 text-primary dark:text-primary-hover border-primary/20 dark:border-primary/30' : 'bg-surface-2 text-ink-subtle border-hairline hover:bg-surface-3'">
                        <Sliders class="w-3 h-3" />
                        Expert Mode
                    </button>
                </CardHeader>
                <CardContent class="p-5 space-y-6">
                    <!-- Target Format & Engine Selection -->
                    <div class="grid gap-6 md:grid-cols-2">
                        <div class="space-y-3">
                            <Label
                                class="text-xs font-semibold uppercase tracking-wider text-ink-subtle flex items-center gap-1.5">
                                <Layers class="w-4 h-4 text-ink-subtle" />
                                {{ $t('new_task.target_format') }}
                            </Label>
                            <Select v-model="targetFormat" :disabled="isFormDisabled">
                                <SelectTrigger
                                    class="h-10 rounded-lg bg-surface-2 border-hairline text-ink focus:ring-0 focus:border-primary text-xs font-medium transition-colors">
                                    <SelectValue placeholder="Select format" />
                                </SelectTrigger>
                                <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                    <SelectItem value="avif">AVIF (Recommended)</SelectItem>
                                    <SelectItem value="webp">WebP</SelectItem>
                                    <SelectItem value="jpeg">JPEG</SelectItem>
                                    <SelectItem value="png">PNG</SelectItem>
                                    <SelectItem value="jxl">JPEG XL</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>

                        <!-- Engine Select (expert mode only) -->
                        <div v-if="expertMode" class="space-y-3">
                            <Label
                                class="text-xs font-semibold uppercase tracking-wider text-ink-subtle flex items-center gap-1.5">
                                <Settings class="w-4 h-4 text-ink-subtle" />
                                Engine
                            </Label>
                            <Select v-model="selectedEngine" :disabled="isParamsDisabled">
                                <SelectTrigger
                                    class="h-10 rounded-lg bg-surface-2 border-hairline text-ink focus:ring-0 focus:border-primary text-xs font-medium transition-colors">
                                    <SelectValue placeholder="Select engine" />
                                </SelectTrigger>
                                <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                    <template v-if="targetFormat === 'avif'">
                                        <SelectItem value="libavif:avif">libavif (Official)</SelectItem>
                                        <SelectItem value="libheif:avif">libheif (avifenc)</SelectItem>
                                    </template>
                                    <template v-else-if="targetFormat === 'webp'">
                                        <SelectItem value="libwebp:webp">libwebp</SelectItem>
                                    </template>
                                    <template v-else-if="targetFormat === 'jpeg'">
                                        <SelectItem value="libjpeg:jpeg">libjpeg</SelectItem>
                                    </template>
                                    <template v-else-if="targetFormat === 'png'">
                                        <SelectItem value="libpng:png">libpng</SelectItem>
                                    </template>
                                    <template v-else-if="targetFormat === 'jxl'">
                                        <SelectItem value="libjxl:jxl">libjxl</SelectItem>
                                    </template>
                                </SelectContent>
                            </Select>
                        </div>
                    </div>

                    <!-- Optimize Profile Presets -->
                    <div class="space-y-3">
                        <Label class="text-xs font-semibold uppercase tracking-wider text-ink-subtle">
                            Optimize Template
                        </Label>
                        <div
                            class="p-1 bg-surface-2/50 rounded-xl border border-hairline flex gap-1 w-full">
                            <button @click="selectedProfile = 'balanced'" :disabled="isParamsDisabled"
                                class="flex-1 flex flex-col md:flex-row items-center justify-center gap-2 py-2 px-3 rounded-lg text-xs font-semibold transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                :class="selectedProfile === 'balanced' ? 'bg-surface-1 text-primary dark:text-primary-hover shadow-sm border border-hairline-strong' : 'text-ink-subtle hover:text-ink'">
                                <Gauge class="w-4 h-4 shrink-0" />
                                <span>Balanced</span>
                            </button>
                            <button @click="selectedProfile = 'size'" :disabled="isParamsDisabled"
                                class="flex-1 flex flex-col md:flex-row items-center justify-center gap-2 py-2 px-3 rounded-lg text-xs font-semibold transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                :class="selectedProfile === 'size' ? 'bg-surface-1 text-primary dark:text-primary-hover shadow-sm border border-hairline-strong' : 'text-ink-subtle hover:text-ink'">
                                <HardDrive class="w-4 h-4 shrink-0" />
                                <span>Size First</span>
                            </button>
                            <button @click="selectedProfile = 'speed'" :disabled="isParamsDisabled"
                                class="flex-1 flex flex-col md:flex-row items-center justify-center gap-2 py-2 px-3 rounded-lg text-xs font-semibold transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                :class="selectedProfile === 'speed' ? 'bg-surface-1 text-primary dark:text-primary-hover shadow-sm border border-hairline-strong' : 'text-ink-subtle hover:text-ink'">
                                <Zap class="w-4 h-4 shrink-0" />
                                <span>Speed First</span>
                            </button>
                            <button disabled
                                class="flex-1 flex flex-col md:flex-row items-center justify-center gap-2 py-2 px-3 rounded-lg text-xs font-semibold border border-transparent transition-all duration-200"
                                :class="selectedProfile === 'custom' ? 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-200/40 dark:border-amber-900/30' : 'text-ink-subtle opacity-30 cursor-not-allowed'">
                                <Sliders class="w-4 h-4 shrink-0" />
                                <span>Custom</span>
                            </button>
                        </div>
                    </div>

                    <!-- Dynamic Parameters Controls -->
                    <div class="border-t border-hairline pt-6 space-y-6">
                        <!-- Quality Option (Universal for most engines except PNG / JXL) -->
                        <div v-if="selectedEngine !== 'libpng:png' && selectedEngine !== 'libjxl:jxl'"
                            class="space-y-3">
                            <div class="flex justify-between items-center">
                                <Label class="text-xs font-semibold uppercase tracking-wider text-ink-subtle">
                                    Quality
                                </Label>
                                <span
                                    class="text-xs font-bold text-primary dark:text-primary-hover bg-primary/10 px-2.5 py-1 rounded-lg">
                                    {{ engineParams.quality }}
                                </span>
                            </div>
                            <input type="range" v-model.number="engineParams.quality" min="0" max="100" :disabled="isParamsDisabled"
                                class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent
                                       [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                       [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed" />
                            <p class="text-[10px] text-ink-subtle font-medium">
                                Higher quality values result in better details but larger file sizes.
                            </p>
                        </div>

                        <!-- JXL Quality / Distance Options -->
                        <div v-if="selectedEngine === 'libjxl:jxl'" class="space-y-6">
                            <div class="flex flex-col gap-2">
                                <Label class="text-xs font-semibold uppercase tracking-wider text-ink-subtle">
                                    Encoding Mode
                                </Label>
                                <Tabs :model-value="engineParams.mode || 'distance'"
                                    @update:model-value="handleJxlModeChange" class="w-full">
                                    <TabsList
                                        class="flex p-1 bg-surface-2/50 border border-hairline rounded-xl w-full max-w-[400px] h-10 gap-1">
                                        <TabsTrigger value="distance" :disabled="isParamsDisabled"
                                            class="flex-1 rounded-lg text-xs font-semibold py-1.5 text-ink-subtle data-[state=active]:bg-white data-[state=active]:bg-surface-1 data-[state=active]:text-primary data-[state=active]:dark:text-primary-hover data-[state=active]:shadow-sm data-[state=active]:border data-[state=active]:border-slate-200/50 data-[state=active]:border-hairline-strong transition-all disabled:opacity-50 disabled:cursor-not-allowed">
                                            Visual Distance (d)
                                        </TabsTrigger>
                                        <TabsTrigger value="quality" :disabled="isParamsDisabled"
                                            class="flex-1 rounded-lg text-xs font-semibold py-1.5 text-ink-subtle data-[state=active]:bg-white data-[state=active]:bg-surface-1 data-[state=active]:text-primary data-[state=active]:dark:text-primary-hover data-[state=active]:shadow-sm data-[state=active]:border data-[state=active]:border-slate-200/50 data-[state=active]:border-hairline-strong transition-all disabled:opacity-50 disabled:cursor-not-allowed">
                                            Target Quality (q)
                                        </TabsTrigger>
                                    </TabsList>
                                </Tabs>
                            </div>

                            <!-- Distance Control -->
                            <div v-if="(engineParams.mode || 'distance') === 'distance'" class="space-y-3">
                                <div class="flex justify-between items-center">
                                    <Label class="text-xs font-semibold uppercase tracking-wider text-ink-subtle">
                                        Visual Distance
                                    </Label>
                                    <span
                                        class="text-xs font-bold text-primary dark:text-primary-hover bg-primary/10 px-2.5 py-1 rounded-lg">
                                        {{ engineParams.distance }}
                                    </span>
                                </div>
                                 <input type="range" v-model.number="engineParams.distance" min="0" max="15" step="0.1" :disabled="isParamsDisabled"
                                    class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent
                                           [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                           [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed" />
                                <p class="text-[10px] text-ink-subtle font-medium">
                                    0.0 is lossless, 1.0 is visually lossless. Higher values mean smaller files and higher degradation (max 15.0).
                                </p>
                            </div>

                            <!-- Quality Control -->
                            <div v-else class="space-y-3">
                                <div class="flex justify-between items-center">
                                    <Label class="text-xs font-semibold uppercase tracking-wider text-ink-subtle">
                                        Target Quality
                                    </Label>
                                    <span
                                        class="text-xs font-bold text-primary dark:text-primary-hover bg-primary/10 px-2.5 py-1 rounded-lg">
                                        {{ engineParams.quality }}
                                    </span>
                                </div>
                                <input type="range" v-model.number="engineParams.quality" min="0" max="100" :disabled="isParamsDisabled"
                                    class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent
                                           [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                           [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed" />
                                <p class="text-[10px] text-ink-subtle font-medium">
                                    0-100 scale, where 100 is mathematically lossless. Recommended range: 80-95.
                                </p>
                            </div>
                        </div>

                        <!-- Advanced Parameters Grid (expert mode only) -->
                        <div v-if="expertMode"
                            class="grid gap-6 md:grid-cols-2 bg-slate-50/50 bg-surface-2/30 p-6 rounded-2xl border border-hairline animate-in fade-in duration-300">
                            <!-- libavif:avif settings -->
                            <template v-if="selectedEngine === 'libavif:avif'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label
                                            class="text-sm font-semibold text-slate-700 text-ink-muted">Speed</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.speed }} / 10</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.speed" min="0" max="10" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed" />
                                    <p class="text-[10px] text-ink-subtle">0 is slowest (highest compression), 10 is fastest (larger size).</p>
                                </div>
                                <div
                                    class="flex items-center justify-between p-3 bg-surface-1 rounded-xl border border-hairline">
                                    <div class="space-y-0.5">
                                        <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Sharp YUV</Label>
                                        <p class="text-[10px] text-ink-subtle">Improve edge details and color matching</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer select-none">
                                        <input type="checkbox" v-model="engineParams.sharp_yuv" :disabled="isParamsDisabled" class="sr-only peer">
                                        <div class="w-10 h-5 bg-slate-200 bg-surface-2 rounded-full peer 
                                                    peer-checked:bg-primary
                                                    peer-disabled:opacity-50 peer-disabled:cursor-not-allowed
                                                    after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all after:shadow-sm
                                                    peer-checked:after:translate-x-5 peer-checked:after:bg-white
                                                    border border-slate-300/10 dark:border-hairline-strong
                                                    transition-all duration-200"></div>
                                    </label>
                                </div>
                                <div class="space-y-3">
                                    <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Chroma Subsampling (YUV)</Label>
                                    <Select v-model="engineParams.yuv" :disabled="isParamsDisabled">
                                        <SelectTrigger
                                            class="h-10 rounded-lg bg-white bg-surface-2 border-hairline text-ink focus:ring-0 focus:border-primary text-xs font-medium transition-colors">
                                            <SelectValue placeholder="Select YUV format" />
                                        </SelectTrigger>
                                        <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                            <SelectItem value="auto">Auto (Default)</SelectItem>
                                            <SelectItem value="420">4:2:0 (Standard / Compact)</SelectItem>
                                            <SelectItem value="422">4:2:2 (High color fidelity)</SelectItem>
                                            <SelectItem value="444">4:4:4 (Lossless color / Sharp details)</SelectItem>
                                        </SelectContent>
                                    </Select>
                                    <p class="text-[10px] text-ink-subtle">Output chroma subsampling format. 4:2:0 is recommended for compatibility.</p>
                                </div>
                                <div class="space-y-3">
                                    <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Jobs (Thread Count)</Label>
                                    <Input type="number" v-model.number="engineParams.jobs" :min="1" :disabled="isParamsDisabled"
                                        placeholder="Auto (All threads)"
                                        class="h-10 rounded-lg bg-white bg-surface-2 border-hairline focus-visible:ring-1 focus-visible:ring-primary/20 focus-visible:border-primary text-ink text-xs disabled:opacity-50 disabled:cursor-not-allowed" />
                                    <p class="text-[10px] text-ink-subtle">Specify maximum encoding threads. Default uses all available cores.</p>
                                </div>
                                <div
                                    class="space-y-3 md:col-span-2 p-4 bg-surface-1 rounded-xl border border-hairline">
                                    <div class="flex items-center justify-between">
                                        <div class="space-y-0.5">
                                            <Label
                                                class="text-sm font-semibold text-slate-700 text-ink-muted">Custom Alpha Quality</Label>
                                            <p class="text-[10px] text-ink-subtle">Enable customized quality setting specifically for the transparency channel</p>
                                        </div>
                                        <label class="relative inline-flex items-center cursor-pointer select-none">
                                            <input type="checkbox" v-model="engineParams.use_custom_alpha_quality" :disabled="isParamsDisabled"
                                                class="sr-only peer">
                                            <div class="w-10 h-5 bg-slate-200 bg-surface-2 rounded-full peer 
                                                        peer-checked:bg-primary
                                                        peer-disabled:opacity-50 peer-disabled:cursor-not-allowed
                                                        after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all after:shadow-sm
                                                        peer-checked:after:translate-x-5 peer-checked:after:bg-white
                                                        border border-slate-300/10 dark:border-hairline-strong
                                                        transition-all duration-200"></div>
                                        </label>
                                    </div>
                                    <div v-if="engineParams.use_custom_alpha_quality"
                                        class="space-y-3 pt-3 border-t border-hairline mt-3 animate-in fade-in duration-200">
                                        <div class="flex justify-between items-center">
                                            <Label
                                                class="text-sm font-semibold text-slate-700 text-ink-muted">Alpha Quality</Label>
                                            <span class="text-xs font-bold text-ink-subtle">{{
                                                engineParams.alpha_quality ?? 100 }} / 100</span>
                                        </div>
                                        <input type="range" v-model.number="engineParams.alpha_quality" min="0" :disabled="isParamsDisabled"
                                            max="100"
                                            class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                                   [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                                   [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    </div>
                                </div>
                                <div class="col-span-2 pt-2 border-t border-hairline">
                                    <h4 class="text-xs font-bold uppercase tracking-wider text-ink-subtle mb-2">AOM Advanced Tuning</h4>
                                </div>
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label
                                            class="text-sm font-semibold text-slate-700 text-ink-muted">Sharpness</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.sharpness ?? 0 }} / 7</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.sharpness" min="0" max="7" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">Sharpness of the transform blocks (0-7, default 0). Higher values reduce blur on line-art.</p>
                                </div>
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Color Sharpness</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.color_sharpness ?? 0 }} / 7</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.color_sharpness" min="0" max="7" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">Sharpness specifically for color channels (0-7). Higher values help with color bleeding.</p>
                                </div>
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Alpha Sharpness</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.alpha_sharpness ?? 0 }} / 7</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.alpha_sharpness" min="0" max="7" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">Sharpness specifically for the alpha transparency channel (0-7).</p>
                                </div>
                            </template>

                            <!-- libheif:avif settings -->
                            <template v-if="selectedEngine === 'libheif:avif'">
                                <div class="space-y-3">
                                    <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Chroma Downsampling</Label>
                                    <Select v-model="engineParams.chroma_downsampling" :disabled="isParamsDisabled">
                                        <SelectTrigger
                                            class="h-10 rounded-lg bg-white bg-surface-2 border-hairline text-ink focus:ring-0 focus:border-primary text-xs font-medium transition-colors">
                                            <SelectValue placeholder="Select method" />
                                        </SelectTrigger>
                                        <SelectContent class="bg-surface-3 border-hairline rounded-lg">
                                            <SelectItem value="nn">Nearest Neighbor (Fastest)</SelectItem>
                                            <SelectItem value="average">Average (Smooth)</SelectItem>
                                            <SelectItem value="sharp-yuv">Sharp YUV (Sharp edges)</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </template>

                            <!-- libwebp:webp settings -->
                            <template v-if="selectedEngine === 'libwebp:webp'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Method (Complexity)</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.method }} / 6</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.method" min="0" max="6" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">0 is fastest, 6 is slowest/best quality compression.</p>
                                </div>
                                <div
                                    class="flex items-center justify-between p-3 bg-surface-1 rounded-xl border border-hairline">
                                    <div class="space-y-0.5">
                                        <Label class="text-sm font-semibold text-slate-700 text-ink-muted">Lossless Mode</Label>
                                        <p class="text-[10px] text-ink-subtle">Enforce mathematical pixel losslessness</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer select-none">
                                        <input type="checkbox" v-model="engineParams.lossless" :disabled="isParamsDisabled" class="sr-only peer">
                                        <div class="w-10 h-5 bg-slate-200 bg-surface-2 rounded-full peer 
                                                    peer-checked:bg-primary
                                                    peer-disabled:opacity-50 peer-disabled:cursor-not-allowed
                                                    after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all after:shadow-sm
                                                    peer-checked:after:translate-x-5 peer-checked:after:bg-white
                                                    border border-slate-300/10 dark:border-hairline-strong
                                                    transition-all duration-200"></div>
                                    </label>
                                </div>
                            </template>

                            <!-- libpng:png settings -->
                            <template v-if="selectedEngine === 'libpng:png'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label
                                            class="text-sm font-semibold text-slate-700 text-ink-muted">Compression Level</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.compression_level }} / 9</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.compression_level" min="0" max="9" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">0 is uncompressed (largest file), 9 is max compression.</p>
                                </div>
                            </template>

                            <!-- libjxl:jxl settings -->
                            <template v-if="selectedEngine === 'libjxl:jxl'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label
                                            class="text-sm font-semibold text-slate-700 text-ink-muted">Effort</Label>
                                        <span class="text-xs font-bold text-ink-subtle">{{
                                            engineParams.effort }} / 10</span>
                                    </div>
                                    <input type="range" v-model.number="engineParams.effort" min="1" max="10" :disabled="isParamsDisabled"
                                        class="w-full h-1 bg-slate-200 bg-surface-2 rounded-lg appearance-none cursor-pointer focus:outline-none accent-transparent disabled:opacity-50 disabled:cursor-not-allowed
                                               [&::-webkit-slider-runnable-track]:bg-slate-200 [&::-webkit-slider-runnable-track]:bg-surface-2 [&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-lg
                                               [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white [&::-webkit-slider-thumb]:dark:bg-ink [&::-webkit-slider-thumb]:border [&::-webkit-slider-thumb]:border-slate-300 [&::-webkit-slider-thumb]:dark:border-hairline-strong [&::-webkit-slider-thumb]:shadow-md [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:transition-all [&::-webkit-slider-thumb]:hover:scale-110 [&::-webkit-slider-thumb]:active:scale-95" />
                                    <p class="text-[10px] text-ink-subtle">1 is fastest, 10 is slowest/most optimized.</p>
                                </div>
                                <div
                                    class="flex items-center justify-between p-3 bg-surface-1 rounded-xl border border-hairline">
                                    <div class="space-y-0.5">
                                        <Label
                                            class="text-sm font-semibold text-slate-700 text-ink-muted">Progressive</Label>
                                        <p class="text-[10px] text-ink-subtle">Support progressive rendering</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer select-none">
                                        <input type="checkbox" v-model="engineParams.progressive" :disabled="isParamsDisabled" class="sr-only peer">
                                        <div class="w-10 h-5 bg-slate-200 bg-surface-2 rounded-full peer 
                                                    peer-checked:bg-primary
                                                    peer-disabled:opacity-50 peer-disabled:cursor-not-allowed
                                                    after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all after:shadow-sm
                                                    peer-checked:after:translate-x-5 peer-checked:after:bg-white
                                                    border border-slate-300/10 dark:border-hairline-strong
                                                    transition-all duration-200"></div>
                                    </label>
                                </div>
                            </template>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <!-- Files Card -->
            <Card
                class="border border-hairline shadow-sm overflow-hidden relative bg-surface-1 rounded-xl transition-all duration-300 py-0 gap-0"
                :class="{
                    'border-primary ring-1 ring-primary/20 bg-surface-2/10': isDragActive
                }" @dragenter.prevent="handleDragEnter" @dragover.prevent="handleDragOver" @drop.prevent="handleDrop">
                <!-- Drag overlay -->
                <Transition enter-active-class="transition duration-200 ease-out" enter-from-class="opacity-0 scale-95"
                    enter-to-class="opacity-100 scale-100" leave-active-class="transition duration-150 ease-in"
                    leave-from-class="opacity-100 scale-100" leave-to-class="opacity-0 scale-95">
                    <div v-if="isDragActive"
                        class="absolute inset-0 bg-canvas/90 backdrop-blur-[1px] z-50 flex flex-col items-center justify-center gap-4 transition-all duration-300 border border-dashed border-primary/50 rounded-xl pointer-events-auto"
                        @dragleave.prevent="handleDragLeave" @dragover.prevent="handleDragOver"
                        @drop.prevent="handleDrop">
                        <div
                            class="h-12 w-12 rounded-lg bg-surface-2 flex items-center justify-center text-primary shadow-sm border border-hairline animate-bounce">
                            <UploadCloud class="w-6 h-6" />
                        </div>
                        <p class="text-xs font-semibold text-primary tracking-wide">
                            {{ $t('new_task.drop_files') }}
                        </p>
                    </div>
                </Transition>

                <CardHeader class="px-5 py-3.5 border-b border-hairline flex flex-row items-center justify-between space-y-0">
                    <CardTitle class="text-sm font-semibold text-ink tracking-tight">
                        {{ $t('new_task.media_files') }}
                    </CardTitle>
                    <div class="relative">
                        <input type="file" multiple
                            class="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed z-10"
                            @change="handleFileSelect" :disabled="isFormDisabled" />
                        <Button variant="secondary" size="sm" class="rounded-lg px-3 h-8 gap-1.5 font-semibold bg-surface-2 text-ink border border-hairline hover:bg-surface-3 cursor-pointer"
                            :disabled="isFormDisabled">
                            <Plus class="w-3.5 h-3.5" />
                            {{ $t('new_task.add_files') }}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent class="p-5">
                    <div v-if="files.length > 0" class="space-y-2">
                        <div v-for="file in files" :key="file.id"
                            class="group flex items-center justify-between p-3 rounded-lg border border-hairline bg-surface-2/40 hover:bg-surface-2 transition-all duration-200">
                            <div class="flex items-center space-x-3 truncate flex-1 pr-4">
                                <div
                                    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface-1 border border-hairline text-ink-subtle shadow-xs group-hover:text-primary group-hover:border-primary/20 transition-all">
                                    <FileImage v-if="file.type?.startsWith('image/')" class="w-4 h-4" />
                                    <FileVideo v-else-if="file.type?.startsWith('video/')" class="w-4 h-4" />
                                    <FileCode v-else class="w-4 h-4" />
                                </div>
                                <div class="flex-1 truncate">
                                    <p class="text-xs font-semibold text-ink truncate">{{ file.name }}</p>
                                    <p v-if="file.status === 'error' && file.error" class="text-xs font-medium text-red-500 mt-1 truncate" :title="file.error">
                                        {{ file.error }}
                                    </p>
                                    <p v-else class="text-[10px] font-medium text-ink-subtle mt-0.5">
                                        {{ (file.size / 1024 / 1024).toFixed(2) }} MB
                                    </p>
                                </div>
                            </div>

                            <div class="flex items-center space-x-4">
                                <div v-if="isUploading || file.status === 'success'"
                                    class="w-24 flex flex-col items-end gap-1 shrink-0">
                                    <span class="text-[9px] font-semibold text-primary">{{ file.progress }}%</span>
                                    <div
                                        class="h-1 w-full rounded-full bg-surface-2 overflow-hidden">
                                        <div class="h-full bg-primary transition-all duration-500 ease-out"
                                            :style="{ width: `${file.progress}%` }" />
                                    </div>
                                </div>

                                <Button v-if="!isUploading && file.status !== 'success'" variant="ghost" size="icon"
                                    @click="removeFile(file.id)"
                                    class="h-7 w-7 rounded-lg text-ink-subtle hover:text-red-500 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-all cursor-pointer">
                                    <X class="w-3.5 h-3.5" />
                                </Button>
                                <div v-else-if="file.status === 'success'"
                                    class="w-7 h-7 rounded-full bg-emerald-500/10 flex items-center justify-center text-emerald-600 dark:text-emerald-400">
                                    <Check class="w-3.5 h-3.5 text-emerald-600 dark:text-emerald-400" stroke-width="2.5" />
                                </div>
                                <div v-else-if="file.status === 'error'"
                                    class="w-7 h-7 rounded-full bg-red-500/10 flex items-center justify-center text-red-500">
                                    <AlertCircle class="w-3.5 h-3.5" />
                                </div>
                            </div>
                        </div>
                    </div>

                    <div v-else
                        class="flex flex-col items-center justify-center py-16 px-6 text-center border-2 border-dashed border-hairline rounded-xl bg-surface-2/20">
                        <div
                            class="h-10 w-10 rounded-lg bg-surface-2 flex items-center justify-center mb-4 text-ink-subtle shadow-xs border border-hairline">
                            <UploadCloud class="w-5 h-5 opacity-70" />
                        </div>
                        <p class="text-xs font-semibold text-ink mb-1">
                            {{ $t('new_task.no_files') }}
                        </p>
                        <p class="text-[10px] text-ink-subtle font-medium max-w-xs">
                            {{ $t('new_task.no_files_desc') }}
                        </p>
                    </div>

                    <div v-if="files.length > 0" class="pt-6 mt-6 border-t border-hairline">
                        <div v-if="isUploading" class="space-y-3 mb-6 max-w-md ml-auto animate-in fade-in duration-300">
                            <div class="flex justify-between items-end">
                                <span class="text-xs font-semibold text-ink-muted">
                                    {{ $t('new_task.uploading', { count: files.length }) }}
                                </span>
                                <span class="text-xl font-semibold text-primary dark:text-primary-hover">{{ globalProgress }}%</span>
                            </div>
                            <div
                                class="h-1.5 w-full rounded-full bg-surface-2 overflow-hidden">
                                <div class="h-full bg-primary rounded-full transition-all duration-500 ease-out shadow-xs"
                                    :style="{ width: `${globalProgress}%` }" />
                            </div>
                        </div>

                        <div class="flex justify-end">
                            <Button @click="handleAction" :disabled="isSubmitDisabled"
                                class="rounded-lg px-6 h-9 text-xs font-semibold bg-primary hover:bg-primary-hover text-primary-foreground shadow-sm transition-all duration-200 cursor-pointer">
                                <Loader2 v-if="isUploading || isCreatingTask" class="mr-2 h-3.5 w-3.5 animate-spin" />
                                {{ submitButtonText }}
                            </Button>
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    </div>
</template>
