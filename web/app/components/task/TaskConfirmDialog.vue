<script setup lang="ts">
import type { Component } from 'vue'
import { Loader2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'

// Confirmation for a destructive task action (cancel, delete).
defineProps<{
    icon: Component
    title: string
    description: string
    confirmLabel: string
    pending: boolean
    /** Show only the spinner, not the label, while the action runs. */
    spinnerOnly?: boolean
}>()

const open = defineModel<boolean>('open', { required: true })

const emit = defineEmits<{
    confirm: []
}>()
</script>

<template>
    <Dialog :open="open" @update:open="val => open = val">
        <DialogContent class="max-w-md bg-surface-1 border-hairline rounded-xl p-6 shadow-xl space-y-4">
            <DialogHeader class="space-y-1.5">
                <DialogTitle class="text-sm font-semibold text-ink flex items-center gap-2">
                    <component :is="icon" class="w-4 h-4 text-rose-600" />
                    {{ title }}
                </DialogTitle>
                <p class="text-xs text-ink-subtle leading-relaxed">
                    {{ description }}
                </p>
            </DialogHeader>

            <div class="flex items-center justify-end gap-2 pt-2 border-t border-hairline">
                <Button variant="outline" size="sm" @click="open = false" class="h-8 px-4 text-xs font-medium border-hairline">
                    {{ $t('common.cancel') }}
                </Button>
                <Button
                    size="sm"
                    @click="emit('confirm')"
                    :disabled="pending"
                    class="h-8 px-4 text-xs font-semibold bg-rose-600 hover:bg-rose-700 text-white shadow-xs"
                >
                    <Loader2 v-if="pending" class="w-3.5 h-3.5 animate-spin" />
                    <span v-if="!(pending && spinnerOnly)">{{ confirmLabel }}</span>
                </Button>
            </div>
        </DialogContent>
    </Dialog>
</template>
