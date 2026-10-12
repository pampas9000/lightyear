<template>
    <SidebarProvider>
        <!-- Sidebar -->
        <Sidebar collapsible="icon" class="border-r border-hairline bg-surface-1">
            <!-- Header -->
            <SidebarHeader class="h-14 border-b border-hairline flex flex-row items-center px-4 group-data-[collapsible=icon]:px-2">
                <NuxtLink to="/" class="flex items-center gap-2.5 min-w-0 overflow-hidden">
                    <div
                        class="w-7 h-7 rounded-lg bg-primary text-primary-foreground flex items-center justify-center shrink-0">
                        <Video class="w-4 h-4" stroke-width="2" />
                    </div>
                    <div class="flex flex-col leading-none group-data-[collapsible=icon]:hidden">
                        <h1 class="text-sm font-semibold tracking-tight text-ink truncate">Lightyear</h1>
                        <p class="text-[11px] text-ink-subtle mt-1 truncate">{{ $t('sidebar.tagline') }}</p>
                    </div>
                </NuxtLink>
            </SidebarHeader>

            <!-- Navigation content -->
            <SidebarContent class="py-3">
                <!-- Action button (New Task) -->
                <div class="px-2 mb-3">
                    <SidebarMenuButton as-child size="lg"
                        class="w-full h-8 bg-primary hover:bg-primary-hover hover:text-primary-foreground text-primary-foreground rounded-md shadow-xs border-none transition-colors gap-2 font-medium text-sm cursor-pointer justify-center">
                        <NuxtLink to="/tasks/new">
                            <Plus class="w-3.5 h-3.5 shrink-0" stroke-width="2.5" />
                            <span class="group-data-[collapsible=icon]:hidden">{{ $t('nav.new_task') }}</span>
                        </NuxtLink>
                    </SidebarMenuButton>
                </div>

                <!-- Main Menu Group -->
                <SidebarGroup class="p-0">
                    <SidebarGroupContent>
                        <SidebarMenu class="px-2 gap-1">
                            <SidebarMenuItem v-for="item in navItems" :key="item.to">
                                <SidebarMenuButton as-child :is-active="$route.path === item.to"
                                    :tooltip="$t(item.label)">
                                    <NuxtLink :to="item.to"
                                        class="w-full justify-start gap-2.5 px-2.5 h-8 text-sm font-medium rounded-md transition-colors cursor-pointer flex items-center text-ink-muted hover:bg-surface-2 hover:text-ink data-[active=true]:bg-sidebar-active data-[active=true]:text-sidebar-active-foreground data-[active=true]:hover:bg-sidebar-active data-[active=true]:hover:text-sidebar-active-foreground">
                                        <component :is="item.icon" class="w-4 h-4 shrink-0" stroke-width="1.75" />
                                        <span class="group-data-[collapsible=icon]:hidden">{{ $t(item.label) }}</span>
                                    </NuxtLink>
                                </SidebarMenuButton>
                            </SidebarMenuItem>
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>
            </SidebarContent>

            <!-- Footer: Settings, switchers, Profile -->
            <SidebarFooter class="border-t border-hairline p-2 gap-1">
                <!-- Switchers -->
                <div class="space-y-0.5 group-data-[collapsible=icon]:hidden">
                    <LanguageSwitcher />
                    <ThemeSwitcher />
                </div>

                <!-- Documentation -->
                <SidebarMenu class="gap-1">
                    <SidebarMenuItem>
                        <SidebarMenuButton as-child :tooltip="$t('common.documentation')">
                            <a href="#"
                                class="w-full justify-start gap-2.5 px-2.5 h-8 text-sm font-medium text-ink-muted hover:bg-surface-2 hover:text-ink rounded-md transition-colors cursor-pointer flex items-center">
                                <BookOpen class="w-4 h-4 shrink-0" stroke-width="1.75" />
                                <span class="group-data-[collapsible=icon]:hidden">{{ $t('common.documentation')
                                }}</span>
                            </a>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>

                <!-- Auth/User profile -->
                <SidebarMenu v-if="isAuthenticated">
                    <SidebarMenuItem>
                        <DropdownMenu>
                            <DropdownMenuTrigger as-child>
                                <SidebarMenuButton
                                    size="lg"
                                    class="w-full flex items-center gap-2.5 px-3 h-12 rounded-lg hover:bg-surface-2 transition-all cursor-pointer border border-transparent hover:border-hairline data-[state=open]:bg-surface-2 group-data-[collapsible=icon]:!p-0 group-data-[collapsible=icon]:!h-9 group-data-[collapsible=icon]:!w-9 group-data-[collapsible=icon]:justify-center"
                                    :tooltip="user?.username"
                                >
                                    <div
                                        class="w-7 h-7 rounded-full bg-surface-2 flex items-center justify-center text-xs font-bold text-ink-subtle shrink-0 border border-hairline overflow-hidden shadow-sm"
                                    >
                                        {{ user?.username?.charAt(0)?.toUpperCase() }}
                                    </div>
                                    <div class="flex flex-col text-left min-w-0 flex-1 group-data-[collapsible=icon]:hidden leading-tight">
                                        <span class="text-xs font-semibold text-ink truncate">{{ user?.username }}</span>
                                        <span class="text-[11px] text-ink-subtle font-medium truncate mt-0.5">{{ user?.email || $t('sidebar.pro_plan') }}</span>
                                    </div>
                                    <ChevronsUpDown class="w-4 h-4 shrink-0 text-ink-subtle group-data-[collapsible=icon]:hidden ml-auto" />
                                </SidebarMenuButton>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent
                                side="top"
                                align="start"
                                class="w-56 rounded-lg shadow-md border-hairline bg-surface-1 p-1"
                                :side-offset="8"
                            >
                                <DropdownMenuLabel class="p-2 font-normal">
                                    <div class="flex items-center gap-2.5 text-left">
                                        <div
                                            class="w-8 h-8 rounded-full bg-surface-2 flex items-center justify-center text-xs font-bold text-ink-subtle shrink-0 border border-hairline overflow-hidden shadow-sm"
                                        >
                                            {{ user?.username?.charAt(0)?.toUpperCase() }}
                                        </div>
                                        <div class="flex flex-col min-w-0 flex-1 leading-tight">
                                            <span class="text-xs font-semibold text-ink truncate">{{ user?.username }}</span>
                                            <span class="text-[11px] text-ink-subtle font-medium truncate mt-0.5">{{ user?.email || $t('sidebar.pro_plan') }}</span>
                                        </div>
                                    </div>
                                </DropdownMenuLabel>
                                <DropdownMenuSeparator class="my-1 bg-hairline" />
                                <DropdownMenuItem
                                    @click="logout"
                                    class="flex items-center gap-2 px-2.5 py-1.5 text-xs text-red-500 dark:text-red-400 hover:bg-red-500/10 focus:bg-red-500/10 focus:text-red-500 rounded-md cursor-pointer font-medium transition-colors"
                                >
                                    <LogOut class="w-3.5 h-3.5 shrink-0" />
                                    <span>{{ $t('nav.logout') }}</span>
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </SidebarMenuItem>
                </SidebarMenu>
                <SidebarMenu v-else>
                    <SidebarMenuItem>
                        <SidebarMenuButton as-child :tooltip="$t('common.login')">
                            <Button @click="showAuthModal = true" variant="outline"
                                class="w-full h-8 mt-1 font-medium text-sm shadow-xs hover:bg-surface-2 rounded-md border-hairline-strong cursor-pointer group-data-[collapsible=icon]:h-8 group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:p-0">
                                <span class="group-data-[collapsible=icon]:hidden">{{ $t('common.login') }}</span>
                                <LogIn class="w-4 h-4 hidden group-data-[collapsible=icon]:block shrink-0" />
                            </Button>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarFooter>
        </Sidebar>

        <!-- Main Content -->
        <SidebarInset
            class="bg-canvas text-ink min-h-screen flex flex-col antialiased font-sans transition-colors duration-200">
            <!-- Dynamic header at the top of main content -->
            <header
                class="flex h-14 shrink-0 items-center gap-2 border-b border-hairline px-4 sm:px-6 bg-canvas z-20 sticky top-0 justify-between">
                <div class="flex items-center gap-2 min-w-0">
                    <SidebarTrigger class="-ml-1 text-ink-subtle hover:text-ink cursor-pointer" />
                    <Separator orientation="vertical" class="mx-1 !h-4" />
                    <span class="text-sm font-medium text-ink truncate">{{ pageTitle }}</span>
                </div>
                <!-- Pages teleport their toolbar actions here. -->
                <div id="header-actions" class="flex items-center gap-2 shrink-0" />
            </header>

            <main class="relative flex-1 overflow-y-auto">
                <div class="w-full max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
                    <slot />
                </div>
            </main>
        </SidebarInset>
    </SidebarProvider>
