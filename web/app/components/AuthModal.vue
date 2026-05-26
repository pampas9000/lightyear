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
        errorMsg.value = result.message || 'Authentication failed'
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
            class="sm:max-w-[400px] p-0 overflow-hidden border-none shadow-2xl rounded-[24px] bg-white dark:bg-slate-900">
            <div class="relative w-full p-8">
                <div class="mb-8 text-center">
                    <div
                        class="inline-flex h-12 w-12 items-center justify-center rounded-2xl bg-slate-50 dark:bg-slate-800 text-slate-900 dark:text-white mb-4 shadow-sm border border-slate-100 dark:border-slate-800 group-hover:scale-110 transition-transform duration-500">
                        <LockKeyhole class="w-5 h-5 stroke-[2.5]" />
                    </div>
                    <DialogHeader>
                        <DialogTitle
                            class="text-2xl font-black tracking-tight text-slate-900 dark:text-white text-center uppercase">
                            {{ mode === 'login' ? 'Welcome back' : 'Create Account' }}
                        </DialogTitle>
                        <DialogDescription
                            class="mt-2 text-xs font-medium text-slate-400 dark:text-slate-500 text-center max-w-[240px] mx-auto leading-relaxed">
                            {{ mode === 'login'
                                ? 'Access your professional media processing pipeline'
                                : 'Join our high-performance transcoding network'
                            }}
                        </DialogDescription>
                    </DialogHeader>
                </div>

                <form @submit.prevent="handleSubmit" class="space-y-4">
                    <div v-if="errorMsg"
                        class="rounded-xl bg-red-50 dark:bg-red-950/30 p-3 text-[10px] font-bold uppercase tracking-wider text-red-600 dark:text-red-400 border border-red-100 dark:border-red-900/30 flex items-center gap-2 animate-in fade-in slide-in-from-top-2">
                        <AlertCircle class="w-3.5 h-3.5 shrink-0 stroke-[3]" />
                        {{ errorMsg }}
                    </div>

                    <div class="space-y-1.5">
                        <Label
                            class="text-[9px] font-black text-slate-400 dark:text-slate-500 uppercase tracking-widest ml-1">Username</Label>
                        <Input v-model="username" type="text" required
                            class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-transparent focus:border-blue-500/50 focus:bg-white dark:focus:bg-slate-800 px-4 text-sm font-bold transition-all duration-300"
                            placeholder="johndoe" />
                    </div>

                    <div v-if="mode === 'register'" class="space-y-1.5">
                        <Label
                            class="text-[9px] font-black text-slate-400 dark:text-slate-500 uppercase tracking-widest ml-1">Email</Label>
                        <Input v-model="email" type="email" required
                            class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-transparent focus:border-blue-500/50 focus:bg-white dark:focus:bg-slate-800 px-4 text-sm font-bold transition-all duration-300"
                            placeholder="john@example.com" />
                    </div>

                    <div class="space-y-1.5">
                        <Label
                            class="text-[9px] font-black text-slate-400 dark:text-slate-500 uppercase tracking-widest ml-1">Password</Label>
                        <Input v-model="password" type="password" required
                            class="h-11 rounded-xl bg-slate-50 dark:bg-slate-800/50 border-transparent focus:border-blue-500/50 focus:bg-white dark:focus:bg-slate-800 px-4 text-sm font-bold transition-all duration-300"
                            placeholder="••••••••" />
                    </div>

                    <Button type="submit" :disabled="loading"
                        class="w-full h-12 rounded-xl text-xs font-black uppercase tracking-widest shadow-xl shadow-blue-600/10 mt-2 bg-blue-600 hover:bg-blue-700 hover:translate-y-[-1px] active:translate-y-[1px] transition-all duration-300">
                        <Loader2 v-if="loading" class="mr-2 h-4 w-4 animate-spin stroke-[3]" />
                        {{ mode === 'login' ? 'Authenticate' : 'Register' }}
                    </Button>
                </form>

                <div class="mt-6 flex items-center gap-4">
                    <Separator class="flex-1 opacity-50" />
                    <span
                        class="text-[8px] text-slate-300 dark:text-slate-600 uppercase tracking-widest font-black">Social
                        Connect</span>
                    <Separator class="flex-1 opacity-50" />
                </div>

                <div class="mt-6 grid grid-cols-2 gap-3">
                    <Button @click="handleOAuth('google')" type="button" variant="outline"
                        class="h-11 rounded-xl gap-2 font-bold text-[10px] uppercase tracking-widest border-slate-100 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800 transition-all duration-300">
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
                        class="h-11 rounded-xl gap-2 font-bold text-[10px] uppercase tracking-widest border-slate-100 dark:border-slate-800 hover:bg-slate-50 dark:hover:bg-slate-800 transition-all duration-300">
                        <svg class="w-3.5 h-3.5 fill-current" viewBox="0 0 24 24">
                            <path
                                d="M12 2A10 10 0 0 0 8.84 21.5c.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5A10 10 0 0 0 12 2Z" />
                        </svg>
                        GitHub
                    </Button>
                </div>

                <div class="mt-8 text-center">
                    <button @click="toggleMode"
                        class="group text-[10px] font-black uppercase tracking-widest text-slate-400 hover:text-blue-600 transition-all duration-300">
                        {{ mode === 'login' ? "New around here?" : "Already a member?" }}
                        <span
                            class="text-blue-600 dark:text-blue-400 ml-1 group-hover:ml-1.5 transition-all underline decoration-2 underline-offset-4 decoration-blue-500/20">
                            {{ mode === 'login' ? 'Create Account' : 'Authenticate' }}
                        </span>
                    </button>
                </div>
            </div>
        </DialogContent>
    </Dialog>
</template>
