<script setup lang="ts">
import { ref } from 'vue'
import { useAuth } from '~/composables/useAuth'

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
    <Teleport to="body">
        <div v-if="showAuthModal" class="fixed inset-0 z-50 flex items-center justify-center">
            <!-- Backdrop -->
            <div class="absolute inset-0 bg-background/80 backdrop-blur-sm" @click="close"></div>

            <!-- Modal Content -->
            <div
                class="relative z-10 w-full max-w-md overflow-hidden rounded-xl border border-slate-100 dark:border-slate-800 bg-white dark:bg-slate-900 p-8 shadow-soft">
                <button @click="close"
                    class="absolute right-6 top-6 text-slate-400 hover:text-slate-900 dark:hover:text-white transition-colors">
                    <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M18 6 6 18" />
                        <path d="m6 6 12 12" />
                    </svg>
                </button>

                <div class="mb-10 text-center">
                    <div
                        class="inline-flex h-12 w-12 items-center justify-center rounded-xl bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 mb-4 shadow-sm">
                        <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none"
                            stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M12 2v20" />
                            <path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
                        </svg>
                    </div>
                    <h2 class="text-2xl font-bold tracking-tight text-slate-900 dark:text-white">
                        {{ mode === 'login' ? 'Welcome back' : 'Create an account' }}
                    </h2>
                    <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">
                        {{ mode === 'login'
                            ?
                            'Enter your credentials to access your dashboard' :
                            'Join Transcode Pro to start processing media'
                        }}
                    </p>
                </div>

                <form @submit.prevent="handleSubmit" class="space-y-5">
                    <div v-if="errorMsg"
                        class="rounded-lg bg-red-50 dark:bg-red-900/20 p-4 text-sm font-medium text-red-600 dark:text-red-400 border border-red-100 dark:border-red-900/30">
                        {{ errorMsg }}
                    </div>

                    <div class="space-y-1.5">
                        <label class="text-xs font-bold text-slate-500 uppercase tracking-wider ml-1">Username</label>
                        <input v-model="username" type="text" required
                            class="w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 px-4 py-2.5 text-sm font-medium text-slate-900 dark:text-white focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 transition-colors"
                            placeholder="johndoe" />
                    </div>

                    <div v-if="mode === 'register'" class="space-y-1.5">
                        <label class="text-xs font-bold text-slate-500 uppercase tracking-wider ml-1">Email</label>
                        <input v-model="email" type="email" required
                            class="w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 px-4 py-2.5 text-sm font-medium text-slate-900 dark:text-white focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 transition-colors"
                            placeholder="john@example.com" />
                    </div>

                    <div class="space-y-1.5">
                        <label class="text-xs font-bold text-slate-500 uppercase tracking-wider ml-1">Password</label>
                        <input v-model="password" type="password" required
                            class="w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 px-4 py-2.5 text-sm font-medium text-slate-900 dark:text-white focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 transition-colors"
                            placeholder="••••••••" />
                    </div>

                    <button type="submit" :disabled="loading"
                        class="w-full inline-flex items-center justify-center rounded-lg bg-blue-600 px-6 py-3 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 transition-all active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed mt-4">
                        <svg v-if="loading" class="animate-spin -ml-1 mr-3 h-4 w-4 text-white"
                            xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4">
                            </circle>
                            <path class="opacity-75" fill="currentColor"
                                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z">
                            </path>
                        </svg>
                        {{ mode === 'login' ? 'Sign In' : 'Create Account' }}
                    </button>
                </form>

                <div class="mt-6 flex items-center justify-between">
                    <hr class="w-full border-slate-200 dark:border-slate-800" />
                    <span class="p-2 text-xs text-slate-400 uppercase tracking-wider font-bold">Or</span>
                    <hr class="w-full border-slate-200 dark:border-slate-800" />
                </div>

                <div class="mt-6 grid grid-cols-2 gap-3">
                    <button @click="handleOAuth('google')" type="button"
                        class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-4 py-2.5 text-sm font-medium text-slate-700 dark:text-slate-300 shadow-sm hover:bg-slate-50 dark:hover:bg-slate-700/50 transition-colors">
                        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"
                            fill="currentColor">
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
                            <path d="M1 1h22v22H1z" fill="none" />
                        </svg>
                        Google
                    </button>
                    <button @click="handleOAuth('github')" type="button"
                        class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-4 py-2.5 text-sm font-medium text-slate-700 dark:text-slate-300 shadow-sm hover:bg-slate-50 dark:hover:bg-slate-700/50 transition-colors">
                        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"
                            fill="currentColor">
                            <path
                                d="M12 2A10 10 0 0 0 8.84 21.5c.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5A10 10 0 0 0 12 2Z" />
                        </svg>
                        GitHub
                    </button>
                </div>
                <div class="mt-8 text-center text-sm">
                    <span class="text-slate-500 dark:text-slate-400">
                        {{ mode === 'login' ? "Don't have an account?" : "Already have an account?" }}
                    </span>
                    <button @click="toggleMode"
                        class="ml-1 font-bold text-blue-600 dark:text-blue-400 hover:text-blue-700 transition-colors">
                        {{ mode === 'login' ? 'Sign up' : 'Sign in' }}
                    </button>
                </div>
            </div>
        </div>
    </Teleport>
</template>
