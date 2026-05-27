<template>
    <div
        class="bg-canvas text-ink min-h-screen flex antialiased font-sans transition-colors duration-200">
        <!-- Sidebar -->
        <aside
            class="w-60 border-r border-hairline bg-surface-1 flex flex-col py-6 px-4 hidden md:flex shrink-0 sticky top-0 h-screen transition-colors duration-200">
            <!-- Brand -->
            <div class="mb-6 px-2">
                <NuxtLink to="/" class="flex items-center gap-2.5">
                    <div
                        class="w-7 h-7 rounded-lg bg-primary text-primary-foreground flex items-center justify-center shrink-0 shadow-sm">
                        <Video class="w-4 h-4" stroke-width="2" />
                    </div>
                    <div>
                        <h1 class="text-xs font-semibold tracking-tight text-ink uppercase">Transcode Pro</h1>
                        <p class="text-[9px] font-medium text-ink-subtle mt-0.5">Premium Media Hub</p>
                    </div>
                </NuxtLink>
            </div>
 
            <NuxtLink to="/tasks/new" class="block mb-4">
                <Button
                    class="w-full h-9 bg-primary hover:bg-primary-hover text-primary-foreground rounded-lg shadow-sm border-none transition-all duration-200 gap-2 font-medium text-xs cursor-pointer">
                    <Plus class="w-3.5 h-3.5" stroke-width="2.5" />
                    {{ $t('nav.new_task') }}
                </Button>
            </NuxtLink>
 
            <nav class="flex-1 space-y-1">
                <NuxtLink v-for="item in navItems" :key="item.to" :to="item.to">
                    <Button variant="ghost"
                        class="w-full justify-start gap-2.5 px-3.5 h-9 text-xs font-medium rounded-lg transition-all cursor-pointer"
                        :class="[
                            $route.path === item.to
                                ? 'bg-primary/10 text-primary dark:text-primary-hover font-semibold'
                                : 'text-ink-subtle hover:bg-surface-2 hover:text-ink'
                        ]">
                        <component :is="item.icon" class="w-4 h-4" :stroke-width="$route.path === item.to ? 2 : 1.5" />
                        {{ $t(item.label) }}
                    </Button>
                </NuxtLink>
            </nav>
 
            <div class="mt-4 px-1 space-y-1">
                <LanguageSwitcher />
                <ThemeSwitcher />
            </div>
 
            <div class="mt-auto border-t border-hairline pt-4 space-y-1">
                <Button variant="ghost"
                    class="w-full justify-start gap-2.5 px-3.5 h-9 text-xs font-medium text-ink-subtle hover:bg-surface-2 hover:text-ink rounded-lg transition-all cursor-pointer">
                    <Settings class="w-4 h-4" stroke-width="1.5" />
                    {{ $t('common.documentation') }}
                </Button>
 
                <div v-if="isAuthenticated"
                    class="flex items-center gap-2.5 px-3 py-2 mt-1 rounded-lg hover:bg-surface-2 transition-all group cursor-pointer border border-transparent hover:border-hairline">
                    <div
                        class="w-7 h-7 rounded-full bg-surface-2 flex items-center justify-center text-[10px] font-bold text-ink-subtle shrink-0 border border-hairline overflow-hidden shadow-sm">
                        {{ user?.username.charAt(0).toUpperCase() }}
                    </div>
                    <div class="flex flex-col min-w-0 flex-1">
                        <span class="text-xs font-semibold text-ink truncate">{{ user?.username }}</span>
                        <span class="text-[9px] text-ink-subtle font-medium truncate">Pro Plan</span>
                    </div>
                    <button @click="logout"
                        class="opacity-0 group-hover:opacity-100 p-1 hover:text-red-500 transition-all cursor-pointer">
                        <LogOut class="w-3.5 h-3.5" />
                    </button>
                </div>
                <div v-else class="mt-2">
                    <Button @click="showAuthModal = true" variant="outline"
                        class="w-full h-9 font-medium text-xs shadow-sm hover:bg-surface-2 rounded-lg border-hairline cursor-pointer">
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
} from '@lucide/vue'
import { Button } from '@/components/ui/button'

const { isAuthenticated, user, logout, showAuthModal } = useAuth()

const navItems = [
    { to: '/', icon: LayoutDashboard, label: 'nav.overview' },
    { to: '/tasks', icon: ListTodo, label: 'nav.tasks' },
]
</script>
