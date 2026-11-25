<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useAuth } from '~/composables/useAuth'
import { useApi, parseApiError } from '~/composables/useApi'
import type { ApiResponse } from '~/lib/types/api'
import type { ApiKey, ApiKeyScope, CreateApiKeyData, ListApiKeysData } from '~/lib/types/api-key'
import { KeyRound, Plus, Loader2, Copy, Check, TriangleAlert, Ban } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogFooter,
} from '@/components/ui/dialog'
import { toast } from 'vue-sonner'

const { t, locale } = useI18n()

useHead({
    title: t('api_keys.title') + ' | Lightyear',
})

const { isAuthenticated, showAuthModal } = useAuth()
const api = useApi()

const keys = ref<ApiKey[]>([])
const loading = ref(true)

const name = ref('')
const scopes = ref<Record<ApiKeyScope, boolean>>({ 'tasks:read': true, 'tasks:write': true })
const creating = ref(false)
const createdKey = ref<string | null>(null)
const copied = ref(false)

const revokeTarget = ref<ApiKey | null>(null)
const revoking = ref(false)

const selectedScopes = computed(() =>
    (Object.keys(scopes.value) as ApiKeyScope[]).filter(s => scopes.value[s]),
)
const canCreate = computed(() => name.value.trim() !== '' && selectedScopes.value.length > 0 && !creating.value)

const scopeLabel = (scope: ApiKeyScope) =>
    scope === 'tasks:read' ? t('api_keys.scope_read') : t('api_keys.scope_write')

const formatDate = (iso: string) =>
    new Date(iso).toLocaleString(locale.value, { dateStyle: 'medium', timeStyle: 'short' })

const fetchKeys = async () => {
    if (!isAuthenticated.value) {
        loading.value = false
        return
    }
    loading.value = true
    try {
        // At most 20 keys are active; one page of 100 also covers the revoked ones.
        const res = await api<ApiResponse<ListApiKeysData>>('/api-keys', { query: { limit: 100 } })
        keys.value = res.data.items
    } catch (err) {
        toast.error(parseApiError(err).userMessage)
    } finally {
        loading.value = false
    }
}

const createKey = async () => {
    if (!canCreate.value) return
    creating.value = true
    try {
        const res = await api<ApiResponse<CreateApiKeyData>>('/api-keys', {
            method: 'POST',
            body: { name: name.value.trim(), scopes: selectedScopes.value },
        })
        createdKey.value = res.data.key
        copied.value = false
        keys.value = [res.data.api_key, ...keys.value]
        name.value = ''
        toast.success(t('api_keys.toast_created'))
    } catch (err) {
        toast.error(parseApiError(err).userMessage)
    } finally {
        creating.value = false
    }
}

const copyKey = async () => {
    if (!createdKey.value) return
    try {
        await navigator.clipboard.writeText(createdKey.value)
        copied.value = true
    } catch {
        toast.error(t('api_keys.toast_copy_failed'))
    }
}

const revokeKey = async () => {
    const target = revokeTarget.value
    if (!target) return
    revoking.value = true
    try {
        const res = await api<ApiResponse<ApiKey>>(`/api-keys/${target.id}`, { method: 'DELETE' })
        keys.value = keys.value.map(k => (k.id === target.id ? res.data : k))
        toast.success(t('api_keys.toast_revoked'))
        revokeTarget.value = null
    } catch (err) {
        toast.error(parseApiError(err).userMessage)
    } finally {
        revoking.value = false
    }
}

watch(isAuthenticated, val => {
    if (val) fetchKeys()
})
onMounted(fetchKeys)
</script>

