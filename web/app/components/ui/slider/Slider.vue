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
    <SliderTrack class="relative h-1.5 w-full grow overflow-hidden rounded-full bg-surface-3">
      <SliderRange class="absolute h-full bg-primary" />
    </SliderTrack>
    <SliderThumb
      class="block h-4 w-4 rounded-full border border-hairline-strong bg-white shadow-sm transition-[box-shadow] focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-primary/20 hover:ring-4 hover:ring-primary/15 disabled:pointer-events-none disabled:opacity-50 cursor-pointer"
    />
  </SliderRoot>
</template>
