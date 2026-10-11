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

const { fetchUser } = useAuth()

await fetchUser()

onMounted(() => {
    fetchUser()
})
</script>