<template>
    <div class="w-full max-w-4xl mx-auto space-y-6 pb-12 font-sans">
        <header>
            <h1 class="text-xl font-semibold tracking-tight text-ink">{{ $t('api_keys.title') }}</h1>
            <p class="mt-1 text-sm text-ink-subtle max-w-2xl">
                {{ $t('api_keys.description') }}
                <code class="font-mono text-xs bg-surface-2 border border-hairline rounded px-1 py-0.5 text-ink">Authorization: Bearer ly_…</code>
            </p>
        </header>

        <div v-if="!isAuthenticated && !loading"
            class="border border-hairline rounded-xl bg-surface-1 p-10 flex flex-col items-center gap-3 text-center">
            <KeyRound class="w-6 h-6 text-ink-subtle" stroke-width="1.75" />
            <p class="text-sm text-ink-subtle">{{ $t('api_keys.sign_in_required') }}</p>
            <Button size="sm" variant="outline" class="cursor-pointer" @click="showAuthModal = true">{{ $t('common.login') }}</Button>
        </div>

        <template v-else>
            <!-- New key, shown once -->
            <section v-if="createdKey" aria-live="polite"
                class="rounded-xl border border-amber-500/30 bg-amber-500/5 p-5 space-y-3">
                <div class="flex items-start gap-2.5">
                    <TriangleAlert class="w-4 h-4 mt-0.5 text-amber-600 dark:text-amber-400 shrink-0" />
                    <div class="space-y-1 min-w-0">
                        <h2 class="text-sm font-semibold text-ink">{{ $t('api_keys.created_title') }}</h2>
                        <p class="text-xs text-ink-subtle">{{ $t('api_keys.created_warning') }}</p>
                    </div>
                </div>
                <div class="flex flex-col sm:flex-row gap-2">
                    <code data-testid="created-key"
                        class="flex-1 min-w-0 font-mono text-xs bg-surface-1 border border-hairline rounded-md px-3 py-2 text-ink break-all select-all">{{ createdKey }}</code>
                    <div class="flex gap-2 shrink-0">
                        <Button size="sm" class="h-8 gap-1.5 cursor-pointer" @click="copyKey">
                            <Check v-if="copied" class="w-3.5 h-3.5" />
                            <Copy v-else class="w-3.5 h-3.5" />
                            {{ copied ? $t('api_keys.copied') : $t('api_keys.copy') }}
                        </Button>
                        <Button size="sm" variant="outline" class="h-8 cursor-pointer" @click="createdKey = null">
                            {{ $t('api_keys.done') }}
                        </Button>
                    </div>
                </div>
            </section>

            <!-- Create -->
            <form class="rounded-xl border border-hairline bg-surface-1 p-5 space-y-4" @submit.prevent="createKey">
                <h2 class="text-sm font-semibold text-ink">{{ $t('api_keys.create_title') }}</h2>
                <div class="space-y-1.5 max-w-sm">
                    <Label for="api-key-name" class="text-xs font-medium text-ink">{{ $t('api_keys.name_label') }}</Label>
                    <Input id="api-key-name" v-model="name" maxlength="64" autocomplete="off"
                        :placeholder="$t('api_keys.name_placeholder')" class="h-8 text-sm" />
                </div>
                <fieldset class="space-y-2">
                    <legend class="text-xs font-medium text-ink mb-1.5">{{ $t('api_keys.scopes_label') }}</legend>
                    <label v-for="scope in (['tasks:read', 'tasks:write'] as ApiKeyScope[])" :key="scope"
                        class="flex items-start gap-3 cursor-pointer">
                        <Switch v-model="scopes[scope]" class="mt-0.5" />
                        <span class="min-w-0">
                            <span class="text-sm text-ink">{{ scopeLabel(scope) }}</span>
                            <code class="ml-1.5 font-mono text-[11px] text-ink-subtle">{{ scope }}</code>
                            <span class="block text-xs text-ink-subtle">
                                {{ scope === 'tasks:read' ? $t('api_keys.scope_read_desc') : $t('api_keys.scope_write_desc') }}
                            </span>
                        </span>
                    </label>
                    <p v-if="selectedScopes.length === 0" class="text-xs text-rose-600 dark:text-rose-400">{{ $t('api_keys.scope_required') }}</p>
                </fieldset>
                <Button type="submit" size="sm" class="h-8 gap-1.5 cursor-pointer" :disabled="!canCreate">
                    <Loader2 v-if="creating" class="w-3.5 h-3.5 animate-spin" />
                    <Plus v-else class="w-3.5 h-3.5" stroke-width="2.25" />
                    {{ $t('api_keys.create_button') }}
                </Button>
            </form>

            <!-- List -->
            <section class="rounded-xl border border-hairline bg-surface-1 overflow-hidden">
                <h2 class="px-5 py-3 text-sm font-semibold text-ink border-b border-hairline">{{ $t('api_keys.list_title') }}</h2>
                <div v-if="loading" class="p-10 flex justify-center">
                    <Loader2 class="h-5 w-5 text-primary animate-spin" />
                </div>
                <p v-else-if="keys.length === 0" class="p-10 text-center text-sm text-ink-subtle">{{ $t('api_keys.empty') }}</p>
                <ul v-else class="divide-y divide-hairline">
                    <li v-for="key in keys" :key="key.id" class="px-5 py-3 flex flex-col sm:flex-row sm:items-center gap-3"
                        :class="key.revoked_at ? 'opacity-60' : ''">
                        <div class="min-w-0 flex-1 space-y-1">
                            <div class="flex flex-wrap items-center gap-2">
                                <span class="text-sm font-medium text-ink truncate">{{ key.name }}</span>
                                <code class="font-mono text-xs text-ink-subtle">{{ key.prefix }}…</code>
                                <Badge v-if="key.revoked_at" variant="outline" class="text-[11px] border-hairline text-ink-subtle">
                                    {{ $t('api_keys.status_revoked') }}
                                </Badge>
                                <Badge v-for="scope in key.scopes" :key="scope" variant="secondary" class="text-[11px] font-mono">
                                    {{ scope }}
                                </Badge>
                            </div>
                            <p class="text-xs text-ink-subtle tabular-nums">
                                {{ $t('api_keys.created_at', { date: formatDate(key.created_at) }) }}
                                ·
                                {{ key.last_used_at ? $t('api_keys.last_used', { date: formatDate(key.last_used_at) }) : $t('api_keys.never_used') }}
                                <template v-if="key.revoked_at">
                                    · {{ $t('api_keys.revoked_at', { date: formatDate(key.revoked_at) }) }}
                                </template>
                            </p>
                        </div>
                        <Button v-if="!key.revoked_at" size="sm" variant="outline"
                            class="h-8 gap-1.5 shrink-0 cursor-pointer text-rose-600 dark:text-rose-400 hover:text-rose-600"
                            @click="revokeTarget = key">
                            <Ban class="w-3.5 h-3.5" />
                            {{ $t('api_keys.revoke') }}
                        </Button>
                    </li>
                </ul>
            </section>
        </template>

        <Dialog :open="revokeTarget !== null" @update:open="val => { if (!val) revokeTarget = null }">
            <DialogContent class="max-w-md bg-surface-1 border-hairline rounded-xl">
                <DialogHeader>
                    <DialogTitle class="text-sm font-semibold text-ink">{{ $t('api_keys.revoke_title') }}</DialogTitle>
                    <DialogDescription class="text-xs text-ink-subtle">
                        {{ $t('api_keys.revoke_description', { name: revokeTarget?.name ?? '' }) }}
                    </DialogDescription>
                </DialogHeader>
                <DialogFooter class="gap-2">
                    <Button size="sm" variant="outline" class="cursor-pointer" @click="revokeTarget = null">{{ $t('common.cancel') }}</Button>
                    <Button size="sm" variant="destructive" class="gap-1.5 cursor-pointer" :disabled="revoking" @click="revokeKey">
                        <Loader2 v-if="revoking" class="w-3.5 h-3.5 animate-spin" />
                        {{ $t('api_keys.revoke') }}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    </div>
</template>
