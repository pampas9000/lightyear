<script setup lang="ts">
import { computed } from 'vue'
import { SwitchRoot, SwitchThumb, type SwitchRootEmits, type SwitchRootProps, useForwardPropsEmits } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<
  SwitchRootProps & {
    class?: string
    checked?: boolean
  }
>()
const emits = defineEmits<
  SwitchRootEmits & {
    'update:checked': [payload: boolean]
  }
>()

// Normalize both v-model (modelValue) and :checked / @update:checked
const rootProps = computed(() => {
  const { checked, ...rest } = props
  if (props.modelValue !== undefined) {
    return rest
  }
  return {
    ...rest,
    modelValue: checked,
  }
})

const forwarded = useForwardPropsEmits(rootProps, emits)

const handleUpdateModelValue = (val: any) => {
  emits('update:checked', !!val)
  emits('update:modelValue', val)
}
</script>

<template>
  <SwitchRoot
    v-bind="forwarded"
    @update:model-value="handleUpdateModelValue"
    :class="
      cn(
        'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-surface-3',
        props.class,
      )
    "
  >
    <SwitchThumb
      :class="
        cn(
          'pointer-events-none block h-4 w-4 rounded-full bg-white shadow-xs ring-0 transition-transform data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0 dark:bg-zinc-100',
        )
      "
    />
  </SwitchRoot>
</template>
