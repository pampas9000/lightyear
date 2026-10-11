<script setup lang="ts">
import { type HTMLAttributes } from 'vue'
import { SliderRange, SliderRoot, type SliderRootEmits, type SliderRootProps, SliderThumb, SliderTrack, useForwardPropsEmits } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<SliderRootProps & { class?: HTMLAttributes['class'] }>()
const emits = defineEmits<SliderRootEmits>()

const forwarded = useForwardPropsEmits(props, emits)
</script>

<template>
  <SliderRoot
    :class="cn('relative flex w-full touch-none select-none items-center cursor-pointer', props.class)"
    v-bind="forwarded"
  >
    <SliderTrack class="relative h-1.5 w-full grow overflow-hidden rounded-full bg-surface-2 dark:bg-zinc-800 border border-hairline">
      <SliderRange class="absolute h-full bg-primary" />
    </SliderTrack>
    <SliderThumb
      class="block h-4 w-4 rounded-full border-2 border-primary bg-surface-1 shadow-sm transition-transform focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 hover:scale-110 active:scale-95 cursor-pointer"
    />
  </SliderRoot>
</template>
