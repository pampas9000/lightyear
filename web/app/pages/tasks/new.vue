<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { Uppy } from '@uppy/core'
import AwsS3 from '@uppy/aws-s3'
import { useApi } from '~/composables/useApi'

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
            const res: any = await api('/s3/multipart', {
                method: 'POST',
                body: { filename: file.name, type: file.type },
            })
            return { uploadId: res.uploadId, key: res.key }
        },
        listParts: async (file, { uploadId, key }) => {
            const res: any = await api(`/s3/multipart/${uploadId}?key=${encodeURIComponent(key)}`)
            return res
        },
        signPart: async (file, { uploadId, key, partNumber }) => {
            const res: any = await api(`/s3/multipart/${uploadId}/${partNumber}?key=${encodeURIComponent(key)}`)
            return res // contains { url }
        },
        abortMultipartUpload: async (file, { uploadId, key }) => {
            await api(`/s3/multipart/${uploadId}?key=${encodeURIComponent(key)}`, {
                method: 'DELETE',
            })
        },
        completeMultipartUpload: async (file, { uploadId, key, parts }) => {
            const res: any = await api(`/s3/multipart/${uploadId}/complete`, {
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
        const response: any = await api('/tasks', {
            method: 'POST',
            body: {
                items: uploadedKeys.value,
                targetFormat: targetFormat.value,
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
</script>

<template>
    <div class="max-w-250 mx-auto w-full">
        <!-- Header -->
        <div class="mb-10">
            <h1 class="text-3xl font-semibold tracking-tight text-slate-900 dark:text-white mb-2">{{
                $t('new_task.title') }}</h1>
            <p class="text-sm text-slate-500 dark:text-slate-400">{{ $t('new_task.subtitle') }}</p>
        </div>

        <div class="space-y-8">
            <!-- Parameters Section -->
            <section
                class="bg-white dark:bg-slate-900 rounded-xl p-8 shadow-sm border border-slate-100 dark:border-slate-800">
                <h2 class="text-lg font-semibold text-slate-900 dark:text-white mb-6">{{ $t('new_task.params') }}</h2>

                <div class="grid gap-8 md:grid-cols-2">
                    <div>
                        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">{{
                            $t('new_task.target_format') }}</label>
                        <div class="relative">
                            <select v-model="targetFormat"
                                class="w-full appearance-none rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 px-4 py-2.5 text-sm font-medium text-slate-900 dark:text-white focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 transition-colors">
                                <option value="avif">AVIF (Recommended)</option>
                                <option value="webp">WebP</option>
                                <option value="jpeg">JPEG</option>
                                <option value="png">PNG</option>
                                <option value="jxl">JPEG XL</option>
                            </select>
                            <div
                                class="pointer-events-none absolute inset-y-0 right-0 flex items-center px-4 text-slate-500">
                                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                    stroke-linejoin="round">
                                    <path d="m6 9 6 6 6-6" />
                                </svg>
                            </div>
                        </div>
                    </div>

                    <div>
                        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">{{
                            $t('new_task.quality') }}
                            (0-100)</label>
                        <input type="number" v-model="quality" min="0" max="100"
                            class="w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 px-4 py-2.5 text-sm font-medium text-slate-900 dark:text-white focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 transition-colors" />
                    </div>
                </div>
            </section>

            <!-- Files Section -->
            <section
                class="bg-white dark:bg-slate-900 rounded-xl p-8 shadow-sm border border-slate-100 dark:border-slate-800">
                <div class="flex items-center justify-between mb-6">
                    <h2 class="text-lg font-semibold text-slate-900 dark:text-white">{{ $t('new_task.media_files') }}
                    </h2>

                    <div class="relative">
                        <input type="file" multiple
                            class="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed"
                            @change="handleFileSelect" :disabled="isUploading || uploadComplete" />
                        <button type="button"
                            class="inline-flex items-center justify-center rounded-lg bg-slate-100 dark:bg-slate-800 px-4 py-2 text-sm font-medium text-slate-700 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-slate-700 transition-colors disabled:opacity-50"
                            :disabled="isUploading || uploadComplete">
                            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"
                                fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                stroke-linejoin="round" class="mr-2">
                                <path d="M5 12h14" />
                                <path d="M12 5v14" />
                            </svg>
                            {{ $t('new_task.add_files') }}
                        </button>
                    </div>
                </div>

                <div v-if="files.length > 0" class="space-y-3">
                    <div v-for="file in files" :key="file.id"
                        class="flex items-center justify-between p-4 rounded-lg border border-slate-100 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-800/30 group transition-colors hover:border-slate-200 dark:hover:border-slate-700">
                        <div class="flex items-center space-x-4 truncate flex-1 pr-4">
                            <div
                                class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 text-slate-500 shadow-sm">
                                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                    stroke-linejoin="round">
                                    <path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z" />
                                    <polyline points="14 2 14 8 20 8" />
                                    <circle cx="10" cy="13" r="2" />
                                    <path d="m20 17-1.09-1.09a2 2 0 0 0-2.82 0L10 22" />
                                </svg>
                            </div>
                            <div class="flex-1 truncate">
                                <p class="text-sm font-medium text-slate-900 dark:text-white truncate">{{ file.name }}
                                </p>
                                <p class="text-xs text-slate-500 mt-0.5">{{ (file.size / 1024 / 1024).toFixed(2) }} MB
                                </p>
                            </div>
                        </div>

                        <div class="flex items-center space-x-4">
                            <div v-if="isUploading || file.status === 'success'" class="w-24">
                                <div class="h-1.5 w-full rounded-full bg-slate-200 dark:bg-slate-700 overflow-hidden">
                                    <div class="h-full bg-blue-600 transition-all duration-300"
                                        :style="{ width: `${file.progress}%` }" />
                                </div>
                            </div>

                            <button v-if="!isUploading && file.status !== 'success'" @click="removeFile(file.id)"
                                class="text-slate-400 hover:text-red-500 opacity-0 group-hover:opacity-100 transition-opacity">
                                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                    stroke-linejoin="round">
                                    <path d="M18 6 6 18" />
                                    <path d="m6 6 12 12" />
                                </svg>
                            </button>
                            <span v-else-if="file.status === 'success'" class="text-emerald-500">
                                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                    stroke-linejoin="round">
                                    <polyline points="20 6 9 17 4 12" />
                                </svg>
                            </span>
                        </div>
                    </div>
                </div>

                <div v-else
                    class="flex flex-col items-center justify-center py-16 px-4 text-center border-2 border-dashed border-slate-200 dark:border-slate-800 rounded-xl bg-slate-50/50 dark:bg-slate-900/50">
                    <div
                        class="h-12 w-12 rounded-full bg-slate-100 dark:bg-slate-800 flex items-center justify-center mb-4 text-slate-500">
                        <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none"
                            stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                            <polyline points="17 8 12 3 7 8" />
                            <line x1="12" x2="12" y1="3" y2="15" />
                        </svg>
                    </div>
                    <p class="text-sm font-medium text-slate-900 dark:text-white mb-1">{{ $t('new_task.no_files') }}</p>
                    <p class="text-xs text-slate-500">{{ $t('new_task.no_files_desc') }}</p>
                </div>

                <div v-if="files.length > 0" class="pt-8 mt-8 border-t border-slate-100 dark:border-slate-800">
                    <div v-if="isUploading" class="space-y-2 mb-6">
                        <div class="flex justify-between text-sm font-medium text-slate-700 dark:text-slate-300">
                            <span>{{ $t('new_task.uploading', { count: files.length }) }}</span>
                            <span>{{ globalProgress }}%</span>
                        </div>
                        <div
                            class="h-2 w-full rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden shadow-inner">
                            <div class="h-full bg-blue-600 transition-all duration-300"
                                :style="{ width: `${globalProgress}%` }" />
                        </div>
                    </div>

                    <div class="flex justify-end">
                        <button @click="startUpload" :disabled="isUploading || uploadComplete"
                            class="inline-flex items-center justify-center rounded-lg bg-blue-600 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-blue-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
                            <svg v-if="isUploading" class="animate-spin -ml-1 mr-2 h-4 w-4 text-white"
                                xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor"
                                    stroke-width="4"></circle>
                                <path class="opacity-75" fill="currentColor"
                                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z">
                                </path>
                            </svg>
                            {{ submitButtonText }}
                        </button>
                    </div>
                </div>
            </section>
        </div>
    </div>
</template>
