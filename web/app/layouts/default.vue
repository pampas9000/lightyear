<template>
    <div class="bg-slate-50 text-slate-900 min-h-screen flex antialiased dark:bg-slate-950 dark:text-slate-100 font-sans">
        <!-- Sidebar -->
        <aside class="w-64 border-r border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 flex-col py-6 px-4 hidden md:flex shrink-0 sticky top-0 h-screen">
            <!-- Brand -->
            <div class="mb-8 px-2">
                <NuxtLink to="/" class="flex items-center gap-3">
                    <div class="w-8 h-8 rounded bg-blue-600 text-white flex items-center justify-center shrink-0">
                        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v20"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
                    </div>
                    <div>
                        <h1 class="text-base font-bold tracking-tight text-slate-900 dark:text-white">Transcode Pro</h1>
                        <p class="text-[10px] font-bold text-slate-500 uppercase tracking-wider mt-0.5">Media Pipeline</p>
                    </div>
                </NuxtLink>
            </div>
            
            <NuxtLink to="/tasks/new" class="mb-6 w-full py-2.5 px-4 bg-blue-600 text-white rounded-lg text-sm font-semibold flex items-center justify-center gap-2 hover:bg-blue-700 transition-all duration-200 shadow-sm shadow-blue-600/20 active:scale-[0.98]">
                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>
                New Task
            </NuxtLink>

            <nav class="flex-1 flex flex-col gap-1">
                <NuxtLink to="/" 
                    class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors duration-200 ease-in-out text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-200" 
                    active-class="text-blue-700 bg-blue-50 dark:bg-blue-900/30 dark:text-blue-400" 
                    exact-active-class="text-blue-700 bg-blue-50 dark:bg-blue-900/30 dark:text-blue-400">
                    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="opacity-80"><rect width="7" height="9" x="3" y="3" rx="1"/><rect width="7" height="5" x="14" y="3" rx="1"/><rect width="7" height="9" x="14" y="12" rx="1"/><rect width="7" height="5" x="3" y="16" rx="1"/></svg>
                    Dashboard
                </NuxtLink>
            </nav>

            <div class="mt-auto border-t border-slate-200 dark:border-slate-800 pt-4">
                <a href="http://localhost:5173" target="_blank" class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-200 transition-colors duration-200 ease-in-out mb-4">
                    <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="opacity-80"><path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/></svg>
                    Documentation
                </a>
                
                <div v-if="isAuthenticated" class="flex items-center gap-3 px-3">
                    <div class="w-8 h-8 rounded-full bg-slate-200 dark:bg-slate-700 flex items-center justify-center text-[10px] font-bold text-slate-700 dark:text-slate-300 shrink-0 border border-slate-300 dark:border-slate-600">
                        {{ user?.username.charAt(0).toUpperCase() }}
                    </div>
                    <div class="flex flex-col min-w-0 flex-1">
                        <span class="text-xs font-semibold text-slate-900 dark:text-white truncate">{{ user?.username }}</span>
                        <button @click="logout" class="text-[10px] font-bold text-slate-500 hover:text-red-500 dark:hover:text-red-400 text-left transition-colors truncate uppercase tracking-tighter">Sign out</button>
                    </div>
                </div>
                <div v-else class="px-3">
                    <button @click="showAuthModal = true" class="w-full py-2 px-4 border border-slate-200 dark:border-slate-700 rounded-lg text-sm font-medium text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800 transition-colors">
                        Sign In
                    </button>
                </div>
            </div>
        </aside>

        <!-- Main Content -->
        <main class="flex-1 flex flex-col min-w-0 h-screen overflow-y-auto">
            <!-- Mobile Header -->
            <header class="md:hidden sticky top-0 z-40 flex justify-between items-center h-16 px-4 border-b border-slate-200 dark:border-slate-800 bg-white/80 dark:bg-slate-950/80 backdrop-blur-md">
                <div class="flex items-center gap-2">
                    <div class="w-6 h-6 rounded bg-blue-600 text-white flex items-center justify-center">
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v20"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
                    </div>
                    <span class="text-base font-semibold tracking-tight">Transcode Pro</span>
                </div>
                <button class="text-slate-500 p-2">
                    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="4" x2="20" y1="12" y2="12"/><line x1="4" x2="20" y1="6" y2="6"/><line x1="4" x2="20" y1="18" y2="18"/></svg>
                </button>
            </header>

            <div class="flex-1 w-full max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8 lg:py-10">
                <slot />
            </div>
        </main>
    </div>
</template>

<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'

const { isAuthenticated, user, logout, showAuthModal } = useAuth()
</script>
