<template>
  <div class='mb-8 flex flex-wrap gap-2.5 px-1'>
    <button
      v-for='item in platforms'
      :key='item'
      type='button'
      class='group relative px-5 py-2.5 rounded-2xl text-xs font-bold transition-all active:scale-95 backdrop-blur'
      :class='item === modelValue ? activeClass : inactiveClass(item)'
      @click='$emit("update:modelValue", item)'
    >
      <span v-if='item === modelValue' class='absolute inset-0 rounded-2xl bg-gradient-to-r from-indigo-500 to-purple-500 opacity-80 blur-md -z-10'></span>
      {{ item === 'all' ? '全部平台' : platformLabel(item) }}
    </button>
  </div>
</template>

<script setup lang='ts'>
import { platformLabel, platformBadgeLightClass } from '@/utils/platformColors'

interface Props {
  platforms: string[]
  modelValue: string
}

defineProps<Props>()

defineEmits<{
  'update:modelValue': [value: string]
}>()

const activeClass = 'relative text-white shadow-lg shadow-indigo-500/20'

function inactiveClass(platform: string) {
  if (platform === 'all') {
    return 'bg-white/5 text-gray-400 border border-white/10 hover:border-white/20 dark:bg-dark-900/60 dark:text-dark-400 dark:border-dark-700/60'
  }
  return platformBadgeLightClass(platform) + ' border border-white/10 dark:border-dark-700/60 hover:border-white/20 dark:hover:border-dark-600'
}
</script>
