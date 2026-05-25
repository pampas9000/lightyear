<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Uppy } from '@uppy/core'
import AwsS3 from '@uppy/aws-s3'
import { useApi } from '~/composables/useApi'
import type { Task } from '~/lib/types/task'
import type { ApiResponse } from '~/lib/types/api'
import type { MultipartUploadResponse, PartSignatureResponse } from '~/lib/types/s3'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select'
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
} from 'lucide-vue-next'

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
    isApplyingTemplate.value = false
}

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
        let match = true
        for (const key in templateData) {
            if (engineParams.value[key] !== templateData[key]) {
                match = false
                break
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

// Uppy state
const files = ref<any[]>([])
const isUploading = ref(false)
const globalProgress = ref(0)
const uploadComplete = ref(false)
const uploadedKeys = ref<{ inputPath: string; outputPath: string }[]>([])

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

        // Extract key from response, handling plugin-injected properties
        const uppyFile = file as any
        const key = uppyFile.s3Multipart?.key || response.body?.key || (response.body as any)?.location?.split('/').slice(2).join('/')

        if (key) {
            uploadedKeys.value.push({
                inputPath: key,
                outputPath: key.replace(/\.[^/.]+$/, "") + "." + targetFormat.value.toLowerCase()
            })
        }
    })

    uppy.on('upload-error', (file, error) => {
        if (!file) return
        const f = files.value.find((f) => f.id === file.id)
        if (f) {
            f.status = 'error'
            f.error = error?.message || 'Unknown upload error'
        }
    })

    uppy.on('complete', (result) => {
        isUploading.value = false
        uploadComplete.value = true
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
    isUploading.value = true
    uppy.upload()
}

const submitTask = async () => {
    if (uploadedKeys.value.length === 0) return

    try {
        const response = await api<ApiResponse<Task>>('/tasks', {
            method: 'POST',
            body: {
                items: uploadedKeys.value,
                target_format: targetFormat.value.toUpperCase(),
                params: {
                    engine: selectedEngine.value,
                    engine_params: engineParams.value
                }
            }
        })

        router.push('/')
    } catch (err) {
        console.error('Failed to create task:', err)
        alert('Upload succeeded but task creation failed.')
    }
}
const submitButtonText = computed(() => {
    if (isUploading.value) return $t('new_task.processing')
    if (uploadComplete.value) return $t('new_task.task_created')
    return $t('new_task.start_processing')
})

// Drag and drop state
const dragCounter = ref(0)
const isDragActive = computed(() => dragCounter.value > 0)

const handleDragEnter = (e: DragEvent) => {
    if (isUploading.value || uploadComplete.value) return
    dragCounter.value++
}

const handleDragLeave = (e: DragEvent) => {
    if (isUploading.value || uploadComplete.value) return
    dragCounter.value = Math.max(0, dragCounter.value - 1)
}

const handleDragOver = (e: DragEvent) => {
    e.preventDefault()
}

const handleDrop = (e: DragEvent) => {
    if (isUploading.value || uploadComplete.value) return
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
    <div class="max-w-4xl mx-auto w-full space-y-10">
        <!-- Header -->
        <div class="flex flex-col gap-2">
            <h1 class="text-4xl font-bold tracking-tight text-slate-900 dark:text-white">{{ $t('new_task.title') }}</h1>
            <p class="text-sm text-slate-500 dark:text-slate-400 font-medium">{{ $t('new_task.subtitle') }}</p>
        </div>

        <div class="grid gap-8">
            <!-- Parameters Card -->
            <Card class="border-slate-200/60 dark:border-slate-800/60 shadow-sm overflow-hidden bg-white dark:bg-slate-900">
                <CardHeader class="px-8 pt-8 pb-4 border-b border-slate-100 dark:border-slate-800 flex flex-row items-center justify-between">
                    <div class="space-y-1">
                        <CardTitle class="text-xl font-bold flex items-center gap-2">
                            <Settings class="w-5 h-5 text-blue-500" />
                            {{ $t('new_task.params') }}
                        </CardTitle>
                        <p class="text-xs text-slate-500 font-medium">Configure image optimization and transcoding parameters</p>
                    </div>
                    <button 
                        @click="expertMode = !expertMode" 
                        class="flex items-center gap-1.5 px-3 py-1.5 rounded-xl border text-xs font-bold transition-all duration-200"
                        :class="expertMode ? 'bg-blue-50 dark:bg-blue-950/30 text-blue-600 border-blue-200 dark:border-blue-900/50' : 'bg-slate-50 dark:bg-slate-800/50 text-slate-600 border-slate-200 dark:border-slate-700'"
                    >
                        <Sliders class="w-3.5 h-3.5" />
                        Expert Mode
                    </button>
                </CardHeader>
                <CardContent class="px-8 py-6 space-y-8">
                    <!-- Target Format & Engine Selection -->
                    <div class="grid gap-6 md:grid-cols-2">
                        <div class="space-y-3">
                            <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300 flex items-center gap-1.5">
                                <Layers class="w-4 h-4 text-slate-400" />
                                {{ $t('new_task.target_format') }}
                            </Label>
                            <Select v-model="targetFormat">
                                <SelectTrigger
                                    class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-slate-200 dark:border-slate-800 focus:ring-blue-500/20">
                                    <SelectValue placeholder="Select format" />
                                </SelectTrigger>
                                <SelectContent>
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
                            <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300 flex items-center gap-1.5">
                                <Settings class="w-4 h-4 text-slate-400" />
                                Engine
                            </Label>
                            <Select v-model="selectedEngine">
                                <SelectTrigger
                                    class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-slate-200 dark:border-slate-800 focus:ring-blue-500/20">
                                    <SelectValue placeholder="Select engine" />
                                </SelectTrigger>
                                <SelectContent>
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
                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Optimize Template</Label>
                        <div class="grid grid-cols-4 gap-3 p-1 bg-slate-50 dark:bg-slate-800/30 rounded-2xl border border-slate-100 dark:border-slate-800/50">
                            <button 
                                @click="selectedProfile = 'balanced'"
                                class="flex flex-col md:flex-row items-center justify-center gap-2 py-3 px-4 rounded-xl text-xs font-bold transition-all duration-200"
                                :class="selectedProfile === 'balanced' ? 'bg-white dark:bg-slate-800 text-blue-600 dark:text-blue-400 shadow-sm border border-slate-200/50 dark:border-slate-700/50' : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'"
                            >
                                <Gauge class="w-4 h-4 shrink-0" />
                                <span>Balanced</span>
                            </button>
                            <button 
                                @click="selectedProfile = 'size'"
                                class="flex flex-col md:flex-row items-center justify-center gap-2 py-3 px-4 rounded-xl text-xs font-bold transition-all duration-200"
                                :class="selectedProfile === 'size' ? 'bg-white dark:bg-slate-800 text-blue-600 dark:text-blue-400 shadow-sm border border-slate-200/50 dark:border-slate-700/50' : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'"
                            >
                                <HardDrive class="w-4 h-4 shrink-0" />
                                <span>Size First</span>
                            </button>
                            <button 
                                @click="selectedProfile = 'speed'"
                                class="flex flex-col md:flex-row items-center justify-center gap-2 py-3 px-4 rounded-xl text-xs font-bold transition-all duration-200"
                                :class="selectedProfile === 'speed' ? 'bg-white dark:bg-slate-800 text-blue-600 dark:text-blue-400 shadow-sm border border-slate-200/50 dark:border-slate-700/50' : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'"
                            >
                                <Zap class="w-4 h-4 shrink-0" />
                                <span>Speed First</span>
                            </button>
                            <button 
                                disabled
                                class="flex flex-col md:flex-row items-center justify-center gap-2 py-3 px-4 rounded-xl text-xs font-bold border border-transparent"
                                :class="selectedProfile === 'custom' ? 'bg-blue-50/50 dark:bg-blue-950/20 text-amber-600 dark:text-amber-400 border-amber-200/40 dark:border-amber-900/30' : 'text-slate-300 dark:text-slate-600 opacity-60'"
                            >
                                <Sliders class="w-4 h-4 shrink-0" />
                                <span>Custom</span>
                            </button>
                        </div>
                    </div>

                    <!-- Dynamic Parameters Controls -->
                    <div class="border-t border-slate-100 dark:border-slate-800 pt-6 space-y-6">
                        <!-- Quality Option (Universal for most engines except PNG / JXL) -->
                        <div v-if="selectedEngine !== 'libpng:png' && selectedEngine !== 'libjxl:jxl'" class="space-y-3">
                            <div class="flex justify-between items-center">
                                <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">
                                    Quality
                                </Label>
                                <span class="text-sm font-bold text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-950/30 px-2.5 py-1 rounded-lg">
                                    {{ engineParams.quality }}
                                </span>
                            </div>
                            <input 
                                type="range" 
                                v-model.number="engineParams.quality" 
                                min="0" 
                                max="100" 
                                class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                            />
                            <p class="text-xs text-slate-400 font-medium">Higher quality values result in better details but larger file sizes.</p>
                        </div>

                        <!-- Distance Option (JXL exclusive) -->
                        <div v-if="selectedEngine === 'libjxl:jxl'" class="space-y-3">
                            <div class="flex justify-between items-center">
                                <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">
                                    Distance (Max visual error)
                                </Label>
                                <span class="text-sm font-bold text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-950/30 px-2.5 py-1 rounded-lg">
                                    {{ engineParams.distance }}
                                </span>
                            </div>
                            <input 
                                type="range" 
                                v-model.number="engineParams.distance" 
                                min="0" 
                                max="5" 
                                step="0.1" 
                                class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                            />
                            <p class="text-xs text-slate-400 font-medium">0.0 is lossless, 1.0 is visually lossless. Higher distance values mean smaller files and higher degradation.</p>
                        </div>

                        <!-- Advanced Parameters Grid (expert mode only) -->
                        <div v-if="expertMode" class="grid gap-6 md:grid-cols-2 bg-slate-50/50 dark:bg-slate-900/50 p-6 rounded-2xl border border-slate-100 dark:border-slate-800/80">
                            <!-- libavif:avif settings -->
                            <template v-if="selectedEngine === 'libavif:avif'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Speed</Label>
                                        <span class="text-xs font-bold text-slate-600 dark:text-slate-400">{{ engineParams.speed }} / 10</span>
                                    </div>
                                    <input 
                                        type="range" 
                                        v-model.number="engineParams.speed" 
                                        min="0" 
                                        max="10" 
                                        class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                                    />
                                    <p class="text-[10px] text-slate-400">0 is slowest (highest compression), 10 is fastest (larger size).</p>
                                </div>
                                <div class="flex items-center justify-between p-3 bg-white dark:bg-slate-900 rounded-xl border border-slate-100 dark:border-slate-800">
                                    <div class="space-y-0.5">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Sharp YUV</Label>
                                        <p class="text-[10px] text-slate-400">Improve edge details and color matching</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer">
                                        <input type="checkbox" v-model="engineParams.sharp_yuv" class="sr-only peer">
                                        <div class="w-11 h-6 bg-slate-200 peer-focus:outline-none rounded-full peer dark:bg-slate-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-slate-600 peer-checked:bg-blue-600"></div>
                                    </label>
                                </div>
                            </template>

                            <!-- libheif:avif settings -->
                            <template v-if="selectedEngine === 'libheif:avif'">
                                <div class="space-y-3">
                                    <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Chroma Downsampling</Label>
                                    <Select v-model="engineParams.chroma_downsampling">
                                        <SelectTrigger
                                            class="h-10 rounded-xl bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800">
                                            <SelectValue placeholder="Select method" />
                                        </SelectTrigger>
                                        <SelectContent>
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
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Method (Complexity)</Label>
                                        <span class="text-xs font-bold text-slate-600 dark:text-slate-400">{{ engineParams.method }} / 6</span>
                                    </div>
                                    <input 
                                        type="range" 
                                        v-model.number="engineParams.method" 
                                        min="0" 
                                        max="6" 
                                        class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                                    />
                                    <p class="text-[10px] text-slate-400">0 is fastest, 6 is slowest/best quality compression.</p>
                                </div>
                                <div class="flex items-center justify-between p-3 bg-white dark:bg-slate-900 rounded-xl border border-slate-100 dark:border-slate-800">
                                    <div class="space-y-0.5">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Lossless Mode</Label>
                                        <p class="text-[10px] text-slate-400">Enforce mathematical pixel losslessness</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer">
                                        <input type="checkbox" v-model="engineParams.lossless" class="sr-only peer">
                                        <div class="w-11 h-6 bg-slate-200 peer-focus:outline-none rounded-full peer dark:bg-slate-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-slate-600 peer-checked:bg-blue-600"></div>
                                    </label>
                                </div>
                            </template>

                            <!-- libpng:png settings -->
                            <template v-if="selectedEngine === 'libpng:png'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Compression Level</Label>
                                        <span class="text-xs font-bold text-slate-600 dark:text-slate-400">{{ engineParams.compression_level }} / 9</span>
                                    </div>
                                    <input 
                                        type="range" 
                                        v-model.number="engineParams.compression_level" 
                                        min="0" 
                                        max="9" 
                                        class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                                    />
                                    <p class="text-[10px] text-slate-400">0 is uncompressed (largest file), 9 is max compression.</p>
                                </div>
                            </template>

                            <!-- libjxl:jxl settings -->
                            <template v-if="selectedEngine === 'libjxl:jxl'">
                                <div class="space-y-3">
                                    <div class="flex justify-between items-center">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Effort</Label>
                                        <span class="text-xs font-bold text-slate-600 dark:text-slate-400">{{ engineParams.effort }} / 9</span>
                                    </div>
                                    <input 
                                        type="range" 
                                        v-model.number="engineParams.effort" 
                                        min="1" 
                                        max="9" 
                                        class="w-full h-1.5 bg-slate-100 dark:bg-slate-800 rounded-lg appearance-none cursor-pointer accent-blue-600 focus:outline-none"
                                    />
                                    <p class="text-[10px] text-slate-400">1 is fastest, 9 is slowest/most optimized.</p>
                                </div>
                                <div class="flex items-center justify-between p-3 bg-white dark:bg-slate-900 rounded-xl border border-slate-100 dark:border-slate-800">
                                    <div class="space-y-0.5">
                                        <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">Progressive</Label>
                                        <p class="text-[10px] text-slate-400">Support progressive rendering</p>
                                    </div>
                                    <label class="relative inline-flex items-center cursor-pointer">
                                        <input type="checkbox" v-model="engineParams.progressive" class="sr-only peer">
                                        <div class="w-11 h-6 bg-slate-200 peer-focus:outline-none rounded-full peer dark:bg-slate-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-slate-600 peer-checked:bg-blue-600"></div>
                                    </label>
                                </div>
                            </template>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <!-- Files Card -->
            <Card
                class="border-slate-200/60 dark:border-slate-800/60 shadow-xl overflow-hidden relative transition-all duration-300"
                :class="{
                    'border-blue-500/50 ring-2 ring-blue-500/20 bg-blue-50/5 dark:bg-blue-950/5': isDragActive
                }"
                @dragenter.prevent="handleDragEnter"
                @dragover.prevent="handleDragOver"
                @drop.prevent="handleDrop"
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
                        class="absolute inset-0 bg-white/80 dark:bg-slate-950/80 backdrop-blur-[2px] z-50 flex flex-col items-center justify-center gap-4 transition-all duration-300 border-2 border-dashed border-blue-500/50 rounded-xl pointer-events-auto"
                        @dragleave.prevent="handleDragLeave"
                        @dragover.prevent="handleDragOver"
                        @drop.prevent="handleDrop"
                    >
                        <div class="h-16 w-16 rounded-2xl bg-white dark:bg-slate-900 flex items-center justify-center text-blue-500 shadow-md border border-blue-100 dark:border-blue-900/50 animate-bounce">
                            <UploadCloud class="w-8 h-8" />
                        </div>
                        <p class="text-sm font-bold text-blue-600 dark:text-blue-400 tracking-wide">
                            {{ $t('new_task.drop_files') }}
                        </p>
                    </div>
                </Transition>

                <CardHeader class="px-8 pt-8 pb-4 flex flex-row items-center justify-between space-y-0">
                    <CardTitle class="text-xl font-bold">{{ $t('new_task.media_files') }}</CardTitle>
                    <div class="relative">
                        <input type="file" multiple
                            class="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed z-10"
                            @change="handleFileSelect" :disabled="isUploading || uploadComplete" />
                        <Button variant="secondary" size="sm" class="rounded-xl px-4 gap-2 font-bold"
                            :disabled="isUploading || uploadComplete">
                            <Plus class="w-4 h-4" />
                            {{ $t('new_task.add_files') }}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent class="px-8 pb-8">
                    <div v-if="files.length > 0" class="space-y-3">
                        <div v-for="file in files" :key="file.id"
                            class="group flex items-center justify-between p-4 rounded-2xl border border-slate-100 dark:border-slate-800 bg-slate-50/30 dark:bg-slate-800/20 hover:border-blue-500/30 transition-all duration-300">
                            <div class="flex items-center space-x-4 truncate flex-1 pr-4">
                                <div
                                    class="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 text-slate-400 shadow-sm group-hover:text-blue-500 group-hover:border-blue-500/30 transition-all">
                                    <FileImage v-if="file.type?.startsWith('image/')" class="w-6 h-6" />
                                    <FileVideo v-else-if="file.type?.startsWith('video/')" class="w-6 h-6" />
                                    <FileCode v-else class="w-6 h-6" />
                                </div>
                                <div class="flex-1 truncate">
                                    <p class="text-sm font-bold text-slate-900 dark:text-white truncate">{{ file.name }}
                                    </p>
                                    <p class="text-[10px] font-bold text-slate-400 mt-1 uppercase tracking-wider">{{
                                        (file.size / 1024 / 1024).toFixed(2) }} MB</p>
                                </div>
                            </div>

                            <div class="flex items-center space-x-6">
                                <div v-if="isUploading || file.status === 'success'"
                                    class="w-32 flex flex-col items-end gap-1.5">
                                    <span class="text-[10px] font-bold text-slate-400">{{ file.progress }}%</span>
                                    <div
                                        class="h-1.5 w-full rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden shadow-inner">
                                        <div class="h-full bg-blue-600 transition-all duration-500 ease-out"
                                            :style="{ width: `${file.progress}%` }" />
                                    </div>
                                </div>

                                <Button v-if="!isUploading && file.status !== 'success'" variant="ghost" size="icon"
                                    @click="removeFile(file.id)"
                                    class="h-8 w-8 rounded-full text-slate-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 opacity-0 group-hover:opacity-100 transition-all">
                                    <X class="w-4 h-4" />
                                </Button>
                                <div v-else-if="file.status === 'success'"
                                    class="w-8 h-8 rounded-full bg-emerald-50 dark:bg-emerald-900/20 flex items-center justify-center text-emerald-500">
                                    <Check class="w-4 h-4 stroke-[3]" />
                                </div>
                                <div v-else-if="file.status === 'error'"
                                    class="w-8 h-8 rounded-full bg-red-50 dark:bg-red-900/20 flex items-center justify-center text-red-500">
                                    <AlertCircle class="w-4 h-4" />
                                </div>
                            </div>
                        </div>
                    </div>

                    <div v-else
                        class="flex flex-col items-center justify-center py-20 px-6 text-center border-2 border-dashed border-slate-200 dark:border-slate-800 rounded-3xl bg-slate-50/50 dark:bg-slate-900/30">
                        <div
                            class="h-16 w-16 rounded-2xl bg-white dark:bg-slate-800 flex items-center justify-center mb-6 text-slate-300 shadow-sm border">
                            <UploadCloud class="w-8 h-8 opacity-40" />
                        </div>
                        <p class="text-base font-bold text-slate-900 dark:text-white mb-2">{{ $t('new_task.no_files') }}
                        </p>
                        <p class="text-sm text-slate-500 font-medium max-w-xs">{{ $t('new_task.no_files_desc') }}</p>
                    </div>

                    <div v-if="files.length > 0" class="pt-10 mt-10 border-t border-slate-100 dark:border-slate-800">
                        <div v-if="isUploading" class="space-y-4 mb-8 max-w-md ml-auto">
                            <div class="flex justify-between items-end">
                                <span class="text-sm font-bold text-slate-700 dark:text-slate-300">{{
                                    $t('new_task.uploading', { count: files.length }) }}</span>
                                <span class="text-2xl font-black text-blue-600">{{ globalProgress }}%</span>
                            </div>
                            <div
                                class="h-3 w-full rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden shadow-inner p-0.5">
                                <div class="h-full bg-blue-600 rounded-full transition-all duration-500 ease-out shadow-lg shadow-blue-600/30"
                                    :style="{ width: `${globalProgress}%` }" />
                            </div>
                        </div>

                        <div class="flex justify-end">
                            <Button @click="startUpload" :disabled="isUploading || uploadComplete" size="lg"
                                class="rounded-xl px-10 h-12 text-base font-bold shadow-xl shadow-blue-600/20">
                                <Loader2 v-if="isUploading" class="mr-2 h-5 w-5 animate-spin" />
                                {{ submitButtonText }}
                            </Button>
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    </div>
</template>
