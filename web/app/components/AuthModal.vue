<script setup lang="ts">
import { ref } from 'vue'
import { useAuth } from '~/composables/useAuth'
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { LockKeyhole, AlertCircle, Loader2 } from '@lucide/vue'

const { login, register, showAuthModal, loading } = useAuth()

const mode = ref<'login' | 'register'>('login')
const username = ref('')
const email = ref('')
const password = ref('')
const errorMsg = ref('')

const handleSubmit = async () => {
    errorMsg.value = ''
    let result

    if (mode.value === 'login') {
        result = await login(username.value, password.value)
    } else {
        result = await register(username.value, email.value, password.value)
        if (result.success) {
            // Auto login after register
            result = await login(username.value, password.value)
        }
    }

    if (result.success) {
        showAuthModal.value = false
        username.value = ''
        password.value = ''
        email.value = ''
    } else {
        errorMsg.value = result.message || $t('auth.auth_failed')
    }
}

const toggleMode = () => {
    mode.value = mode.value === 'login' ? 'register' : 'login'
    errorMsg.value = ''
}

const close = () => {
    showAuthModal.value = false
    username.value = ''
    password.value = ''
    email.value = ''
    errorMsg.value = ''
}

const handleOAuth = async (provider: 'google' | 'github') => {
    try {
        const res = await $fetch<any>(`/api/auth/${provider}`)
        if (res.success && res.data?.url) {
            window.location.href = res.data.url
        }
    } catch (err) {
        errorMsg.value = `Failed to start ${provider} login`
    }
}
</script>

<template>
    <Dialog :open="showAuthModal" @update:open="close">
        <DialogContent
            class="sm:max-w-[360px] p-0 overflow-hidden border border-hairline shadow-md rounded-xl bg-surface-1">
            <div class="relative w-full p-6 space-y-6">
                <div class="text-center space-y-2">
                    <div
                        class="inline-flex h-10 w-10 items-center justify-center rounded-lg bg-surface-2 text-ink shadow-sm border border-hairline group-hover:scale-105 transition-transform duration-300">
                        <LockKeyhole class="w-4 h-4 stroke-[1.5]" />
                    </div>
                    <DialogHeader class="space-y-1">
                        <DialogTitle
                            class="text-lg font-semibold tracking-tight text-ink text-center">
                            {{ mode === 'login' ? $t('auth.sign_in') : $t('auth.create_account') }}
                        </DialogTitle>
                        <DialogDescription
                            class="text-xs text-ink-subtle text-center max-w-[220px] mx-auto leading-normal">
                            {{ mode === 'login'
                                ? $t('auth.sign_in_desc')
                                : $t('auth.create_account_desc')
                            }}
                        </DialogDescription>
                    </DialogHeader>
                </div>

                <form @submit.prevent="handleSubmit" class="space-y-4">
                    <div v-if="errorMsg"
                        class="rounded-lg bg-red-500/10 p-2.5 text-xs font-medium text-red-600 dark:text-red-400 border border-red-500/20 flex items-center gap-2 animate-in fade-in duration-200">
                        <AlertCircle class="w-4 h-4 shrink-0 stroke-[1.5]" />
                        <span>{{ errorMsg }}</span>
                    </div>

                    <div class="space-y-1.5">
                        <Label
                            class="text-xs font-semibold text-ink-subtle ml-0.5">{{ $t('auth.username') }}</Label>
                        <Input v-model="username" type="text" required
                            class="h-10 rounded-lg bg-surface-2 border border-hairline text-ink focus:border-primary px-3 text-xs font-medium transition-all duration-200"
                            placeholder="johndoe" />
                    </div>

                    <div v-if="mode === 'register'" class="space-y-1.5">
                        <Label
                            class="text-xs font-semibold text-ink-subtle ml-0.5">{{ $t('auth.email') }}</Label>
                        <Input v-model="email" type="email" required
                            class="h-10 rounded-lg bg-surface-2 border border-hairline text-ink focus:border-primary px-3 text-xs font-medium transition-all duration-200"
                            placeholder="john@example.com" />
                    </div>

                    <div class="space-y-1.5">
                        <Label
                            class="text-xs font-semibold text-ink-subtle ml-0.5">{{ $t('auth.password') }}</Label>
                        <Input v-model="password" type="password" required
                            class="h-10 rounded-lg bg-surface-2 border border-hairline text-ink focus:border-primary px-3 text-xs font-medium transition-all duration-200"
                            placeholder="••••••••" />
                    </div>

                    <Button type="submit" :disabled="loading"
                        class="w-full h-10 rounded-lg text-xs font-semibold mt-2 bg-primary hover:bg-primary-hover text-primary-foreground shadow-sm transition-all duration-200 cursor-pointer">
                        <Loader2 v-if="loading" class="mr-2 h-3.5 w-3.5 animate-spin" />
                        {{ mode === 'login' ? $t('auth.sign_in') : $t('auth.sign_up') }}
                    </Button>
                </form>

                <div class="flex items-center gap-3">
                    <Separator class="flex-1 bg-hairline" />
                    <span
                        class="text-xs text-ink-subtle font-medium">{{ $t('auth.or_continue_with') }}</span>
                    <Separator class="flex-1 bg-hairline" />
                </div>

                <div class="grid grid-cols-2 gap-3">
                    <Button @click="handleOAuth('google')" type="button" variant="outline"
                        class="h-10 rounded-lg gap-2 font-semibold text-xs border border-hairline bg-surface-1 hover:bg-surface-2 text-ink shadow-sm transition-all duration-200 cursor-pointer">
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24">
                            <path
                                d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
                                fill="#4285F4" />
                            <path
                                d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
                                fill="#34A853" />
                            <path
                                d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
                                fill="#FBBC05" />
                            <path
                                d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
                                fill="#EA4335" />
                        </svg>
                        Google
                    </Button>
                    <Button @click="handleOAuth('github')" type="button" variant="outline"
                        class="h-10 rounded-lg gap-2 font-semibold text-xs border border-hairline bg-surface-1 hover:bg-surface-2 text-ink shadow-sm transition-all duration-200 cursor-pointer">
                        <svg class="w-3.5 h-3.5 fill-current" viewBox="0 0 24 24">
                            <path
                                d="M12 2A10 10 0 0 0 8.84 21.5c.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5A10 10 0 0 0 12 2Z" />
                        </svg>
                        GitHub
                    </Button>
                </div>

                <div class="text-center pt-2">
                    <button @click="toggleMode"
                        class="group text-xs font-medium text-ink-subtle hover:text-ink transition-all duration-200 cursor-pointer">
                        {{ mode === 'login' ? $t('auth.new_user') : $t('auth.existing_user') }}
                        <span
                            class="text-primary dark:text-primary-hover ml-1 group-hover:underline transition-all">
                            {{ mode === 'login' ? $t('auth.create_one') : $t('auth.sign_in') }}
                        </span>
                    </button>
                </div>
            </div>
        </DialogContent>
    </Dialog>
</template>
