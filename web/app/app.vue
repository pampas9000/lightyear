<template>
    <div class="app-shell">
        <NuxtLayout>
            <NuxtPage />
        </NuxtLayout>
        <AuthModal />
        <Toaster />
    </div>
</template>

<script setup lang="ts">
import { Toaster } from '@/components/ui/sonner'
import 'vue-sonner/style.css'
import { useAuth } from '~/composables/useAuth'
import type { ApiResponse } from '~/lib/types/api'
import type { User } from '~/composables/useAuth'

const { user } = useAuth()

// Fetch user session state during SSR / initial hydration by forwarding the client's cookies.
const headers = useRequestHeaders(['cookie'])
const { data } = await useAsyncData('auth_user_init', () => {
    return $fetch<ApiResponse<User>>('/api/me', { headers }).catch(() => null)
})

if (data.value && data.value.success) {
    user.value = data.value.data
}
</script>
