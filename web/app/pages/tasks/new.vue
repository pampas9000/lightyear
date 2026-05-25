<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
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
    AlertCircle
} from 'lucide-vue-next'

useHead({
    title: $t('new_task.title') + ' | Transcoder',
})

const router = useRouter()
const api = useApi()

// Form state
const targetFormat = ref('avif')
const quality = ref(80)

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
                outputPath: key.replace(/\.[^/.]+$/, "") + "." + targetFormat.value
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
                target_format: targetFormat.value,
                params: {
                    quality: quality.value,
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
            <Card class="border-slate-200/60 dark:border-slate-800/60 shadow-sm overflow-hidden">
                <CardHeader class="px-8 pt-8 pb-4">
                    <CardTitle class="text-xl font-bold">{{ $t('new_task.params') }}</CardTitle>
                </CardHeader>
                <CardContent class="px-8 pb-8">
                    <div class="grid gap-8 md:grid-cols-2">
                        <div class="space-y-3">
                            <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">{{
                                $t('new_task.target_format') }}</Label>
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

                        <div class="space-y-3">
                            <Label class="text-sm font-semibold text-slate-700 dark:text-slate-300">
                                {{ $t('new_task.quality') }} (0-100)
                            </Label>
                            <Input type="number" v-model="quality" min="0" max="100"
                                class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-slate-200 dark:border-slate-800 focus:ring-blue-500/20" />
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
