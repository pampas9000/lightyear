<template>
    <div class="bg-slate-50 text-slate-900 min-h-screen flex antialiased dark:bg-slate-950 dark:text-slate-100 font-sans">
        <!-- Sidebar -->
        <aside class="w-64 border-r border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 flex flex-col py-8 px-5 hidden md:flex shrink-0 sticky top-0 h-screen">
            <!-- Brand -->
            <div class="mb-8 px-1">
                <NuxtLink to="/" class="flex items-center gap-3">
                    <div class="w-8 h-8 rounded-lg bg-blue-600 text-white flex items-center justify-center shrink-0 shadow-lg shadow-blue-600/20">
                        <Video class="w-5 h-5" stroke-width="2.5" />
                    </div>
                    <div>
                        <h1 class="text-sm font-bold tracking-tight text-slate-900 dark:text-white">Transcode Pro</h1>
                        <p class="text-[10px] font-medium text-slate-400 mt-0.5">Premium Transcoding</p>
                    </div>
                </NuxtLink>
            </div>
            
            <NuxtLink to="/tasks/new" class="block mb-6">
                <Button class="w-full h-10 bg-blue-600 hover:bg-blue-700 text-white rounded-lg shadow-lg shadow-blue-600/20 border-none transition-all duration-300 gap-2 font-semibold text-xs">
                    <Plus class="w-4 h-4" stroke-width="3" />
                    New Task
                </Button>
            </NuxtLink>

            <nav class="flex-1 space-y-1">
                <NuxtLink v-for="item in navItems" :key="item.to" :to="item.to">
                    <Button variant="ghost" class="w-full justify-start gap-3 px-3 h-10 text-xs font-semibold rounded-lg transition-all" 
                        :class="[
                            $route.path === item.to 
                            ? 'bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400' 
                            : 'text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 hover:text-slate-900'
                        ]">
                        <component :is="item.icon" class="w-4 h-4" :stroke-width="$route.path === item.to ? 2 : 1.5" />
                        {{ $t(item.label) }}
                    </Button>
                </NuxtLink>
            </nav>

            <div class="mt-4 px-1">
                <LanguageSwitcher />
            </div>

            <div class="mt-auto border-t border-slate-100 dark:border-slate-800 pt-6 space-y-1">
                <Button variant="ghost" class="w-full justify-start gap-3 px-3 h-10 text-xs font-semibold text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 rounded-lg">
                    <Settings class="w-4 h-4" stroke-width="1.5" />
                    {{ $t('common.documentation') }}
                </Button>
                
                <div v-if="isAuthenticated" class="flex items-center gap-3 px-3 py-2 mt-2 rounded-xl hover:bg-slate-50 dark:hover:bg-slate-800/50 transition-all group cursor-pointer">
                    <div class="w-8 h-8 rounded-full bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-[10px] font-bold text-slate-500 shrink-0 border border-slate-200 dark:border-slate-700 overflow-hidden shadow-sm">
                        {{ user?.username.charAt(0).toUpperCase() }}
                    </div>
                    <div class="flex flex-col min-w-0 flex-1">
                        <span class="text-xs font-bold text-slate-900 dark:text-white truncate">{{ user?.username }}</span>
                        <span class="text-[10px] text-slate-400 font-medium truncate">Pro Plan</span>
                    </div>
                    <button @click="logout" class="opacity-0 group-hover:opacity-100 p-1 hover:text-red-500 transition-all">
                        <LogOut class="w-3 h-3" />
                    </button>
                </div>
                <div v-else class="mt-2">
                    <Button @click="showAuthModal = true" variant="outline" class="w-full h-10 font-bold text-xs shadow-sm hover:bg-slate-50 dark:hover:bg-slate-800 rounded-lg border-slate-200 dark:border-slate-800">
                        {{ $t('common.login') }}
                    </Button>
                </div>
            </div>
        </aside>

        <!-- Main Content -->
        <main class="flex-1 flex flex-col min-w-0 h-screen overflow-y-auto">
            <div class="flex-1 w-full max-w-7xl mx-auto px-6 lg:px-10 py-10">
                <slot />
            </div>
        </main>
    </div>
</template>

<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { 
    Plus, 
    LayoutDashboard, 
    ListTodo, 
    FolderKanban, 
    PlayCircle, 
    Settings,
    LogOut,
    Video
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

const { isAuthenticated, user, logout, showAuthModal } = useAuth()

const navItems = [
    { to: '/', icon: LayoutDashboard, label: 'nav.console' },
    { to: '/tasks', icon: ListTodo, label: 'nav.history' },
    { to: '/projects', icon: FolderKanban, label: 'nav.projects' },
    { to: '/assets', icon: PlayCircle, label: 'nav.assets' }
]
</script>