</template>

<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import {
    Plus,
    LayoutDashboard,
    ListTodo,
    FolderKanban,
    Zap,
    BookOpen,
    LogOut,
    LogIn,
    ChevronsUpDown,
    Video
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
    DropdownMenu,
    DropdownMenuTrigger,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuItem
} from '@/components/ui/dropdown-menu'
import {
    SidebarProvider,
    Sidebar,
    SidebarHeader,
    SidebarContent,
    SidebarGroup,
    SidebarGroupContent,
    SidebarMenu,
    SidebarMenuItem,
    SidebarMenuButton,
    SidebarFooter,
    SidebarInset,
    SidebarTrigger
} from '@/components/ui/sidebar'
import { Separator } from '@/components/ui/separator'

const { isAuthenticated, user, logout, showAuthModal } = useAuth()
const route = useRoute()
const { t } = useI18n()

const navItems = [
    { to: '/', icon: Zap, label: 'nav.local_transcode' },
    { to: '/overview', icon: LayoutDashboard, label: 'nav.overview' },
    { to: '/tasks', icon: ListTodo, label: 'nav.tasks' },
]

const pageTitle = computed(() => {
    if (route.path === '/tasks/new') return t('nav.new_task')
    if (route.path.startsWith('/tasks/')) return t('nav.tasks')
    const item = navItems.find(i => i.to === route.path)
    return item ? t(item.label) : 'Lightyear'
})
</script>
