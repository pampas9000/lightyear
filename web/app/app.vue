<template>
  <div class="app-shell">
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
    <AuthModal />
  </div>
</template>

<script setup lang="ts">
import { useAuth } from '~/composables/useAuth'
import { onMounted } from 'vue'

const { fetchUser } = useAuth()

onMounted(() => {
  fetchUser()
})
</script>
