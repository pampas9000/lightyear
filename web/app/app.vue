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
import { onMounted } from 'vue'

const { fetchUser } = useAuth()

onMounted(() => {
    fetchUser()
})
</script>
